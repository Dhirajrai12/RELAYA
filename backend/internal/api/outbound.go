package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
	"relaya/internal/ingest"
)

// Outbound webhooks: the org sends events ("messages") to its own customers'
// endpoints. Each customer is an app backed by a hidden outbound webhook; its
// endpoints are destinations on it, signed with Standard Webhooks headers.

var (
	appUIDRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	eventTypeRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,99}$`)
	portalTTL    = 24 * time.Hour
	maxEndpoints = 20 // per app
)

type outboundApp struct {
	ID        string    `json:"id"`
	UID       string    `json:"uid"`
	Name      string    `json:"name"`
	WebhookID string    `json:"webhook_id"`
	Endpoints int       `json:"endpoints"`
	Messages  int       `json:"messages_24h"`
	Failing   int       `json:"failed_24h"`
	CreatedAt time.Time `json:"created_at"`
	projectID string
}

const outboundAppSelect = `
	SELECT a.id, a.uid, a.name, a.webhook_id, w.project_id, a.created_at,
	       (SELECT count(*) FROM destinations d WHERE d.webhook_id = a.webhook_id),
	       (SELECT count(*) FROM events e WHERE e.webhook_id = a.webhook_id AND e.received_at > now() - interval '24 hours'),
	       (SELECT count(*) FROM deliveries dl WHERE dl.webhook_id = a.webhook_id AND dl.status = 'failed' AND dl.completed_at > now() - interval '24 hours')
	FROM outbound_apps a JOIN webhooks w ON w.id = a.webhook_id`

func scanOutboundApp(row pgx.Row) (outboundApp, error) {
	var a outboundApp
	err := row.Scan(&a.ID, &a.UID, &a.Name, &a.WebhookID, &a.projectID, &a.CreatedAt, &a.Endpoints, &a.Messages, &a.Failing)
	return a, err
}

// appByRef finds an app by its uid or ID.
func (s *Server) appByRef(ctx context.Context, q rowQuerier, orgID, ref string) (outboundApp, error) {
	col := "a.uid"
	if uuidRe.MatchString(ref) {
		col = "a.id::text"
	}
	a, err := scanOutboundApp(q.QueryRow(ctx, outboundAppSelect+` WHERE a.org_id = $1 AND `+col+` = $2`, orgID, ref))
	return a, notFoundIfNoRows(err)
}

// ---- endpoints (shared by the API and the portal) ---------------------------------

type endpointView struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Description string     `json:"description"`
	EventTypes  []string   `json:"event_types"`
	Enabled     bool       `json:"enabled"`
	CreatedAt   time.Time  `json:"created_at"`
	Succeeded   int        `json:"succeeded_24h"`
	Failed      int        `json:"failed_24h"`
	Retrying    int        `json:"retrying"`
	LastSuccess *time.Time `json:"last_success_at"`
}

const endpointSelect = `
	SELECT d.id, d.url, d.name, d.event_types, d.enabled, d.created_at,
	       coalesce(s.ok, 0), coalesce(s.failed, 0), coalesce(s.retrying, 0), s.last_ok
	FROM destinations d` + destinationStats

func scanEndpoint(row pgx.Row) (endpointView, error) {
	var v endpointView
	err := row.Scan(&v.ID, &v.URL, &v.Description, &v.EventTypes, &v.Enabled, &v.CreatedAt, &v.Succeeded, &v.Failed, &v.Retrying, &v.LastSuccess)
	return v, err
}

func (s *Server) listEndpoints(ctx context.Context, webhookID string) ([]endpointView, error) {
	rows, err := s.Pool.Query(ctx, endpointSelect+` WHERE d.webhook_id = $1 ORDER BY d.created_at`, webhookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []endpointView{}
	for rows.Next() {
		v, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type endpointInput struct {
	URL         *string   `json:"url"`
	Description *string   `json:"description"`
	EventTypes  *[]string `json:"event_types"`
	Enabled     *bool     `json:"enabled"`
}

func (s *Server) validEndpoint(in endpointInput) error {
	if in.URL != nil {
		if err := s.DeliveryPolicy.ValidateURL(strings.TrimSpace(*in.URL)); err != nil {
			return httpx.BadRequest("url: %v", err)
		}
	}
	if in.Description != nil && len(*in.Description) > 200 {
		return httpx.BadRequest("description must be at most 200 characters")
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

// endpointName is what the destination list shows: the description, else the host.
func endpointName(rawURL, description string) string {
	if d := strings.TrimSpace(description); d != "" {
		return truncate(d, 100)
	}
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "endpoint"
}

// createEndpoint adds an endpoint to an app and returns it with its secret.
func (s *Server) createEndpoint(ctx context.Context, tx pgx.Tx, orgID string, app outboundApp, in endpointInput, actor audit.Entry) (endpointView, string, error) {
	if in.URL == nil {
		return endpointView{}, "", httpx.BadRequest("url is required")
	}
	if err := s.validEndpoint(in); err != nil {
		return endpointView{}, "", err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM destinations WHERE webhook_id = $1`, app.WebhookID).Scan(&n); err != nil {
		return endpointView{}, "", err
	}
	if n >= maxEndpoints {
		return endpointView{}, "", httpx.BadRequest("at most %d endpoints per app", maxEndpoints)
	}
	desc, types := "", []string{}
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	if in.EventTypes != nil {
		types = *in.EventTypes
	}
	secret := delivery.NewStandardSecret()
	enc, err := s.Vault.Encrypt(ctx, orgID, []byte(secret))
	if err != nil {
		return endpointView{}, "", err
	}
	u := strings.TrimSpace(*in.URL)
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO destinations (org_id, webhook_id, name, url, signing_secret_enc, signing_scheme, event_types)
		VALUES ($1, $2, $3, $4, $5, 'standard', $6) RETURNING id`,
		orgID, app.WebhookID, endpointName(u, desc), u, enc, types).Scan(&id); err != nil {
		return endpointView{}, "", err
	}
	v, err := scanEndpoint(tx.QueryRow(ctx, endpointSelect+` WHERE d.id = $1`, id))
	if err != nil {
		return v, "", err
	}
	actor.Action, actor.TargetType, actor.TargetID = "outbound.endpoint.create", "destination", id
	actor.Metadata = map[string]any{"app": app.UID, "url": u, "event_types": types}
	return v, secret, audit.Record(ctx, tx, actor)
}

// updateEndpoint changes an app's endpoint.
func (s *Server) updateEndpoint(ctx context.Context, tx pgx.Tx, app outboundApp, id string, in endpointInput, actor audit.Entry) (endpointView, error) {
	if err := s.validEndpoint(in); err != nil {
		return endpointView{}, err
	}
	cur, err := scanEndpoint(tx.QueryRow(ctx, endpointSelect+` WHERE d.id = $1 AND d.webhook_id = $2 FOR UPDATE OF d`, id, app.WebhookID))
	if err != nil {
		return cur, notFoundIfNoRows(err)
	}
	u, desc, types, enabled := cur.URL, cur.Description, cur.EventTypes, cur.Enabled
	if in.URL != nil {
		u = strings.TrimSpace(*in.URL)
	}
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	if in.EventTypes != nil {
		types = *in.EventTypes
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := tx.Exec(ctx, `UPDATE destinations SET url = $2, name = $3, event_types = $4, enabled = $5, updated_at = now() WHERE id = $1`,
		id, u, endpointName(u, desc), types, enabled); err != nil {
		return cur, err
	}
	actor.Action, actor.TargetType, actor.TargetID = "outbound.endpoint.update", "destination", id
	actor.Metadata = map[string]any{"app": app.UID}
	if err := audit.Record(ctx, tx, actor); err != nil {
		return cur, err
	}
	return scanEndpoint(tx.QueryRow(ctx, endpointSelect+` WHERE d.id = $1`, id))
}

// endpointSecret returns an endpoint's signing secret (receivers need it to verify).
func (s *Server) endpointSecret(ctx context.Context, orgID string, app outboundApp, id string) (string, error) {
	var enc []byte
	if err := s.Pool.QueryRow(ctx, `SELECT signing_secret_enc FROM destinations WHERE id = $1 AND webhook_id = $2`, id, app.WebhookID).Scan(&enc); err != nil {
		return "", notFoundIfNoRows(err)
	}
	b, err := s.Vault.Decrypt(ctx, orgID, enc)
	return string(b), err
}

func (s *Server) rotateEndpointSecret(ctx context.Context, tx pgx.Tx, orgID string, app outboundApp, id string, actor audit.Entry) (string, error) {
	secret := delivery.NewStandardSecret()
	enc, err := s.Vault.Encrypt(ctx, orgID, []byte(secret))
	if err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx, `UPDATE destinations SET signing_secret_enc = $3, updated_at = now() WHERE id = $1 AND webhook_id = $2`, id, app.WebhookID, enc)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", httpx.ErrNotFound
	}
	actor.Action, actor.TargetType, actor.TargetID = "outbound.endpoint.rotate_secret", "destination", id
	return secret, audit.Record(ctx, tx, actor)
}

func (s *Server) deleteEndpoint(ctx context.Context, tx pgx.Tx, app outboundApp, id string, actor audit.Entry) error {
	tag, err := tx.Exec(ctx, `DELETE FROM destinations WHERE id = $1 AND webhook_id = $2`, id, app.WebhookID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	actor.Action, actor.TargetType, actor.TargetID = "outbound.endpoint.delete", "destination", id
	return audit.Record(ctx, tx, actor)
}

// testEndpoint sends an example message right now (not stored) and reports the answer.
func (s *Server) testEndpoint(ctx context.Context, orgID string, app outboundApp, id, eventType string) (map[string]any, error) {
	var u string
	var enc []byte
	var timeoutMS int
	if err := s.Pool.QueryRow(ctx, `SELECT url, signing_secret_enc, timeout_ms FROM destinations WHERE id = $1 AND webhook_id = $2`, id, app.WebhookID).
		Scan(&u, &enc, &timeoutMS); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	secret, err := s.Vault.Decrypt(ctx, orgID, enc)
	if err != nil {
		return nil, err
	}
	if eventType == "" {
		eventType = "webhook.test"
	}
	msgID := "msg_test_" + hex.EncodeToString(auth.HashToken(time.Now().String()))[:16]
	body, _ := json.Marshal(map[string]any{"type": eventType, "test": true, "data": map[string]any{}, "timestamp": time.Now().UTC().Format(time.RFC3339)})
	res := s.Sender.Send(ctx, delivery.Request{
		URL: u, Secret: secret, Timeout: time.Duration(timeoutMS) * time.Millisecond, Scheme: "standard",
		EventID: msgID, DeliveryID: msgID, EventType: eventType, Attempt: 1, Body: body, ContentType: "application/json",
	})
	out := map[string]any{
		"ok": delivery.Classify(res) == delivery.Succeeded, "status_code": res.StatusCode,
		"duration_ms": res.Duration.Milliseconds(), "response_body": res.Body, "error": "",
	}
	if res.Err != nil {
		out["error"] = res.Err.Error()
	}
	return out, nil
}

// ---- apps (org API) ----------------------------------------------------------------

func (s *Server) listOutboundApps(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), outboundAppSelect+` WHERE a.org_id = $1 ORDER BY a.created_at DESC LIMIT 1000`, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []outboundApp{}
	for rows.Next() {
		a, err := scanOutboundApp(rows)
		if err != nil {
			return err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// outboundProject is the project outbound webhooks live in: "Outbound", created on first use.
func outboundProject(ctx context.Context, tx pgx.Tx, orgID string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE org_id = $1 AND slug LIKE 'outbound%' ORDER BY created_at LIMIT 1`, orgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO projects (org_id, name, slug) VALUES ($1, 'Outbound', $2) RETURNING id`, orgID, "outbound-"+randomSuffix()).Scan(&id)
	}
	return id, err
}

func (s *Server) createOutboundApp(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.UID = strings.TrimSpace(in.UID)
	if !appUIDRe.MatchString(in.UID) {
		return httpx.BadRequest("uid is required: your ID for this customer (letters, digits, _ . : -; up to 128)")
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = in.UID
	}
	name, err := requireName(in.Name, "name", 100)
	if err != nil {
		return err
	}
	var app outboundApp
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		project, err := outboundProject(r.Context(), tx, orgID)
		if err != nil {
			return err
		}
		var whID string
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO webhooks (org_id, project_id, name, provider, ingest_token, kind)
			VALUES ($1, $2, $3, 'generic', $4, 'outbound') RETURNING id`,
			orgID, project, truncate("Outbound: "+name, 100), auth.RandomToken(ingestTokenPrefix)).Scan(&whID); err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(r.Context(), `INSERT INTO outbound_apps (org_id, uid, name, webhook_id) VALUES ($1, $2, $3, $4) RETURNING id`,
			orgID, in.UID, name, whID).Scan(&id); err != nil {
			if isUniqueViolation(err) {
				return httpx.Conflict("an app with uid %q already exists", in.UID)
			}
			return err
		}
		if app, err = s.appByRef(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "outbound.app.create", "outbound_app", id)
		e.Metadata = map[string]any{"uid": in.UID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, app)
	return nil
}

