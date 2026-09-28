package api

import (
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
)

// The portal (web: /portal#<token>) is where an org's customer manages its
// own endpoints. Its user has no Relaya login: the portal token (Authorization:
// Bearer ps_…) is the credential, scoped to one app, valid 24 hours.

type portalCtx struct {
	orgID, orgName string
	app            outboundApp
	expires        time.Time
}

func (s *Server) portal(w http.ResponseWriter, r *http.Request) (portalCtx, error) {
	var pc portalCtx
	if ok, wait := s.Limits.ConnectIP.Allow("portal:" + httpx.ClientIP(r, s.TrustProxyHeaders)); !ok {
		return pc, httpx.TooManyRequests(w, wait, "too many requests; wait a moment")
	}
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(token, "ps_") || len(token) > 100 {
		return pc, httpx.ErrUnauthorized
	}
	var appID string
	err := s.Pool.QueryRow(r.Context(), `
		SELECT p.org_id, o.name, p.app_id, p.expires_at FROM portal_sessions p JOIN organizations o ON o.id = p.org_id
		WHERE p.token_hash = $1 AND p.expires_at > now()`, hex.EncodeToString(auth.HashToken(token))).
		Scan(&pc.orgID, &pc.orgName, &appID, &pc.expires)
	if err != nil {
		return pc, httpx.NewError(http.StatusUnauthorized, "portal_expired", "this portal link is invalid or has expired; ask for a new one")
	}
	pc.app, err = s.appByRef(r.Context(), s.Pool, pc.orgID, appID)
	return pc, err
}

// portalActor records portal changes as made by the app's user.
func (pc portalCtx) actor() audit.Entry {
	return audit.Entry{OrgID: pc.orgID, ActorType: "system", ActorID: "portal:" + pc.app.UID}
}

func (s *Server) portalInfo(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	types, err := s.eventTypes(r.Context(), pc.orgID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"org_name": pc.orgName, "app": map[string]string{"uid": pc.app.UID, "name": pc.app.Name},
		"event_types": types, "expires_at": pc.expires,
	})
	return nil
}

func (s *Server) portalListEndpoints(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	eps, err := s.listEndpoints(r.Context(), pc.app.WebhookID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": eps})
	return nil
}

func (s *Server) portalCreateEndpoint(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	var in endpointInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var v endpointView
	var secret string
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		v, secret, err = s.createEndpoint(r.Context(), tx, pc.orgID, pc.app, in, pc.actor())
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"endpoint": v, "signing_secret": secret})
	return nil
}

func (s *Server) portalUpdateEndpoint(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	var in endpointInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var v endpointView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		v, err = s.updateEndpoint(r.Context(), tx, pc.app, id, in, pc.actor())
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) portalDeleteEndpoint(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	if err := s.tx(r.Context(), func(tx pgx.Tx) error { return s.deleteEndpoint(r.Context(), tx, pc.app, id, pc.actor()) }); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) portalEndpointSecret(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	secret, err := s.endpointSecret(r.Context(), pc.orgID, pc.app, id)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]string{"signing_secret": secret})
	return nil
}

func (s *Server) portalRotateSecret(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	var secret string
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		secret, err = s.rotateEndpointSecret(r.Context(), tx, pc.orgID, pc.app, id, pc.actor())
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"signing_secret": secret})
	return nil
}

func (s *Server) portalTestEndpoint(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	out, err := s.testEndpoint(r.Context(), pc.orgID, pc.app, id, r.URL.Query().Get("event_type"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type portalDelivery struct {
	ID            string     `json:"id"`
	MessageID     string     `json:"message_id"`
	EventType     string     `json:"event_type"`
	EndpointID    string     `json:"endpoint_id"`
	EndpointURL   string     `json:"endpoint_url"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	StatusCode    *int       `json:"last_status_code"`
	LastError     string     `json:"last_error"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at"`
	Response      string     `json:"last_response"`
}

// portalDeliveries lists the app's deliveries, newest first, 100 at a time
// (?before=<created_at of the last one seen>, ?endpoint=, ?status=).
func (s *Server) portalDeliveries(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"dl.webhook_id = $1"}
	args := []any{pc.app.WebhookID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if e := q.Get("endpoint"); e != "" {
		if !uuidRe.MatchString(e) {
			return httpx.BadRequest("invalid endpoint")
		}
		add("dl.destination_id = ?", e)
	}
	if st := q.Get("status"); st != "" {
		if st != "succeeded" && st != "failed" && st != "pending" && st != "retrying" {
			return httpx.BadRequest("invalid status")
		}
		add("dl.status = ?", st)
	}
	if b := q.Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			return httpx.BadRequest("before must be a timestamp")
		}
		add("dl.created_at < ?", t)
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT dl.id, dl.event_id, e.type, dl.destination_id, d.url, dl.status, dl.attempts, dl.last_status_code, dl.last_error,
		       dl.next_attempt_at, dl.created_at, dl.completed_at, coalesce(a.response_body, '')
		FROM deliveries dl
		JOIN destinations d ON d.id = dl.destination_id
		JOIN events e ON e.id = dl.event_id AND e.received_at = dl.event_received_at
		LEFT JOIN LATERAL (SELECT response_body FROM delivery_attempts WHERE delivery_id = dl.id ORDER BY started_at DESC LIMIT 1) a ON true
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY dl.created_at DESC LIMIT 101`, args...)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[portalDelivery])
	if err != nil {
		return err
	}
	if out == nil {
		out = []portalDelivery{}
	}
	var next *string
	if len(out) > 100 {
		out = out[:100]
		n := out[99].CreatedAt.Format(time.RFC3339Nano)
		next = &n
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_before": next})
	return nil
}

// portalRetry sends a delivery again now.
func (s *Server) portalRetry(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.portal(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "delivery")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE deliveries dl SET status = 'pending', next_attempt_at = now(), completed_at = NULL,
			       attempts = LEAST(dl.attempts, d.max_attempts - 1)
			FROM destinations d
			WHERE d.id = dl.destination_id AND dl.id = $1 AND dl.webhook_id = $2 AND dl.status IN ('failed', 'retrying')`, id, pc.app.WebhookID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("only failed or retrying deliveries can be sent again")
		}
		if _, err := tx.Exec(r.Context(), `SELECT pg_notify($1, '')`, delivery.NotifyChannel); err != nil {
			return err
		}
		e := pc.actor()
		e.Action, e.TargetType, e.TargetID = "outbound.delivery.retry", "delivery", id
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
	return nil
}
