// Package ingest is the webhook gateway's receive path: verify, store, return
// 200 fast. It does no delivery or analysis; workers pick events up later.
package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/alerts"
	"relaya/internal/contract"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
	"relaya/internal/mask"
	"relaya/internal/provider"
	"relaya/internal/ratelimit"
	"relaya/internal/realtime"
	"relaya/internal/vault"
)

type Handler struct {
	Pool              *pgxpool.Pool
	Vault             vault.Vault
	MaxBodyBytes      int64
	TrustProxyHeaders bool // trust X-Real-IP / X-Forwarded-For (only behind our own proxy)
	// PublicBaseURLs are the public bases of ingest URLs (INGEST_BASE_URL, and any
	// previous one still given to providers), for providers that sign the URL.
	PublicBaseURLs []string

	// Rate limits (nil = unlimited). Providers retry a 429 later, so nothing is lost.
	PerWebhook *ratelimit.Limiter // requests per ingest URL
	UnknownIP  *ratelimit.Limiter // requests to unknown URLs per IP (URL scanning)

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

	token := r.PathValue("token")
	ip := httpx.ClientIP(r, h.TrustProxyHeaders)
	if ok, wait := h.UnknownIP.Check(ip); !ok {
		return httpx.TooManyRequests(w, wait, "too many requests to unknown ingest URLs from this address")
	}
	wh, err := h.lookup(ctx, token)
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			h.UnknownIP.Take(ip)
		}
		return err
	}
	if ok, wait := h.PerWebhook.Allow(token); !ok {
		return httpx.TooManyRequests(w, wait, "this webhook is receiving more than its rate limit; retry later")
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
	preq := provider.Request{Header: r.Header, Body: body, Now: now, Method: r.Method, URLs: h.publicURLs(r)}

	cfg := provider.Config{SignatureHeader: wh.SignatureHeader}
	if len(wh.SecretEnc) > 0 {
		if cfg.Secret, err = h.Vault.Decrypt(ctx, wh.OrgID, wh.SecretEnc); err != nil {
			return err
		}
	}
	sig := p.Verify(preq, cfg)
	accepted := sig == provider.SigValid || sig == provider.SigNotConfigured
	// A provider checking a new URL (Slack's url_verification) gets its answer;
	// the handshake is not an event.
	if ch, ok := p.(provider.Challenger); ok && accepted {
		if resp, contentType, ok := ch.Challenge(preq); ok {
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(resp)
			return nil
		}
	}
	status := "received"
	if !accepted {
		status = "rejected"
	}

	headers, err := json.Marshal(mask.Headers(r.Header))
	if err != nil {
		return err
	}

	ev := Event{
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

	id, duplicate, err := Store(ctx, h.Pool, ev, accepted)
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

// Event is one stored event: from a webhook, or produced by a sync.
type Event struct {
	OrgID, ProjectID, WebhookID string
	DedupKey, Type, Status      string
	Signature, ContentType      string
	Headers                     []byte
	Payload                     []byte
	SourceIP                    *string
	ReceivedAt                  time.Time
	// Simulated events come from the event simulator: forwarded like any other,
	// but never learned or checked by contracts.
	Simulated bool
}

// Store writes the event, queues its deliveries and contract check. Accepted
// events are deduplicated per webhook; a duplicate returns the original event's
// ID and stores nothing new. Rejected events skip dedup so a forged request
// cannot claim a real delivery's key.
func Store(ctx context.Context, pool *pgxpool.Pool, ev Event, dedup bool) (id string, duplicate bool, err error) {
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		id, duplicate, err = StoreTx(ctx, tx, ev, dedup)
		return err
	})
	return id, duplicate, err
}