func (s *Server) getOutboundApp(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	app, err := s.appByRef(r.Context(), s.Pool, orgID, r.PathValue("app"))
	if err != nil {
		return err
	}
	eps, err := s.listEndpoints(r.Context(), app.WebhookID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"app": app, "endpoints": eps})
	return nil
}

func (s *Server) deleteOutboundApp(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	app, err := s.appByRef(r.Context(), s.Pool, orgID, r.PathValue("app"))
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		// The webhook takes the app, endpoints and portal sessions with it.
		if _, err := tx.Exec(r.Context(), `DELETE FROM webhooks WHERE id = $1 AND org_id = $2`, app.WebhookID, orgID); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "outbound.app.delete", "outbound_app", app.ID)
		e.Metadata = map[string]any{"uid": app.UID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- endpoints (org API) ------------------------------------------------------------

func (s *Server) orgApp(r *http.Request, min auth.Role) (string, auth.Principal, outboundApp, error) {
	orgID, p, _, err := s.orgAccess(r, min)
	if err != nil {
		return "", p, outboundApp{}, err
	}
	app, err := s.appByRef(r.Context(), s.Pool, orgID, r.PathValue("app"))
	return orgID, p, app, err
}

func (s *Server) createOutboundEndpoint(w http.ResponseWriter, r *http.Request) error {
	orgID, p, app, err := s.orgApp(r, auth.RoleAdmin)
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
		v, secret, err = s.createEndpoint(r.Context(), tx, orgID, app, in, audit.ByPrincipal(p, orgID, "", "", ""))
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"endpoint": v, "signing_secret": secret})
	return nil
}

