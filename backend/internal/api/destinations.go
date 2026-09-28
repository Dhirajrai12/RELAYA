package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
)

type destinationView struct {
	ID          string    `json:"id"`
	WebhookID   string    `json:"webhook_id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Enabled     bool      `json:"enabled"`
	TimeoutMS   int       `json:"timeout_ms"`
	MaxAttempts int       `json:"max_attempts"`
	EventTypes  []string  `json:"event_types"` // only these types (empty = all)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Health over the last 24 hours plus what's currently queued.
	Stats struct {
		Succeeded24h int        `json:"succeeded_24h"`
		Failed24h    int        `json:"failed_24h"`
		Retrying     int        `json:"retrying"`
		Pending      int        `json:"pending"`
		LastSuccess  *time.Time `json:"last_success_at"`
	} `json:"stats"`
}

const destinationCols = `d.id, d.webhook_id, d.name, d.url, d.enabled, d.timeout_ms, d.max_attempts, d.event_types, d.created_at, d.updated_at,
	coalesce(s.ok, 0), coalesce(s.failed, 0), coalesce(s.retrying, 0), coalesce(s.pending, 0), s.last_ok`

// destinationStats is joined LATERAL onto destinations d.
const destinationStats = `
	LEFT JOIN LATERAL (
		SELECT count(*) FILTER (WHERE status = 'succeeded' AND completed_at > now() - interval '24 hours') AS ok,
		       count(*) FILTER (WHERE status = 'failed' AND completed_at > now() - interval '24 hours') AS failed,
		       count(*) FILTER (WHERE status IN ('retrying', 'in_flight')) AS retrying,
		       count(*) FILTER (WHERE status = 'pending') AS pending,
		       max(completed_at) FILTER (WHERE status = 'succeeded') AS last_ok
		FROM deliveries WHERE destination_id = d.id
	) s ON true`

func scanDestination(row pgx.Row) (destinationView, error) {
	var v destinationView
	err := row.Scan(&v.ID, &v.WebhookID, &v.Name, &v.URL, &v.Enabled, &v.TimeoutMS, &v.MaxAttempts, &v.EventTypes, &v.CreatedAt, &v.UpdatedAt,
		&v.Stats.Succeeded24h, &v.Stats.Failed24h, &v.Stats.Retrying, &v.Stats.Pending, &v.Stats.LastSuccess)
	return v, err
}

// rowQuerier is satisfied by both the pool and a transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getDestination(ctx context.Context, q rowQuerier, orgID, id string) (destinationView, error) {
	v, err := scanDestination(q.QueryRow(ctx,
		`SELECT `+destinationCols+` FROM destinations d`+destinationStats+` WHERE d.id = $1 AND d.org_id = $2`, id, orgID))
	return v, notFoundIfNoRows(err)
}

func (s *Server) listDestinations(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	webhookID, err := pathID(r, "webhook")
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), `SELECT `+destinationCols+` FROM destinations d`+destinationStats+`
		WHERE d.webhook_id = $1 AND d.org_id = $2 ORDER BY d.created_at`, webhookID, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []destinationView{}
	for rows.Next() {
		v, err := scanDestination(rows)
		if err != nil {
			return err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

type destinationInput struct {
	Name        *string   `json:"name"`
	URL         *string   `json:"url"`
	Enabled     *bool     `json:"enabled"`
	TimeoutMS   *int      `json:"timeout_ms"`
	MaxAttempts *int      `json:"max_attempts"`
	EventTypes  *[]string `json:"event_types"` // routing: only these event types (empty = all)
}

func (s *Server) validateDestination(in destinationInput) error {
	if in.URL != nil {
		if err := s.DeliveryPolicy.ValidateURL(*in.URL); err != nil {
			return httpx.BadRequest("url: %v", err)
		}
	}
	if in.TimeoutMS != nil && (*in.TimeoutMS < 1000 || *in.TimeoutMS > 30000) {
		return httpx.BadRequest("timeout_ms must be between 1000 and 30000")
	}
	if in.MaxAttempts != nil && (*in.MaxAttempts < 1 || *in.MaxAttempts > 20) {
		return httpx.BadRequest("max_attempts must be between 1 and 20")
	}
	if in.EventTypes != nil {
		if len(*in.EventTypes) > 100 {
			return httpx.BadRequest("at most 100 event types")
		}
		for _, t := range *in.EventTypes {
			if !eventTypeRe.MatchString(t) {
				return httpx.BadRequest("invalid event type %q", t)
			}
		}
	}
	return nil
}

// createDestination returns the signing secret once; only its ciphertext is stored.
func (s *Server) createDestination(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	webhookID, err := pathID(r, "webhook")
	if err != nil {
		return err
	}
	var in destinationInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Name == nil || in.URL == nil {
		return httpx.BadRequest("name and url are required")
	}
	name, err := requireName(*in.Name, "name", 100)
	if err != nil {
		return err
	}
	if err := s.validateDestination(in); err != nil {
		return err
	}
	timeout, attempts, types := 10000, 8, []string{}
	if in.EventTypes != nil {
		types = *in.EventTypes
	}
	if in.TimeoutMS != nil {
		timeout = *in.TimeoutMS
	}
	if in.MaxAttempts != nil {
		attempts = *in.MaxAttempts
	}

	secret := delivery.NewSecret()
	secretEnc, err := s.Vault.Encrypt(r.Context(), orgID, []byte(secret))
	if err != nil {
		return err
	}

	var v destinationView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM webhooks WHERE id = $1 AND org_id = $2)`, webhookID, orgID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return httpx.ErrNotFound
		}
		var id string
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO destinations (org_id, webhook_id, name, url, signing_secret_enc, timeout_ms, max_attempts, event_types)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			orgID, webhookID, name, strings.TrimSpace(*in.URL), secretEnc, timeout, attempts, types).Scan(&id); err != nil {
			return err
		}
		if v, err = getDestination(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "destination.create", "destination", id)
		e.Metadata = map[string]any{"webhook_id": webhookID, "url": v.URL}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"destination": v, "signing_secret": secret})
	return nil
}