// StoreTx is Store inside the caller's transaction.
func StoreTx(ctx context.Context, tx pgx.Tx, ev Event, dedup bool) (id string, duplicate bool, err error) {
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
			return "", false, err
		}
		if !inserted {
			return id, true, nil
		}
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO events (id, org_id, project_id, webhook_id, dedup_key, type, status, signature,
		                    content_type, headers, payload, payload_size, source_ip, received_at, contract_status, simulated)
		VALUES (COALESCE($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id`,
		nullIfEmpty(id), ev.OrgID, ev.ProjectID, ev.WebhookID, ev.DedupKey, ev.Type, ev.Status, ev.Signature,
		ev.ContentType, ev.Headers, ev.Payload, len(ev.Payload), ev.SourceIP, ev.ReceivedAt, contractStatus(ev, dedup), ev.Simulated).Scan(&id)
	if err != nil {
		return "", false, err
	}
	if err := realtime.Notify(ctx, tx, realtime.Message{
		Type: "event", OrgID: ev.OrgID, WebhookID: ev.WebhookID, EventID: id, Status: ev.Status,
	}); err != nil {
		return "", false, err
	}
	if !dedup {
		// Rejected events are evidence only: never forwarded or checked, but alerted
		// (at most once per webhook per hour).
		return id, false, alertSignatureFailure(ctx, tx, ev.WebhookID)
	}
	if contractStatus(ev, dedup) == "pending" {
		if err := enqueueContractCheck(ctx, tx, id, ev); err != nil {
			return "", false, err
		}
	}
	return id, false, enqueueDeliveries(ctx, tx, id, ev)
}

// enqueueDeliveries creates one delivery job per enabled destination that
// takes this event type (no event types = all), in the same transaction as the
// event, and wakes the workers once it commits.
func enqueueDeliveries(ctx context.Context, tx pgx.Tx, eventID string, ev Event) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO deliveries (org_id, event_id, event_received_at, webhook_id, destination_id)
		SELECT org_id, $1, $2, webhook_id, id FROM destinations
		WHERE webhook_id = $3 AND enabled AND (cardinality(event_types) = 0 OR $4 = ANY(event_types))`,
		eventID, ev.ReceivedAt, ev.WebhookID, ev.Type)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify($1, '')`, delivery.NotifyChannel)
	return err
}

// publicURLs lists the URLs the provider may have called for this request, for
// providers that sign the URL: each configured public base, then the host the
// request arrived on (behind IIS that is the public host, or X-Forwarded-Host).
func (h *Handler) publicURLs(r *http.Request) []string {
	path := r.URL.RequestURI() // path and query string, as sent
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, b := range h.PublicBaseURLs {
		add(strings.TrimRight(b, "/") + path)
	}
	host := r.Host
	if h.TrustProxyHeaders {
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			host = fh
		}
	}
	if host != "" {
		add("https://" + host + path)
		add("http://" + host + path)
	}
	return out
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// clientIP returns the caller's IP (see httpx.ClientIP), or nil.
func (h *Handler) clientIP(r *http.Request) *string {
	return nullIfEmpty(httpx.ClientIP(r, h.TrustProxyHeaders))
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
		FROM webhooks WHERE ingest_token = $1 AND kind = 'inbound'`, token). // outbound webhooks take messages from the API only
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

// alertSignatureFailure queues a "bad signature" alert unless one was sent for
// this webhook in the last hour.
func alertSignatureFailure(ctx context.Context, tx pgx.Tx, webhookID string) error {
	var orgID, name string
	err := tx.QueryRow(ctx, `
		UPDATE webhooks SET last_signature_alert_at = now()
		WHERE id = $1 AND (last_signature_alert_at IS NULL OR last_signature_alert_at < now() - interval '1 hour')
		RETURNING org_id, name`, webhookID).Scan(&orgID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // alerted recently
	}
	if err != nil {
		return err
	}
	return alerts.Enqueue(ctx, tx, orgID, alerts.SignatureFailuresAlert(webhookID, name))
}

// contractStatus is "pending" for accepted events that can be checked against a
// contract (a JSON object with an event type), else "none".
func contractStatus(ev Event, accepted bool) string {
	body := bytes.TrimSpace(ev.Payload)
	if !accepted || ev.Simulated || ev.Type == "" || len(body) == 0 || body[0] != '{' {
		return "none"
	}
	return "pending"
}

// enqueueContractCheck queues the event for the contract checker (in the
// worker), so learning and checking never slow down ingest.
func enqueueContractCheck(ctx context.Context, tx pgx.Tx, eventID string, ev Event) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO contract_queue (event_id, event_received_at, org_id, webhook_id, event_type)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, eventID, ev.ReceivedAt, ev.OrgID, ev.WebhookID, ev.Type); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, contract.QueueChannel)
	return err
}
