package e2e

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestEventSimulator(t *testing.T) {
	e := setup(t)
	rc := newReceiver(t)
	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "sim@example.com", "password": "correct-horse-5", "org_name": "Sim shop"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	webhook := func(name, provider, secret string) string {
		t.Helper()
		body := map[string]any{"project_id": proj, "name": name, "provider": provider}
		if secret != "" {
			body["signing_secret"] = secret
		}
		return e.call("POST", base+"/webhooks", tok, body, 201)["id"].(string)
	}

	// Razorpay, signed with the webhook's own secret.
	rzp := webhook("Payments", "razorpay", "rzp-secret")
	e.call("POST", base+"/webhooks/"+rzp+"/destinations", tok, map[string]any{"name": "App", "url": rc.srv.URL}, 201)
	samples := e.call("GET", base+"/webhooks/"+rzp+"/samples", tok, nil, 200)
	list := samples["data"].([]any)
	if samples["provider"] != "razorpay" || samples["signed"] != true || len(list) < 3 || list[0].(map[string]any)["type"] != "payment.captured" {
		t.Fatalf("samples: %v", samples)
	}
	r := e.call("POST", base+"/webhooks/"+rzp+"/simulate", tok, map[string]any{"event_type": "payment.captured"}, 200)
	if r["status"] != "received" || r["signature"] != "valid" || r["event_type"] != "payment.captured" || r["deliveries"] != float64(1) || r["duplicate"] != false {
		t.Fatalf("simulate: %v", r)
	}
	e.runWorker()
	if rc.count() != 1 {
		t.Fatalf("forwarded %d", rc.count())
	}
	got := rc.last()
	if got.header.Get("X-Razorpay-Signature") == "" || !strings.Contains(string(got.body), `"payment.captured"`) {
		t.Fatalf("forwarded request: %v %s", got.header, got.body)
	}

	// The event is labelled simulated and stays out of contracts.
	ev := e.call("GET", base+"/events/"+r["id"].(string), tok, nil, 200)
	if ev["simulated"] != true || ev["contract_status"] != "none" || !strings.Contains(mustJSON(ev["headers"]), "relaya-simulated") {
		t.Fatalf("event: simulated=%v contract=%v", ev["simulated"], ev["contract_status"])
	}
	for i := 0; i < 5; i++ {
		e.call("POST", base+"/webhooks/"+rzp+"/simulate", tok, map[string]any{"event_type": "payment.captured"}, 200)
	}
	e.runChecker()
	if c := e.call("GET", base+"/contracts", tok, nil, 200)["data"].([]any); len(c) != 0 {
		t.Fatalf("simulated events taught a contract: %v", c)
	}

	// An edited payload is sent as edited; the same payload again is a duplicate
	// only where the provider dedupes on the body (Stripe's event ID).
	edited := strings.Replace(list[1].(map[string]any)["payload"].(string), "Payment was cancelled", "Card declined", 1)
	r = e.call("POST", base+"/webhooks/"+rzp+"/simulate", tok, map[string]any{"event_type": "payment.failed", "payload": edited}, 200)
	if r["event_type"] != "payment.failed" || r["signature"] != "valid" {
		t.Fatalf("edited: %v", r)
	}
	stripe := webhook("Stripe", "stripe", "whsec_stripe_test")
	payload := e.call("GET", base+"/webhooks/"+stripe+"/samples", tok, nil, 200)["data"].([]any)[0].(map[string]any)["payload"].(string)
	first := e.call("POST", base+"/webhooks/"+stripe+"/simulate", tok, map[string]any{"payload": payload}, 200)
	again := e.call("POST", base+"/webhooks/"+stripe+"/simulate", tok, map[string]any{"payload": payload}, 200)
	if first["signature"] != "valid" || first["duplicate"] != false || again["duplicate"] != true || again["id"] != first["id"] {
		t.Fatalf("stripe: %v / %v", first, again)
	}

	// Header-typed providers: Shopify's topic comes from the chosen event type.
	shop := webhook("Store", "shopify", "shpss_secret")
	if r := e.call("POST", base+"/webhooks/"+shop+"/simulate", tok, map[string]any{"event_type": "orders/paid"}, 200); r["event_type"] != "orders/paid" || r["signature"] != "valid" {
		t.Fatalf("shopify: %v", r)
	}
	// No secret: sent unsigned, accepted as not configured.
	open := webhook("Open", "generic", "")
	if r := e.call("POST", base+"/webhooks/"+open+"/simulate", tok, map[string]any{}, 200); r["signature"] != "not_configured" || r["event_type"] != "order.created" {
		t.Fatalf("unsigned: %v", r)
	}

	// Refusals.
	e.call("POST", base+"/webhooks/"+rzp+"/simulate", tok, map[string]any{"payload": "{not json"}, 400)
	e.call("POST", base+"/webhooks/"+rzp+"/simulate", tok, map[string]any{"event_type": "bad type!"}, 400)
	pk := webhook("Svix", "standardwebhooks", "whpk_MCowBQYDK2VwAyEAq6T1FyH0p0Nq1b9m8W1H5kGmN1a2b3c4d5e6f7g8h9i=")
	if r := e.call("POST", base+"/webhooks/"+pk+"/simulate", tok, map[string]any{}, 400); !strings.Contains(mustJSON(r), "public key") {
		t.Fatalf("whpk_: %v", r)
	}
	e.call("POST", base+"/outbound/apps", tok, map[string]any{"uid": "acme"}, 201)
	outWh := e.call("GET", base+"/webhooks?kind=outbound", tok, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	e.call("POST", base+"/webhooks/"+outWh+"/simulate", tok, map[string]any{}, 400)

	// The event list shows which events were simulated.
	evs := e.call("GET", base+"/events?webhook_id="+rzp, tok, nil, 200)["data"].([]any)
	for _, x := range evs {
		if x.(map[string]any)["simulated"] != true {
			t.Fatalf("list: %v", x)
		}
	}
	// The raw event is exactly what arrived, with the provider's signature header,
	// so the CLI can forward it and the local app's own check still passes.
	firstID := evs[len(evs)-1].(map[string]any)["id"].(string)
	raw := e.call("GET", base+"/events/"+firstID+"/raw", tok, nil, 200)
	rawBody, _ := base64.StdEncoding.DecodeString(raw["body_base64"].(string))
	hdrs := raw["headers"].(map[string]any)
	if raw["simulated"] != true || hdrs["x-razorpay-signature"] != sign("rzp-secret", string(rawBody)) || !strings.Contains(string(rawBody), `"email":"customer@example.com"`) &&
		!strings.Contains(string(rawBody), `"email": "customer@example.com"`) {
		t.Fatalf("raw: %v / %s", hdrs, rawBody)
	}

	var audit []any
	_ = json.Unmarshal([]byte(mustJSON(e.call("GET", base+"/audit-logs", tok, nil, 200)["data"])), &audit)
	if !strings.Contains(mustJSON(audit), "webhook.simulate") {
		t.Fatal("simulations are not audited")
	}
}
