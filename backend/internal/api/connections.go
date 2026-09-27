package api

import (
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
	"relaya/internal/connect"
	"relaya/internal/httpx"
)

// Connect links last this long and work once.
const connectSessionTTL = 30 * time.Minute

var integrationKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// ---- provider catalog ------------------------------------------------------------

func (s *Server) listConnectProviders(w http.ResponseWriter, r *http.Request) error {
	callback := ""
	if s.Connect != nil {
		callback = s.Connect.RedirectURI
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": connect.List(), "callback_url": callback})
	return nil
}

// ---- integrations -----------------------------------------------------------------

type integrationView struct {
	ID              string    `json:"id"`
	Key             string    `json:"key"`
	Provider        string    `json:"provider"`
	ProviderName    string    `json:"provider_name"`
	Auth            string    `json:"auth"`
	Name            string    `json:"name"`
	ClientID        string    `json:"client_id"`
	HasClientSecret bool      `json:"has_client_secret"`
	Scopes          []string  `json:"scopes"`
	Connections     int       `json:"connections"`
	Broken          int       `json:"broken"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const integrationSelect = `
	SELECT i.id, i.key, i.provider, i.name, i.client_id, i.client_secret_enc IS NOT NULL, i.scopes,
	       (SELECT count(*) FROM connections c WHERE c.integration_id = i.id),
	       (SELECT count(*) FROM connections c WHERE c.integration_id = i.id AND c.status = 'broken'),
	       i.created_at, i.updated_at
	FROM integrations i`

func scanIntegration(row pgx.Row) (integrationView, error) {
	var v integrationView
	err := row.Scan(&v.ID, &v.Key, &v.Provider, &v.Name, &v.ClientID, &v.HasClientSecret, &v.Scopes,
		&v.Connections, &v.Broken, &v.CreatedAt, &v.UpdatedAt)
	v.ProviderName, v.Auth = v.Provider, ""
	if p, ok := connect.Get(v.Provider); ok {
		v.ProviderName, v.Auth = p.Name, string(p.Auth)
	}
	return v, err
}

func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), integrationSelect+` WHERE i.org_id = $1 ORDER BY i.created_at`, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []integrationView{}
	for rows.Next() {
		v, err := scanIntegration(rows)
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

func validScopes(in []string) ([]string, error) {
	if len(in) > 50 {
		return nil, httpx.BadRequest("at most 50 scopes")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, sc := range in {
		sc = strings.TrimSpace(sc)
		if sc == "" || seen[sc] {
			continue
		}
		if len(sc) > 200 || strings.ContainsAny(sc, " ,\r\n") {
			return nil, httpx.BadRequest("invalid scope %q", sc)
		}
		seen[sc] = true
		out = append(out, sc)
	}
	return out, nil
}

func (s *Server) createIntegration(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Provider     string    `json:"provider"`
		Key          string    `json:"key"`
		Name         string    `json:"name"`
		ClientID     string    `json:"client_id"`
		ClientSecret string    `json:"client_secret"`
		Scopes       *[]string `json:"scopes"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	prov, ok := connect.Get(in.Provider)
	if !ok {
		names := []string{}
		for _, p := range connect.List() {
			names = append(names, p.Key)
		}
		return httpx.BadRequest("unknown provider %q; supported: %s", in.Provider, strings.Join(names, ", "))
	}
	if in.Key == "" {
		in.Key = prov.Key
	}
	if !integrationKeyRe.MatchString(in.Key) {
		return httpx.BadRequest("key must be 1-40 lowercase letters, digits, - or _")
	}
	if in.Name == "" {
		in.Name = prov.Name
	}
	name, err := requireName(in.Name, "name", 100)
	if err != nil {
		return err
	}
	scopes := []string{}
	var secretEnc []byte
	if prov.Auth == connect.OAuth2 {
		in.ClientID = strings.TrimSpace(in.ClientID)
		if in.ClientID == "" || len(in.ClientID) > 500 || strings.TrimSpace(in.ClientSecret) == "" {
			return httpx.BadRequest("%s needs your OAuth app's client_id and client_secret", prov.Name)
		}
		if in.Scopes == nil {
			scopes = prov.DefaultScopes
		} else if scopes, err = validScopes(*in.Scopes); err != nil {
			return err
		}
		if secretEnc, err = s.encryptSecret(r.Context(), orgID, strings.TrimSpace(in.ClientSecret)); err != nil {
			return err
		}
	} else {
		in.ClientID = ""
	}

	var v integrationView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO integrations (org_id, key, provider, name, client_id, client_secret_enc, scopes)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			orgID, in.Key, prov.Key, name, in.ClientID, secretEnc, scopes).Scan(&id); err != nil {
			if isUniqueViolation(err) {
				return httpx.Conflict("an integration with key %q already exists", in.Key)
			}
			return err
		}
		if v, err = scanIntegration(tx.QueryRow(r.Context(), integrationSelect+` WHERE i.id = $1`, id)); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "integration.create", "integration", id)
		e.Metadata = map[string]any{"provider": prov.Key, "key": in.Key, "scopes": scopes}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, v)
	return nil
}

