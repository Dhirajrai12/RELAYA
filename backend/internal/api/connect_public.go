package api

import (
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/auth"
	"relaya/internal/connect"
	"relaya/internal/httpx"
)

// The Connect page (web: /connect/{token}) is opened by an organization's end
// user, who has no Relaya login: the link token is the only credential. These
// endpoints are public and rate-limited per IP.

type connectSession struct {
	id, orgID, orgName, integrationID, integrationName, providerKey, endUserID string
	clientID, state, codeVerifier, returnURL, errText                          string
	clientSecretEnc                                                            []byte
	scopes                                                                     []string
	expiresAt                                                                  time.Time
	completedAt                                                                *time.Time
	connectionID                                                               *string
	popup                                                                      bool
}

const connectSessionSelect = `
	SELECT s.id, s.org_id, o.name, s.integration_id, i.name, i.provider, s.end_user_id,
	       i.client_id, coalesce(s.state, ''), s.code_verifier, s.return_url, s.error,
	       i.client_secret_enc, i.scopes, s.expires_at, s.completed_at, s.connection_id, s.popup
	FROM connect_sessions s
	JOIN integrations i ON i.id = s.integration_id
	JOIN organizations o ON o.id = s.org_id`

func scanConnectSession(row pgx.Row) (connectSession, error) {
	var c connectSession
	err := row.Scan(&c.id, &c.orgID, &c.orgName, &c.integrationID, &c.integrationName, &c.providerKey, &c.endUserID,
		&c.clientID, &c.state, &c.codeVerifier, &c.returnURL, &c.errText,
		&c.clientSecretEnc, &c.scopes, &c.expiresAt, &c.completedAt, &c.connectionID, &c.popup)
	return c, err
}

// markPopup records that the link runs in connect.js's popup (?popup=1), so
// the result page can close itself.
func (s *Server) markPopup(r *http.Request, c connectSession) error {
	if r.URL.Query().Get("popup") != "1" || c.popup {
		return nil
	}
	_, err := s.Pool.Exec(r.Context(), `UPDATE connect_sessions SET popup = true WHERE id = $1`, c.id)
	return err
}

func (c connectSession) status() string {
	switch {
	case c.completedAt != nil:
		return "completed"
	case time.Now().After(c.expiresAt):
		return "expired"
	}
	return "open"
}

func (s *Server) limitConnect(r *http.Request, w http.ResponseWriter) error {
	if ok, wait := s.Limits.ConnectIP.Allow(httpx.ClientIP(r, s.TrustProxyHeaders)); !ok {
		return httpx.TooManyRequests(w, wait, "too many requests; wait a moment and try again")
	}
	return nil
}

// sessionByToken loads the session for a link token (404 for unknown tokens).
func (s *Server) sessionByToken(r *http.Request) (connectSession, *connect.Provider, error) {
	token := r.PathValue("token")
	if !strings.HasPrefix(token, "cs_") || len(token) > 100 {
		return connectSession{}, nil, httpx.ErrNotFound
	}
	c, err := scanConnectSession(s.Pool.QueryRow(r.Context(), connectSessionSelect+` WHERE s.token_hash = $1`,
		hex.EncodeToString(auth.HashToken(token))))
	if err != nil {
		return c, nil, notFoundIfNoRows(err)
	}
	p, ok := connect.Get(c.providerKey)
	if !ok {
		return c, nil, httpx.ErrNotFound
	}
	return c, p, nil
}

