package delivery

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
	"relaya/internal/repair"
	"relaya/internal/vault"
)

// NotifyChannel is the Postgres channel ingest notifies when deliveries are queued.
const NotifyChannel = "relaya_deliveries"

// leaseDuration is how long a claimed delivery stays in_flight before another
// worker may take it over (covers a worker crashing mid-attempt).
const leaseDuration = 5 * time.Minute

// Worker claims due deliveries from Postgres (FOR UPDATE SKIP LOCKED, so any
// number of workers can run), sends them and records the outcome.
type Worker struct {
	Pool        *pgxpool.Pool
	Vault       vault.Vault
	Sender      *Sender
	Concurrency int
	PollEvery   time.Duration
}

// Run processes deliveries until ctx is cancelled, then waits for in-flight
// attempts to finish.
func (w *Worker) Run(ctx context.Context) {
	if w.Concurrency <= 0 {
		w.Concurrency = 8
	}
	if w.PollEvery <= 0 {
		w.PollEvery = time.Second
	}
	wake := make(chan struct{}, 1)
	go db.Listen(ctx, w.Pool, NotifyChannel, wake) // deliveries go out within milliseconds

	sem := make(chan struct{}, w.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()

	ticker := time.NewTicker(w.PollEvery)
	defer ticker.Stop()
	for {
		free := w.Concurrency - len(sem)
		claimed := 0
		if free > 0 {
			jobs, err := w.claim(ctx, free)
			if err != nil && ctx.Err() == nil {
				slog.Error("claim deliveries", "err", err)
			}
			claimed = len(jobs)
			for _, j := range jobs {
				sem <- struct{}{}
				wg.Add(1)
				go func() {
					defer func() { <-sem; wg.Done() }()
					w.process(context.WithoutCancel(ctx), j)
				}()
			}
		}
		if claimed == free && free > 0 {
			continue // there may be more due right now
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}

// RunOnce claims and processes up to n due deliveries synchronously (tests, tools).
func (w *Worker) RunOnce(ctx context.Context, n int) (int, error) {
	jobs, err := w.claim(ctx, n)
	for _, j := range jobs {
		w.process(ctx, j)
	}
	return len(jobs), err
}

type job struct {
	ID            string
	OrgID         string
	EventID       string
	EventReceived time.Time
	DestinationID string
	Attempt       int
	ReplayID      *string // set when this attempt belongs to an incident replay
}

func (w *Worker) claim(ctx context.Context, n int) ([]job, error) {
	rows, err := w.Pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM deliveries
			WHERE (status IN ('pending', 'retrying') AND next_attempt_at <= now())
			   OR (status = 'in_flight' AND locked_until < now())
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE deliveries d
		SET status = 'in_flight', locked_until = now() + $2::interval,
		    attempts = d.attempts + 1, last_attempt_at = now()
		FROM due WHERE d.id = due.id
		RETURNING d.id, d.org_id, d.event_id, d.event_received_at, d.destination_id, d.attempts, d.replay_id`,
		n, fmt.Sprintf("%d seconds", int(leaseDuration.Seconds())))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[job])
}

type target struct {
	URL         string
	Enabled     bool
	SecretEnc   []byte
	TimeoutMS   int
	MaxAttempts int
	Scheme      string
}

// Load fetches the destination and event for a delivery.
func (w *Worker) load(ctx context.Context, j job) (target, Request, error) {
	var t target
	err := w.Pool.QueryRow(ctx, `
		SELECT url, enabled, signing_secret_enc, timeout_ms, max_attempts, signing_scheme
		FROM destinations WHERE id = $1`, j.DestinationID).
		Scan(&t.URL, &t.Enabled, &t.SecretEnc, &t.TimeoutMS, &t.MaxAttempts, &t.Scheme)
	if err != nil {
		return t, Request{}, fmt.Errorf("load destination: %w", err)
	}

	r := Request{EventID: j.EventID, DeliveryID: j.ID, Attempt: j.Attempt, URL: t.URL, Timeout: time.Duration(t.TimeoutMS) * time.Millisecond, Scheme: t.Scheme}
	if j.ReplayID != nil {
		r.ReplayID = *j.ReplayID
	}
	var headers []byte
	var webhookID string
	err = w.Pool.QueryRow(ctx, `
		SELECT webhook_id, type, content_type, headers, payload FROM events
		WHERE id = $1 AND received_at = $2`, j.EventID, j.EventReceived).
		Scan(&webhookID, &r.EventType, &r.ContentType, &headers, &r.Body)
	if err != nil {
		return t, r, fmt.Errorf("load event: %w", err)
	}
	// Repair rules run at send time, so retries and replays use the current rules.
	rules, err := repair.Load(ctx, w.Pool, webhookID, r.EventType)
	if err != nil {
		return t, r, fmt.Errorf("load repair rules: %w", err)
	}
	if len(rules) > 0 {
		res, err := repair.Apply(r.Body, r.EventType, rules)
		if err != nil {
			return t, r, fmt.Errorf("apply repair rules: %w", err)
		}
		if res.Changed {
			r.Body = res.Body
			r.RepairedIDs = res.Applied
			r.RepairedNames = repair.Names(rules, res.Applied)
		}
	}
	if err := json.Unmarshal(headers, &r.Headers); err != nil {
		return t, r, err
	}
	if r.Secret, err = w.Vault.Decrypt(ctx, j.OrgID, t.SecretEnc); err != nil {
		return t, r, fmt.Errorf("decrypt destination secret: %w", err)
	}
	return t, r, nil
}

func (w *Worker) process(ctx context.Context, j job) {
	started := time.Now()
	t, req, err := w.load(ctx, j)
	var res Result
	sent := false // a real HTTP attempt was made (counts toward destination health)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		res = Result{Err: errors.New("event or destination no longer exists")}
	case err != nil:
		slog.Error("load delivery", "delivery", j.ID, "err", err)
		res = Result{Err: errors.New("internal error preparing the delivery")}
	case !t.Enabled:
		res = Result{Err: errors.New("destination is disabled")}
	default:
		res = w.Sender.Send(ctx, req)
		sent = true
	}

	outcome := Classify(res)
	if (err != nil || !t.Enabled) && outcome == Retry {
		outcome = Failed // nothing to retry against
	}
	if outcome == Retry && j.Attempt >= max(t.MaxAttempts, 1) {
		outcome = Failed
	}
	if err := w.record(ctx, j, started, res, outcome, sent, req); err != nil {
		slog.Error("record delivery attempt", "delivery", j.ID, "err", err)
	}
}

func (w *Worker) record(ctx context.Context, j job, started time.Time, res Result, outcome Outcome, sent bool, req Request) error {
	errText := ""
	if res.Err != nil {
		errText = res.Err.Error()
	}
	var code *int
	if res.StatusCode != 0 {
		code = &res.StatusCode
	}
	return pgx.BeginFunc(ctx, w.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO delivery_attempts (delivery_id, attempt, started_at, duration_ms, status_code, error, response_body, outcome, replay_id, repaired_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			j.ID, j.Attempt, started, res.Duration.Milliseconds(), code, errText, res.Body, string(outcome), j.ReplayID, nonNil(req.RepairedNames)); err != nil {
			return err
		}
		if sent && len(req.RepairedIDs) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE repair_rules SET applied_count = applied_count + 1, last_applied_at = now()
				WHERE id = ANY($1)`, req.RepairedIDs); err != nil {
				return err
			}
		}
		var status string
		var err error
		switch outcome {
		case Succeeded:
			status = "succeeded"
			_, err = tx.Exec(ctx, `
				UPDATE deliveries SET status = 'succeeded', locked_until = NULL, completed_at = now(),
				       last_status_code = $2, last_error = '' WHERE id = $1`, j.ID, code)
		case Retry:
			status = "retrying"
			delay := NextDelay(j.Attempt, res.RetryAfter)
			_, err = tx.Exec(ctx, `
				UPDATE deliveries SET status = 'retrying', locked_until = NULL,
				       next_attempt_at = now() + $4::interval, last_status_code = $2, last_error = $3
				WHERE id = $1`, j.ID, code, errText, fmt.Sprintf("%d milliseconds", delay.Milliseconds()))
		default:
			status = "failed"
			_, err = tx.Exec(ctx, `
				UPDATE deliveries SET status = 'failed', locked_until = NULL, completed_at = now(),
				       last_status_code = $2, last_error = $3 WHERE id = $1`, j.ID, code, errText)
		}
		if err != nil {
			return err
		}
		if sent {
			if err := trackHealth(ctx, tx, j, outcome, res, errText); err != nil {
				return err
			}
		}
		if j.ReplayID != nil && outcome != Retry {
			if err := finishReplayDelivery(ctx, tx, j, outcome); err != nil {
				return err
			}
		}
		return realtime.Notify(ctx, tx, realtime.Message{
			Type: "delivery", OrgID: j.OrgID, EventID: j.EventID, DeliveryID: j.ID, DestinationID: j.DestinationID, Status: status,
		})
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