func (s *Server) updateIntegration(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "integration")
	if err != nil {
		return err
	}
	var in struct {
		Name         *string   `json:"name"`
		ClientID     *string   `json:"client_id"`
		ClientSecret *string   `json:"client_secret"`
		Scopes       *[]string `json:"scopes"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var v integrationView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		cur, err := scanIntegration(tx.QueryRow(r.Context(), integrationSelect+` WHERE i.id = $1 AND i.org_id = $2 FOR UPDATE OF i`, id, orgID))
		if err != nil {
			return notFoundIfNoRows(err)
		}
		oauth := cur.Auth == string(connect.OAuth2)
		changed := []string{}
		sets := []string{"updated_at = now()"}
		args := []any{id}
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
		if oauth && in.ClientID != nil {
			cid := strings.TrimSpace(*in.ClientID)
			if cid == "" || len(cid) > 500 {
				return httpx.BadRequest("client_id is required")
			}
			set("client_id", cid)
		}
		if oauth && in.ClientSecret != nil {
			if strings.TrimSpace(*in.ClientSecret) == "" {
				return httpx.BadRequest("client_secret cannot be empty")
			}
			enc, err := s.encryptSecret(r.Context(), orgID, strings.TrimSpace(*in.ClientSecret))
			if err != nil {
				return err
			}
			set("client_secret_enc", enc)
		}
		if oauth && in.Scopes != nil {
			scopes, err := validScopes(*in.Scopes)
			if err != nil {
				return err
			}
			set("scopes", scopes)
		}
		if _, err := tx.Exec(r.Context(), `UPDATE integrations SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...); err != nil {
			return err
		}
		if v, err = scanIntegration(tx.QueryRow(r.Context(), integrationSelect+` WHERE i.id = $1`, id)); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "integration.update", "integration", id)
		e.Metadata = map[string]any{"fields": changed} // never the secret
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteIntegration(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "integration")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM integrations WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "integration.delete", "integration", id))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- connections -----------------------------------------------------------------

