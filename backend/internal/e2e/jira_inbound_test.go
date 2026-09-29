package e2e

import (
	"strings"
	"testing"
)

// Jira's own webhooks: verified with X-Hub-Signature, deduped on the delivery identifier.
func TestJiraInbound(t *testing.T) {
	e := setup(t)
	rc := newReceiver(t)
	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "jira-in@example.com", "password": "correct-horse-7", "org_name": "Jira in"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "Jira", "provider": "jira", "signing_secret": "jira-secret"}, 201)
	e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/destinations", tok, map[string]any{"name": "Tracker", "url": rc.srv.URL}, 201)
	url := wh["ingest_url"].(string)

	body := `{"timestamp":1759046400000,"webhookEvent":"jira:issue_created","issue":{"id":"10001","key":"OPS-1","fields":{"summary":"Checkout is down"}}}`
	good := map[string]string{"X-Hub-Signature": "sha256=" + sign("jira-secret", body), "X-Atlassian-Webhook-Identifier": "delivery-1"}

	status, res := e.deliver(url, body, good)
	if status != 200 || res["duplicate"] != false {
		t.Fatalf("signed delivery: %d %v", status, res)
	}
	// Jira retries with the same identifier: a duplicate, not a second event.
	if status, res := e.deliver(url, body, good); status != 200 || res["duplicate"] != true {
		t.Fatalf("retry: %d %v", status, res)
	}
	if status, _ := e.deliver(url, body, map[string]string{"X-Atlassian-Webhook-Identifier": "delivery-2"}); status != 401 {
		t.Fatalf("unsigned: %d", status)
	}
	forged := strings.Replace(body, "Checkout is down", "All fine", 1)
	if status, _ := e.deliver(url, forged, map[string]string{"X-Hub-Signature": good["X-Hub-Signature"], "X-Atlassian-Webhook-Identifier": "delivery-3"}); status != 401 {
		t.Fatalf("forged: %d", status)
	}

	events := e.call("GET", base+"/events?webhook_id="+wh["id"].(string), tok, nil, 200)["data"].([]any)
	accepted := 0
	for _, ev := range events {
		m := ev.(map[string]any)
		if m["status"] == "received" {
			accepted++
			if m["type"] != "jira:issue_created" || m["signature"] != "valid" {
				t.Fatalf("event: %v", m)
			}
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted events: %d of %d", accepted, len(events))
	}
	e.runWorker()
	if rc.count() != 1 {
		t.Fatalf("forwarded %d", rc.count())
	}
}
