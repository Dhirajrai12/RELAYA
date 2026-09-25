package e2e

import (
	"context"
	"fmt"
	"testing"
)

func (e *env) runChecker() {
	e.t.Helper()
	if _, err := e.checker.RunOnce(context.Background(), 100); err != nil {
		e.t.Fatal(err)
	}
}

func TestContractsEndToEnd(t *testing.T) {
	e := setup(t)
	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "shape@example.com", "password": "correct-horse-6", "org_name": "Shapes"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "Pay"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "payments", "provider": "generic"}, 201)
	url := wh["ingest_url"].(string)

	n := 0
	send := func(body string) string {
		t.Helper()
		n++
		status, res := e.deliver(url, body, map[string]string{"X-Event-Id": fmt.Sprintf("e%d", n)})
		if status != 200 {
			t.Fatalf("ingest %d", status)
		}
		e.runChecker()
		return res["id"].(string)
	}
	event := func(id string) map[string]any { return e.call("GET", base+"/events/"+id, tok, nil, 200) }
	good := func(amount string) string {
		return fmt.Sprintf(`{"type":"payment.captured","id":"pay_%d","amount":%s,"status":"captured","currency":"INR"}`, n, amount)
	}

	// 1. Learning: 3 samples, then proposed.
	for i := 0; i < 3; i++ {
		id := send(good("50000"))
		if st := event(id)["contract_status"]; st != "learning" {
			t.Fatalf("learning event status %v", st)
		}
	}
	cs := e.call("GET", base+"/contracts", tok, nil, 200)["data"].([]any)
	c := cs[0].(map[string]any)
	if len(cs) != 1 || c["status"] != "proposed" || c["event_type"] != "payment.captured" || c["samples"] != 3.0 {
		t.Fatalf("after learning: %v", cs)
	}
	cid := c["id"].(string)
	detail := e.call("GET", base+"/contracts/"+cid, tok, nil, 200)
	var amountField map[string]any
	for _, f := range detail["fields"].([]any) {
		if fm := f.(map[string]any); fm["path"] == "amount" {
			amountField = fm
		}
	}
	if amountField == nil || amountField["required"] != true || amountField["types"].([]any)[0] != "integer" {
		t.Fatalf("amount field: %v", amountField)
	}

	// 2. Activate with critical fields. Members can't; unknown fields are rejected.
	e.call("POST", base+"/contracts/"+cid+"/versions", tok, map[string]any{"critical_fields": []string{"nope"}}, 400)
	other := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "viewer@example.com", "password": "correct-horse-5", "org_name": "X"}, 201)
	e.call("POST", base+"/members", tok, map[string]any{"email": "viewer@example.com", "role": "member"}, 201)
	e.call("POST", base+"/contracts/"+cid+"/versions", other["token"].(string), map[string]any{"critical_fields": []string{"amount"}}, 403)
	v := e.call("POST", base+"/contracts/"+cid+"/versions", tok, map[string]any{"critical_fields": []string{"amount", "status"}}, 201)
	if v["version"] != 1.0 {
		t.Fatalf("version: %v", v)
	}

	// 3. Checking.
	if st := event(send(good("120000")))["contract_status"]; st != "ok" {
		t.Fatalf("same shape: %v", st)
	}
	if st := event(send(`{"type":"payment.captured","id":"x","amount":1,"status":"captured","currency":"INR","utm_source":"ads"}`))["contract_status"]; st != "compatible" {
		t.Fatalf("new field: %v", st)
	}
	retyped := send(good(`"500"`))
	ev := event(retyped)
	viol := ev["violations"].([]any)
	if ev["contract_status"] != "breaking" || len(viol) != 1 || viol[0].(map[string]any)["kind"] != "type_changed" || ev["contract_id"] != cid {
		t.Fatalf("retyped: %v %v", ev["contract_status"], viol)
	}
	send(good(`"700"`))                                                               // same break again: same incident, count 2
	send(`{"type":"payment.captured","id":"y","status":"captured","currency":"INR"}`) // amount missing

	incidents := e.call("GET", base+"/incidents", tok, nil, 200)["data"].([]any)
	if len(incidents) != 2 {
		t.Fatalf("want 2 open incidents, got %v", incidents)
	}
	byKind := map[string]map[string]any{}
	for _, i := range incidents {
		im := i.(map[string]any)
		byKind[im["kind"].(string)] = im
	}
	if tc := byKind["type_changed"]; tc == nil || tc["event_count"] != 2.0 || tc["path"] != "amount" || tc["expected"] != "integer" || tc["actual"] != "string" {
		t.Fatalf("type_changed incident: %v", tc)
	}
	if mf := byKind["missing_field"]; mf == nil || mf["title"] != "payment.captured: critical field amount is missing" {
		t.Fatalf("missing_field incident: %v", mf)
	}
	if got := e.call("GET", base+"/events?contract_status=breaking", tok, nil, 200)["data"].([]any); len(got) != 3 {
		t.Fatalf("breaking filter: %d events", len(got))
	}
	stats := e.call("GET", base+"/events/stats", tok, nil, 200)["contracts"].(map[string]any)
	if stats["open_incidents"] != 2.0 || stats["breaking_24h"] != 3.0 {
		t.Fatalf("stats: %v", stats)
	}
	cd := e.call("GET", base+"/contracts/"+cid, tok, nil, 200)
	if _, ok := cd["new_fields"].(map[string]any)["utm_source"]; !ok {
		t.Fatalf("new_fields: %v", cd["new_fields"])
	}

	// 4. Resolve one manually; accepting changes (new version from observed) resolves the rest.
	e.call("POST", base+"/incidents/"+byKind["missing_field"]["id"].(string)+"/resolve", tok, map[string]any{"resolution": "provider fixed it"}, 204)
	if open := e.call("GET", base+"/incidents", tok, nil, 200)["data"].([]any); len(open) != 1 {
		t.Fatalf("after resolve: %d open", len(open))
	}
	v2 := e.call("POST", base+"/contracts/"+cid+"/versions", tok, map[string]any{"critical_fields": []string{"status"}, "source": "observed"}, 201)
	if v2["version"] != 2.0 {
		t.Fatalf("v2: %v", v2)
	}
	if open := e.call("GET", base+"/incidents", tok, nil, 200)["data"].([]any); len(open) != 0 {
		t.Fatalf("accepting changes should resolve incidents: %v", open)
	}
	resolved := e.call("GET", base+"/incidents?status=resolved", tok, nil, 200)["data"].([]any)
	if len(resolved) != 2 {
		t.Fatalf("resolved: %d", len(resolved))
	}
	cd = e.call("GET", base+"/contracts/"+cid, tok, nil, 200)
	if cd["contract"].(map[string]any)["active_version"] != 2.0 || len(cd["versions"].([]any)) != 2 || len(cd["new_fields"].(map[string]any)) != 0 {
		t.Fatalf("after accept: %v", cd["contract"])
	}

	// 5. Relearn starts over.
	e.call("POST", base+"/contracts/"+cid+"/relearn", tok, nil, 204)
	c = e.call("GET", base+"/contracts/"+cid, tok, nil, 200)["contract"].(map[string]any)
	if c["status"] != "learning" || c["active_version"] != nil {
		t.Fatalf("relearn: %v", c)
	}

	// Rejected or non-JSON events are never checked.
	if st := event(func() string {
		_, res := e.deliver(url, "plain text", map[string]string{"X-Event-Type": "note"})
		e.runChecker()
		return res["id"].(string)
	}())["contract_status"]; st != "none" {
		t.Fatalf("non-JSON: %v", st)
	}
}