func (s *Server) updateDestination(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "destination")
	if err != nil {
		return err
	}
	var in destinationInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := s.validateDestination(in); err != nil {
		return err
	}

	sets := []string{"updated_at = now()"}
	args := []any{id, orgID}
	changed := []string{}
	set := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+itoa(len(args)))
		changed = append(changed, col)
	}
	if in.Name != nil {
		name, err := requireName(*in.Name, "name", 100)
		if err != nil {
			return err
		}
		set("name", name)
	}
	if in.URL != nil {
		set("url", strings.TrimSpace(*in.URL))
	}
	if in.Enabled != nil {
		set("enabled", *in.Enabled)
	}
	if in.TimeoutMS != nil {
		set("timeout_ms", *in.TimeoutMS)
	}
	if in.MaxAttempts != nil {
		set("max_attempts", *in.MaxAttempts)
	}
	if in.EventTypes != nil {
		set("event_types", *in.EventTypes)
	}

	var v destinationView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE destinations SET `+strings.Join(sets, ", ")+` WHERE id = $1 AND org_id = $2`, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		if v, err = getDestination(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "destination.update", "destination", id)
		e.Metadata = map[string]any{"fields": changed}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteDestination(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "destination")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM destinations WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "destination.delete", "destination", id))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) rotateDestinationSecret(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "destination")
	if err != nil {
		return err
	}
	secret := delivery.NewSecret()
	enc, err := s.Vault.Encrypt(r.Context(), orgID, []byte(secret))
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE destinations SET signing_secret_enc = $3, updated_at = now() WHERE id = $1 AND org_id = $2`, id, orgID, enc)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "destination.rotate_secret", "destination", id))
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"signing_secret": secret})
	return nil
}

// testDestination sends a synthetic "relaya.test" event right now and reports
// what the endpoint answered. Nothing is stored except an audit entry.
func (s *Server) testDestination(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "destination")
	if err != nil {
		return err
	}
	var url string
	var secretEnc []byte
	var timeoutMS int
	err = s.Pool.QueryRow(r.Context(), `SELECT url, signing_secret_enc, timeout_ms FROM destinations WHERE id = $1 AND org_id = $2`, id, orgID).
		Scan(&url, &secretEnc, &timeoutMS)
	if err != nil {
		return notFoundIfNoRows(err)
	}
	secret, err := s.Vault.Decrypt(r.Context(), orgID, secretEnc)
	if err != nil {
		return err
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	testID := "test_" + hex.EncodeToString(b)
	body, _ := json.Marshal(map[string]any{"type": "relaya.test", "id": testID, "sent_at": time.Now().UTC().Format(time.RFC3339)})
	res := s.Sender.Send(r.Context(), delivery.Request{
		URL: url, Secret: secret, Timeout: time.Duration(timeoutMS) * time.Millisecond,
		EventID: testID, DeliveryID: testID, EventType: "relaya.test", Attempt: 1,
		Body: body, ContentType: "application/json",
	})

	out := map[string]any{
		"ok":            delivery.Classify(res) == delivery.Succeeded,
		"status_code":   res.StatusCode,
		"duration_ms":   res.Duration.Milliseconds(),
		"response_body": res.Body,
		"error":         "",
	}
	if res.Err != nil {
		out["error"] = res.Err.Error()
	}
	e := audit.ByPrincipal(p, orgID, "destination.test", "destination", id)
	e.Metadata = map[string]any{"status_code": res.StatusCode}
	if out["ok"] != true {
		e.Result = "failure"
	}
	if err := audit.Record(r.Context(), s.Pool, e); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