func (s *Server) getConnectSession(w http.ResponseWriter, r *http.Request) error {
	// connect.js polls this from the developer's own site: readable from any
	// origin, without credentials (the link token in the URL is the credential).
	// Set first, so errors (404, 429) are readable too.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.limitConnect(r, w); err != nil {
		return err
	}
	c, p, err := s.sessionByToken(r)
	if err != nil {
		return err
	}
	out := map[string]any{
		"org_name":         c.orgName,
		"integration_name": c.integrationName,
		"provider":         p.Key,
		"provider_name":    p.Name,
		"auth":             p.Auth,
		"login_label":      p.LoginLabel,
		"status":           c.status(),
		"started":          c.state != "", // the user went on to the provider's sign-in
		"error":            c.errText,
		"expires_at":       c.expiresAt,
	}
	if c.completedAt != nil && c.connectionID != nil {
		out["connection_id"] = *c.connectionID
		out["end_user_id"] = c.endUserID
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func openOrError(c connectSession) error {
	switch c.status() {
	case "completed":
		return httpx.Conflict("this link was already used; the account is connected")
	case "expired":
		return httpx.NewError(http.StatusGone, "expired", "this link has expired; ask for a new one")
	}
	return nil
}

// authorizeConnectSession starts the provider login: returns the URL to send the user to.
func (s *Server) authorizeConnectSession(w http.ResponseWriter, r *http.Request) error {
	if err := s.limitConnect(r, w); err != nil {
		return err
	}
	c, p, err := s.sessionByToken(r)
	if err != nil {
		return err
	}
	if err := openOrError(c); err != nil {
		return err
	}
	if p.Auth != connect.OAuth2 {
		return httpx.BadRequest("%s connects with a login form, not a redirect", p.Name)
	}
	if err := s.markPopup(r, c); err != nil {
		return err
	}
	state, verifier := connect.NewState(p)
	if _, err := s.Pool.Exec(r.Context(), `UPDATE connect_sessions SET state = $2, code_verifier = $3, error = '' WHERE id = $1`,
		c.id, state, verifier); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{
		"redirect_url": connect.AuthorizeURL(p, c.clientID, s.Connect.RedirectURI, c.scopes, state, verifier),
	})
	return nil
}

// loginConnectSession connects a login-based provider (Shiprocket) with the user's API login.
func (s *Server) loginConnectSession(w http.ResponseWriter, r *http.Request) error {
	if err := s.limitConnect(r, w); err != nil {
		return err
	}
	c, p, err := s.sessionByToken(r)
	if err != nil {
		return err
	}
	if err := openOrError(c); err != nil {
		return err
	}
	if p.Auth != connect.Login {
		return httpx.BadRequest("%s connects by signing in at %s", p.Name, p.Name)
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Email = strings.TrimSpace(in.Email)
	if in.Email == "" || in.Password == "" || len(in.Email) > 320 || len(in.Password) > 500 {
		return httpx.BadRequest("email and password are required")
	}
	cred, err := s.Connect.Client.Login(r.Context(), p, in.Email, in.Password)
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) && ce.Permanent {
			return httpx.BadRequest("%s did not accept this login: %s", p.Name, ce.Message)
		}
		return httpx.NewError(http.StatusBadGateway, "provider_unavailable", "could not reach "+p.Name+"; try again in a moment")
	}
	if err := s.markPopup(r, c); err != nil {
		return err
	}
	c.popup = c.popup || r.URL.Query().Get("popup") == "1"
	connID, err := s.completeConnect(r, c, cred, connect.Metadata{})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "connected", "redirect_url": s.connectResultURL(c, p, "connected", connID, "")})
	return nil
}

