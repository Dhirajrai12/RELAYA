// Package auth authenticates API callers (dashboard sessions and API keys) and
// checks their role inside an organization.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"relaya/internal/httpx"
)

// Role is a member's role in an organization. Higher ranks include lower ones.
type Role string

const (
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleAdmin:
		return 2
	case RoleMember:
		return 1
	}
	return 0
}

// AtLeast reports whether r grants everything min grants.
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() && r.rank() > 0 }

func ParseRole(s string) (Role, bool) {
	r := Role(s)
	return r, r.rank() > 0
}

const (
	SessionPrefix = "rs_"
	APIKeyPrefix  = "rk_"
)

// Principal is the authenticated caller.
type Principal struct {
	UserID string // set for sessions

	APIKeyID    string // set for API keys
	APIKeyOrgID string
	APIKeyRole  Role
}

// ActorType and ActorID identify the principal in audit logs.
func (p Principal) ActorType() string {
	if p.APIKeyID != "" {
		return "api_key"
	}
	return "user"
}

func (p Principal) ActorID() string {
	if p.APIKeyID != "" {
		return p.APIKeyID
	}
	return p.UserID
}

type ctxKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Service authenticates requests against sessions and api_keys.
type Service struct {
	Pool       *pgxpool.Pool
	SessionTTL time.Duration
}

// SessionCookie holds the dashboard's session token (httpOnly, set on sign-in).
const SessionCookie = "relaya_session"

// RequestToken is the caller's token: an explicit Authorization bearer (API keys,
// SDKs, the CLI, scripts) wins over the dashboard's session cookie.
func RequestToken(r *http.Request) string {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.TrimSpace(t) != "" {
		return strings.TrimSpace(t)
	}
	if c, err := r.Cookie(SessionCookie); err == nil {
		return strings.TrimSpace(c.Value)
	}
	return ""
}

// Middleware requires a valid token (Authorization header or session cookie) and stores the Principal in the context.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := RequestToken(r)
		if token == "" {
			httpx.WriteError(w, r, httpx.ErrUnauthorized)
			return
		}
		p, err := s.authenticate(r.Context(), token)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func (s *Service) authenticate(ctx context.Context, token string) (Principal, error) {
	hash := HashToken(token)
	switch {
	case strings.HasPrefix(token, SessionPrefix):
		var p Principal
		err := s.Pool.QueryRow(ctx,
			`SELECT user_id FROM sessions WHERE token_hash = $1 AND expires_at > now()`, hash).Scan(&p.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return p, httpx.ErrUnauthorized
		}
		return p, err

	case strings.HasPrefix(token, APIKeyPrefix):
		var p Principal
		var role string
		err := s.Pool.QueryRow(ctx, `
			UPDATE api_keys SET last_used_at = now()
			WHERE key_hash = $1 AND revoked_at IS NULL
			RETURNING id, org_id, role`, hash).Scan(&p.APIKeyID, &p.APIKeyOrgID, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return p, httpx.ErrUnauthorized
		}
		p.APIKeyRole = Role(role)
		return p, err
	}
	return Principal{}, httpx.ErrUnauthorized
}

// Authorize returns the caller's role in orgID and fails unless it is at least min.
// A caller with no access to the org gets 404, so org IDs cannot be probed.
func (s *Service) Authorize(ctx context.Context, orgID string, min Role) (Principal, Role, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return p, "", httpx.ErrUnauthorized
	}

	var role Role
	if p.APIKeyID != "" {
		if p.APIKeyOrgID != orgID {
			return p, "", httpx.ErrNotFound
		}
		role = p.APIKeyRole
	} else {
		var r string
		err := s.Pool.QueryRow(ctx,
			`SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, p.UserID).Scan(&r)
		if errors.Is(err, pgx.ErrNoRows) {
			return p, "", httpx.ErrNotFound
		}
		if err != nil {
			return p, "", err
		}
		role = Role(r)
	}
	if !role.AtLeast(min) {
		return p, role, httpx.ErrForbidden
	}
	return p, role, nil
}

// CreateSession issues a new session token for userID.
func (s *Service) CreateSession(ctx context.Context, q Querier, userID string) (string, time.Time, error) {
	token := SessionPrefix + randomToken()
	expires := time.Now().Add(s.SessionTTL)
	_, err := q.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, HashToken(token), expires)
	return token, expires, err
}

// DeleteSession revokes the session behind token (logout).
func (s *Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, HashToken(token))
	return err
}

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// NewAPIKey returns a fresh API key, its display prefix and its hash.
func NewAPIKey() (key, prefix string, hash []byte) {
	key = APIKeyPrefix + randomToken()
	return key, key[:len(APIKeyPrefix)+8], HashToken(key)
}

// HashToken hashes a high-entropy token for storage. Tokens are random, so a
// plain SHA-256 is enough; passwords use bcrypt instead.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// RandomToken returns a URL-safe random token with the given prefix.
func RandomToken(prefix string) string { return prefix + randomToken() }

const bcryptCost = 12

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash is compared against when a login email is unknown, so response
// time does not reveal which emails have accounts.
var dummyHash, _ = HashPassword("not-a-real-password")

func CheckPasswordOrDummy(hash string, found bool, pw string) bool {
	if !found {
		CheckPassword(dummyHash, pw)
		return false
	}
	return CheckPassword(hash, pw)
}

// Authenticate validates a bearer token (session or API key) outside of the
// HTTP middleware, e.g. for a WebSocket that authenticates with its first message.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	return s.authenticate(ctx, strings.TrimSpace(token))
}

// WithPrincipal returns ctx carrying p, as the middleware would.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}
