package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"relaya/internal/delivery"
)

func TestRepairRulesEndToEnd(t *testing.T) {
	e := setup(t)
	rc := newReceiver(t)

	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "repair@example.com", "password": "correct-horse-5", "org_name": "Repair"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "payments", "provider": "generic"}, 201)
	whID, url := wh["id"].(string), wh["ingest_url"].(string)
	secret := e.call("POST", base+"/webhooks/"+whID+"/destinations", tok, map[string]any{"name": "app", "url": rc.srv.URL}, 201)["signing_secret"].(string)

	n := 0
	send := func(body string) string {
		t.Helper()
		n++
		_, res := e.deliver(url, body, map[string]string{"X-Event-Id": fmt.Sprintf("p%d", n), "X-Provider-Signature": "sha256=provider-signed-original"})
		e.runChecker()
		e.runWorker()
		return res["id"].(string)
	}
	received := func() map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(rc.last().body, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	openIncidents := func() []any { return e.call("GET", base+"/incidents", tok, nil, 200)["data"].([]any) }
	eventStatus := func(id string) string {
		return e.call("GET", base+"/events/"+id, tok, nil, 200)["contract_status"].(string)
	}

	// Learn a contract with amount critical.
	for i := 0; i < 3; i++ {
		send(fmt.Sprintf(`{"type":"payment.captured","id":"p%d","amount":%d,"status":"captured"}`, i, 100*(i+1)))
	}
	cid := e.call("GET", base+"/contracts", tok, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	e.call("POST", base+"/contracts/"+cid+"/versions", tok, map[string]any{"critical_fields": []string{"amount"}}, 201)

	// The provider starts sending amount as a string: incident, forwarded as-is.
	bad := send(`{"type":"payment.captured","id":"p9","amount":"500","status":"captured"}`)
	inc := openIncidents()
	if len(inc) != 1 || eventStatus(bad) != "breaking" {
		t.Fatalf("incident: %v", inc)
	}
	incID := inc[0].(map[string]any)["id"].(string)
	if _, isString := received()["amount"].(string); !isString {
		t.Fatal("before a rule the payload must be forwarded unchanged")
	}

	// The suggestion: convert amount back to an integer.
	sug := e.call("GET", base+"/incidents/"+incID+"/repair-suggestion", tok, nil, 200)
	ops := sug["ops"].([]any)
	if sug["name"] != "Convert amount back to integer" || sug["needs_value"] != false || len(ops) != 1 ||
		ops[0].(map[string]any)["op"] != "convert" || ops[0].(map[string]any)["type"] != "integer" {
		t.Fatalf("suggestion: %v", sug)
	}

	// Preview on the incident's sample event: fixed, and passes the contract.
	pv := e.call("POST", base+"/repair-rules/preview", tok, map[string]any{
		"webhook_id": whID, "event_type": "payment.captured", "ops": ops, "event_id": bad,
	}, 200)
	contract := pv["contract"].(map[string]any)
	if pv["changed"] != true || pv["after"].(map[string]any)["amount"] != 500.0 || pv["before"].(map[string]any)["amount"] != "500" ||
		contract["before"].(map[string]any)["status"] != "breaking" || contract["after"].(map[string]any)["status"] != "ok" ||
		pv["op_changes"].([]any)[0] != 1.0 {
		t.Fatalf("preview: %v", pv)
	}

	// Validation.
	e.call("POST", base+"/repair-rules", tok, map[string]any{"webhook_id": whID, "name": "x", "ops": []any{map[string]any{"op": "convert", "path": "amount", "type": "date"}}}, 400)
	e.call("POST", base+"/repair-rules", tok, map[string]any{"webhook_id": whID, "name": "x", "ops": []any{map[string]any{"op": "set", "path": "a", "value": json.RawMessage(`1`)}}, "incident_id": "00000000-0000-0000-0000-000000000000"}, 400)

	rule := e.call("POST", base+"/repair-rules", tok, map[string]any{
		"webhook_id": whID, "event_type": "payment.captured", "name": sug["name"], "ops": ops, "incident_id": incID,
	}, 201)
	ruleID := rule["id"].(string)
	if rule["enabled"] != true || rule["webhook_name"] != "payments" {
		t.Fatalf("rule: %v", rule)
	}

	// New broken events are repaired before forwarding, re-signed by Relaya, and open no incident.
	fixed := send(`{"type":"payment.captured","id":"p10","amount":"600","status":"captured"}`)
	last := rc.last()
	if received()["amount"] != 600.0 {
		t.Fatalf("repaired body: %s", last.body)
	}
	if last.header.Get("Relaya-Repaired") != ruleID || last.header.Get("X-Provider-Signature") != "" {
		t.Fatalf("headers: %v", last.header)
	}
	if !delivery.Verify([]byte(secret), last.header.Get("Relaya-Signature"), last.body, time.Now(), time.Minute) {
		t.Fatal("Relaya-Signature must cover the repaired body")
	}
	if eventStatus(fixed) != "repaired" {
		t.Fatalf("event status %q", eventStatus(fixed))
	}
	if inc := openIncidents(); len(inc) != 1 || inc[0].(map[string]any)["event_count"] != 1.0 {
		t.Fatalf("a repaired event must not count toward the incident: %v", inc)
	}
	ev := e.call("GET", base+"/events/"+fixed, tok, nil, 200)
	if v := ev["violations"].([]any); len(v) != 1 || v[0].(map[string]any)["repaired"] != true {
		t.Fatalf("violations: %v", ev["violations"])
	}
	// The stored event is unchanged.
	if ev["payload_json"].(map[string]any)["amount"] != "600" {
		t.Fatal("the stored payload must stay as the provider sent it")
	}
	c := e.call("GET", base+"/contracts/"+cid, tok, nil, 200)["contract"].(map[string]any)
	if c["repaired_24h"] != 1.0 || c["breaking_24h"] != 1.0 {
		t.Fatalf("contract counts: %v", c)
	}
	dl := ev["deliveries"].([]any)[0].(map[string]any)["id"].(string)
	att := e.call("GET", base+"/deliveries/"+dl, tok, nil, 200)["attempts"].([]any)[0].(map[string]any)
	if rb := att["repaired_by"].([]any); len(rb) != 1 || rb[0] != sug["name"] {
		t.Fatalf("attempt: %v", att)
	}
	// Events that are already fine pass through byte for byte.
	clean := `{"type":"payment.captured","id":"p11","amount":700,"status":"captured"}`
	send(clean)
	if string(rc.last().body) != clean || rc.last().header.Get("Relaya-Repaired") != "" || rc.last().header.Get("X-Provider-Signature") == "" {
		t.Fatalf("clean event changed: %s %v", rc.last().body, rc.last().header)
	}

	// The incident shows its rule; replaying it sends the old event repaired and resolves it.
	inc = openIncidents()
	if rr := inc[0].(map[string]any)["repair_rule"].(map[string]any); rr["id"] != ruleID || rr["applied_count"] != 1.0 {
		t.Fatalf("incident rule: %v", inc[0])
	}
	e.call("POST", base+"/incidents/"+incID+"/replay", tok, map[string]any{"confirm": true}, 201)
	e.runWorker()
	if received()["amount"] != 500.0 || rc.last().header.Get("Relaya-Replay") == "" {
		t.Fatalf("replayed body: %s", rc.last().body)
	}
	if len(openIncidents()) != 0 {
		t.Fatal("incident should resolve after the repaired replay succeeded")
	}
	rules := e.call("GET", base+"/repair-rules?webhook_id="+whID, tok, nil, 200)["data"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["applied_count"] != 2.0 {
		t.Fatalf("rules: %v", rules)
	}

	// Disabled: forwarded as-is again, and it breaks again.
	e.call("PATCH", base+"/repair-rules/"+ruleID, tok, map[string]any{"enabled": false}, 200)
	raw := send(`{"type":"payment.captured","id":"p12","amount":"800","status":"captured"}`)
	if _, isString := received()["amount"].(string); !isString || eventStatus(raw) != "breaking" || len(openIncidents()) != 1 {
		t.Fatal("a disabled rule must not apply")
	}
	e.call("PATCH", base+"/repair-rules/"+ruleID, tok, map[string]any{"webhook_id": whID}, 400)

	// A renamed field: the suggestion is a rename.
	e.call("PATCH", base+"/repair-rules/"+ruleID, tok, map[string]any{"enabled": true}, 200)
	e.call("POST", base+"/incidents/"+openIncidents()[0].(map[string]any)["id"].(string)+"/resolve", tok, map[string]any{"resolution": "rule back on"}, 204)
	send(`{"type":"payment.captured","id":"p13","amount_paise":900,"status":"captured"}`)
	var missing map[string]any
	for _, i := range openIncidents() {
		if i.(map[string]any)["kind"] == "missing_field" {
			missing = i.(map[string]any)
		}
	}
	if missing == nil {
		t.Fatalf("expected a missing_field incident: %v", openIncidents())
	}
	sug = e.call("GET", base+"/incidents/"+missing["id"].(string)+"/repair-suggestion", tok, nil, 200)
	op := sug["ops"].([]any)[0].(map[string]any)
	if op["op"] != "rename" || op["from"] != "amount_paise" || op["to"] != "amount" {
		t.Fatalf("rename suggestion: %v", sug)
	}

	// Delete, audited.
	e.call("DELETE", base+"/repair-rules/"+ruleID, tok, nil, 204)
	e.call("DELETE", base+"/repair-rules/"+ruleID, tok, nil, 404)
	logs := e.call("GET", base+"/audit-logs", tok, nil, 200)["data"].([]any)
	var actions []string
	for _, l := range logs {
		if a := l.(map[string]any)["action"].(string); strings.HasPrefix(a, "repair_rule.") {
			actions = append(actions, a)
		}
	}
	if strings.Join(actions, ",") != "repair_rule.delete,repair_rule.enable,repair_rule.disable,repair_rule.create" {
		t.Fatalf("audit: %v", actions)
	}
}
