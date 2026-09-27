package syncs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/alerts"
	"relaya/internal/connect"
	"relaya/internal/db"
	"relaya/internal/ingest"
)

// Channel wakes the runner (a sync was created or "Run now" was pressed).
const Channel = "relaya_syncs"

// FailingAfter consecutive failed runs send a sync_failing alert.
const FailingAfter = 3

// Runner runs due syncs. Several workers may run; a sync runs on one at a time.
type Runner struct {
	Pool    *pgxpool.Pool
	Connect *connect.Service
	HTTP    *http.Client // proxy client (no redirects)
	// MaxRecords per run (default 5000); the rest waits for the next run.
	MaxRecords int
}

type syncRow struct {
	id, orgID, connID, webhookID, projectID, model, endUser, integrationKey, cursor string
	config                                                                          map[string]string
	interval                                                                        time.Duration
	emitExisting, baselineDone                                                      bool
	failures                                                                        int
}

// Run runs due syncs until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	wake := make(chan struct{}, 1)
	go db.Listen(ctx, r.Pool, Channel, wake)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	lastCleanup := time.Time{}
	for {
		if n, err := r.RunDue(ctx); err != nil && ctx.Err() == nil {
			slog.Error("run syncs", "err", err)
		} else if n > 0 {
			slog.Info("ran syncs", "count", n)
		}
		if time.Since(lastCleanup) > time.Hour {
			if _, err := r.Pool.Exec(ctx, `DELETE FROM sync_runs WHERE started_at < now() - interval '30 days'`); err != nil && ctx.Err() == nil {
				slog.Error("clean up sync runs", "err", err)
			}
			lastCleanup = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-tick.C:
		}
	}
}

// RunDue runs the syncs whose time has come and returns how many ran.
func (r *Runner) RunDue(ctx context.Context) (int, error) {
	n := 0
	for {
		id, ok, err := r.claim(ctx)
		if err != nil || !ok {
			return n, err
		}
		r.RunOne(ctx, id)
		n++
		if ctx.Err() != nil {
			return n, nil
		}
	}
}

// claim takes one due sync, so no other worker runs it meanwhile. A run that
// died (worker crash) is taken over after 15 minutes.
func (r *Runner) claim(ctx context.Context) (string, bool, error) {
	var id string
	err := r.Pool.QueryRow(ctx, `
		UPDATE syncs SET run_started_at = now() WHERE id = (
			SELECT id FROM syncs
			WHERE enabled AND next_run_at <= now() AND (run_started_at IS NULL OR run_started_at < now() - interval '15 minutes')
			ORDER BY next_run_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

// RunOne runs a claimed sync: fetch pages, compare each record with its last
// hash, and store an event per new or changed record. Records and events are
// written together per page, so a crash never loses or repeats a change.
func (r *Runner) RunOne(ctx context.Context, syncID string) {
	s, err := r.load(ctx, syncID)
	if err != nil {
		slog.Error("load sync", "sync", syncID, "err", err)
		_, _ = r.Pool.Exec(ctx, `UPDATE syncs SET run_started_at = NULL WHERE id = $1`, syncID)
		return
	}
	var runID int64
	if err := r.Pool.QueryRow(ctx, `INSERT INTO sync_runs (org_id, sync_id) VALUES ($1, $2) RETURNING id`, s.orgID, s.id).Scan(&runID); err != nil {
		slog.Error("start sync run", "sync", syncID, "err", err)
		return
	}

	fetched, created, updated, more, runErr := r.pull(ctx, s)

	status, errText := "ok", ""
	if runErr != nil {
		status, errText = "error", runErr.Error()
		if len(errText) > 500 {
			errText = errText[:500]
		}
	}
	next := time.Now().Add(s.interval)
	if more && runErr == nil {
		next = time.Now().Add(time.Minute) // hit the per-run limit: continue soon
	}
	err = pgx.BeginFunc(ctx, r.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE sync_runs SET finished_at = now(), status = $2, fetched = $3, created = $4, updated = $5, error = $6 WHERE id = $1`,
			runID, status, fetched, created, updated, errText); err != nil {
			return err
		}
		var failures int
		if err := tx.QueryRow(ctx, `
			UPDATE syncs SET run_started_at = NULL, last_run_at = now(), last_status = $2, last_error = $3, next_run_at = $4,
				consecutive_failures = CASE WHEN $2 = 'ok' THEN 0 ELSE consecutive_failures + 1 END,
				-- the baseline is only complete once a run got to the end
				baseline_done = baseline_done OR ($2 = 'ok' AND NOT $5),
				records = (SELECT count(*) FROM sync_records WHERE sync_id = $1), updated_at = now()
			WHERE id = $1 RETURNING consecutive_failures`, s.id, status, errText, next, more).Scan(&failures); err != nil {
			return err
		}
		// Broken connections already alert on their own.
		broken := errors.Is(runErr, connect.ErrBroken)
		name := s.integrationKey + " " + s.model + " for " + s.endUser
		switch {
		case runErr != nil && failures == FailingAfter && !broken:
			return alerts.Enqueue(ctx, tx, s.orgID, alerts.SyncFailingAlert(name, errText, failures))
		case runErr == nil && s.failures >= FailingAfter:
			return alerts.Enqueue(ctx, tx, s.orgID, alerts.SyncRecoveredAlert(name))
		}
		return nil
	})
	if err != nil {
		slog.Error("finish sync run", "sync", syncID, "err", err)
	}
	if runErr != nil {
		slog.Warn("sync run failed", "sync", syncID, "err", runErr)
	}
}

