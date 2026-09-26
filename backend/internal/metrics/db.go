package metrics

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DBCollector reports queue depth and lag, recent throughput and backlog from
// the database. Queue lag is the key signal that a worker is stuck or down.
func DBCollector(pool *pgxpool.Pool) Collector {
	return func(ctx context.Context) ([]Gauge, error) {
		var out []Gauge
		add := func(name, help string, labels map[string]string, v float64) {
			out = append(out, Gauge{Name: name, Help: help, Labels: labels, Value: v})
		}

		rows, err := pool.Query(ctx, `SELECT status, count(*) FROM deliveries WHERE status IN ('pending', 'retrying', 'in_flight') GROUP BY status`)
		if err != nil {
			return nil, err
		}
		depth := map[string]float64{"pending": 0, "retrying": 0, "in_flight": 0}
		for rows.Next() {
			var s string
			var n float64
			if err := rows.Scan(&s, &n); err != nil {
				rows.Close()
				return nil, err
			}
			depth[s] = n
		}
		rows.Close()
		for s, n := range depth {
			add("relaya_delivery_queue", "Deliveries waiting or in progress, by status.", map[string]string{"status": s}, n)
		}

		var deliveryLag, contractDepth, contractLag, alertsPending, openIncidents, dbBytes float64
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(extract(epoch FROM now() - min(next_attempt_at)), 0)
			FROM deliveries WHERE status IN ('pending', 'retrying') AND next_attempt_at <= now()`).Scan(&deliveryLag); err != nil {
			return nil, err
		}
		if err := pool.QueryRow(ctx, `
			SELECT count(*), coalesce(extract(epoch FROM now() - min(enqueued_at)), 0) FROM contract_queue`).Scan(&contractDepth, &contractLag); err != nil {
			return nil, err
		}
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM alerts WHERE status = 'pending'),
			       (SELECT count(*) FROM incidents WHERE status = 'open'),
			       pg_database_size(current_database())`).Scan(&alertsPending, &openIncidents, &dbBytes); err != nil {
			return nil, err
		}
		add("relaya_delivery_lag_seconds", "How overdue the oldest due delivery is (grows when the worker is stuck).", nil, deliveryLag)
		add("relaya_contract_queue", "Events waiting for a contract check.", nil, contractDepth)
		add("relaya_contract_lag_seconds", "Age of the oldest event waiting for a contract check.", nil, contractLag)
		add("relaya_alerts_pending", "Alerts waiting to be sent.", nil, alertsPending)
		add("relaya_incidents_open", "Open incidents across all orgs.", nil, openIncidents)
		add("relaya_database_size_bytes", "Size of the database on disk.", nil, dbBytes)

		rows, err = pool.Query(ctx, `
			SELECT status, count(*) FROM events WHERE received_at > now() - interval '5 minutes' GROUP BY status`)
		if err != nil {
			return nil, err
		}
		events := map[string]float64{"received": 0, "rejected": 0}
		for rows.Next() {
			var s string
			var n float64
			if err := rows.Scan(&s, &n); err != nil {
				rows.Close()
				return nil, err
			}
			events[s] = n
		}
		rows.Close()
		for s, n := range events {
			add("relaya_events_5m", "Events ingested in the last 5 minutes, by status.", map[string]string{"status": s}, n)
		}

		rows, err = pool.Query(ctx, `
			SELECT outcome, count(*) FROM delivery_attempts WHERE started_at > now() - interval '5 minutes' GROUP BY outcome`)
		if err != nil {
			return nil, err
		}
		attempts := map[string]float64{"succeeded": 0, "retry": 0, "failed": 0}
		for rows.Next() {
			var s string
			var n float64
			if err := rows.Scan(&s, &n); err != nil {
				rows.Close()
				return nil, err
			}
			attempts[s] = n
		}
		rows.Close()
		for s, n := range attempts {
			add("relaya_delivery_attempts_5m", "Delivery attempts in the last 5 minutes, by outcome.", map[string]string{"outcome": s}, n)
		}
		return out, nil
	}
}
