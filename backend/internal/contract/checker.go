package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/db"
	"relaya/internal/realtime"
)

// QueueChannel is notified by ingest when events are queued for checking.
const QueueChannel = "relaya_contracts"

// Checker learns contracts and checks events. It runs in the worker process,
// off the ingest path, one event per transaction (safe with several workers).
type Checker struct {
	Pool        *pgxpool.Pool
	MinSamples  int           // propose after this many samples…
	LearnWindow time.Duration // …or after this long with at least 3 samples
	PollEvery   time.Duration
	// Close incidents that stopped this long ago and were followed by a clean event (0 = off).
	AutoResolveAfter time.Duration

	mu       sync.Mutex
	versions map[string]*version // "contractID:version" -> immutable version
}

type version struct {
	schema   *Schema
	critical map[string]bool
}

func (c *Checker) defaults() {
	if c.MinSamples <= 0 {
		c.MinSamples = 20
	}
	if c.LearnWindow <= 0 {
		c.LearnWindow = 24 * time.Hour
	}
	if c.PollEvery <= 0 {
		c.PollEvery = time.Second
	}
}

// Run processes the queue until ctx is done.
func (c *Checker) Run(ctx context.Context) {
	c.defaults()
	wake := make(chan struct{}, 1)
	go db.Listen(ctx, c.Pool, QueueChannel, wake)
	t := time.NewTicker(c.PollEvery)
	defer t.Stop()
	sweep := time.NewTicker(time.Minute)
	defer sweep.Stop()
	for {
		n, err := c.RunOnce(ctx, 100)
		if err != nil && ctx.Err() == nil {
			slog.Error("contract check", "err", err)
		}
		if n == 100 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-t.C:
		case <-sweep.C:
			if n, err := c.AutoResolve(ctx, c.AutoResolveAfter); err != nil && ctx.Err() == nil {
				slog.Error("auto-resolve incidents", "err", err)
			} else if n > 0 {
				slog.Info("auto-resolved incidents", "count", n)
			}
		}
	}
}

// RunOnce checks up to max queued events and returns how many it processed.
func (c *Checker) RunOnce(ctx context.Context, max int) (int, error) {
	c.defaults()
	n := 0
	for n < max {
		found, err := c.processNext(ctx)
		if err != nil || !found {
			return n, err
		}
		n++
	}
	return n, nil
}

type queued struct {
	EventID    string
	ReceivedAt time.Time
	OrgID      string
	WebhookID  string
	EventType  string
}

func (c *Checker) processNext(ctx context.Context) (found bool, err error) {
	err = pgx.BeginFunc(ctx, c.Pool, func(tx pgx.Tx) error {
		var q queued
		err := tx.QueryRow(ctx, `
			SELECT event_id, event_received_at, org_id, webhook_id, event_type FROM contract_queue
			ORDER BY enqueued_at LIMIT 1 FOR UPDATE SKIP LOCKED`).
			Scan(&q.EventID, &q.ReceivedAt, &q.OrgID, &q.WebhookID, &q.EventType)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true

		var payload []byte
		status, contractID := "none", ""
		err = tx.QueryRow(ctx, `SELECT payload FROM events WHERE id = $1 AND received_at = $2`, q.EventID, q.ReceivedAt).Scan(&payload)
		switch {
		case errors.Is(err, pgx.ErrNoRows): // event deleted meanwhile
		case err != nil:
			return err
		default:
			if status, contractID, err = c.check(ctx, tx, q, payload); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE events SET contract_status = $3 WHERE id = $1 AND received_at = $2`,
				q.EventID, q.ReceivedAt, status); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM contract_queue WHERE event_id = $1`, q.EventID); err != nil {
			return err
		}
		return realtime.Notify(ctx, tx, realtime.Message{
			Type: "contract", OrgID: q.OrgID, WebhookID: q.WebhookID, EventID: q.EventID, Status: status, TargetID: contractID,
		})
	})
	return found, err
}