func (r *Runner) pull(ctx context.Context, s syncRow) (fetched, created, updated int, more bool, err error) {
	m, ok := GetModel(s.model)
	if !ok {
		return 0, 0, 0, false, fmt.Errorf("unknown sync model %s", s.model)
	}
	limit := r.MaxRecords
	if limit <= 0 {
		limit = 5000
	}
	f := &proxyFetcher{r: r, orgID: s.orgID, connID: s.connID, syncID: s.id}
	// Continue where the last run stopped. For models that look at everything,
	// a run that got to the end saved an empty cursor: the next starts over.
	cursor := s.cursor
	for page := 0; page < 200; page++ {
		p, err := m.Fetch(ctx, f, s.config, cursor)
		if err != nil {
			return fetched, created, updated, false, err
		}
		fetched += len(p.Records)
		c, u, err := r.store(ctx, s, m, p, cursor)
		if err != nil {
			return fetched, created, updated, false, err
		}
		created, updated = created+c, updated+u
		cursor = p.Cursor
		if !p.More {
			return fetched, created, updated, false, nil
		}
		if fetched >= limit {
			return fetched, created, updated, true, nil
		}
	}
	return fetched, created, updated, true, nil
}

// store compares a page's records with their last hashes and writes events
// for new and changed ones, plus the cursor, in one transaction.
func (r *Runner) store(ctx context.Context, s syncRow, m *Model, p Page, prevCursor string) (created, updated int, err error) {
	err = pgx.BeginFunc(ctx, r.Pool, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		events := 0
		for _, rec := range p.Records {
			if rec.ID == "" || len(rec.ID) > 300 {
				continue
			}
			sum := sha256.Sum256(canonical(rec.Data))
			hash := hex.EncodeToString(sum[:])
			var isNew bool
			err := tx.QueryRow(ctx, `
				INSERT INTO sync_records (sync_id, record_id, hash) VALUES ($1, $2, $3)
				ON CONFLICT (sync_id, record_id) DO UPDATE SET hash = EXCLUDED.hash, changed_at = now()
				WHERE sync_records.hash <> EXCLUDED.hash
				RETURNING (xmax = 0)`, s.id, rec.ID, hash).Scan(&isNew)
			if errors.Is(err, pgx.ErrNoRows) {
				continue // unchanged
			}
			if err != nil {
				return err
			}
			change := "updated"
			if isNew {
				change = "created"
				if !s.baselineDone && !s.emitExisting {
					continue // first run: remember what exists, don't announce it
				}
			}
			typ := m.EventType(s.config, change)
			payload, _ := json.Marshal(map[string]any{
				"type": typ, "change": change, "record_id": rec.ID, "record": rec.Data,
				"sync_id": s.id, "connection_id": s.connID, "end_user_id": s.endUser, "integration": s.integrationKey,
				"synced_at": now.Format(time.RFC3339),
			})
			headers, _ := json.Marshal(map[string]string{"relaya-sync": s.id, "relaya-sync-model": s.model})
			if _, _, err := ingest.StoreTx(ctx, tx, ingest.Event{
				OrgID: s.orgID, ProjectID: s.projectID, WebhookID: s.webhookID,
				DedupKey: "sync:" + s.id + ":" + rec.ID + ":" + hash[:16], Type: typ, Status: "received",
				Signature: "valid", ContentType: "application/json", Headers: headers, Payload: payload, ReceivedAt: now,
			}, true); err != nil {
				return err
			}
			events++
			if isNew {
				created++
			} else {
				updated++
			}
		}
		_, err := tx.Exec(ctx, `UPDATE syncs SET cursor = $2, events = events + $3 WHERE id = $1`, s.id, p.Cursor, events)
		return err
	})
	return created, updated, err
}

