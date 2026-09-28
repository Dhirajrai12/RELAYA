package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
)

// affectedEvents selects the events an incident covers: those with a finding of
// the same kind and path since the incident was first seen. Events a repair
// rule already fixed before forwarding are left out. Params: $1 incident id.
const affectedEvents = `
	SELECT DISTINCT v.event_id, v.event_received_at
	FROM incidents i
	JOIN contract_violations v ON v.contract_id = i.contract_id AND v.kind = i.kind AND v.path = i.path
	WHERE i.id = $1 AND NOT v.repaired AND v.created_at >= i.first_seen_at - interval '1 second'`

type replayPlanRow struct {
	DestinationID    string `json:"destination_id"`
	DestinationName  string `json:"destination_name"`
	DestinationURL   string `json:"destination_url"`
	Enabled          bool   `json:"enabled"`
	Deliveries       int    `json:"deliveries"`        // (event, destination) pairs
	AlreadySucceeded int    `json:"already_succeeded"` // will be sent again, same Idempotency-Key
	InFlight         int    `json:"in_flight"`         // being sent right now: skipped
}

type replayPlan struct {
	Events       int             `json:"events"`
	Destinations []replayPlanRow `json:"destinations"`
	WillSend     int             `json:"will_send"` // deliveries a replay would queue
}

// planReplay is the dry run: what a replay would send, without changing anything.
func (s *Server) planReplay(ctx context.Context, q pgx.Tx, incidentID string) (replayPlan, error) {
	var p replayPlan
	if err := q.QueryRow(ctx, `SELECT count(*) FROM (`+affectedEvents+`) ev`, incidentID).Scan(&p.Events); err != nil {
		return p, err
	}
	rows, err := q.Query(ctx, `
		WITH ev AS (`+affectedEvents+`)
		SELECT d.id, d.name, d.url, d.enabled, count(*),
		       count(dl.id) FILTER (WHERE dl.status = 'succeeded'),
		       count(dl.id) FILTER (WHERE dl.status = 'in_flight')
		FROM ev
		CROSS JOIN destinations d
		LEFT JOIN deliveries dl ON dl.event_id = ev.event_id AND dl.destination_id = d.id
		WHERE d.webhook_id = (SELECT webhook_id FROM incidents WHERE id = $1) AND (cardinality(d.event_types) = 0 OR (SELECT c.event_type FROM incidents i2 JOIN contracts c ON c.id = i2.contract_id WHERE i2.id = $1) = ANY(d.event_types))
		GROUP BY d.id, d.name, d.url, d.enabled, d.created_at
		ORDER BY d.created_at`, incidentID)
	if err != nil {
		return p, err
	}
	if p.Destinations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[replayPlanRow]); err != nil {
		return p, err
	}
	for _, d := range p.Destinations {
		if d.Enabled {
			p.WillSend += d.Deliveries - d.InFlight
		}
	}
	return p, nil
}

// lockIncident checks the incident belongs to orgID (and locks it).
func lockIncident(ctx context.Context, tx pgx.Tx, orgID, id string) error {
	var x string
	err := tx.QueryRow(ctx, `SELECT id FROM incidents WHERE id = $1 AND org_id = $2 FOR UPDATE`, id, orgID).Scan(&x)
	return notFoundIfNoRows(err)
}

// previewReplay: GET /incidents/{incident}/replay — the dry run.
func (s *Server) previewReplay(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "incident")
	if err != nil {
		return err
	}
	var plan replayPlan
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		if err := lockIncident(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		plan, err = s.planReplay(r.Context(), tx, id)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

type replayView struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Total       int        `json:"total"`
	Succeeded   int        `json:"succeeded"`
	Failed      int        `json:"failed"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// startReplay: POST /incidents/{incident}/replay — re-queue every affected
// delivery (enabled destinations, not currently sending) under one replay.
// The worker sends them with the original Idempotency-Key and a Relaya-Replay
// header; when all succeed the incident resolves itself as verified.
func (s *Server) startReplay(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "incident")
	if err != nil {
		return err
	}
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !in.Confirm {
		return httpx.BadRequest(`preview first (GET), then send {"confirm": true} to replay`)
	}

	var v replayView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		if err := lockIncident(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		var running bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM replays WHERE incident_id = $1 AND status = 'running')`, id).Scan(&running); err != nil {
			return err
		}
		if running {
			return httpx.Conflict("a replay for this incident is already running")
		}
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO replays (org_id, incident_id, total, created_by) VALUES ($1, $2, 0, $3)
			RETURNING id, status, total, succeeded, failed, created_by, created_at, completed_at`, orgID, id, p.ActorID()).
			Scan(&v.ID, &v.Status, &v.Total, &v.Succeeded, &v.Failed, &v.CreatedBy, &v.CreatedAt, &v.CompletedAt); err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), `
			WITH ev AS (`+affectedEvents+`)
			INSERT INTO deliveries (org_id, event_id, event_received_at, webhook_id, destination_id, status, next_attempt_at, replay_id)
			SELECT $2, ev.event_id, ev.event_received_at, d.webhook_id, d.id, 'pending', now(), $3
			FROM ev CROSS JOIN destinations d
			WHERE d.webhook_id = (SELECT webhook_id FROM incidents WHERE id = $1) AND d.enabled AND (cardinality(d.event_types) = 0 OR (SELECT c.event_type FROM incidents i2 JOIN contracts c ON c.id = i2.contract_id WHERE i2.id = $1) = ANY(d.event_types))
			ON CONFLICT (event_id, destination_id) DO UPDATE
			SET status = 'pending', next_attempt_at = now(), attempts = 0, completed_at = NULL,
			    last_error = '', replay_id = EXCLUDED.replay_id
			WHERE deliveries.status <> 'in_flight'`, id, orgID, v.ID)
		if err != nil {
			return err
		}
		v.Total = int(tag.RowsAffected())
		if v.Total == 0 {
			return httpx.Conflict("nothing to replay: this webhook has no enabled destinations, or every affected delivery is being sent right now")
		}
		if _, err := tx.Exec(r.Context(), `UPDATE replays SET total = $2 WHERE id = $1`, v.ID, v.Total); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `SELECT pg_notify($1, '')`, delivery.NotifyChannel); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "incident.replay", "incident", id)
		e.Metadata = map[string]any{"replay_id": v.ID, "deliveries": v.Total}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, v)
	return nil
}
