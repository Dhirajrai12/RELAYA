package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/auth"
	"relaya/internal/connect"
	"relaya/internal/httpx"
)

// Headers from the caller that go to the provider as they are. Anything else
// can be sent with the prefix "Relaya-Proxy-" (removed on the way), e.g.
// Relaya-Proxy-X-com-zoho-invoice-organizationid.
var proxyForward = []string{"Content-Type", "Accept", "Accept-Language", "If-Match", "If-None-Match", "If-Modified-Since", "Idempotency-Key"}

// Never taken from the caller, even with the prefix.
var proxyBlocked = map[string]bool{"Authorization": true, "Host": true, "Cookie": true, "Connection": true, "Content-Length": true,
	"Transfer-Encoding": true, "Proxy-Authorization": true, "Te": true, "Upgrade": true}

const (
	proxyMaxBody = 10 << 20
	proxyTimeout = 110 * time.Second
)

// proxyConnection calls the provider's API with the connection's token:
// /v1/orgs/{org}/connections/{connection}/proxy/{path...}. Errors that come
// from Relaya itself (not the provider) carry the header Relaya-Proxy-Error.
func (s *Server) proxyConnection(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	connID, err := pathID(r, "connection")
	if err != nil {
		return err
	}
	relayaErr := func(e *httpx.Error) error {
		w.Header().Set("Relaya-Proxy-Error", "true")
		return e
	}
	// Provider calls can outlast the server's 30s write timeout; allow up to
	// ~110s in total (IIS gives the backend 2 minutes).
	ctx, cancel := context.WithTimeout(r.Context(), proxyTimeout)
	defer cancel()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(proxyTimeout + 5*time.Second))
	_ = rc.SetReadDeadline(time.Now().Add(proxyTimeout))
	body, err := io.ReadAll(io.LimitReader(r.Body, proxyMaxBody+1))
	if err != nil {
		return relayaErr(httpx.BadRequest("could not read the request body"))
	}
	if len(body) > proxyMaxBody {
		return relayaErr(httpx.NewError(http.StatusRequestEntityTooLarge, "too_large", "request body over 10 MB"))
	}

	h := http.Header{}
	for _, k := range proxyForward {
		if v := r.Header.Values(k); len(v) > 0 {
			h[k] = v
		}
	}
	for k, v := range r.Header {
		if name, ok := strings.CutPrefix(k, "Relaya-Proxy-"); ok && name != "" && name != "Base-Url" {
			if name = http.CanonicalHeaderKey(name); !proxyBlocked[name] {
				h[name] = v
			}
		}
	}

	path := r.PathValue("path")
	start := time.Now()
	res, err := s.Connect.Proxy(ctx, s.ProxyHTTP, orgID, connID, connect.ProxyRequest{
		Method: r.Method, Path: path, RawQuery: r.URL.RawQuery, BaseURL: r.Header.Get("Relaya-Proxy-Base-Url"), Header: h, Body: body,
	})
	logCall := func(host string, status, attempts int, errText string) {
		// Best effort: a failed log write doesn't fail the call.
		_, _ = s.Pool.Exec(r.Context(), `
			INSERT INTO proxy_calls (org_id, connection_id, method, host, path, status, attempts, duration_ms, error, actor)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			orgID, connID, r.Method, host, "/"+strings.TrimLeft(truncate(path, 500), "/"), status, attempts,
			time.Since(start).Milliseconds(), truncate(errText, 500), p.ActorID())
	}

	var ce *connect.Error
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return httpx.ErrNotFound
	case errors.Is(err, connect.ErrHostNotAllowed):
		return relayaErr(httpx.BadRequest("%v", err))
	case errors.Is(err, connect.ErrBroken):
		logCall("", 0, 0, err.Error())
		return relayaErr(httpx.NewError(http.StatusConflict, "connection_broken", err.Error()+"; the user must connect again"))
	case errors.As(err, &ce) && ce.Permanent:
		logCall("", 0, 0, ce.Error())
		return relayaErr(httpx.NewError(http.StatusConflict, "connection_broken", "the provider refused to renew access ("+ce.Error()+"); the user must connect again"))
	case errors.As(err, &ce):
		logCall("", 0, 0, ce.Error())
		return relayaErr(httpx.NewError(http.StatusBadGateway, "provider_unavailable", ce.Error()))
	case err != nil:
		return err
	}
	defer res.Resp.Body.Close()

	for k, v := range res.Resp.Header {
		switch {
		case k == "Content-Type", k == "Etag", k == "Last-Modified", k == "Location", k == "Retry-After", k == "Link",
			strings.HasPrefix(k, "X-") && k != "X-Frame-Options":
			w.Header()[k] = v
		}
	}
	w.Header().Set("Relaya-Proxy-Attempts", strconv.Itoa(res.Attempts))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.Resp.StatusCode)
	_, copyErr := io.Copy(w, res.Resp.Body)
	errText := ""
	if copyErr != nil {
		errText = "response cut short: " + copyErr.Error()
	}
	logCall(res.Host, res.Resp.StatusCode, res.Attempts, errText)
	return nil
}

type proxyCallView struct {
	ID           int64     `json:"id"`
	ConnectionID string    `json:"connection_id"`
	EndUserID    string    `json:"end_user_id"`
	Integration  string    `json:"integration_name"`
	Method       string    `json:"method"`
	Host         string    `json:"host"`
	Path         string    `json:"path"`
	Status       int       `json:"status"`
	Attempts     int       `json:"attempts"`
	DurationMS   int       `json:"duration_ms"`
	Error        string    `json:"error"`
	CreatedAt    time.Time `json:"created_at"`
}

// listProxyCalls returns the latest API calls (100), optionally for one connection.
func (s *Server) listProxyCalls(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	q := `SELECT pc.id, pc.connection_id, c.end_user_id, i.name, pc.method, pc.host, pc.path, pc.status, pc.attempts, pc.duration_ms, pc.error, pc.created_at
		FROM proxy_calls pc JOIN connections c ON c.id = pc.connection_id JOIN integrations i ON i.id = c.integration_id
		WHERE pc.org_id = $1`
	args := []any{orgID}
	if id := r.URL.Query().Get("connection"); id != "" {
		if !uuidRe.MatchString(id) {
			return httpx.BadRequest("invalid connection")
		}
		q += ` AND pc.connection_id = $2`
		args = append(args, id)
	}
	rows, err := s.Pool.Query(r.Context(), q+` ORDER BY pc.created_at DESC LIMIT 100`, args...)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[proxyCallView])
	if err != nil {
		return err
	}
	if out == nil {
		out = []proxyCallView{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}