// check learns from and checks one event; returns the event's contract status.
func (c *Checker) check(ctx context.Context, tx pgx.Tx, q queued, payload []byte) (string, string, error) {
	o, ok := Observe(payload)
	if !ok {
		return "none", "", nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO contracts (org_id, webhook_id, event_type) VALUES ($1, $2, $3)
		ON CONFLICT (webhook_id, event_type) DO NOTHING`, q.OrgID, q.WebhookID, q.EventType); err != nil {
		return "", "", err
	}
	var (
		id, status         string
		observedRaw, nfRaw []byte
		activeVersion      *int
		firstSeen          time.Time
	)
	// FOR UPDATE serializes learning per contract across workers.
	if err := tx.QueryRow(ctx, `
		SELECT id, status, observed, new_fields, active_version, first_seen_at FROM contracts
		WHERE webhook_id = $1 AND event_type = $2 FOR UPDATE`, q.WebhookID, q.EventType).
		Scan(&id, &status, &observedRaw, &nfRaw, &activeVersion, &firstSeen); err != nil {
		return "", "", err
	}

	observed := NewSchema()
	if err := json.Unmarshal(observedRaw, observed); err != nil {
		return "", "", err
	}
	if observed.Fields == nil {
		observed.Fields = map[string]*Field{}
	}
	observed.Merge(o)
	if status == "learning" &&
		(observed.Samples >= c.MinSamples || (time.Since(firstSeen) >= c.LearnWindow && observed.Samples >= 3)) {
		status = "proposed"
	}

	eventStatus := "learning"
	newFields := map[string]map[string]any{}
	_ = json.Unmarshal(nfRaw, &newFields)

	if status == "active" && activeVersion != nil {
		v, err := c.version(ctx, tx, id, *activeVersion)
		if err != nil {
			return "", "", err
		}
		findings, sev := Check(v.schema, v.critical, o)
		eventStatus = sev.String()
		for _, f := range findings {
			switch f.Severity {
			case Compatible:
				nf := newFields[f.Path]
				if nf == nil {
					nf = map[string]any{"first_seen": time.Now().UTC(), "count": 0.0}
					newFields[f.Path] = nf
				}
				count, _ := nf["count"].(float64)
				nf["count"] = count + 1
				nf["types"] = f.Actual
			case Suspicious, Breaking:
				if err := c.recordFinding(ctx, tx, q, id, *activeVersion, f); err != nil {
					return "", "", err
				}
			}
		}
	}

	obsJSON, err := json.Marshal(observed)
	if err != nil {
		return "", "", err
	}
	nfJSON, err := json.Marshal(newFields)
	if err != nil {
		return "", "", err
	}
	_, err = tx.Exec(ctx, `
		UPDATE contracts SET observed = $2, new_fields = $3, status = $4, last_seen_at = now(), updated_at = now()
		WHERE id = $1`, id, obsJSON, nfJSON, status)
	return eventStatus, id, err
}

func (c *Checker) recordFinding(ctx context.Context, tx pgx.Tx, q queued, contractID string, ver int, f Finding) error {
	sev := f.Severity.String()
	if _, err := tx.Exec(ctx, `
		INSERT INTO contract_violations (org_id, contract_id, version, event_id, event_received_at, severity, kind, path, expected, actual)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		q.OrgID, contractID, ver, q.EventID, q.ReceivedAt, sev, f.Kind, f.Path, f.Expected, f.Actual); err != nil {
		return err
	}
	if f.Severity != Breaking {
		return nil
	}
	// One open incident per (contract, kind, path); repeats bump its count.
	_, err := tx.Exec(ctx, `
		INSERT INTO incidents (org_id, webhook_id, contract_id, kind, path, title, expected, actual, event_count, sample_event_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9)
		ON CONFLICT (contract_id, kind, path) WHERE status = 'open'
		DO UPDATE SET event_count = incidents.event_count + 1, last_seen_at = now(),
		              actual = EXCLUDED.actual, sample_event_id = EXCLUDED.sample_event_id`,
		q.OrgID, q.WebhookID, contractID, f.Kind, f.Path, Title(q.EventType, f), f.Expected, f.Actual, q.EventID)
	return err
}

// Title is the one-line incident summary shown in alerts and the dashboard.
func Title(eventType string, f Finding) string {
	switch f.Kind {
	case KindMissingField:
		s := fmt.Sprintf("%s: critical field %s is missing", eventType, f.Path)
		if f.Nested > 0 {
			s += fmt.Sprintf(" (with %d nested fields)", f.Nested)
		}
		return s
	case KindTypeChanged:
		return fmt.Sprintf("%s: %s changed type from %s to %s", eventType, f.Path, f.Expected, f.Actual)
	case KindNullValue:
		return fmt.Sprintf("%s: %s is null (expected %s)", eventType, f.Path, f.Expected)
	}
	return fmt.Sprintf("%s: %s (%s)", eventType, f.Path, f.Kind)
}

// version loads an immutable contract version, cached in memory.
func (c *Checker) version(ctx context.Context, tx pgx.Tx, contractID string, ver int) (*version, error) {
	key := fmt.Sprintf("%s:%d", contractID, ver)
	c.mu.Lock()
	if c.versions == nil {
		c.versions = map[string]*version{}
	}
	v := c.versions[key]
	c.mu.Unlock()
	if v != nil {
		return v, nil
	}
	var raw []byte
	var critical []string
	if err := tx.QueryRow(ctx, `SELECT schema, critical_fields FROM contract_versions WHERE contract_id = $1 AND version = $2`,
		contractID, ver).Scan(&raw, &critical); err != nil {
		return nil, fmt.Errorf("load contract version: %w", err)
	}
	v = &version{schema: NewSchema(), critical: map[string]bool{}}
	if err := json.Unmarshal(raw, v.schema); err != nil {
		return nil, err
	}
	for _, p := range critical {
		v.critical[p] = true
	}
	c.mu.Lock()
	c.versions[key] = v
	c.mu.Unlock()
	return v, nil
}