// canonical re-encodes JSON with sorted keys, so the hash only changes when
// the content does.
func canonical(raw []byte) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func (r *Runner) load(ctx context.Context, id string) (syncRow, error) {
	var s syncRow
	var cfg []byte
	var secs int
	err := r.Pool.QueryRow(ctx, `
		SELECT s.id, s.org_id, s.connection_id, s.webhook_id, w.project_id, s.model, c.end_user_id, i.key, s.cursor,
		       s.config, s.interval_seconds, s.emit_existing, s.baseline_done, s.consecutive_failures
		FROM syncs s JOIN connections c ON c.id = s.connection_id JOIN integrations i ON i.id = c.integration_id
		JOIN webhooks w ON w.id = s.webhook_id
		WHERE s.id = $1`, id).
		Scan(&s.id, &s.orgID, &s.connID, &s.webhookID, &s.projectID, &s.model, &s.endUser, &s.integrationKey, &s.cursor,
			&cfg, &secs, &s.emitExisting, &s.baselineDone, &s.failures)
	s.config = map[string]string{}
	_ = json.Unmarshal(cfg, &s.config)
	s.interval = time.Duration(secs) * time.Second
	return s, err
}

// proxyFetcher makes a sync's API calls through the proxy (token, renewal,
// retries, host allowlist) and logs them like any proxy call.
type proxyFetcher struct {
	r                     *Runner
	orgID, connID, syncID string
}

func (f *proxyFetcher) Call(ctx context.Context, method, base, path string, query url.Values, body any, header map[string]string) (int, []byte, error) {
	var b []byte
	h := http.Header{"Accept": {"application/json"}}
	if body != nil {
		b, _ = json.Marshal(body)
		h.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		h.Set(k, v)
	}
	start := time.Now()
	res, err := f.r.Connect.Proxy(ctx, f.r.HTTP, f.orgID, f.connID, connect.ProxyRequest{
		Method: method, Path: path, RawQuery: query.Encode(), BaseURL: base, Header: h, Body: b,
	})
	status, attempts, host, errText := 0, 0, "", ""
	var raw []byte
	if err == nil {
		defer res.Resp.Body.Close()
		raw, err = io.ReadAll(io.LimitReader(res.Resp.Body, 50<<20))
		status, attempts, host = res.Resp.StatusCode, res.Attempts, res.Host
	}
	if err != nil {
		errText = err.Error()
	}
	_, _ = f.r.Pool.Exec(ctx, `
		INSERT INTO proxy_calls (org_id, connection_id, method, host, path, status, attempts, duration_ms, error, actor)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		f.orgID, f.connID, method, host, "/"+strings.TrimLeft(path, "/"), status, attempts, time.Since(start).Milliseconds(), errText, "sync:"+f.syncID)
	return status, raw, err
}

// Wake tells running workers to look for due syncs now (after the caller's
// transaction commits).
func Wake(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, Channel)
	return err
}
