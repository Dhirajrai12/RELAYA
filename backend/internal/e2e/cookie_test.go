package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The dashboard's session is an httpOnly cookie; API keys keep using the Authorization header.
func TestSessionCookie(t *testing.T) {
	e := setup(t)
	body, _ := json.Marshal(map[string]any{"email": "cookie@example.com", "password": "correct-horse-8", "org_name": "Cookie Co"})
	res, err := http.Post(e.api.URL+"/v1/auth/signup", "application/json", bytes.NewReader(body))
	if err != nil || res.StatusCode != 201 {
		t.Fatalf("signup: %v %v", err, res.Status)
	}
	var sess struct {
		Token     string
		ExpiresAt time.Time `json:"expires_at"`
	}
	json.NewDecoder(res.Body).Decode(&sess)
	res.Body.Close()

	var c *http.Cookie
	for _, x := range res.Cookies() {
		if x.Name == "relaya_session" {
			c = x
		}
	}
	if c == nil || c.Value != sess.Token || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie: %+v", c)
	}
	if d := c.Expires.Sub(sess.ExpiresAt); d > time.Second || d < -time.Second { // the cookie ends with the session
		t.Fatalf("cookie expires %s, session %s", c.Expires, sess.ExpiresAt)
	}

	do := func(method, path, bearer string, cookie *http.Cookie) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, e.api.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r
	}

	// The cookie alone signs the dashboard in.
	if r := do("GET", "/v1/me", "", c); r.StatusCode != 200 {
		t.Fatalf("me with cookie: %d", r.StatusCode)
	}
	orgID := e.call("GET", "/v1/me", sess.Token, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)

	// An explicit bearer wins over a stale cookie (API keys, the CLI, scripts).
	stale := &http.Cookie{Name: "relaya_session", Value: "rs_stale"}
	if r := do("GET", "/v1/me", sess.Token, stale); r.StatusCode != 200 {
		t.Fatalf("bearer with stale cookie: %d", r.StatusCode)
	}
	if r := do("GET", "/v1/me", "", stale); r.StatusCode != 401 {
		t.Fatalf("stale cookie alone: %d", r.StatusCode)
	}

	// The live stream signs in with the cookie from the upgrade request.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.api.URL, "http")+"/v1/orgs/"+orgID+"/stream",
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {c.String()}}})
	if err != nil {
		t.Fatal(err)
	}
	ws.Write(ctx, websocket.MessageText, []byte(`{"type":"auth"}`))
	if _, msg, err := ws.Read(ctx); err != nil || !strings.Contains(string(msg), "ready") {
		t.Fatalf("stream with cookie: %v %s", err, msg)
	}
	ws.CloseNow()
	// Without cookie or token it is refused.
	ws2, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.api.URL, "http")+"/v1/orgs/"+orgID+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	ws2.Write(ctx, websocket.MessageText, []byte(`{"type":"auth"}`))
	if _, _, err := ws2.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("stream without session: %v", err)
	}

	// Logging out ends the session and clears the cookie.
	r := do("POST", "/v1/auth/logout", "", c)
	if r.StatusCode != 204 {
		t.Fatalf("logout: %d", r.StatusCode)
	}
	cleared := false
	for _, x := range r.Cookies() {
		cleared = cleared || (x.Name == "relaya_session" && x.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("logout didn't clear the cookie")
	}
	if r := do("GET", "/v1/me", "", c); r.StatusCode != 401 {
		t.Fatalf("cookie after logout: %d", r.StatusCode)
	}
	// Logout needs a session, not an API key.
	key := e.call("POST", "/v1/orgs/"+orgID+"/api-keys", e.call("POST", "/v1/auth/login", "", map[string]any{"email": "cookie@example.com", "password": "correct-horse-8"}, 200)["token"].(string),
		map[string]any{"name": "k", "role": "admin"}, 201)["key"].(string)
	if r := do("POST", "/v1/auth/logout", key, nil); r.StatusCode != 400 {
		t.Fatalf("logout with API key: %d", r.StatusCode)
	}
}
