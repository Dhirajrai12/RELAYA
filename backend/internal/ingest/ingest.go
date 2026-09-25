// Package ingest is the webhook gateway's receive path: verify, store, return
// 200 fast. It does no delivery or analysis; workers pick events up later.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/httpx"
	"relaya/internal/mask"
	"relaya/internal/provider"
	"relaya/internal/vault"
)

type Handler struct {
	Pool              *pgxpool.Pool
	Vault             vault.Vault
	MaxBodyBytes      int64
	TrustProxyHeaders bool // trust X-Real-IP / X-Forwarded-For (only behind our own proxy)

	cache webhookCache
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/in/{token}", httpx.HandlerFunc(h.receive))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

type webhook struct {
	ID              string
	OrgID           string
	ProjectID       string
	Provider        string
	SecretEnc       []byte
	SignatureHeader string
}

func (h *Handler) receive(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	now := time.Now().UTC()

	wh, err := h.lookup(ctx, r.PathValue("token"))
	if err != nil {
		return err
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "payload exceeds size limit")
		}
		return httpx.BadRequest("could not read body")
	}

	p, ok := provider.Get(wh.Provider)
	if !ok {
		p, _ = provider.Get("generic")
	}
	preq := provider.Request{Header: r.Header, Body: body, Now: now}

	cfg := provider.Config{SignatureHeader: wh.SignatureHeader}
	if len(wh.SecretEnc) > 0 {
		if cfg.Secret, err = h.Vault.Decrypt(ctx, wh.OrgID, wh.SecretEnc); err != nil {
			return err
		}
	}
	sig := p.Verify(preq, cfg)
	accepted := sig == provider.SigValid || sig == provider.SigNotConfigured
	status := "received"
	if !accepted {
		status = "rejected"
	}

	headers, err := json.Marshal(mask.Headers(r.Header))
	if err != nil {
		return err
	}

	ev := eventRow{
		OrgID:       wh.OrgID,
		ProjectID:   wh.ProjectID,
		WebhookID:   wh.ID,
		DedupKey:    provider.DedupKey(p, preq),
		Type:        p.EventType(preq),
		Status:      status,
		Signature:   string(sig),
		ContentType: r.Header.Get("Content-Type"),
		Headers:     headers,
		Payload:     body,
		SourceIP:    h.clientIP(r),
		ReceivedAt:  now,
	}

	id, duplicate, err := h.store(ctx, ev, accepted)
	if err != nil {
		return err
	}

	if !accepted {
		// Stored as evidence so a misconfigured secret shows up in the Explorer,
		// but the provider must see a failure.
		return httpx.NewError(http.StatusUnauthorized, "invalid_signature", "signature verification failed")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": id, "duplicate": duplicate})
	return nil
}

type eventRow struct {
	OrgID, ProjectID, WebhookID string
	DedupKey, Type, Status      string
	Signature, ContentType      string
	Headers                     []byte
	Payload                     []byte
	SourceIP                    *string
	ReceivedAt                  time.Time
}

// store writes the event. Accepted events are deduplicated per webhook; a
// duplicate returns the original event's ID and stores nothing new. Rejected
// events skip dedup so a forged request cannot claim a real delivery's key.
func (h *Handler) store(ctx context.Context, ev eventRow, dedup bool) (id string, duplicate bool, err error) {
	err = pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		if dedup {
			var inserted bool
			err := tx.QueryRow(ctx, `
				INSERT INTO event_dedup (webhook_id, dedup_key, event_id, received_at)
				VALUES ($1, $2, gen_random_uuid(), $3)
				ON CONFLICT (webhook_id, dedup_key)
				DO UPDATE SET duplicates = event_dedup.duplicates + 1
				RETURNING event_id, (xmax = 0)`,
				ev.WebhookID, ev.DedupKey, ev.ReceivedAt).Scan(&id, &inserted)
			if err != nil {
				return err
			}
			if !inserted {
				duplicate = true
				return nil
			}
		}
		return tx.QueryRow(ctx, `
			INSERT INTO events (id, org_id, project_id, webhook_id, dedup_key, type, status, signature,
			                    content_type, headers, payload, payload_size, source_ip, received_at)
			VALUES (COALESCE($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			RETURNING id`,
			nullIfEmpty(id), ev.OrgID, ev.ProjectID, ev.WebhookID, ev.DedupKey, ev.Type, ev.Status, ev.Signature,
			ev.ContentType, ev.Headers, ev.Payload, len(ev.Payload), ev.SourceIP, ev.ReceivedAt).Scan(&id)
	})
	return id, duplicate, err
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// clientIP returns the caller's IP. Behind our proxy (IIS/ARR), X-Real-IP is
// overwritten by the proxy's rewrite rule, so clients cannot spoof it. The
// X-Forwarded-For fallback takes the last entry, the one our proxy appended;
// earlier entries come from the client.
func (h *Handler) clientIP(r *http.Request) *string {
	ip := ""
	if h.TrustProxyHeaders {
		if v := r.Header.Get("X-Real-IP"); v != "" {
			ip = v
		} else if v := r.Header.Get("X-Forwarded-For"); v != "" {
			ip = v[strings.LastIndex(v, ",")+1:]
		}
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	ip = stripPort(strings.TrimSpace(ip))
	if net.ParseIP(ip) == nil {
		return nil
	}
	return &ip
}

// stripPort handles "1.2.3.4:5678" and "[::1]:5678" (ARR includes the port in
// X-Forwarded-For by default) as well as bare IPv4 and IPv6 addresses.
func stripPort(s string) string {
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}
	return strings.Trim(s, "[]")
}

// ---- webhook lookup with a short cache --------------------------------------------

const cacheTTL = 30 * time.Second

type cacheEntry struct {
	wh      *webhook // nil = token not found
	expires time.Time
}

type webhookCache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
}

func (h *Handler) lookup(ctx context.Context, token string) (*webhook, error) {
	notFound := httpx.NewError(http.StatusNotFound, "unknown_endpoint", "unknown ingest endpoint")

	h.cache.mu.Lock()
	if h.cache.m == nil {
		h.cache.m = map[string]cacheEntry{}
	}
	e, ok := h.cache.m[token]
	h.cache.mu.Unlock()
	if ok && time.Now().Before(e.expires) {
		if e.wh == nil {
			return nil, notFound
		}
		return e.wh, nil
	}

	var wh webhook
	err := h.Pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, provider, signing_secret_enc, signature_header
		FROM webhooks WHERE ingest_token = $1`, token).
		Scan(&wh.ID, &wh.OrgID, &wh.ProjectID, &wh.Provider, &wh.SecretEnc, &wh.SignatureHeader)
	var found *webhook
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		found = &wh
	}

	h.cache.mu.Lock()
	if len(h.cache.m) > 10_000 { // crude bound against token-guessing floods
		clear(h.cache.m)
	}
	h.cache.m[token] = cacheEntry{wh: found, expires: time.Now().Add(cacheTTL)}
	h.cache.mu.Unlock()

	if found == nil {
		return nil, notFound
	}
	return found, nil
}

// Maintain keeps monthly partitions ahead of time and prunes old dedup keys.
// It runs once immediately and then every interval until ctx is done.
func Maintain(ctx context.Context, pool *pgxpool.Pool, dedupRetention, interval time.Duration) {
	run := func() {
		if _, err := pool.Exec(ctx, `SELECT ensure_event_partitions(now(), 2)`); err != nil {
			slog.Error("ensure partitions", "err", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM event_dedup WHERE received_at < $1`,
			time.Now().Add(-dedupRetention)); err != nil {
			slog.Error("prune dedup keys", "err", err)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
