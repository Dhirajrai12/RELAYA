package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"relaya/internal/delivery"
)

// alertsOf returns the alert payloads the receiver got, optionally filtered by type.
func alertsOf(rc *receiver, typ string) []map[string]any {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []map[string]any
	for _, r := range rc.requests {
		var m map[string]any
		if json.Unmarshal(r.body, &m) == nil && (typ == "" || m["type"] == typ) {
			out = append(out, m)
		}
	}
	return out
}

func (e *env) sendAlerts() {
	e.t.Helper()
	if _, err := e.alerts.RunOnce(context.Background(), 100); err != nil {
		e.t.Fatal(err)
	}
}

func TestAlertsEndToEnd(t *testing.T) {
	e := setup(t)
	inbox := newReceiver(t) // the alert webhook channel
	app := newReceiver(t)   // a delivery destination we can break

	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "alerts@example.com", "password": "correct-horse-3", "org_name": "Alerting"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID

	// ---- channels ----
	settings := e.call("GET", base+"/alert-settings", tok, nil, 200)
	if settings["email_enabled"] != false || len(settings["kinds"].([]any)) != 5 {
		t.Fatalf("settings: %v", settings)
	}
	all := []string{"incident_opened", "incident_resolved", "destination_failing", "destination_recovered", "signature_failures"}
	ch := e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "webhook", "name": "Ops inbox", "url": inbox.srv.URL, "events": all}, 201)
	secret := []byte(ch["signing_secret"].(string))
	chID := ch["channel"].(map[string]any)["id"].(string)
	e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "slack", "name": "x", "url": "https://evil.example/hook", "events": all}, 400)
	e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "email", "name": "x", "email": "stranger@example.com", "events": all}, 400)
	mail := e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "email", "name": "Me", "email": "alerts@example.com", "events": []string{"incident_opened"}}, 201)
	if r := e.call("POST", base+"/alert-channels/"+mail["channel"].(map[string]any)["id"].(string)+"/test", tok, nil, 200); r["ok"] != false || !strings.Contains(r["error"].(string), "not configured") {
		t.Fatalf("email test without SMTP: %v", r)
	}
	e.call("PATCH", base+"/alert-channels/"+mail["channel"].(map[string]any)["id"].(string), tok, map[string]any{"enabled": false}, 200)

	// Send test: delivered synchronously and signed.
	if r := e.call("POST", base+"/alert-channels/"+chID+"/test", tok, nil, 200); r["ok"] != true {
		t.Fatalf("test alert: %v", r)
	}
	got := inbox.last()
	if !delivery.Verify(secret, got.header.Get("Relaya-Signature"), got.body, time.Now(), time.Minute) {
		t.Fatal("alert webhook not signed with the channel secret")
	}
	if a := alertsOf(inbox, "test"); len(a) != 1 || a[0]["link"] != "https://dash.example/settings/alerts" {
		t.Fatalf("test alert payload: %v", a)
	}

	// ---- incident opened / resolved ----
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "Payments", "provider": "generic"}, 201)
	n := 0
	send := func(url, body string) {
		t.Helper()
		n++
		e.deliver(url, body, map[string]string{"X-Event-Id": fmt.Sprintf("a%d", n)})
		e.runChecker()
		e.runWorker()
		e.sendAlerts()
	}
	for i := 0; i < 3; i++ {
		send(wh["ingest_url"].(string), `{"type":"pay","amount":1}`)
	}
	cid := e.call("GET", base+"/contracts", tok, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	e.call("POST", base+"/contracts/"+cid+"/versions", tok, map[string]any{"critical_fields": []string{"amount"}}, 201)
	send(wh["ingest_url"].(string), `{"type":"pay","amount":"1"}`)
	send(wh["ingest_url"].(string), `{"type":"pay","amount":"2"}`) // same incident: no second alert
	opened := alertsOf(inbox, "incident_opened")
	if len(opened) != 1 || !strings.HasPrefix(opened[0]["title"].(string), "Breaking change: pay: amount changed type") ||
		!strings.Contains(opened[0]["body"].(string), "Webhook: Payments") {
		t.Fatalf("incident opened alerts: %v", opened)
	}
	incID := e.call("GET", base+"/incidents", tok, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	e.call("POST", base+"/incidents/"+incID+"/resolve", tok, map[string]any{"resolution": "provider fixed it"}, 204)
	e.sendAlerts()
	if res := alertsOf(inbox, "incident_resolved"); len(res) != 1 || !strings.Contains(res[0]["body"].(string), "provider fixed it") {
		t.Fatalf("incident resolved alerts: %v", res)
	}

	// ---- destination failing / recovered ----
	wh2 := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "Orders", "provider": "generic"}, 201)
	e.call("POST", base+"/webhooks/"+wh2["id"].(string)+"/destinations", tok, map[string]any{"name": "Order app", "url": app.srv.URL}, 201)
	app.set(http.StatusInternalServerError)
	for i := 0; i < 2; i++ {
		send(wh2["ingest_url"].(string), `{"id":"x"}`)
	}
	if len(alertsOf(inbox, "destination_failing")) != 0 {
		t.Fatal("2 failures must not alert yet")
	}
	send(wh2["ingest_url"].(string), `{"id":"x3"}`)
	send(wh2["ingest_url"].(string), `{"id":"x4"}`) // still failing: no second alert
	failing := alertsOf(inbox, "destination_failing")
	if len(failing) != 1 || failing[0]["title"] != "Deliveries to Order app are failing" || !strings.Contains(failing[0]["body"].(string), "HTTP 500") {
		t.Fatalf("failing alerts: %v", failing)
	}
	app.set(http.StatusOK)
	send(wh2["ingest_url"].(string), `{"id":"x5"}`)
	if rec := alertsOf(inbox, "destination_recovered"); len(rec) != 1 || rec[0]["title"] != "Deliveries to Order app have recovered" {
		t.Fatalf("recovered alerts: %v", rec)
	}

	// ---- signature failures (throttled) ----
	wh3 := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "Stripe", "provider": "stripe", "signing_secret": "whsec_x"}, 201)
	send(wh3["ingest_url"].(string), `{"id":"evt_1","type":"invoice.paid"}`)
	send(wh3["ingest_url"].(string), `{"id":"evt_2","type":"invoice.paid"}`)
	if sig := alertsOf(inbox, "signature_failures"); len(sig) != 1 || !strings.Contains(sig[0]["title"].(string), "Stripe") {
		t.Fatalf("signature alerts: %v", sig)
	}

	// ---- log; disabled channel got nothing ----
	log := e.call("GET", base+"/alerts", tok, nil, 200)["data"].([]any)
	sent := 0
	for _, a := range log {
		am := a.(map[string]any)
		if am["channel_name"] == "Me" && am["kind"] != "test" {
			t.Fatalf("disabled email channel got an alert: %v", am)
		}
		if am["status"] == "sent" {
			sent++
		}
	}
	if sent != 6 { // test + opened + resolved + failing + recovered + signature
		t.Fatalf("sent alerts in log: %d (%v)", sent, log)
	}
	chs := e.call("GET", base+"/alert-channels", tok, nil, 200)["data"].([]any)
	for _, c := range chs {
		if cm := c.(map[string]any); cm["name"] == "Ops inbox" && cm["sent_7d"] != 6.0 {
			t.Fatalf("channel stats: %v", cm)
		}
	}
}
