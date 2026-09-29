package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/alerts"
	"relaya/internal/audit"
	"relaya/internal/vault"
)

// Service stores connections and keeps their tokens fresh.
type Service struct {
	Pool   *pgxpool.Pool
	Vault  vault.Vault
	Client *Client
	// RedirectURI is the OAuth callback URL organizations register in their OAuth apps.
	RedirectURI string
	// RefreshWindow: tokens expiring within this are refreshed by the worker (default 10 minutes).
	RefreshWindow time.Duration
}

// ErrBroken means the connection needs the user to connect again.
var ErrBroken = errors.New("connection is broken")

// Token is a usable access token and where to use it.
type Token struct {
	AccessToken string     `json:"access_token"`
	TokenType   string     `json:"token_type"`
	ExpiresAt   *time.Time `json:"expires_at"`
	APIBase     string     `json:"api_base"`
	Provider    string     `json:"provider"`
	EndUserID   string     `json:"end_user_id"`
}

type conn struct {
	id, orgID, integrationID, integrationName, endUserID, status, providerKey, lastError string
	clientID                                                                             string
	clientSecretEnc, credEnc                                                             []byte
	meta                                                                                 Metadata
	expiresAt                                                                            *time.Time
	failures                                                                             int
}

const connSelect = `
	SELECT c.id, c.org_id, c.integration_id, i.name, c.end_user_id, c.status, i.provider, c.last_error,
	       i.client_id, i.client_secret_enc, c.credentials_enc, c.metadata, c.expires_at, c.refresh_failures
	FROM connections c JOIN integrations i ON i.id = c.integration_id`

func scanConn(row pgx.Row) (conn, error) {
	var c conn
	var meta []byte
	err := row.Scan(&c.id, &c.orgID, &c.integrationID, &c.integrationName, &c.endUserID, &c.status, &c.providerKey, &c.lastError,
		&c.clientID, &c.clientSecretEnc, &c.credEnc, &meta, &c.expiresAt, &c.failures)
	if err == nil {
		c.meta = Metadata{}
		_ = json.Unmarshal(meta, &c.meta)
	}
	return c, err
}

func (s *Service) window() time.Duration {
	if s.RefreshWindow > 0 {
		return s.RefreshWindow
	}
	return 10 * time.Minute
}

// Save stores a newly connected account, or replaces the tokens of an existing
// connection for the same end user (reconnecting also repairs a broken one).
func (s *Service) Save(ctx context.Context, tx pgx.Tx, orgID, integrationID, endUserID string, cred Credentials, meta Metadata) (string, error) {
	enc, err := s.encrypt(ctx, orgID, cred)
	if err != nil {
		return "", err
	}
	metaJSON, _ := json.Marshal(meta)
	var id string
	var created bool
	var prev *string
	err = tx.QueryRow(ctx, `
		WITH prev AS (SELECT status FROM connections WHERE integration_id = $2 AND end_user_id = $3)
		INSERT INTO connections (org_id, integration_id, end_user_id, credentials_enc, expires_at, metadata, last_refreshed_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (integration_id, end_user_id) DO UPDATE SET
			credentials_enc = EXCLUDED.credentials_enc, expires_at = EXCLUDED.expires_at, metadata = EXCLUDED.metadata,
			status = 'active', last_refreshed_at = now(), refresh_failures = 0, last_error = '', broken_at = NULL, updated_at = now()
		RETURNING id, (xmax = 0), (SELECT status FROM prev)`,
		orgID, integrationID, endUserID, enc, cred.ExpiresAt, metaJSON).Scan(&id, &created, &prev)
	if err != nil {
		return "", err
	}
	action := "connection.create"
	if !created {
		action = "connection.reconnect"
	}
	e := audit.Entry{OrgID: orgID, ActorType: "system", ActorID: "end_user:" + endUserID, Action: action, TargetType: "connection", TargetID: id}
	e.Metadata = map[string]any{"integration_id": integrationID, "end_user_id": endUserID}
	if err := audit.Record(ctx, tx, e); err != nil {
		return "", err
	}
	if prev != nil && *prev == "broken" {
		if err := s.recovered(ctx, tx, orgID, id); err != nil {
			return "", err
		}
	}
	return id, nil
}

