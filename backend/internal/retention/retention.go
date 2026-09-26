// Package retention deletes data that has outlived its retention period:
// event payloads with their deliveries and contract findings, the alert log
// and expired sessions. Incidents, contracts and the audit log are kept.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Policy is how long each kind of data is kept.
type Policy struct {
	Events time.Duration // payloads, deliveries (with attempts) and contract findings
	Alerts time.Duration // the alert log
}

// Stats reports what one run removed.
type Stats struct {
	PartitionsDropped []string
	Events            int64 // rows deleted outside dropped partitions
	Deliveries        int64
	Violations        int64
	Sessions          int64
	Alerts            int64
}

func (s Stats) Empty() bool {
	return len(s.PartitionsDropped) == 0 && s.Events+s.Deliveries+s.Violations+s.Sessions+s.Alerts == 0
}

const batch = 5000

// Run applies the policy now and then every interval until ctx is done.
func Run(ctx context.Context, pool *pgxpool.Pool, p Policy, every time.Duration) {
	run := func() {
		st, err := RunOnce(ctx, pool, time.Now(), p)
		if err != nil && ctx.Err() == nil {
			slog.Error("retention", "err", err)
			return
		}
		if !st.Empty() {
			slog.Info("retention", "partitions_dropped", st.PartitionsDropped, "events", st.Events, "deliveries", st.Deliveries,
				"violations", st.Violations, "sessions", st.Sessions, "alerts", st.Alerts)
		}
	}
	run()
	t := time.NewTicker(every)
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

// RunOnce deletes everything older than the policy allows, as of now.
func RunOnce(ctx context.Context, pool *pgxpool.Pool, now time.Time, p Policy) (Stats, error) {
	var st Stats
	var err error
	if p.Events > 0 {
		cutoff := now.Add(-p.Events)
		// Rows that point at events (no foreign keys into a partitioned table).
		if st.Deliveries, err = deleteBatches(ctx, pool, `
			DELETE FROM deliveries WHERE id IN (SELECT id FROM deliveries WHERE event_received_at < $1 LIMIT $2)`, cutoff); err != nil {
			return st, fmt.Errorf("deliveries: %w", err)
		}
		if st.Violations, err = deleteBatches(ctx, pool, `
			DELETE FROM contract_violations WHERE id IN (SELECT id FROM contract_violations WHERE event_received_at < $1 LIMIT $2)`, cutoff); err != nil {
			return st, fmt.Errorf("contract findings: %w", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM contract_queue WHERE event_received_at < $1`, cutoff); err != nil {
			return st, fmt.Errorf("contract queue: %w", err)
		}
		// Whole months past the cutoff: drop the partition (instant, no bloat).
		if st.PartitionsDropped, err = dropPartitions(ctx, pool, cutoff); err != nil {
			return st, fmt.Errorf("partitions: %w", err)
		}
		// The rest (the month the cutoff falls in, and the default partition).
		if st.Events, err = deleteBatches(ctx, pool, `
			DELETE FROM events WHERE (id, received_at) IN (SELECT id, received_at FROM events WHERE received_at < $1 LIMIT $2)`, cutoff); err != nil {
			return st, fmt.Errorf("events: %w", err)
		}
		// Incidents stay, but their sample event may be gone.
		if _, err := pool.Exec(ctx, `
			UPDATE incidents i SET sample_event_id = NULL
			WHERE sample_event_id IS NOT NULL AND last_seen_at < $1
			  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.id = i.sample_event_id)`, cutoff); err != nil {
			return st, fmt.Errorf("incident samples: %w", err)
		}
	}
	if p.Alerts > 0 {
		if st.Alerts, err = deleteBatches(ctx, pool, `
			DELETE FROM alerts WHERE id IN (SELECT id FROM alerts WHERE created_at < $1 AND status <> 'pending' LIMIT $2)`, now.Add(-p.Alerts)); err != nil {
			return st, fmt.Errorf("alerts: %w", err)
		}
	}
	tag, err := pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, now)
	if err != nil {
		return st, fmt.Errorf("sessions: %w", err)
	}
	st.Sessions = tag.RowsAffected()
	return st, nil
}

// deleteBatches runs a "... LIMIT $2" delete until it removes nothing, so no
// single statement holds locks on a large number of rows.
func deleteBatches(ctx context.Context, pool *pgxpool.Pool, sql string, cutoff time.Time) (int64, error) {
	var total int64
	for {
		tag, err := pool.Exec(ctx, sql, cutoff, batch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			return total, nil
		}
	}
}

// dropPartitions drops monthly event partitions (events_YYYY_MM) that end at
// or before the cutoff. Bounds are computed in SQL, in the database's time
// zone, exactly as ensure_event_partitions created them.
func dropPartitions(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'events'::regclass
		  AND c.relname ~ '^events_[0-9]{4}_[0-9]{2}$'
		  AND (to_date(substr(c.relname, 8), 'YYYY_MM') + interval '1 month')::timestamptz <= $1
		ORDER BY c.relname`, cutoff)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP TABLE %s`, pgx.Identifier{n}.Sanitize())); err != nil {
			return nil, err
		}
	}
	return names, nil
}
