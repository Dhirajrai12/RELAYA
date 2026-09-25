package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"relaya/internal/auth"
	"relaya/internal/httpx"
	"relaya/internal/mask"
)

type eventSummary struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	WebhookID   string    `json:"webhook_id"`
	DedupKey    string    `json:"dedup_key"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	Signature   string    `json:"signature"`
	ContentType string    `json:"content_type"`
	PayloadSize int       `json:"payload_size"`
	ReceivedAt  time.Time `json:"received_at"`
	// Forwarding state across destinations: none, pending, delivered, failed.
	Delivery string `json:"delivery"`
	// Contract check: none, pending, learning, ok, compatible, suspicious, breaking.
	ContractStatus string `json:"contract_status"`
}

const eventSummaryCols = `id, project_id, webhook_id, dedup_key, type, status, signature,
	content_type, payload_size, received_at, contract_status`

// listEvents is the Explorer's search: newest first, keyset-paginated.
//
// Filters: project_id, webhook_id, type, status, signature, dedup_key, since, until (RFC 3339).
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"org_id = $1"}
	args := []any{orgID}
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}

	for _, f := range []string{"project_id", "webhook_id"} {
		if v := q.Get(f); v != "" {
			if !uuidRe.MatchString(v) {
				return httpx.BadRequest("invalid %s", f)
			}
			add(f+" = ?", v)
		}
	}
	for _, f := range []string{"type", "status", "signature", "dedup_key", "contract_status"} {
		if v := q.Get(f); v != "" {
			add(f+" = ?", v)
		}
	}
	for f, op := range map[string]string{"since": ">=", "until": "<"} {
		if v := q.Get(f); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return httpx.BadRequest("%s must be an RFC 3339 timestamp", f)
			}
			add("received_at "+op+" ?", t)
		}
	}
	if c := q.Get("cursor"); c != "" {
		ts, id, err := decodeCursor(c)
		if err != nil {
			return httpx.BadRequest("invalid cursor")
		}
		args = append(args, ts, id)
		where = append(where, "(received_at, id) < ($"+itoa(len(args)-1)+", $"+itoa(len(args))+")")
	}

	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return httpx.BadRequest("limit must be between 1 and 200")
		}
		limit = n
	}
	args = append(args, limit+1)

	rows, err := s.Pool.Query(r.Context(), `SELECT `+eventSummaryCols+` FROM events WHERE `+
		strings.Join(where, " AND ")+` ORDER BY received_at DESC, id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []eventSummary{}
	for rows.Next() {
		var e eventSummary
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.WebhookID, &e.DedupKey, &e.Type, &e.Status, &e.Signature,
			&e.ContentType, &e.PayloadSize, &e.ReceivedAt, &e.ContractStatus); err != nil {
			return err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var next *string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		c := encodeCursor(last.ReceivedAt, last.ID)
		next = &c
	}
	if err := s.attachDeliverySummaries(r.Context(), out); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

type eventDetail struct {
	eventSummary
	Deliveries []deliveryView    `json:"deliveries"`
	Violations []violationView   `json:"violations"`  // this event's contract findings
	ContractID *string           `json:"contract_id"` // contract for this webhook + event type, if any
	Headers    map[string]string `json:"headers"`
	SourceIP   *string           `json:"source_ip"`
	// Exactly one of these is set, depending on what the payload is.
	PayloadJSON   json.RawMessage `json:"payload_json,omitempty"`
	PayloadText   *string         `json:"payload_text,omitempty"`
	PayloadBase64 *string         `json:"payload_base64,omitempty"`
}

// getEvent returns one event with headers and a masked payload.
func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "event")
	if err != nil {
		return err
	}
	var e eventDetail
	var headers, payload []byte
	err = s.Pool.QueryRow(r.Context(), `SELECT `+eventSummaryCols+`, headers, payload, host(source_ip)
		FROM events WHERE id = $1 AND org_id = $2`, id, orgID).
		Scan(&e.ID, &e.ProjectID, &e.WebhookID, &e.DedupKey, &e.Type, &e.Status, &e.Signature,
			&e.ContentType, &e.PayloadSize, &e.ReceivedAt, &e.ContractStatus, &headers, &payload, &e.SourceIP)
	if err != nil {
		return notFoundIfNoRows(err)
	}
	if err := json.Unmarshal(headers, &e.Headers); err != nil {
		return err
	}

	// The raw payload is kept for replay; the Explorer only ever sees it masked.
	masked := mask.JSON(payload)
	switch {
	case json.Valid(masked) && len(masked) > 0:
		e.PayloadJSON = masked
	case utf8.Valid(masked):
		t := string(masked)
		e.PayloadText = &t
	default:
		b := base64.StdEncoding.EncodeToString(masked)
		e.PayloadBase64 = &b
	}
	if e.Deliveries, err = s.queryDeliveries(r.Context(), "dl.event_id = $1 AND dl.org_id = $2 ORDER BY dl.created_at", id, orgID); err != nil {
		return err
	}
	e.Delivery = summarize(e.Deliveries)

	rows, err := s.Pool.Query(r.Context(), `
		SELECT event_id, severity, kind, path, expected, actual, created_at
		FROM contract_violations WHERE event_id = $1 AND org_id = $2 ORDER BY id`, id, orgID)
	if err != nil {
		return err
	}
	if e.Violations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[violationView]); err != nil {
		return err
	}
	err = s.Pool.QueryRow(r.Context(), `SELECT id FROM contracts WHERE webhook_id = $1 AND event_type = $2`,
		e.WebhookID, e.Type).Scan(&e.ContractID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	httpx.JSON(w, http.StatusOK, e)
	return nil
}

