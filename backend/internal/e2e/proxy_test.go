package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/api"
	"relaya/internal/connect"
	"relaya/internal/ingest"
)

// fakeAPI is a provider API: /echo reports what it received, /flaky fails
// once with 503, /strict rejects the first token with 401, /redirect points elsewhere.
type fakeAPI struct {
	mu    sync.Mutex
	srv   *httptest.Server
	flaky int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		auth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/echo":
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-RateLimit-Remaining", "99")
			w.Header().Set("Set-Cookie", "session=provider")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"method": r.Method, "auth": auth, "query": r.URL.RawQuery, "custom": r.Header.Get("X-Custom"),
				"leaked_auth": r.Header.Values("Authorization"), "content_type": r.Header.Get("Content-Type"), "body": string(body),
			})
		case "/api/flaky":
			f.flaky++
			if f.flaky == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/strict":
			if auth == "Bearer access-seed" { // revoked early at the provider
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/redirect":
			http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestProxyEndToEnd(t *testing.T) {
	fake := newFakeProvider(t) // token endpoint
	apiSrv := newFakeAPI(t)
	local := regexp.MustCompile(`^http://127\.0\.0\.1:\d+$`)
	defer connect.Override(&connect.Provider{Key: "fakeoauth", Name: "FakeOAuth", Auth: connect.OAuth2,
		AuthURL: fake.srv.URL + "/authorize", TokenURL: fake.srv.URL + "/token", APIBase: apiSrv.srv.URL + "/api",
		ProxyHosts: []*regexp.Regexp{local}})()
	defer connect.Override(&connect.Provider{Key: "fakezoho", Name: "FakeZoho", Auth: connect.OAuth2,
		AuthURL: fake.srv.URL + "/authorize", TokenURL: fake.srv.URL + "/token", APIBase: apiSrv.srv.URL + "/api",
		ProxyHosts: []*regexp.Regexp{local}, AuthScheme: "Zoho-oauthtoken"})()

	var svc *connect.Service
	e := setupWith(t, func(s *api.Server, _ *ingest.Handler) { svc = s.Connect })
	ctx := context.Background()
	owner := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "p@example.com", "password": "correct-horse-1", "org_name": "Proxy Co"}, 201)
	tok := owner["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID

	connectAs := func(provider, user string) string {
		integ := e.call("POST", base+"/integrations", tok, map[string]any{"provider": provider, "client_id": "cid-1", "client_secret": "client-secret-1"}, 201)
		exp := time.Now().Add(time.Hour)
		var id string
		if err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) (err error) {
			id, err = svc.Save(ctx, tx, orgID, integ["id"].(string), user,
				connect.Credentials{AccessToken: "access-seed", RefreshToken: "refresh-1", ExpiresAt: &exp}, connect.Metadata{})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	connID := connectAs("fakeoauth", "user-1")
	px := base + "/connections/" + connID + "/proxy/"

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// raw sends a proxy request and returns status, headers and the JSON body.
	raw := func(method, path, body string, hdr map[string]string) (int, http.Header, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, e.api.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		b, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(b, &out)
		return resp.StatusCode, resp.Header, out
	}

	// GET: query, allowed headers and prefixed custom headers go through; the
	// caller's Relaya credentials never do; the user's token is added.
	st, h, out := raw("GET", px+"echo?x=1&y=two", "", map[string]string{
		"Accept": "application/json", "Relaya-Proxy-X-Custom": "abc", "Relaya-Proxy-Authorization": "Bearer evil", "Cookie": "relaya=1",
	})
	if st != 200 || out["auth"] != "Bearer access-seed" || out["query"] != "x=1&y=two" || out["custom"] != "abc" || len(out["leaked_auth"].([]any)) != 1 {
		t.Fatalf("echo: %d %v", st, out)
	}
	if h.Get("X-Ratelimit-Remaining") != "99" || h.Get("Set-Cookie") != "" || h.Get("Relaya-Proxy-Attempts") != "1" || h.Get("Relaya-Proxy-Error") != "" {
		t.Fatalf("echo headers: %v", h)
	}
	// POST body and content type.
	st, _, out = raw("POST", px+"echo", `{"name":"Asha"}`, map[string]string{"Content-Type": "application/json"})
	if st != 200 || out["method"] != "POST" || out["body"] != `{"name":"Asha"}` || out["content_type"] != "application/json" {
		t.Fatalf("post: %d %v", st, out)
	}

	// 503 then 200: GET is retried, POST is not (it might have been processed).
	st, h, _ = raw("GET", px+"flaky", "", nil)
	if st != 200 || h.Get("Relaya-Proxy-Attempts") != "2" {
		t.Fatalf("flaky GET: %d attempts %s", st, h.Get("Relaya-Proxy-Attempts"))
	}
	apiSrv.mu.Lock()
	apiSrv.flaky = 0
	apiSrv.mu.Unlock()
	if st, h, _ = raw("POST", px+"flaky", "{}", nil); st != 503 || h.Get("Relaya-Proxy-Attempts") != "1" {
		t.Fatalf("flaky POST: %d attempts %s", st, h.Get("Relaya-Proxy-Attempts"))
	}

	// 401: the token is renewed and the call made again, once.
	st, h, _ = raw("GET", px+"strict", "", nil)
	if st != 200 || h.Get("Relaya-Proxy-Attempts") != "2" {
		t.Fatalf("strict: %d attempts %s", st, h.Get("Relaya-Proxy-Attempts"))
	}
	if st, _, out = raw("GET", px+"echo", "", nil); !strings.HasPrefix(out["auth"].(string), "Bearer access-") || out["auth"] == "Bearer access-seed" {
		t.Fatalf("token not renewed: %v", out)
	}

	// Redirects are handed back, never followed with the token.
	if st, h, _ = raw("GET", px+"redirect", "", nil); st != 302 || h.Get("Location") != "https://evil.example/steal" {
		t.Fatalf("redirect: %d %v", st, h)
	}

	// The token only goes to the provider's hosts.
	st, h, out = raw("GET", px+"echo", "", map[string]string{"Relaya-Proxy-Base-Url": "https://evil.example"})
	if st != 400 || h.Get("Relaya-Proxy-Error") != "true" {
		t.Fatalf("evil base: %d %v", st, out)
	}
	if st, _, _ = raw("GET", px+"echo", "", map[string]string{"Relaya-Proxy-Base-Url": apiSrv.srv.URL + "/api"}); st != 200 {
		t.Fatalf("allowed base override: %d", st)
	}

	// Zoho-style providers get their own header format.
	zohoConn := connectAs("fakezoho", "user-2")
	if _, _, out = raw("GET", base+"/connections/"+zohoConn+"/proxy/echo", "", nil); out["auth"] != "Zoho-oauthtoken access-seed" {
		t.Fatalf("zoho auth: %v", out)
	}

	// Broken connection: a clear error from Relaya, not the provider.
	e.pool.Exec(ctx, `UPDATE connections SET status = 'broken', last_error = 'invalid_grant' WHERE id = $1`, connID)
	st, h, out = raw("GET", px+"echo", "", nil)
	if st != 409 || h.Get("Relaya-Proxy-Error") != "true" || out["error"].(map[string]any)["code"] != "connection_broken" {
		t.Fatalf("broken: %d %v", st, out)
	}
	raw("GET", base+"/connections/00000000-0000-0000-0000-000000000000/proxy/echo", "", nil) // unknown: 404, nothing logged

	// Call log: newest first, no query strings, statuses and attempts.
	calls := e.call("GET", base+"/proxy-calls?connection="+connID, tok, nil, 200)["data"].([]any)
	if len(calls) != 9 { // the refused evil.example call never left Relaya, so it isn't logged

		t.Fatalf("logged %d calls: %v", len(calls), calls)
	}
	first := calls[len(calls)-1].(map[string]any)
	if first["method"] != "GET" || first["path"] != "/echo" || first["status"] != float64(200) || strings.Contains(mustJSON(calls), "x=1") {
		t.Fatalf("first logged call: %v", first)
	}
	if last := calls[0].(map[string]any); last["status"] != float64(0) || !strings.Contains(last["error"].(string), "broken") {
		t.Fatalf("last logged call: %v", last)
	}
	if s := mustJSON(calls); strings.Contains(s, "access-") {
		t.Fatal("token in call log")
	}
}
