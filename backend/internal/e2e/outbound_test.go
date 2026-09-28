package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"relaya/internal/provider"
)

// verifyStandard checks a received request with a Standard Webhooks verifier.
func verifyStandard(t *testing.T, r received, secret string) {
	t.Helper()
	p, _ := provider.Get("standardwebhooks")
	if v := p.Verify(provider.Request{Header: r.header, Body: r.body, Now: time.Now()}, provider.Config{Secret: []byte(secret)}); v != provider.SigValid {
		t.Fatalf("signature: %s", v)
	}
	for k := range r.header {
		if strings.HasPrefix(strings.ToLower(k), "relaya-") {
			t.Fatalf("outbound request carries %s", k)
		}
	}
}

func TestOutboundEndToEnd(t *testing.T) {
	e := setup(t)
	recA, recB, recC := newReceiver(t), newReceiver(t), newReceiver(t)
	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "out@example.com", "password": "correct-horse-1", "org_name": "Billing SaaS"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID

	// ---- apps and endpoints ----
	e.call("POST", base+"/outbound/apps", tok, map[string]any{"uid": "bad uid!"}, 400)
	app := e.call("POST", base+"/outbound/apps", tok, map[string]any{"uid": "acme", "name": "Acme Corp"}, 201)
	e.call("POST", base+"/outbound/apps", tok, map[string]any{"uid": "acme"}, 409)
	e.call("POST", base+"/outbound/apps", tok, map[string]any{"uid": "globex"}, 201)
	if app["uid"] != "acme" || app["name"] != "Acme Corp" {
		t.Fatalf("app: %v", app)
	}
	a := e.call("POST", base+"/outbound/apps/acme/endpoints", tok, map[string]any{"url": recA.srv.URL, "description": "Invoices", "event_types": []string{"invoice.paid"}}, 201)
	b := e.call("POST", base+"/outbound/apps/acme/endpoints", tok, map[string]any{"url": recB.srv.URL}, 201)
	secretA, secretB := a["signing_secret"].(string), b["signing_secret"].(string)
	if !strings.HasPrefix(secretA, "whsec_") || secretA == secretB {
		t.Fatalf("secrets %q %q", secretA, secretB)
	}
	e.call("POST", base+"/outbound/apps/acme/endpoints", tok, map[string]any{"url": recB.srv.URL, "event_types": []string{"bad type!"}}, 400)
	if eps := e.call("GET", base+"/outbound/apps/acme", tok, nil, 200)["endpoints"].([]any); len(eps) != 2 {
		t.Fatalf("endpoints: %v", eps)
	}

	// ---- messages: each endpoint gets the types it asked for ----
	e.call("POST", base+"/outbound/messages", tok, map[string]any{"app": "nobody", "event_type": "x", "payload": map[string]any{}}, 400)
	e.call("POST", base+"/outbound/messages", tok, map[string]any{"app": "acme", "event_type": "bad type!", "payload": map[string]any{}}, 400)
	e.call("POST", base+"/outbound/messages", tok, map[string]any{"app": "acme", "event_type": "x", "payload": []int{1}}, 400)
	m1 := e.call("POST", base+"/outbound/messages", tok, map[string]any{"app": "acme", "event_type": "invoice.paid",
		"payload": map[string]any{"invoice_id": "in_1", "amount": 1999}, "idempotency_key": "inv-1-paid"}, 202)
	if m1["endpoints"] != float64(2) || m1["duplicate"] != false {
		t.Fatalf("m1: %v", m1)
	}
	if m := e.call("POST", base+"/outbound/messages", tok, map[string]any{"app": "acme", "event_type": "invoice.paid",
		"payload": map[string]any{"invoice_id": "in_1"}, "idempotency_key": "inv-1-paid"}, 202); m["duplicate"] != true || m["id"] != m1["id"] {
		t.Fatalf("duplicate: %v", m)
	}
	if m := e.call("POST", base+"/outbound/messages", tok, map[string]any{"app": "acme", "event_type": "user.created", "payload": map[string]any{"user": "u1"}}, 202); m["endpoints"] != float64(1) {
		t.Fatalf("m2 endpoints: %v", m)
	}
	e.runWorker()
	if recA.count() != 1 || recB.count() != 2 {
		t.Fatalf("A got %d, B got %d", recA.count(), recB.count())
	}
	verifyStandard(t, recA.last(), secretA)
	verifyStandard(t, recB.last(), secretB)
	var body map[string]any
	_ = json.Unmarshal(recA.last().body, &body)
	if body["type"] != "invoice.paid" || body["data"].(map[string]any)["invoice_id"] != "in_1" || body["timestamp"] == nil {
		t.Fatalf("body: %v", body)
	}
	if recA.last().header.Get("webhook-id") != m1["id"] {
		t.Fatal("webhook-id is not the message id")
	}
	types := mustJSON(e.call("GET", base+"/outbound/event-types", tok, nil, 200))
	if !strings.Contains(types, "invoice.paid") || !strings.Contains(types, "user.created") {
		t.Fatalf("event types: %s", types)
	}

	// Outbound webhooks stay off the inbound list and can't be posted into.
	if s := mustJSON(e.call("GET", base+"/webhooks?kind=inbound", tok, nil, 200)); strings.Contains(s, "Outbound:") {
		t.Fatal("outbound webhook in the inbound list")
	}
	whs := e.call("GET", base+"/webhooks?kind=outbound", tok, nil, 200)["data"].([]any)
	if len(whs) != 2 {
		t.Fatalf("outbound webhooks: %d", len(whs))
	}
	if st, _ := e.deliver(whs[0].(map[string]any)["ingest_url"].(string), `{"type":"forged"}`, nil); st != 404 {
		t.Fatalf("posting into an outbound webhook: %d", st)
	}

	// ---- portal ----
	link := e.call("POST", base+"/outbound/apps/acme/portal-link", tok, nil, 201)
	portalTok := strings.SplitN(link["url"].(string), "#", 2)[1]
	if !strings.HasPrefix(link["url"].(string), "https://relaya.test/portal#ps_") {
		t.Fatalf("portal link %v", link)
	}
	portal := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, e.api.URL+"/v1/portal"+path, rd)
		req.Header.Set("Authorization", "Bearer "+portalTok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("portal %s %s: %d, want %d: %s", method, path, resp.StatusCode, want, raw)
		}
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return out
	}
	info := portal("GET", "/app", nil, 200)
	if info["org_name"] != "Billing SaaS" || info["app"].(map[string]any)["name"] != "Acme Corp" || !strings.Contains(mustJSON(info), "invoice.paid") {
		t.Fatalf("portal info: %v", info)
	}
	if eps := portal("GET", "/endpoints", nil, 200)["data"].([]any); len(eps) != 2 {
		t.Fatalf("portal endpoints: %d", len(eps))
	}
	c := portal("POST", "/endpoints", map[string]any{"url": recC.srv.URL, "event_types": []string{"user.created"}}, 201)
	cID := c["endpoint"].(map[string]any)["id"].(string)
	if sec := portal("GET", "/endpoints/"+cID+"/secret", nil, 200)["signing_secret"]; sec != c["signing_secret"] {
		t.Fatal("secret mismatch")
	}
	rotated := portal("POST", "/endpoints/"+cID+"/rotate-secret", nil, 200)["signing_secret"].(string)
	if rotated == c["signing_secret"] {
		t.Fatal("secret not rotated")
	}
	if r := portal("POST", "/endpoints/"+cID+"/test?event_type=user.created", nil, 200); r["ok"] != true {
		t.Fatalf("test: %v", r)
	}
	verifyStandard(t, recC.last(), rotated)
	portal("PATCH", "/endpoints/"+cID, map[string]any{"description": "Users"}, 200)

	// Another app's endpoints are out of reach.
	other := e.call("POST", base+"/outbound/apps/globex/endpoints", tok, map[string]any{"url": recB.srv.URL}, 201)["endpoint"].(map[string]any)["id"].(string)
	portal("GET", "/endpoints/"+other+"/secret", nil, 404)
	portal("DELETE", "/endpoints/"+other, nil, 404)

	// Failures show up, and can be sent again.
	recA.set(500)
	e.call("POST", base+"/outbound/messages", tok, map[string]any{"app": "acme", "event_type": "invoice.paid", "payload": map[string]any{"invoice_id": "in_2"}}, 202)
	e.runWorker()
	dl := portal("GET", "/deliveries?status=retrying", nil, 200)["data"].([]any)
	if len(dl) != 1 || dl[0].(map[string]any)["last_status_code"] != float64(500) || dl[0].(map[string]any)["event_type"] != "invoice.paid" {
		t.Fatalf("retrying deliveries: %v", dl)
	}
	recA.set(200)
	portal("POST", "/deliveries/"+dl[0].(map[string]any)["id"].(string)+"/retry", nil, 202)
	e.runWorker()
	// in_1 to A and B, user.created to B, in_2 to A and B (B takes all types)
	if all := portal("GET", "/deliveries", nil, 200)["data"].([]any); len(all) != 5 {
		t.Fatalf("deliveries: %d", len(all))
	}
	if ok := portal("GET", "/deliveries?status=succeeded", nil, 200)["data"].([]any); len(ok) != 5 {
		t.Fatalf("succeeded: %d", len(ok))
	}

	// Bad or expired tokens.
	req, _ := http.NewRequest("GET", e.api.URL+"/v1/portal/app", nil)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	e.pool.Exec(t.Context(), `UPDATE portal_sessions SET expires_at = now() - interval '1 minute'`)
	portal("GET", "/app", nil, 401)

	// ---- routing on inbound destinations ----
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "In"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "Payments", "provider": "generic"}, 201)
	rec := newReceiver(t)
	d := e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/destinations", tok, map[string]any{"name": "billing", "url": rec.srv.URL, "event_types": []string{"payment.captured"}}, 201)
	if types := d["destination"].(map[string]any)["event_types"].([]any); len(types) != 1 {
		t.Fatalf("destination types: %v", types)
	}
	e.deliver(wh["ingest_url"].(string), `{"type":"payment.captured"}`, map[string]string{"X-Event-Id": "1"})
	e.deliver(wh["ingest_url"].(string), `{"type":"payment.failed"}`, map[string]string{"X-Event-Id": "2"})
	before := rec.count()
	e.runWorker()
	if got := rec.count() - before; got != 1 {
		t.Fatalf("routed deliveries: %d, want 1", got)
	}
}