func (s *Server) updateOutboundEndpoint(w http.ResponseWriter, r *http.Request) error {
	orgID, p, app, err := s.orgApp(r, auth.RoleAdmin)
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
		v, err = s.updateEndpoint(r.Context(), tx, app, id, in, audit.ByPrincipal(p, orgID, "", "", ""))
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteOutboundEndpoint(w http.ResponseWriter, r *http.Request) error {
	orgID, p, app, err := s.orgApp(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	if err := s.tx(r.Context(), func(tx pgx.Tx) error {
		return s.deleteEndpoint(r.Context(), tx, app, id, audit.ByPrincipal(p, orgID, "", "", ""))
	}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) outboundEndpointSecret(w http.ResponseWriter, r *http.Request) error {
	orgID, _, app, err := s.orgApp(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	secret, err := s.endpointSecret(r.Context(), orgID, app, id)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]string{"signing_secret": secret})
	return nil
}

func (s *Server) testOutboundEndpoint(w http.ResponseWriter, r *http.Request) error {
	orgID, _, app, err := s.orgApp(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "endpoint")
	if err != nil {
		return err
	}
	out, err := s.testEndpoint(r.Context(), orgID, app, id, r.URL.Query().Get("event_type"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// ---- messages ------------------------------------------------------------------------

// sendOutboundMessage stores a message on the app's webhook; every enabled
// endpoint subscribed to its type gets a delivery.
func (s *Server) sendOutboundMessage(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		App            string          `json:"app"`
		EventType      string          `json:"event_type"`
		Payload        json.RawMessage `json:"payload"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !eventTypeRe.MatchString(in.EventType) {
		return httpx.BadRequest("event_type is required (letters, digits, _ . : -; up to 100)")
	}
	payload := strings.TrimSpace(string(in.Payload))
	if !strings.HasPrefix(payload, "{") {
		return httpx.BadRequest("payload must be a JSON object")
	}
	if len(in.IdempotencyKey) > 200 {
		return httpx.BadRequest("idempotency_key must be at most 200 characters")
	}
	app, err := s.appByRef(r.Context(), s.Pool, orgID, in.App)
	if errors.Is(err, httpx.ErrNotFound) {
		return httpx.BadRequest("no app %q; create it first", in.App)
	}
	if err != nil {
		return err
	}
	// The body receivers get: the type alongside the org's payload.
	body, _ := json.Marshal(map[string]any{"type": in.EventType, "timestamp": time.Now().UTC().Format(time.RFC3339), "data": json.RawMessage(payload)})
	dedup := in.IdempotencyKey
	if dedup == "" {
		dedup = "msg:" + hex.EncodeToString(auth.HashToken(auth.RandomToken("")))[:24]
	}
	var id string
	var duplicate bool
	var endpoints int
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		headers, _ := json.Marshal(map[string]string{"relaya-outbound-app": app.UID})
		id, duplicate, err = ingest.StoreTx(r.Context(), tx, ingest.Event{
			OrgID: orgID, ProjectID: app.projectID, WebhookID: app.WebhookID, DedupKey: dedup, Type: in.EventType,
			Status: "received", Signature: "valid", ContentType: "application/json", Headers: headers, Payload: body,
			ReceivedAt: time.Now().UTC(),
		}, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO outbound_event_types (org_id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, orgID, in.EventType); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT count(*) FROM deliveries WHERE event_id = $1`, id).Scan(&endpoints)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"id": id, "app": app.UID, "event_type": in.EventType, "endpoints": endpoints, "duplicate": duplicate})
	return nil
}

// ---- event types ---------------------------------------------------------------------

type eventTypeView struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) eventTypes(ctx context.Context, orgID string) ([]eventTypeView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT name, description, created_at FROM outbound_event_types WHERE org_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[eventTypeView])
	if out == nil {
		out = []eventTypeView{}
	}
	return out, err
}

