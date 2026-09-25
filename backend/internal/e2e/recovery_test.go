package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRecoveryEndToEnd(t *testing.T) {
	e := setup(t)
	rc := newReceiver(t)

	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "recover@example.com", "password": "correct-horse-4", "org_name": "Recover"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "orders", "provider": "generic"}, 201)
	e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/destinations", tok, map[string]any{"name": "app", "url": rc.srv.URL, "max_attempts": 5}, 201)
	url := wh["ingest_url"].(string)

	n := 0
	send := func(amount string) string {
		t.Helper()
		n++
		_, res := e.deliver(url, fmt.Sprintf(`{"type":"order.paid","id":"o%d","amount":%s,"status":"paid"}`, n, amount),
			map[string]string{"X-Event-Id": fmt.Sprintf("o%d", n)})
		e.runChecker()
		e.runWorker()
		return res["id"].(string)
	}
	openIncidents := func() []any { return e.call("GET", base+"/incidents", tok, nil, 200)["data"].([]any) }

	// Learn + activate with amount critical.
	for i := 0; i < 3; i++ {
		send("100")
	}
	cid := e.call("GET", base+"/contracts", tok, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	e.call("POST", base+"/contracts/"+cid+"/versions", tok, map[string]any{"critical_fields": []string{"amount"}}, 201)

	// Provider starts sending amount as a string; our endpoint chokes on it (500).
	rc.set(http.StatusInternalServerError)
	bad1, bad2 := send(`"100"`), send(`"250"`)
	inc := openIncidents()
	if len(inc) != 1 || inc[0].(map[string]any)["event_count"] != 2.0 {
		t.Fatalf("incident: %v", inc)
	}
	incID := inc[0].(map[string]any)["id"].(string)
	for _, id := range []string{bad1, bad2} {
		if d := e.call("GET", base+"/events/"+id, tok, nil, 200)["deliveries"].([]any)[0].(map[string]any); d["status"] != "retrying" {
			t.Fatalf("delivery before fix: %v", d)
		}
	}

	// Dry run: shows the plan, changes nothing.
	plan := e.call("GET", base+"/incidents/"+incID+"/replay", tok, nil, 200)
	dest := plan["destinations"].([]any)[0].(map[string]any)
	if plan["events"] != 2.0 || plan["will_send"] != 2.0 || dest["deliveries"] != 2.0 || dest["enabled"] != true {
		t.Fatalf("plan: %v", plan)
	}
	e.call("POST", base+"/incidents/"+incID+"/replay", tok, map[string]any{}, 400) // must confirm

	// We fix our endpoint, then replay.
	rc.set(http.StatusOK)
	before := rc.count()
	rp := e.call("POST", base+"/incidents/"+incID+"/replay", tok, map[string]any{"confirm": true}, 201)
	if rp["total"] != 2.0 || rp["status"] != "running" {
		t.Fatalf("replay: %v", rp)
	}
	e.call("POST", base+"/incidents/"+incID+"/replay", tok, map[string]any{"confirm": true}, 409) // one at a time
	e.runWorker()
	if rc.count()-before != 2 {
		t.Fatalf("replayed %d requests, want 2", rc.count()-before)
	}
	last := rc.last()
	if last.header.Get("Relaya-Replay") != rp["id"] || last.header.Get("Idempotency-Key") == "" {
		t.Fatalf("replay headers: %v", last.header)
	}

	// Verified: every replayed delivery succeeded, so the incident resolved itself.
	if len(openIncidents()) != 0 {
		t.Fatal("incident should be resolved after a fully successful replay")
	}
	res := e.call("GET", base+"/incidents?status=resolved", tok, nil, 200)["data"].([]any)[0].(map[string]any)
	r := res["replay"].(map[string]any)
	if res["resolved_by"] != "system" || !strings.Contains(res["resolution"].(string), "verified by replay: 2 of 2") ||
		r["status"] != "completed" || r["succeeded"] != 2.0 || r["failed"] != 0.0 {
		t.Fatalf("resolved incident: %v", res)
	}

	// A replay that fails leaves the incident open.
	rc.set(http.StatusBadRequest)
	send(`null`) // amount null: a new breaking incident (null_value)
	inc = openIncidents()
	if len(inc) != 1 {
		t.Fatalf("want 1 open incident, got %v", inc)
	}
	incID = inc[0].(map[string]any)["id"].(string)
	e.call("POST", base+"/incidents/"+incID+"/replay", tok, map[string]any{"confirm": true}, 201)
	e.runWorker()
	inc = openIncidents()
	if len(inc) != 1 {
		t.Fatal("a failed replay must not resolve the incident")
	}
	if r := inc[0].(map[string]any)["replay"].(map[string]any); r["status"] != "completed" || r["failed"] != 1.0 {
		t.Fatalf("failed replay: %v", r)
	}

	// Auto-resolve: not while the provider is merely quiet...
	ctx := context.Background()
	time.Sleep(10 * time.Millisecond)
	if n, err := e.checker.AutoResolve(ctx, time.Millisecond); err != nil || n != 0 {
		t.Fatalf("auto-resolve without a later clean event: %d %v", n, err)
	}
	// ...but once a later event matches the contract again.
	rc.set(http.StatusOK)
	send("300")
	time.Sleep(10 * time.Millisecond)
	if n, err := e.checker.AutoResolve(ctx, time.Millisecond); err != nil || n != 1 {
		t.Fatalf("auto-resolve after a clean event: %d %v", n, err)
	}
	if len(openIncidents()) != 0 {
		t.Fatal("incident should be auto-resolved")
	}
	logs := mustJSON(e.call("GET", base+"/audit-logs", tok, nil, 200))
	for _, want := range []string{`"incident.replay"`, `"auto-resolve"`, `verified by replay`} {
		if !strings.Contains(logs, want) {
			t.Errorf("audit log missing %s", want)
		}
	}
}
