package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"relaya/internal/alerts"
	"relaya/internal/audit"
	"relaya/internal/realtime"
)

// finishReplayDelivery records the final outcome of one replayed delivery.
// When the last delivery of a replay finishes, the replay completes; if every
// delivery succeeded, its incident is resolved as verified. Runs in the same
// transaction as the delivery update.
func finishReplayDelivery(ctx context.Context, tx pgx.Tx, j job, outcome Outcome) error {
	ok, bad := 0, 1
	if outcome == Succeeded {
		ok, bad = 1, 0
	}
	// Unlink so a later manual retry of this delivery isn't counted twice.
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET replay_id = NULL WHERE id = $1`, j.ID); err != nil {
		return err
	}
	var total, succeeded, failed int
	var incidentID *string
	err := tx.QueryRow(ctx, `
		UPDATE replays SET succeeded = succeeded + $2, failed = failed + $3
		WHERE id = $1 AND status = 'running'
		RETURNING total, succeeded, failed, incident_id`, *j.ReplayID, ok, bad).
		Scan(&total, &succeeded, &failed, &incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // replay already completed
	}
	if err != nil {
		return err
	}
	if succeeded+failed < total {
		return nil
	}

	if _, err := tx.Exec(ctx, `UPDATE replays SET status = 'completed', completed_at = now() WHERE id = $1`, *j.ReplayID); err != nil {
		return err
	}
	if failed == 0 && incidentID != nil {
		note := fmt.Sprintf("verified by replay: %d of %d deliveries accepted by the destination", succeeded, total)
		tag, err := tx.Exec(ctx, `
			UPDATE incidents SET status = 'resolved', resolved_at = now(), resolved_by = 'system', resolution = $2
			WHERE id = $1 AND status = 'open'`, *incidentID, note)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			if err := audit.Record(ctx, tx, audit.Entry{
				OrgID: j.OrgID, ActorType: "system", ActorID: "replay", Action: "incident.resolve",
				TargetType: "incident", TargetID: *incidentID, Reason: note,
				Metadata: map[string]any{"replay_id": *j.ReplayID},
			}); err != nil {
				return err
			}
			if err := alerts.NotifyIncidentsResolved(ctx, tx, []string{*incidentID}); err != nil {
				return err
			}
		}
	}
	return realtime.Notify(ctx, tx, realtime.Message{Type: "change", OrgID: j.OrgID, Action: "replay.completed", TargetID: *j.ReplayID})
}