func (s *Server) listOutboundEventTypes(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	out, err := s.eventTypes(r.Context(), orgID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Server) upsertOutboundEventType(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !eventTypeRe.MatchString(in.Name) {
		return httpx.BadRequest("invalid event type name")
	}
	if len(in.Description) > 300 {
		return httpx.BadRequest("description must be at most 300 characters")
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO outbound_event_types (org_id, name, description) VALUES ($1, $2, $3)
			ON CONFLICT (org_id, name) DO UPDATE SET description = EXCLUDED.description`, orgID, in.Name, strings.TrimSpace(in.Description)); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "outbound.event_type.save", "outbound_event_type", in.Name))
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"name": in.Name, "description": strings.TrimSpace(in.Description)})
	return nil
}

func (s *Server) deleteOutboundEventType(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM outbound_event_types WHERE org_id = $1 AND name = $2`, orgID, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "outbound.event_type.delete", "outbound_event_type", name))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- portal links ----------------------------------------------------------------------

// createPortalLink returns a link where the app's user manages its endpoints
// for 24 hours. The token is in the URL fragment, so it never reaches server logs.
func (s *Server) createPortalLink(w http.ResponseWriter, r *http.Request) error {
	orgID, p, app, err := s.orgApp(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	token := auth.RandomToken("ps_")
	expires := time.Now().Add(portalTTL).UTC()
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO portal_sessions (org_id, app_id, token_hash, expires_at, created_by) VALUES ($1, $2, $3, $4, $5)`,
			orgID, app.ID, hex.EncodeToString(auth.HashToken(token)), expires, p.ActorID()); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM portal_sessions WHERE expires_at < now() - interval '7 days'`); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "outbound.portal_link.create", "outbound_app", app.ID)
		e.Metadata = map[string]any{"uid": app.UID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"url": s.DashboardURL + "/portal#" + token, "expires_at": expires})
	return nil
}
