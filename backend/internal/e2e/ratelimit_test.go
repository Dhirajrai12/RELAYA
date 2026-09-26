package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
	"time"

	"relaya/internal/api"
	"relaya/internal/ingest"
	"relaya/internal/ratelimit"
)

func post(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestRateLimits(t *testing.T) {
	e := setupWith(t, func(s *api.Server, _ *ingest.Handler) {
		s.Limits = api.Limits{
			LoginIP:    ratelimit.New(5, time.Hour, 5),
			LoginEmail: ratelimit.New(2, time.Hour, 2),
			SignupIP:   ratelimit.New(3, time.Hour, 3),
			Caller:     ratelimit.New(4, time.Hour, 4),
		}
	})

	// Sign-up: 3 per IP.
	var tok string
	for i := 0; i < 3; i++ {
		s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": fmt.Sprintf("rl%d@example.com", i), "password": "correct-horse-6", "org_name": "RL"}, 201)
		if i == 0 {
			tok = s["token"].(string)
		}
	}
	res := post(t, e.api.URL+"/v1/auth/signup", "", `{"email":"rl9@example.com","password":"correct-horse-6","org_name":"RL"}`)
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" {
		t.Fatalf("4th sign-up: %d %v", res.StatusCode, res.Header)
	}

	// Sign-in: only failures count against the account, so the owner can still sign in
	// after one wrong password; after 2 failures the account is blocked for everyone.
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "rl1@example.com", "password": "wrong-password"}, 401)
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "rl1@example.com", "password": "correct-horse-6"}, 200)
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "RL1@example.com", "password": "wrong-password"}, 401) // case-insensitive
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "rl1@example.com", "password": "correct-horse-6"}, 429)
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "rl2@example.com", "password": "correct-horse-6"}, 200) // other accounts fine
	// …and 5 attempts per IP in total.
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "rl2@example.com", "password": "correct-horse-6"}, 429)

	// Authenticated API: 4 requests per caller.
	for i := 0; i < 4; i++ {
		e.call("GET", "/v1/me", tok, nil, 200)
	}
	e.call("GET", "/v1/me", tok, nil, 429)
}

func TestIngestRateLimits(t *testing.T) {
	e := setupWith(t, func(s *api.Server, h *ingest.Handler) {
		h.PerWebhook = ratelimit.New(3, time.Hour, 3)
		h.UnknownIP = ratelimit.New(2, time.Hour, 2)
	})
	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "ingest-rl@example.com", "password": "correct-horse-7", "org_name": "RL"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	proj := e.call("POST", "/v1/orgs/"+orgID+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	url := e.call("POST", "/v1/orgs/"+orgID+"/webhooks", tok, map[string]any{"project_id": proj, "name": "w", "provider": "generic"}, 201)["ingest_url"].(string)

	for i := 0; i < 3; i++ {
		if res := post(t, url, "", `{"i":1}`); res.StatusCode != 200 {
			t.Fatalf("event %d: %d", i+1, res.StatusCode)
		}
	}
	if res := post(t, url, "", `{"i":1}`); res.StatusCode != 429 || res.Header.Get("Retry-After") == "" {
		t.Fatalf("4th event: %d", res.StatusCode)
	}

	// Scanning unknown URLs: 404 twice, then 429, even for a real URL from that IP.
	for i := 0; i < 2; i++ {
		if res := post(t, e.ingest.URL+"/v1/in/in_not_a_real_token_"+fmt.Sprint(i), "", `{}`); res.StatusCode != 404 {
			t.Fatalf("unknown %d: %d", i, res.StatusCode)
		}
	}
	if res := post(t, e.ingest.URL+"/v1/in/in_not_a_real_token_x", "", `{}`); res.StatusCode != 429 {
		t.Fatalf("scanning: %d", res.StatusCode)
	}
}
