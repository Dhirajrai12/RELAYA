// Package vault encrypts secrets (signing secrets, OAuth tokens, API keys) with
// envelope encryption: each organization has its own data key (DEK), and the
// DEK is stored wrapped by a master key. The master key is local for now and
// moves to AWS KMS later by swapping the KeyWrapper.
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Vault encrypts and decrypts secrets for one organization.
type Vault interface {
	Encrypt(ctx context.Context, orgID string, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, orgID string, ciphertext []byte) ([]byte, error)
}

// KeyWrapper wraps and unwraps data keys. LocalWrapper today, KMS later.
type KeyWrapper interface {
	KeyID() string
	Wrap(dek []byte) ([]byte, error)
	Unwrap(wrapped []byte) ([]byte, error)
}

// LocalWrapper wraps data keys with AES-256-GCM under a master key held in memory.
type LocalWrapper struct {
	id  string
	key []byte
}

func NewLocalWrapper(id string, masterKey []byte) (*LocalWrapper, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("vault: master key must be 32 bytes")
	}
	return &LocalWrapper{id: id, key: masterKey}, nil
}

func (w *LocalWrapper) KeyID() string                   { return w.id }
func (w *LocalWrapper) Wrap(dek []byte) ([]byte, error) { return seal(w.key, dek) }
func (w *LocalWrapper) Unwrap(b []byte) ([]byte, error) { return open(w.key, b) }

// PGVault stores wrapped data keys in the org_keys table and caches unwrapped
// keys in memory for the life of the process.
type PGVault struct {
	pool    *pgxpool.Pool
	wrapper KeyWrapper

	mu   sync.RWMutex
	deks map[string][]byte
}

func NewPGVault(pool *pgxpool.Pool, wrapper KeyWrapper) *PGVault {
	return &PGVault{pool: pool, wrapper: wrapper, deks: map[string][]byte{}}
}

func (v *PGVault) Encrypt(ctx context.Context, orgID string, plaintext []byte) ([]byte, error) {
	dek, err := v.dek(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return seal(dek, plaintext)
}

func (v *PGVault) Decrypt(ctx context.Context, orgID string, ciphertext []byte) ([]byte, error) {
	dek, err := v.dek(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return open(dek, ciphertext)
}

// dek returns the organization's data key, creating it on first use.
func (v *PGVault) dek(ctx context.Context, orgID string) ([]byte, error) {
	v.mu.RLock()
	dek, ok := v.deks[orgID]
	v.mu.RUnlock()
	if ok {
		return dek, nil
	}

	var wrapped []byte
	err := v.pool.QueryRow(ctx, `SELECT wrapped_dek FROM org_keys WHERE org_id = $1`, orgID).Scan(&wrapped)
	if errors.Is(err, pgx.ErrNoRows) {
		fresh := make([]byte, 32)
		if _, err := rand.Read(fresh); err != nil {
			return nil, err
		}
		w, err := v.wrapper.Wrap(fresh)
		if err != nil {
			return nil, err
		}
		// Another process may create the key concurrently; whichever row wins is used.
		err = v.pool.QueryRow(ctx, `
			INSERT INTO org_keys (org_id, wrapped_dek, master_kid) VALUES ($1, $2, $3)
			ON CONFLICT (org_id) DO UPDATE SET org_id = EXCLUDED.org_id
			RETURNING wrapped_dek`, orgID, w, v.wrapper.KeyID()).Scan(&wrapped)
		if err != nil {
			return nil, fmt.Errorf("vault: store data key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("vault: load data key: %w", err)
	}

	dek, err = v.wrapper.Unwrap(wrapped)
	if err != nil {
		return nil, fmt.Errorf("vault: unwrap data key: %w", err)
	}
	v.mu.Lock()
	v.deks[orgID] = dek
	v.mu.Unlock()
	return dek, nil
}

// seal returns nonce || AES-256-GCM ciphertext.
func seal(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func open(key, box []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(box) < gcm.NonceSize() {
		return nil, errors.New("vault: ciphertext too short")
	}
	return gcm.Open(nil, box[:gcm.NonceSize()], box[gcm.NonceSize():], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
