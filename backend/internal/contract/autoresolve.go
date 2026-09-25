package contract

import (
	"context"
	"fmt"
	"time"

	"relaya/internal/alerts"
	"relaya/internal/audit"
)

// AutoResolve closes open incidents that have provably stopped: no occurrence
// for `after`, AND at least one later event of the same contract was checked
// without this problem. If the provider simply went quiet we can't tell it's
// fixed, so those incidents stay open. Returns how many were resolved.
func (c *Checker) AutoResolve(ctx context.Context, after time.Duration) (int, error) {
	if after <= 0 {
		return 0, nil
	}
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	note := fmt.Sprintf("stopped happening: no new occurrences for %s and later events matched the contract", humanDuration(after))
	rows, err := tx.Query(ctx, `
		UPDATE incidents i
		SET status = 'resolved', resolved_at = now(), resolved_by = 'system', resolution = $2
		FROM contracts c
		WHERE c.id = i.contract_id AND i.status = 'open'
		  AND i.last_seen_at < now() - $1::interval
		  AND EXISTS (
		      SELECT 1 FROM events e
		      WHERE e.webhook_id = i.webhook_id AND e.type = c.event_type
		        AND e.received_at > i.last_seen_at
		        AND e.contract_status IN ('ok', 'compatible', 'suspicious', 'breaking')
		        AND NOT EXISTS (
		            SELECT 1 FROM contract_violations v
		            WHERE v.event_id = e.id AND v.kind = i.kind AND v.path = i.path))
		RETURNING i.id, i.org_id`,
		fmt.Sprintf("%d seconds", int(after.Seconds())), note)
	if err != nil {
		return 0, err
	}
	type resolved struct{ id, org string }
	var done []resolved
	for rows.Next() {
		var r resolved
		if err := rows.Scan(&r.id, &r.org); err != nil {
			rows.Close()
			return 0, err
		}
		done = append(done, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	// Audit (which also pushes a realtime "change" so dashboards update).
	ids := make([]string, 0, len(done))
	for _, r := range done {
		if err := audit.Record(ctx, tx, audit.Entry{
			OrgID: r.org, ActorType: "system", ActorID: "auto-resolve", Action: "incident.resolve",
			TargetType: "incident", TargetID: r.id, Reason: note,
		}); err != nil {
			return 0, err
		}
		ids = append(ids, r.id)
	}
	if err := alerts.NotifyIncidentsResolved(ctx, tx, ids); err != nil {
		return 0, err
	}
	return len(done), tx.Commit(ctx)
}

func humanDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		if d == time.Hour {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d%time.Minute == 0:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	return d.String()
}