type hourBucket struct {
	Hour     time.Time `json:"hour"`
	Received int       `json:"received"`
	Rejected int       `json:"rejected"`
}

type webhookHealth struct {
	WebhookID      string     `json:"webhook_id"`
	Received       int        `json:"received"`
	Rejected       int        `json:"rejected"`
	LastReceivedAt *time.Time `json:"last_received_at"`
}

// eventStats powers the Overview page: hourly counts for the last 24 hours
// (every hour present, zeros included) and per-webhook health.
func (s *Server) eventStats(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	ctx := r.Context()

	rows, err := s.Pool.Query(ctx, `
		WITH hours AS (
			SELECT generate_series(date_trunc('hour', now()) - interval '23 hours',
			                       date_trunc('hour', now()), interval '1 hour') AS hour
		)
		SELECT h.hour,
		       count(e.id) FILTER (WHERE e.status = 'received'),
		       count(e.id) FILTER (WHERE e.status = 'rejected')
		FROM hours h
		LEFT JOIN events e ON e.org_id = $1
		                  AND e.received_at >= h.hour AND e.received_at < h.hour + interval '1 hour'
		GROUP BY h.hour ORDER BY h.hour`, orgID)
	if err != nil {
		return err
	}
	hours, err := pgx.CollectRows(rows, pgx.RowToStructByPos[hourBucket])
	if err != nil {
		return err
	}

	rows, err = s.Pool.Query(ctx, `
		SELECT w.id,
		       count(e.id) FILTER (WHERE e.status = 'received'),
		       count(e.id) FILTER (WHERE e.status = 'rejected'),
		       (SELECT max(received_at) FROM events l WHERE l.webhook_id = w.id AND l.org_id = $1)
		FROM webhooks w
		LEFT JOIN events e ON e.webhook_id = w.id AND e.org_id = $1
		                  AND e.received_at >= date_trunc('hour', now()) - interval '23 hours'
		WHERE w.org_id = $1
		GROUP BY w.id ORDER BY w.created_at`, orgID)
	if err != nil {
		return err
	}
	health, err := pgx.CollectRows(rows, pgx.RowToStructByPos[webhookHealth])
	if err != nil {
		return err
	}

	var total struct {
		Received int `json:"received"`
		Rejected int `json:"rejected"`
	}
	for _, h := range hours {
		total.Received += h.Received
		total.Rejected += h.Rejected
	}
	var fwd struct {
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
		InFlight  int `json:"in_progress"`
	}
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'succeeded' AND completed_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE status = 'failed' AND completed_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE status IN ('pending', 'retrying', 'in_flight'))
		FROM deliveries WHERE org_id = $1 AND created_at > now() - interval '7 days'`, orgID).
		Scan(&fwd.Succeeded, &fwd.Failed, &fwd.InFlight); err != nil {
		return err
	}
	var contracts struct {
		OpenIncidents int `json:"open_incidents"`
		Breaking24h   int `json:"breaking_24h"`
		Suspicious24h int `json:"suspicious_24h"`
	}
	if err := s.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM incidents WHERE org_id = $1 AND status = 'open'),
		       count(*) FILTER (WHERE severity = 'breaking'),
		       count(*) FILTER (WHERE severity = 'suspicious')
		FROM contract_violations WHERE org_id = $1 AND created_at > now() - interval '24 hours'`, orgID).
		Scan(&contracts.OpenIncidents, &contracts.Breaking24h, &contracts.Suspicious24h); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"hours": hours, "totals": total, "webhooks": health, "forwarded": fwd, "contracts": contracts})
	return nil
}

func encodeCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", err
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || !uuidRe.MatchString(id) {
		return time.Time{}, "", strconv.ErrSyntax
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	return t, id, err
}

func itoa(n int) string { return strconv.Itoa(n) }

// attachDeliverySummaries fills eventSummary.Delivery for a page of events.
func (s *Server) attachDeliverySummaries(ctx context.Context, events []eventSummary) error {
	if len(events) == 0 {
		return nil
	}
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	rows, err := s.Pool.Query(ctx, `SELECT event_id, status FROM deliveries WHERE event_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	byEvent := map[string][]deliveryView{}
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return err
		}
		byEvent[id] = append(byEvent[id], deliveryView{Status: status})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range events {
		events[i].Delivery = summarize(byEvent[events[i].ID])
	}
	return nil
}

// summarize reduces an event's deliveries to one state: failed wins, then
// pending (queued or retrying), then delivered; none when there are no destinations.
func summarize(ds []deliveryView) string {
	if len(ds) == 0 {
		return "none"
	}
	state := "delivered"
	for _, d := range ds {
		switch d.Status {
		case "failed":
			return "failed"
		case "pending", "retrying", "in_flight":
			state = "pending"
		}
	}
	return state
}