// Token returns a working access token, refreshing it first when it is about
// to expire. Callers use it right away rather than caching it for long.
func (s *Service) Token(ctx context.Context, orgID, connID string) (Token, error) {
	c, err := scanConn(s.Pool.QueryRow(ctx, connSelect+` WHERE c.id = $1 AND c.org_id = $2`, connID, orgID))
	if err != nil {
		return Token{}, err
	}
	if c.status == "broken" {
		return Token{}, fmt.Errorf("%w: %s", ErrBroken, c.lastError)
	}
	if c.expiresAt != nil && time.Until(*c.expiresAt) < time.Minute {
		if err := s.Refresh(ctx, connID, false, false); err != nil {
			return Token{}, err
		}
		if c, err = scanConn(s.Pool.QueryRow(ctx, connSelect+` WHERE c.id = $1`, connID)); err != nil {
			return Token{}, err
		}
	}
	cred, err := s.decrypt(ctx, c.orgID, c.credEnc)
	if err != nil {
		return Token{}, err
	}
	t := Token{AccessToken: cred.AccessToken, TokenType: cred.TokenType, ExpiresAt: cred.ExpiresAt, Provider: c.providerKey, EndUserID: c.endUserID}
	if t.TokenType == "" {
		t.TokenType = "Bearer"
	}
	t.APIBase, _ = c.meta["api_base"].(string)
	if p, ok := Get(c.providerKey); ok && t.APIBase == "" {
		t.APIBase = p.APIBase
	}
	return t, nil
}