// connectCallback is the OAuth redirect URI: providers send the user's browser
// here with a code (or an error). It always answers with a redirect.
func (s *Server) connectCallback(w http.ResponseWriter, r *http.Request) error {
	if err := s.limitConnect(r, w); err != nil {
		return err
	}
	q := r.URL.Query()
	state := q.Get("state")
	fail := func(c *connectSession, p *connect.Provider, msg string) error {
		if c != nil && c.id != "" {
			_, _ = s.Pool.Exec(r.Context(), `UPDATE connect_sessions SET error = $2 WHERE id = $1 AND completed_at IS NULL`, c.id, msg)
		}
		var sess connectSession
		if c != nil {
			sess = *c
		}
		http.Redirect(w, r, s.connectResultURL(sess, p, "error", "", msg), http.StatusFound)
		return nil
	}
	if state == "" {
		return fail(nil, nil, "The sign-in response is missing its state. Start again from your link.")
	}
	c, err := scanConnectSession(s.Pool.QueryRow(r.Context(), connectSessionSelect+` WHERE s.state = $1`, state))
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(nil, nil, "This sign-in is not recognised. Start again from your link.")
	}
	if err != nil {
		return err
	}
	p, ok := connect.Get(c.providerKey)
	if !ok {
		return fail(&c, nil, "This app is no longer available.")
	}
	if c.completedAt != nil && c.connectionID != nil { // the browser came back twice
		http.Redirect(w, r, s.connectResultURL(c, p, "connected", *c.connectionID, ""), http.StatusFound)
		return nil
	}
	if c.status() == "expired" {
		return fail(&c, p, "This link has expired. Ask for a new one.")
	}
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		if e == "access_denied" {
			msg = "Access was not granted at " + p.Name + "."
		}
		return fail(&c, p, truncate(msg, 300))
	}
	code := q.Get("code")
	if code == "" {
		return fail(&c, p, p.Name+" did not return an authorization code.")
	}
	app := connect.App{ClientID: c.clientID}
	if len(c.clientSecretEnc) > 0 {
		secret, err := s.Vault.Decrypt(r.Context(), c.orgID, c.clientSecretEnc)
		if err != nil {
			return err
		}
		app.ClientSecret = string(secret)
	}
	cred, meta, err := s.Connect.Client.Exchange(r.Context(), p, app, code, s.Connect.RedirectURI, c.codeVerifier, q)
	if err != nil {
		slog.Warn("connect: code exchange failed", "provider", p.Key, "org", c.orgID, "err", err)
		return fail(&c, p, "Could not finish connecting to "+p.Name+": "+truncate(err.Error(), 300))
	}
	connID, err := s.completeConnect(r, c, cred, meta)
	if err != nil {
		return err
	}
	http.Redirect(w, r, s.connectResultURL(c, p, "connected", connID, ""), http.StatusFound)
	return nil
}

// completeConnect saves the connection and closes the session, in one transaction.
func (s *Server) completeConnect(r *http.Request, c connectSession, cred connect.Credentials, meta connect.Metadata) (string, error) {
	var connID string
	err := s.tx(r.Context(), func(tx pgx.Tx) error {
		// Claim the session first, so a double submit can't connect twice.
		tag, err := tx.Exec(r.Context(), `
			UPDATE connect_sessions SET completed_at = now(), error = '' WHERE id = $1 AND completed_at IS NULL AND expires_at > now()`, c.id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("this link was already used or has expired")
		}
		if connID, err = s.Connect.Save(r.Context(), tx, c.orgID, c.integrationID, c.endUserID, cred, meta); err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `UPDATE connect_sessions SET connection_id = $2 WHERE id = $1`, c.id, connID)
		return err
	})
	return connID, err
}

// connectResultURL is where the user lands afterwards: the organization's
// return_url when set, else Relaya's result page.
func (s *Server) connectResultURL(c connectSession, p *connect.Provider, status, connID, errMsg string) string {
	q := url.Values{"status": {status}}
	if errMsg != "" {
		q.Set("error", errMsg)
	}
	if c.returnURL != "" {
		if u, err := url.Parse(c.returnURL); err == nil {
			rq := u.Query()
			for k, v := range q {
				rq[k] = v
			}
			if connID != "" {
				rq.Set("connection_id", connID)
			}
			rq.Set("end_user_id", c.endUserID)
			u.RawQuery = rq.Encode()
			return u.String()
		}
	}
	if p != nil {
		q.Set("provider", p.Name)
	}
	if c.orgName != "" {
		q.Set("org", c.orgName)
	}
	if connID != "" {
		q.Set("connection_id", connID)
	}
	if c.popup {
		q.Set("popup", "1") // connect.js is waiting: the page closes itself
	}
	return s.DashboardURL + "/connect/result?" + q.Encode()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
