// Package e2e drives the API and ingest handlers against a real Postgres.
// It runs only when TEST_DATABASE_URL points at a database whose name contains
// "test"; that database's public schema is wiped first.
package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/alerts"
	"relaya/internal/api"
	"relaya/internal/auth"
	"relaya/internal/connect"
	"relaya/internal/contract"
	"relaya/internal/db"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
	"relaya/internal/ingest"
	"relaya/internal/realtime"
	"relaya/internal/vault"
)

type env struct {
	t       *testing.T
	api     *httptest.Server
	ingest  *httptest.Server
	worker  *delivery.Worker
	checker *contract.Checker
	alerts  *alerts.Sender
	pool    *pgxpool.Pool
}

func setup(t *testing.T) *env { return setupWith(t, nil) }

// setupWith lets a test configure the API server and ingest handler (e.g. rate limits).
func setupWith(t *testing.T, configure func(*api.Server, *ingest.Handler)) *env {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.Contains(u.Path, "test") {
		t.Fatal("TEST_DATABASE_URL must name a database containing 'test'")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	key := make([]byte, 32)
	rand.Read(key)
	wrapper, _ := vault.NewLocalWrapper("test", key)

	ingHandler := &ingest.Handler{Pool: pool, Vault: vault.NewPGVault(pool, wrapper), MaxBodyBytes: 1 << 20}
	ing := httptest.NewServer(httpx.Chain(ingHandler.Routes(), httpx.Recover))
	t.Cleanup(ing.Close)

	srv := &api.Server{
		Pool:          pool,
		Auth:          &auth.Service{Pool: pool, SessionTTL: time.Hour},
		Vault:         vault.NewPGVault(pool, wrapper),
		IngestBaseURL: ing.URL,
	}
	// Tests deliver to httptest servers on 127.0.0.1, so allow private + http here.
	policy := delivery.Policy{AllowHTTP: true, AllowPrivate: true}
	srv.DeliveryPolicy = policy
	srv.Sender = delivery.NewSender(policy)
	srv.AlertSender = &alerts.Sender{Pool: pool, Vault: srv.Vault, HTTP: policy.Client(), Sign: delivery.Sign, DashboardURL: "https://dash.example"}
	srv.Hub = realtime.NewHub(pool)
	srv.Connect = &connect.Service{Pool: pool, Vault: srv.Vault, Client: connect.NewClient(), RedirectURI: "https://relaya.test/api/v1/connect/callback"}
	srv.DashboardURL = "https://relaya.test"
	srv.ProxyHTTP = connect.NewProxyClient()
	hubCtx, stopHub := context.WithCancel(context.Background())
	t.Cleanup(stopHub)
	go srv.Hub.Run(hubCtx)
	if configure != nil {
		configure(srv, ingHandler)
	}
	a := httptest.NewServer(httpx.Chain(srv.Routes(), httpx.Recover))
	t.Cleanup(a.Close)
	w := &delivery.Worker{Pool: pool, Vault: vault.NewPGVault(pool, wrapper), Sender: delivery.NewSender(policy)}
	checker := &contract.Checker{Pool: pool, MinSamples: 3, LearnWindow: time.Hour}
	return &env{t: t, api: a, ingest: ing, worker: w, checker: checker, alerts: srv.AlertSender, pool: pool}
}

// call sends a JSON request and decodes the JSON response into a map.
func (e *env) call(method, path, token string, body any, wantStatus int) map[string]any {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.api.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, wantStatus, raw)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (e *env) deliver(ingestURL string, body string, headers map[string]string) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", ingestURL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

func TestGatewayEndToEnd(t *testing.T) {
	e := setup(t)

	// Sign up: creates user, org (owner) and a session.
	owner := e.call("POST", "/v1/auth/signup", "", map[string]any{
		"email": "dhiraj@example.com", "password": "correct-horse-1", "name": "Dhiraj", "org_name": "Acme SaaS",
	}, 201)
	ownerTok := owner["token"].(string)
	e.call("POST", "/v1/auth/signup", "", map[string]any{
		"email": "DHIRAJ@example.com", "password": "correct-horse-1", "org_name": "Dup",
	}, 409)
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "dhiraj@example.com", "password": "wrong-password"}, 401)
	e.call("POST", "/v1/auth/login", "", map[string]any{"email": "dhiraj@example.com", "password": "correct-horse-1"}, 200)

	me := e.call("GET", "/v1/me", ownerTok, nil, 200)
	org := me["orgs"].([]any)[0].(map[string]any)
	orgID := org["id"].(string)
	if org["role"] != "owner" {
		t.Fatalf("role = %v", org["role"])
	}
	base := "/v1/orgs/" + orgID

	project := e.call("POST", base+"/projects", ownerTok, map[string]any{"name": "Payments"}, 201)
	projectID := project["id"].(string)

	wh := e.call("POST", base+"/webhooks", ownerTok, map[string]any{
		"project_id": projectID, "name": "Razorpay prod", "provider": "razorpay", "signing_secret": "rzp_secret",
	}, 201)
	if wh["has_signing_secret"] != true {
		t.Fatal("secret not recorded")
	}
	if strings.Contains(mustJSON(wh), "rzp_secret") {
		t.Fatal("secret leaked in API response")
	}
	ingestURL := wh["ingest_url"].(string)

	// Valid delivery is stored; the same delivery again is a duplicate.
	body := `{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_1","amount":50000,"card":{"card_number":"4111111111111111"}}}}}`
	hdr := map[string]string{"X-Razorpay-Signature": sign("rzp_secret", body), "X-Razorpay-Event-Id": "evt_1"}
	status, res := e.deliver(ingestURL, body, hdr)
	if status != 200 || res["duplicate"] != false {
		t.Fatalf("first delivery: %d %v", status, res)
	}
	eventID := res["id"].(string)
	status, res = e.deliver(ingestURL, body, hdr)
	if status != 200 || res["duplicate"] != true || res["id"] != eventID {
		t.Fatalf("duplicate delivery: %d %v", status, res)
	}

	// Forged signature is rejected, stored as evidence, and does not claim the dedup key.
	status, _ = e.deliver(ingestURL, `{"event":"payment.captured"}`,
		map[string]string{"X-Razorpay-Signature": "00", "X-Razorpay-Event-Id": "evt_2"})
	if status != 401 {
		t.Fatalf("forged delivery status %d", status)
	}
	body2 := `{"event":"payment.failed"}`
	status, res = e.deliver(ingestURL, body2,
		map[string]string{"X-Razorpay-Signature": sign("rzp_secret", body2), "X-Razorpay-Event-Id": "evt_2"})
	if status != 200 || res["duplicate"] != false {
		t.Fatalf("real delivery after forged one: %d %v", status, res)
	}

	if status, _ := e.deliver(e.ingest.URL+"/v1/in/in_nope", body, nil); status != 404 {
		t.Fatalf("unknown token status %d", status)
	}

	// Explorer: 3 stored events, newest first, paginated.
	list := e.call("GET", base+"/events?limit=2", ownerTok, nil, 200)
	if n := len(list["data"].([]any)); n != 2 || list["next_cursor"] == nil {
		t.Fatalf("page 1: %d events, cursor %v", n, list["next_cursor"])
	}
	page2 := e.call("GET", base+"/events?limit=2&cursor="+list["next_cursor"].(string), ownerTok, nil, 200)
	if n := len(page2["data"].([]any)); n != 1 || page2["next_cursor"] != nil {
		t.Fatalf("page 2: %d events", n)
	}
	rejected := e.call("GET", base+"/events?status=rejected", ownerTok, nil, 200)["data"].([]any)
	if len(rejected) != 1 || rejected[0].(map[string]any)["signature"] != "invalid" {
		t.Fatalf("rejected filter: %v", rejected)
	}
	byType := e.call("GET", base+"/events?type=payment.captured&status=received", ownerTok, nil, 200)["data"].([]any)
	if len(byType) != 1 {
		t.Fatalf("type filter: %d", len(byType))
	}

	// Overview stats: 24 hourly buckets; the current hour holds 2 received + 1 rejected.
	stats := e.call("GET", base+"/events/stats", ownerTok, nil, 200)
	hours := stats["hours"].([]any)
	last := hours[len(hours)-1].(map[string]any)
	totals := stats["totals"].(map[string]any)
	health := stats["webhooks"].([]any)[0].(map[string]any)
	if len(hours) != 24 || last["received"] != 2.0 || last["rejected"] != 1.0 ||
		totals["received"] != 2.0 || totals["rejected"] != 1.0 ||
		health["received"] != 2.0 || health["rejected"] != 1.0 || health["last_received_at"] == nil {
		t.Fatalf("stats: %d hours, last %v, totals %v, webhook %v", len(hours), last, totals, health)
	}

	detail := e.call("GET", base+"/events/"+eventID, ownerTok, nil, 200)
	d := mustJSON(detail)
	if strings.Contains(d, "4111111111111111") || !strings.Contains(d, "pay_1") {
		t.Fatalf("masking wrong: %s", d)
	}
	if detail["type"] != "payment.captured" || detail["dedup_key"] != "evt_1" || detail["signature"] != "valid" {
		t.Fatalf("detail: %v", detail)
	}

	// RBAC: an outsider sees 404; a member can read but not configure.
	other := e.call("POST", "/v1/auth/signup", "", map[string]any{
		"email": "priya@example.com", "password": "correct-horse-2", "org_name": "Other Co",
	}, 201)
	otherTok := other["token"].(string)
	e.call("GET", base+"/events", otherTok, nil, 404)
	e.call("POST", base+"/members", ownerTok, map[string]any{"email": "priya@example.com", "role": "member"}, 201)
	e.call("GET", base+"/events", otherTok, nil, 200)
	e.call("POST", base+"/webhooks", otherTok, map[string]any{"project_id": projectID, "name": "x"}, 403)
	e.call("GET", base+"/audit-logs", otherTok, nil, 403)

	// The last owner cannot be demoted.
	ownerID := owner["user"].(map[string]any)["id"].(string)
	e.call("PATCH", base+"/members/"+ownerID, ownerTok, map[string]any{"role": "admin"}, 409)

	// API keys: shown once, work for their org only, stop working when revoked.
	k := e.call("POST", base+"/api-keys", ownerTok, map[string]any{"name": "CI", "role": "member"}, 201)
	apiKey := k["key"].(string)
	keyID := k["api_key"].(map[string]any)["id"].(string)
	e.call("GET", base+"/events", apiKey, nil, 200)
	otherOrg := e.call("GET", "/v1/orgs", otherTok, nil, 200)["data"].([]any)
	for _, o := range otherOrg {
		if id := o.(map[string]any)["id"].(string); id != orgID {
			e.call("GET", "/v1/orgs/"+id+"/events", apiKey, nil, 404)
		}
	}
	e.call("DELETE", base+"/api-keys/"+keyID, ownerTok, nil, 204)
	e.call("GET", base+"/events", apiKey, nil, 401)

	// Removing the secret switches verification off; rotating the URL kills the old one.
	e.call("PATCH", base+"/webhooks/"+wh["id"].(string), ownerTok, map[string]any{"signing_secret": ""}, 200)
	rotated := e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/rotate-url", ownerTok, nil, 200)
	if rotated["ingest_url"] == ingestURL {
		t.Fatal("URL not rotated")
	}
	if status, _ := e.deliver(rotated["ingest_url"].(string), `{"event":"x"}`, nil); status != 200 {
		t.Fatalf("new URL status %d", status)
	}

	// Audit trail recorded every change, without secrets.
	logs := e.call("GET", base+"/audit-logs", ownerTok, nil, 200)
	l := mustJSON(logs)
	for _, action := range []string{"org.create", "project.create", "webhook.create", "member.add",
		"api_key.create", "api_key.revoke", "webhook.update", "webhook.rotate_url"} {
		if !strings.Contains(l, `"`+action+`"`) {
			t.Errorf("audit log missing %s", action)
		}
	}
	if strings.Contains(l, "rzp_secret") {
		t.Fatal("secret leaked into audit log")
	}

	// Logout invalidates the session.
	e.call("POST", "/v1/auth/logout", ownerTok, nil, 204)
	e.call("GET", "/v1/me", ownerTok, nil, 401)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
