package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"relaya/internal/api"
	"relaya/internal/connect"
	"relaya/internal/ingest"
)

// fakeProvider is an OAuth server plus a login API, switchable between
// healthy, down (503) and revoked (invalid_grant).
type fakeProvider struct {
	mu        sync.Mutex
	srv       *httptest.Server
	mode      string // "", "down", "revoked"
	refreshes int
	issued    int
	challenge string // PKCE challenge from the last authorize URL
}

func newFakeProvider(t *testing.T) *fakeProvider {
	f := &fakeProvider{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			form := r.PostForm
			if form.Get("client_id") != "cid-1" || form.Get("client_secret") != "client-secret-1" {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
				return
			}
			switch form.Get("grant_type") {
			case "authorization_code":
				sum := sha256.Sum256([]byte(form.Get("code_verifier")))
				if form.Get("code") != "good-code" || form.Get("redirect_uri") != "https://relaya.test/api/v1/connect/callback" ||
					base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"bad code, redirect or verifier"}`))
					return
				}
			case "refresh_token":
				f.refreshes++
				switch {
				case f.mode == "down":
					w.WriteHeader(503)
					return
				case f.mode == "revoked" || form.Get("refresh_token") != "refresh-1":
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
					return
				}
			}
			f.issued++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-" + strconv.Itoa(f.issued), "refresh_token": "refresh-1", "expires_in": 3600, "token_type": "Bearer", "scope": "read",
			})
		case "/login":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["password"] != "right-password" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"message":"Invalid email and password combination"}`))
				return
			}
			_, _ = w.Write([]byte(`{"token":"login-token"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeProvider) set(mode string) { f.mu.Lock(); f.mode = mode; f.mu.Unlock() }

func TestConnectionsEndToEnd(t *testing.T) {
	fake := newFakeProvider(t)
	defer connect.Override(&connect.Provider{Key: "fakeoauth", Name: "FakeOAuth", Auth: connect.OAuth2,
		AuthURL: fake.srv.URL + "/authorize", TokenURL: fake.srv.URL + "/token", DefaultScopes: []string{"read"}, PKCE: true,
		APIBase: "https://api.fake.test"})()
	defer connect.Override(&connect.Provider{Key: "fakelogin", Name: "FakeLogin", Auth: connect.Login,
		LoginURL: fake.srv.URL + "/login", LoginTTL: 9 * 24 * time.Hour, APIBase: "https://api.fakelogin.test"})()

	var svc *connect.Service
	e := setupWith(t, func(s *api.Server, _ *ingest.Handler) {
		svc = &connect.Service{Pool: s.Pool, Vault: s.Vault, Client: connect.NewClient(), RedirectURI: "https://relaya.test/api/v1/connect/callback"}
		s.Connect = svc
		s.DashboardURL = "https://relaya.test"
	})
	ctx := context.Background()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// callback hits the OAuth redirect URI like a browser and returns where it was sent.
	callback := func(q url.Values) *url.URL {
		t.Helper()
		resp, err := noRedirect.Get(e.api.URL + "/v1/connect/callback?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 302 {
			t.Fatalf("callback status %d", resp.StatusCode)
		}
		u, _ := url.Parse(resp.Header.Get("Location"))
		return u
	}

	owner := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "o@example.com", "password": "correct-horse-1", "org_name": "Acme"}, 201)
	tok := owner["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "webhook", "name": "Ops", "url": "https://ops.example/hook",
		"events": []string{"connection_broken", "connection_recovered"}}, 201)

	provs := e.call("GET", "/v1/connect/providers", tok, nil, 200)
	if provs["callback_url"] != "https://relaya.test/api/v1/connect/callback" || !strings.Contains(mustJSON(provs), `"key":"zoho"`) {
		t.Fatalf("providers: %v", provs)
	}

	// ---- integration: the org's own OAuth app ----
	e.call("POST", base+"/integrations", tok, map[string]any{"provider": "fakeoauth", "client_id": "cid-1"}, 400) // no secret
	e.call("POST", base+"/integrations", tok, map[string]any{"provider": "nope"}, 400)
	integ := e.call("POST", base+"/integrations", tok, map[string]any{"provider": "fakeoauth", "client_id": "cid-1", "client_secret": "client-secret-1"}, 201)
	if integ["key"] != "fakeoauth" || integ["has_client_secret"] != true || strings.Contains(mustJSON(integ), "client-secret-1") {
		t.Fatalf("integration: %v", integ)
	}
	e.call("POST", base+"/integrations", tok, map[string]any{"provider": "fakeoauth", "client_id": "cid-1", "client_secret": "x"}, 409)

	// ---- Connect link → provider login → callback ----
	e.call("POST", base+"/connect-sessions", tok, map[string]any{"integration": "fakeoauth", "end_user_id": "user-42", "return_url": "javascript:alert(1)"}, 400)
	e.call("POST", base+"/connect-sessions", tok, map[string]any{"integration": "missing", "end_user_id": "user-42"}, 400)
	sess := e.call("POST", base+"/connect-sessions", tok, map[string]any{"integration": "fakeoauth", "end_user_id": "user-42", "return_url": "https://app.example/done?x=1"}, 201)
	link := sess["url"].(string)
	if !strings.HasPrefix(link, "https://relaya.test/connect/cs_") {
		t.Fatalf("link %s", link)
	}
	linkTok := strings.TrimPrefix(link, "https://relaya.test/connect/")
	pub := "/v1/connect/sessions/" + linkTok

	info := e.call("GET", pub, "", nil, 200)
	if info["status"] != "open" || info["provider_name"] != "FakeOAuth" || info["org_name"] != "Acme" || info["auth"] != "oauth2" {
		t.Fatalf("session info: %v", info)
	}
	e.call("GET", "/v1/connect/sessions/cs_unknown", "", nil, 404)
	e.call("POST", pub+"/login", "", map[string]any{"email": "a", "password": "b"}, 400) // OAuth provider: no login form

	authz := e.call("POST", pub+"/authorize", "", nil, 200)
	au, _ := url.Parse(authz["redirect_url"].(string))
	aq := au.Query()
	if !strings.HasPrefix(authz["redirect_url"].(string), fake.srv.URL+"/authorize?") || aq.Get("client_id") != "cid-1" ||
		aq.Get("redirect_uri") != "https://relaya.test/api/v1/connect/callback" || aq.Get("scope") != "read" || aq.Get("state") == "" || aq.Get("code_challenge") == "" {
		t.Fatalf("authorize URL: %s", au)
	}
	fake.challenge = aq.Get("code_challenge")
	state := aq.Get("state")

	// A wrong code fails without using up the link; the right one connects.
	if u := callback(url.Values{"state": {state}, "code": {"bad-code"}}); u.Query().Get("status") != "error" || u.Host != "app.example" {
		t.Fatalf("bad code redirect: %s", u)
	}
	u := callback(url.Values{"state": {state}, "code": {"good-code"}})
	connID := u.Query().Get("connection_id")
	if u.Host != "app.example" || u.Query().Get("status") != "connected" || connID == "" || u.Query().Get("end_user_id") != "user-42" || u.Query().Get("x") != "1" {
		t.Fatalf("success redirect: %s", u)
	}
	// Browser back/refresh: same answer, no second connection.
	if u2 := callback(url.Values{"state": {state}, "code": {"good-code"}}); u2.Query().Get("connection_id") != connID {
		t.Fatalf("second callback: %s", u2)
	}
	if e.call("GET", pub, "", nil, 200)["status"] != "completed" {
		t.Fatal("session not completed")
	}
	e.call("POST", pub+"/authorize", "", nil, 409)
	// Unknown state: lands on Relaya's result page with an error.
	if u := callback(url.Values{"state": {"forged"}, "code": {"good-code"}}); u.Host != "relaya.test" || u.Path != "/connect/result" || u.Query().Get("status") != "error" {
		t.Fatalf("forged state: %s", u)
	}

	conns := e.call("GET", base+"/connections?integration=fakeoauth", tok, nil, 200)["data"].([]any)
	if len(conns) != 1 {
		t.Fatalf("connections: %v", conns)
	}
	c0 := conns[0].(map[string]any)
	if c0["id"] != connID || c0["status"] != "active" || c0["end_user_id"] != "user-42" || c0["expires_at"] == nil {
		t.Fatalf("connection: %v", c0)
	}
	if s := mustJSON(conns); strings.Contains(s, "access-") || strings.Contains(s, "refresh-1") {
		t.Fatal("tokens leaked in connection list")
	}

	// ---- token for the org's backend ----
	tk := e.call("GET", base+"/connections/"+connID+"/token", tok, nil, 200)
	if tk["access_token"] != "access-1" || tk["api_base"] != "https://api.fake.test" || tk["token_type"] != "Bearer" {
		t.Fatalf("token: %v", tk)
	}
	// About to expire: the token call refreshes first.
	e.pool.Exec(ctx, `UPDATE connections SET expires_at = now() + interval '30 seconds' WHERE id = $1`, connID)
	if tk = e.call("GET", base+"/connections/"+connID+"/token", tok, nil, 200); tk["access_token"] != "access-2" {
		t.Fatalf("token after refresh: %v", tk)
	}

	// ---- worker refresh ----
	e.pool.Exec(ctx, `UPDATE connections SET expires_at = now() + interval '5 minutes' WHERE id = $1`, connID)
	if n, err := svc.RefreshDue(ctx); err != nil || n != 1 {
		t.Fatalf("refresh due: %d %v", n, err)
	}
	if n, _ := svc.RefreshDue(ctx); n != 0 {
		t.Fatal("refreshed a fresh token again")
	}

	// Provider down: stays active, counts the failure, backs off.
	fake.set("down")
	e.pool.Exec(ctx, `UPDATE connections SET expires_at = now() + interval '5 minutes', updated_at = now() - interval '1 hour' WHERE id = $1`, connID)
	svc.RefreshDue(ctx)
	c1 := e.call("GET", base+"/connections/"+connID, tok, nil, 200)
	if c1["status"] != "active" || c1["refresh_failures"] != float64(1) || !strings.Contains(c1["last_error"].(string), "503") {
		t.Fatalf("after outage: %v", c1)
	}
	before := fake.refreshes
	svc.RefreshDue(ctx) // within the backoff: not retried
	if fake.refreshes != before {
		t.Fatal("retried inside the backoff")
	}

	// Access revoked: broken, one alert, token calls say so.
	fake.set("revoked")
	e.pool.Exec(ctx, `UPDATE connections SET updated_at = now() - interval '1 hour' WHERE id = $1`, connID)
	svc.RefreshDue(ctx)
	c2 := e.call("GET", base+"/connections/"+connID, tok, nil, 200)
	if c2["status"] != "broken" || c2["broken_at"] == nil || !strings.Contains(c2["last_error"].(string), "invalid_grant") {
		t.Fatalf("after revoke: %v", c2)
	}
	e.call("GET", base+"/connections/"+connID+"/token", tok, nil, 409)
	r := e.call("POST", base+"/connections/"+connID+"/refresh", tok, nil, 200) // manual retry: still refused
	if r["refreshed"] != false || !strings.Contains(r["error"].(string), "invalid_grant") {
		t.Fatalf("manual refresh: %v", r)
	}
	if n := countAlerts(t, e, orgID, "connection_broken"); n != 1 {
		t.Fatalf("broken alerts: %d", n)
	}
	if f := e.call("GET", base+"/integrations", tok, nil, 200)["data"].([]any)[0].(map[string]any); f["connections"] != float64(1) || f["broken"] != float64(1) {
		t.Fatalf("integration counts: %v", f)
	}

	// ---- reconnect: same connection, working again, recovered alert ----
	fake.set("")
	sess2 := e.call("POST", base+"/connect-sessions", tok, map[string]any{"integration": integ["id"], "end_user_id": "user-42"}, 201)
	pub2 := "/v1/connect/sessions/" + strings.TrimPrefix(sess2["url"].(string), "https://relaya.test/connect/")
	a2, _ := url.Parse(e.call("POST", pub2+"/authorize", "", nil, 200)["redirect_url"].(string))
	fake.challenge = a2.Query().Get("code_challenge")
	u = callback(url.Values{"state": {a2.Query().Get("state")}, "code": {"good-code"}})
	if u.Host != "relaya.test" || u.Path != "/connect/result" || u.Query().Get("status") != "connected" || u.Query().Get("provider") != "FakeOAuth" {
		t.Fatalf("reconnect redirect: %s", u)
	}
	c3 := e.call("GET", base+"/connections/"+connID, tok, nil, 200)
	if c3["status"] != "active" || c3["refresh_failures"] != float64(0) || c3["last_error"] != "" {
		t.Fatalf("after reconnect: %v", c3)
	}
	if n := countAlerts(t, e, orgID, "connection_recovered"); n != 1 {
		t.Fatalf("recovered alerts: %d", n)
	}

	// The user declines at the provider.
	sess3 := e.call("POST", base+"/connect-sessions", tok, map[string]any{"integration": "fakeoauth", "end_user_id": "user-7"}, 201)
	pub3 := "/v1/connect/sessions/" + strings.TrimPrefix(sess3["url"].(string), "https://relaya.test/connect/")
	a3, _ := url.Parse(e.call("POST", pub3+"/authorize", "", nil, 200)["redirect_url"].(string))
	if u := callback(url.Values{"state": {a3.Query().Get("state")}, "error": {"access_denied"}}); u.Query().Get("status") != "error" ||
		!strings.Contains(u.Query().Get("error"), "not granted") {
		t.Fatalf("denied: %s", u)
	}
	if info := e.call("GET", pub3, "", nil, 200); info["status"] != "open" || info["error"] == "" {
		t.Fatalf("session after denial: %v", info)
	}

	// Expired link.
	e.pool.Exec(ctx, `UPDATE connect_sessions SET expires_at = now() - interval '1 minute' WHERE end_user_id = 'user-7'`)
	e.call("POST", pub3+"/authorize", "", nil, 410)

	// ---- login provider (Shiprocket-style) ----
	e.call("POST", base+"/integrations", tok, map[string]any{"provider": "fakelogin"}, 201)
	sess4 := e.call("POST", base+"/connect-sessions", tok, map[string]any{"integration": "fakelogin", "end_user_id": "shop-1"}, 201)
	pub4 := "/v1/connect/sessions/" + strings.TrimPrefix(sess4["url"].(string), "https://relaya.test/connect/")
	if e.call("GET", pub4, "", nil, 200)["auth"] != "login" {
		t.Fatal("login provider auth type")
	}
	e.call("POST", pub4+"/authorize", "", nil, 400)
	e.call("POST", pub4+"/login", "", map[string]any{"email": "api@shop.in", "password": "wrong"}, 400)
	done := e.call("POST", pub4+"/login", "", map[string]any{"email": "api@shop.in", "password": "right-password"}, 200)
	if done["status"] != "connected" || !strings.Contains(done["redirect_url"].(string), "/connect/result?") {
		t.Fatalf("login connect: %v", done)
	}
	e.call("POST", pub4+"/login", "", map[string]any{"email": "api@shop.in", "password": "right-password"}, 409)
	lc := e.call("GET", base+"/connections?integration=fakelogin", tok, nil, 200)["data"].([]any)[0].(map[string]any)
	if tk := e.call("GET", base+"/connections/"+lc["id"].(string)+"/token", tok, nil, 200); tk["access_token"] != "login-token" {
		t.Fatalf("login token: %v", tk)
	}

	// Secrets never reach the audit log.
	if logs := mustJSON(e.call("GET", base+"/audit-logs?limit=200", tok, nil, 200)); strings.Contains(logs, "client-secret-1") ||
		strings.Contains(logs, "right-password") || strings.Contains(logs, "access-") || strings.Contains(logs, "refresh-1") {
		t.Fatal("secret in audit log")
	}

	// Deleting the integration removes its connections.
	e.call("DELETE", base+"/integrations/"+integ["id"].(string), tok, nil, 204)
	e.call("GET", base+"/connections/"+connID, tok, nil, 404)
}

func countAlerts(t *testing.T, e *env, orgID, kind string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM alerts WHERE org_id = $1 AND kind = $2`, orgID, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
