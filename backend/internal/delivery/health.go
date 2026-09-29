package delivery

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"relaya/internal/alerts"
)

// trackHealth updates a destination's failure streak after a real attempt and
// queues one "failing" alert per outage (after alerts.FailingAfter failures in
// a row) and one "recovered" alert when it next succeeds.
func trackHealth(ctx context.Context, tx pgx.Tx, j job, outcome Outcome, res Result, errText string) error {
	var orgID, name, url, health string
	var streak int
	if outcome == Succeeded {
		err := tx.QueryRow(ctx, `
			UPDATE destinations d SET consecutive_failures = 0, health = 'ok'
			FROM (SELECT id, health FROM destinations WHERE id = $1 FOR UPDATE) old
			WHERE d.id = old.id
			RETURNING d.org_id, d.name, d.url, old.health`, j.DestinationID).Scan(&orgID, &name, &url, &health)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // destination deleted meanwhile
		}
		if err != nil || health != "failing" {
			return err
		}
		return alerts.Enqueue(ctx, tx, orgID, alerts.DestinationRecoveredAlert(j.DestinationID, name, url))
	}

	err := tx.QueryRow(ctx, `
		UPDATE destinations SET consecutive_failures = consecutive_failures + 1 WHERE id = $1
		RETURNING org_id, name, url, health, consecutive_failures`, j.DestinationID).
		Scan(&orgID, &name, &url, &health, &streak)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || health == "failing" || streak < alerts.FailingAfter {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE destinations SET health = 'failing' WHERE id = $1`, j.DestinationID); err != nil {
		return err
	}
	return alerts.Enqueue(ctx, tx, orgID, alerts.DestinationFailingAlert(j.DestinationID, name, url, res.StatusCode, errText, streak))
}