type connectionView struct {
	ID              string         `json:"id"`
	IntegrationID   string         `json:"integration_id"`
	IntegrationKey  string         `json:"integration_key"`
	IntegrationName string         `json:"integration_name"`
	Provider        string         `json:"provider"`
	EndUserID       string         `json:"end_user_id"`
	Status          string         `json:"status"`
	ExpiresAt       *time.Time     `json:"expires_at"`
	LastRefreshedAt *time.Time     `json:"last_refreshed_at"`
	RefreshFailures int            `json:"refresh_failures"`
	LastError       string         `json:"last_error"`
	BrokenAt        *time.Time     `json:"broken_at"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

const connectionSelect = `
	SELECT c.id, c.integration_id, i.key, i.name, i.provider, c.end_user_id, c.status, c.expires_at, c.last_refreshed_at,
	       c.refresh_failures, c.last_error, c.broken_at, c.metadata, c.created_at, c.updated_at
	FROM connections c JOIN integrations i ON i.id = c.integration_id`

func scanConnection(row pgx.Row) (connectionView, error) {
	var v connectionView
	var meta []byte
	err := row.Scan(&v.ID, &v.IntegrationID, &v.IntegrationKey, &v.IntegrationName, &v.Provider, &v.EndUserID, &v.Status,
		&v.ExpiresAt, &v.LastRefreshedAt, &v.RefreshFailures, &v.LastError, &v.BrokenAt, &meta, &v.CreatedAt, &v.UpdatedAt)
	v.Metadata = map[string]any{}
	_ = json.Unmarshal(meta, &v.Metadata)
	return v, err
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"c.org_id = $1"}
	args := []any{orgID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if ref := q.Get("integration"); ref != "" {
		if uuidRe.MatchString(ref) {
			add("i.id = ?", ref)
		} else {
			add("i.key = ?", ref)
		}
	}
	if u := q.Get("end_user_id"); u != "" {
		add("c.end_user_id = ?", u)
	}
	if st := q.Get("status"); st != "" {
		if st != "active" && st != "broken" {
			return httpx.BadRequest("status must be active or broken")
		}
		add("c.status = ?", st)
	}
	rows, err := s.Pool.Query(r.Context(), connectionSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY c.created_at DESC LIMIT 500`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []connectionView{}
	for rows.Next() {
		v, err := scanConnection(rows)
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

func (s *Server) getConnection(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "connection")
	if err != nil {
		return err
	}
	v, err := scanConnection(s.Pool.QueryRow(r.Context(), connectionSelect+` WHERE c.id = $1 AND c.org_id = $2`, id, orgID))
	if err != nil {
		return notFoundIfNoRows(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "connection")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM connections WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "connection.delete", "connection", id))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// refreshConnection renews the token now, e.g. to check a connection works.
// A failed refresh still answers 200 with the connection and the error.
func (s *Server) refreshConnection(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "connection")
	if err != nil {
		return err
	}
	if _, err := scanConnection(s.Pool.QueryRow(r.Context(), connectionSelect+` WHERE c.id = $1 AND c.org_id = $2`, id, orgID)); err != nil {
		return notFoundIfNoRows(err)
	}
	refreshErr := s.Connect.Refresh(r.Context(), id, true, false)
	var ce *connect.Error
	if refreshErr != nil && !errors.As(refreshErr, &ce) {
		return refreshErr
	}
	e := audit.ByPrincipal(p, orgID, "connection.refresh", "connection", id)
	if refreshErr != nil {
		e.Result, e.Metadata = "failure", map[string]any{"error": refreshErr.Error()}
	}
	if err := audit.Record(r.Context(), s.Pool, e); err != nil {
		return err
	}
	v, err := scanConnection(s.Pool.QueryRow(r.Context(), connectionSelect+` WHERE c.id = $1`, id))
	if err != nil {
		return err
	}
	out := map[string]any{"connection": v, "refreshed": refreshErr == nil}
	if refreshErr != nil {
		out["error"] = refreshErr.Error()
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// connectionToken hands a working access token to the organization's backend.
func (s *Server) connectionToken(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "connection")
	if err != nil {
		return err
	}
	t, err := s.Connect.Token(r.Context(), orgID, id)
	var ce *connect.Error
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return httpx.ErrNotFound
	case errors.Is(err, connect.ErrBroken):
		return httpx.NewError(http.StatusConflict, "connection_broken", err.Error()+"; the user must connect again")
	case errors.As(err, &ce) && ce.Permanent:
		return httpx.NewError(http.StatusConflict, "connection_broken", "the provider refused to renew access ("+ce.Error()+"); the user must connect again")
	case errors.As(err, &ce):
		return httpx.NewError(http.StatusBadGateway, "provider_unavailable", "could not renew the token: "+ce.Error())
	case err != nil:
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, t)
	return nil
}

// ---- Connect links -----------------------------------------------------------------

func (s *Server) createConnectSession(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Integration string `json:"integration"` // key or ID
		EndUserID   string `json:"end_user_id"`
		ReturnURL   string `json:"return_url"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	endUser, err := requireName(in.EndUserID, "end_user_id", 200)
	if err != nil {
		return err
	}
	if in.ReturnURL != "" {
		u, err := url.Parse(in.ReturnURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(in.ReturnURL) > 2000 {
			return httpx.BadRequest("return_url must be an absolute http(s) URL")
		}
	}
	col := "key"
	if uuidRe.MatchString(in.Integration) {
		col = "id"
	}
	var integrationID string
	if err := s.Pool.QueryRow(r.Context(), `SELECT id FROM integrations WHERE org_id = $1 AND `+col+` = $2`, orgID, in.Integration).
		Scan(&integrationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.BadRequest("no integration %q; create it first", in.Integration)
		}
		return err
	}
	token := auth.RandomToken("cs_")
	expires := time.Now().Add(connectSessionTTL).UTC()
	var id string
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO connect_sessions (org_id, integration_id, end_user_id, token_hash, return_url, expires_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			orgID, integrationID, endUser, hex.EncodeToString(auth.HashToken(token)), in.ReturnURL, expires, p.ActorID()).Scan(&id); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "connect_session.create", "integration", integrationID)
		e.Metadata = map[string]any{"end_user_id": endUser}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id": id, "url": s.DashboardURL + "/connect/" + token, "expires_at": expires,
	})
	return nil
}
