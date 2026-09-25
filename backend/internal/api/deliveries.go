package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
)

type deliveryView struct {
	ID              string     `json:"id"`
	EventID         string     `json:"event_id"`
	WebhookID       string     `json:"webhook_id"`
	DestinationID   string     `json:"destination_id"`
	DestinationName string     `json:"destination_name"`
	DestinationURL  string     `json:"destination_url"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"max_attempts"`
	NextAttemptAt   *time.Time `json:"next_attempt_at"`
	LastStatusCode  *int       `json:"last_status_code"`
	LastError       string     `json:"last_error"`
	LastAttemptAt   *time.Time `json:"last_attempt_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

const deliveryCols = `dl.id, dl.event_id, dl.webhook_id, dl.destination_id, d.name, d.url, dl.status, dl.attempts, d.max_attempts,
	CASE WHEN dl.status IN ('pending', 'retrying') THEN dl.next_attempt_at END,
	dl.last_status_code, dl.last_error, dl.last_attempt_at, dl.completed_at, dl.created_at`

const deliveryFrom = ` FROM deliveries dl JOIN destinations d ON d.id = dl.destination_id`

func scanDelivery(row pgx.Row) (deliveryView, error) {
	var v deliveryView
	err := row.Scan(&v.ID, &v.EventID, &v.WebhookID, &v.DestinationID, &v.DestinationName, &v.DestinationURL, &v.Status,
		&v.Attempts, &v.MaxAttempts, &v.NextAttemptAt, &v.LastStatusCode, &v.LastError, &v.LastAttemptAt, &v.CompletedAt, &v.CreatedAt)
	return v, err
}

func (s *Server) queryDeliveries(ctx context.Context, where string, args ...any) ([]deliveryView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+deliveryCols+deliveryFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []deliveryView{}
	for rows.Next() {
		v, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// listDeliveries filters by event_id, destination_id, webhook_id and status; newest first, max 100.
func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := "dl.org_id = $1"
	args := []any{orgID}
	for _, f := range []string{"event_id", "destination_id", "webhook_id"} {
		if v := q.Get(f); v != "" {
			if !uuidRe.MatchString(v) {
				return httpx.BadRequest("invalid %s", f)
			}
			args = append(args, v)
			where += " AND dl." + f + " = $" + strconv.Itoa(len(args))
		}
	}
	if v := q.Get("status"); v != "" {
		args = append(args, v)
		where += " AND dl.status = $" + strconv.Itoa(len(args))
	}
	out, err := s.queryDeliveries(r.Context(), where+" ORDER BY dl.created_at DESC, dl.id DESC LIMIT 100", args...)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

type attemptView struct {
	Attempt      int       `json:"attempt"`
	StartedAt    time.Time `json:"started_at"`
	DurationMS   int       `json:"duration_ms"`
	StatusCode   *int      `json:"status_code"`
	Error        string    `json:"error"`
	ResponseBody string    `json:"response_body"`
	Outcome      string    `json:"outcome"`
	RepairedBy   []string  `json:"repaired_by"` // repair rules that changed the body sent
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "delivery")
	if err != nil {
		return err
	}
	v, err := scanDelivery(s.Pool.QueryRow(r.Context(), `SELECT `+deliveryCols+deliveryFrom+` WHERE dl.id = $1 AND dl.org_id = $2`, id, orgID))
	if err != nil {
		return notFoundIfNoRows(err)
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT attempt, started_at, duration_ms, status_code, error, response_body, outcome, repaired_by
		FROM delivery_attempts WHERE delivery_id = $1 ORDER BY attempt DESC, id DESC`, id)
	if err != nil {
		return err
	}
	attempts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[attemptView])
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivery": v, "attempts": attempts})
	return nil
}

// retryDelivery schedules one more attempt right away. The Idempotency-Key
// stays the delivery ID, so a receiver that already processed it won't double-apply.
func (s *Server) retryDelivery(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "delivery")
	if err != nil {
		return err
	}
	var v deliveryView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(r.Context(), `SELECT status FROM deliveries WHERE id = $1 AND org_id = $2 FOR UPDATE`, id, orgID).Scan(&status)
		if err != nil {
			return notFoundIfNoRows(err)
		}
		switch status {
		case "succeeded":
			return httpx.Conflict("this delivery already succeeded")
		case "in_flight":
			return httpx.Conflict("an attempt is in progress right now")
		}
		// A retry of an exhausted delivery gets one more attempt: lower the
		// attempt count so the worker's max_attempts check allows it.
		if _, err := tx.Exec(r.Context(), `
			UPDATE deliveries dl SET status = 'pending', next_attempt_at = now(), completed_at = NULL,
			       attempts = LEAST(dl.attempts, d.max_attempts - 1)
			FROM destinations d WHERE d.id = dl.destination_id AND dl.id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `SELECT pg_notify($1, '')`, delivery.NotifyChannel); err != nil {
			return err
		}
		if v, err = scanDelivery(tx.QueryRow(r.Context(), `SELECT `+deliveryCols+deliveryFrom+` WHERE dl.id = $1`, id)); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "delivery.retry", "delivery", id)
		e.Metadata = map[string]any{"previous_status": status, "event_id": v.EventID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}