// Refresh renews a connection's token. Without force it does nothing when the
// token isn't due (another process refreshed it meanwhile). skipLocked makes
// it give up instead of waiting when another process is refreshing it.
// A permanent failure marks the connection broken and alerts; a temporary one
// is retried by the worker with backoff.
func (s *Service) Refresh(ctx context.Context, connID string, force, skipLocked bool) error {
	lock := ` FOR UPDATE OF c`
	if skipLocked {
		lock += ` SKIP LOCKED`
	}
	var refreshErr error
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		c, err := scanConn(tx.QueryRow(ctx, connSelect+` WHERE c.id = $1`+lock, connID))
		if errors.Is(err, pgx.ErrNoRows) && skipLocked {
			return nil // being refreshed elsewhere, or gone
		}
		if err != nil {
			return err
		}
		if !force && (c.status != "active" || c.expiresAt == nil || time.Until(*c.expiresAt) > s.window()) {
			return nil
		}
		p, ok := Get(c.providerKey)
		if !ok {
			refreshErr = &Error{Permanent: true, Message: "unknown provider " + c.providerKey}
			return s.fail(ctx, tx, c, refreshErr.(*Error))
		}
		cred, err := s.decrypt(ctx, c.orgID, c.credEnc)
		if err != nil {
			return err
		}
		app := App{ClientID: c.clientID}
		if len(c.clientSecretEnc) > 0 {
			secret, err := s.Vault.Decrypt(ctx, c.orgID, c.clientSecretEnc)
			if err != nil {
				return err
			}
			app.ClientSecret = string(secret)
		}

		fresh, meta, err := s.Client.Refresh(ctx, p, app, cred)
		if err != nil {
			var ce *Error
			if !errors.As(err, &ce) {
				ce = &Error{Message: err.Error()}
			}
			refreshErr = ce
			return s.fail(ctx, tx, c, ce)
		}
		enc, err := s.encrypt(ctx, c.orgID, fresh)
		if err != nil {
			return err
		}
		for k, v := range meta {
			c.meta[k] = v
		}
		metaJSON, _ := json.Marshal(c.meta)
		if _, err := tx.Exec(ctx, `
			UPDATE connections SET credentials_enc = $2, expires_at = $3, metadata = $4, status = 'active',
				last_refreshed_at = now(), refresh_failures = 0, last_error = '', broken_at = NULL, updated_at = now()
			WHERE id = $1`, c.id, enc, fresh.ExpiresAt, metaJSON); err != nil {
			return err
		}
		if c.status == "broken" {
			return s.recovered(ctx, tx, c.orgID, c.id)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return refreshErr
}

// fail records a refresh failure; permanent ones break the connection (once).
func (s *Service) fail(ctx context.Context, tx pgx.Tx, c conn, ce *Error) error {
	msg := ce.Error()
	if !ce.Permanent {
		_, err := tx.Exec(ctx, `UPDATE connections SET refresh_failures = refresh_failures + 1, last_error = $2, updated_at = now() WHERE id = $1`, c.id, msg)
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE connections SET status = 'broken', broken_at = coalesce(broken_at, now()), refresh_failures = refresh_failures + 1,
			last_error = $2, updated_at = now()
		WHERE id = $1`, c.id, msg); err != nil {
		return err
	}
	if c.status == "broken" {
		return nil // already alerted
	}
	e := audit.Entry{OrgID: c.orgID, ActorType: "system", Action: "connection.broken", TargetType: "connection", TargetID: c.id, Result: "failure"}
	e.Metadata = map[string]any{"error": msg, "end_user_id": c.endUserID}
	if err := audit.Record(ctx, tx, e); err != nil {
		return err
	}
	return alerts.Enqueue(ctx, tx, c.orgID, alerts.ConnectionBrokenAlert(c.id, c.integrationName, c.endUserID, msg))
}

func (s *Service) recovered(ctx context.Context, tx pgx.Tx, orgID, connID string) error {
	var name, endUser string
	if err := tx.QueryRow(ctx, `SELECT i.name, c.end_user_id FROM connections c JOIN integrations i ON i.id = c.integration_id WHERE c.id = $1`, connID).
		Scan(&name, &endUser); err != nil {
		return err
	}
	if err := audit.Record(ctx, tx, audit.Entry{OrgID: orgID, ActorType: "system", Action: "connection.recovered", TargetType: "connection", TargetID: connID}); err != nil {
		return err
	}
	return alerts.Enqueue(ctx, tx, orgID, alerts.ConnectionRecoveredAlert(connID, name, endUser))
}

// RunRefresher refreshes tokens before they expire, until ctx is done.
func (s *Service) RunRefresher(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	lastCleanup := time.Time{}
	for {
		n, err := s.RefreshDue(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("refresh connections", "err", err)
		} else if n > 0 {
			slog.Info("refreshed connections", "count", n)
		}
		if time.Since(lastCleanup) > time.Hour {
			if _, err := s.Pool.Exec(ctx, `DELETE FROM connect_sessions WHERE created_at < now() - interval '7 days'`); err != nil && ctx.Err() == nil {
				slog.Error("clean up connect sessions", "err", err)
			}
			if _, err := s.Pool.Exec(ctx, `DELETE FROM proxy_calls WHERE created_at < now() - interval '30 days'`); err != nil && ctx.Err() == nil {
				slog.Error("clean up proxy call log", "err", err)
			}
			lastCleanup = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// RefreshDue refreshes active connections expiring within the window. Failed
// ones are retried after a backoff of one minute per failure (at most 10).
func (s *Service) RefreshDue(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id FROM connections
		WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at < now() + make_interval(secs => $1)
		  AND updated_at < now() - make_interval(mins => least(refresh_failures, 10))
		ORDER BY expires_at LIMIT 100`, s.window().Seconds())
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		if err := s.Refresh(ctx, id, false, true); err != nil {
			if ctx.Err() != nil {
				return done, nil
			}
			slog.Warn("connection refresh failed", "connection", id, "err", err) // the error never contains tokens
			continue
		}
		done++
	}
	return done, nil
}

func (s *Service) encrypt(ctx context.Context, orgID string, c Credentials) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return s.Vault.Encrypt(ctx, orgID, raw)
}

func (s *Service) decrypt(ctx context.Context, orgID string, enc []byte) (Credentials, error) {
	var c Credentials
	raw, err := s.Vault.Decrypt(ctx, orgID, enc)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(raw, &c)
}
