package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// orgRoutes is the permission policy for every /v1/orgs/{org} route. The test
// fails if server.go gains a route that isn't listed here.
var orgRoutes = []struct {
	method, path, min string
}{
	{"GET", "/v1/orgs/{org}", "member"},
	{"GET", "/v1/orgs/{org}/members", "member"},
	{"POST", "/v1/orgs/{org}/members", "admin"},
	{"PATCH", "/v1/orgs/{org}/members/{user}", "admin"},
	{"DELETE", "/v1/orgs/{org}/members/{user}", "admin"}, // members may only remove themselves
	{"GET", "/v1/orgs/{org}/api-keys", "admin"},
	{"POST", "/v1/orgs/{org}/api-keys", "admin"},
	{"DELETE", "/v1/orgs/{org}/api-keys/{key}", "admin"},
	{"GET", "/v1/orgs/{org}/projects", "member"},
	{"POST", "/v1/orgs/{org}/projects", "admin"},
	{"GET", "/v1/orgs/{org}/projects/{project}", "member"},
	{"DELETE", "/v1/orgs/{org}/projects/{project}", "owner"},
	{"GET", "/v1/orgs/{org}/webhooks", "member"},
	{"POST", "/v1/orgs/{org}/webhooks", "admin"},
	{"GET", "/v1/orgs/{org}/webhooks/{webhook}", "member"},
	{"PATCH", "/v1/orgs/{org}/webhooks/{webhook}", "admin"},
	{"POST", "/v1/orgs/{org}/webhooks/{webhook}/rotate-url", "admin"},
	{"DELETE", "/v1/orgs/{org}/webhooks/{webhook}", "admin"},
	{"GET", "/v1/orgs/{org}/events", "member"},
	{"GET", "/v1/orgs/{org}/events/stats", "member"},
	{"GET", "/v1/orgs/{org}/events/{event}", "member"},
	{"GET", "/v1/orgs/{org}/webhooks/{webhook}/destinations", "member"},
	{"POST", "/v1/orgs/{org}/webhooks/{webhook}/destinations", "admin"},
	{"PATCH", "/v1/orgs/{org}/destinations/{destination}", "admin"},
	{"DELETE", "/v1/orgs/{org}/destinations/{destination}", "admin"},
	{"POST", "/v1/orgs/{org}/destinations/{destination}/rotate-secret", "admin"},
	{"POST", "/v1/orgs/{org}/destinations/{destination}/test", "admin"},
	{"GET", "/v1/orgs/{org}/deliveries", "member"},
	{"GET", "/v1/orgs/{org}/deliveries/{delivery}", "member"},
	{"POST", "/v1/orgs/{org}/deliveries/{delivery}/retry", "admin"},
	{"GET", "/v1/orgs/{org}/contracts", "member"},
	{"GET", "/v1/orgs/{org}/contracts/{contract}", "member"},
	{"POST", "/v1/orgs/{org}/contracts/{contract}/versions", "admin"},
	{"POST", "/v1/orgs/{org}/contracts/{contract}/relearn", "admin"},
	{"GET", "/v1/orgs/{org}/incidents", "member"},
	{"POST", "/v1/orgs/{org}/incidents/{incident}/resolve", "admin"},
	{"GET", "/v1/orgs/{org}/incidents/{incident}/replay", "member"},
	{"POST", "/v1/orgs/{org}/incidents/{incident}/replay", "admin"},
	{"GET", "/v1/orgs/{org}/incidents/{incident}/repair-suggestion", "member"},
	{"GET", "/v1/orgs/{org}/alert-settings", "member"},
	{"GET", "/v1/orgs/{org}/alert-channels", "member"},
	{"POST", "/v1/orgs/{org}/alert-channels", "admin"},
	{"PATCH", "/v1/orgs/{org}/alert-channels/{channel}", "admin"},
	{"DELETE", "/v1/orgs/{org}/alert-channels/{channel}", "admin"},
	{"POST", "/v1/orgs/{org}/alert-channels/{channel}/test", "admin"},
	{"GET", "/v1/orgs/{org}/alerts", "member"},
	{"GET", "/v1/orgs/{org}/repair-rules", "member"},
	{"POST", "/v1/orgs/{org}/repair-rules", "admin"},
	{"POST", "/v1/orgs/{org}/repair-rules/preview", "member"},
	{"PATCH", "/v1/orgs/{org}/repair-rules/{rule}", "admin"},
	{"DELETE", "/v1/orgs/{org}/repair-rules/{rule}", "admin"},
	{"GET", "/v1/orgs/{org}/audit-logs", "admin"},
	{"GET", "/v1/orgs/{org}/integrations", "member"},
	{"POST", "/v1/orgs/{org}/integrations", "admin"},
	{"PATCH", "/v1/orgs/{org}/integrations/{integration}", "admin"},
	{"DELETE", "/v1/orgs/{org}/integrations/{integration}", "admin"},
	{"GET", "/v1/orgs/{org}/connections", "member"},
	{"GET", "/v1/orgs/{org}/connections/{connection}", "member"},
	{"DELETE", "/v1/orgs/{org}/connections/{connection}", "admin"},
	{"POST", "/v1/orgs/{org}/connections/{connection}/refresh", "admin"},
	{"GET", "/v1/orgs/{org}/connections/{connection}/token", "admin"}, // hands out the user's access token
	{"POST", "/v1/orgs/{org}/connect-sessions", "admin"},
}

func TestEveryOrgRouteHasAPolicy(t *testing.T) {
	src, err := os.ReadFile("../api/server.go")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, r := range orgRoutes {
		listed[r.method+" "+r.path] = true
	}
	re := regexp.MustCompile(`h\("(\w+) (/v1/orgs/\{org\}[^"]*)"`)
	n := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		n++
		if !listed[m[1]+" "+m[2]] {
			t.Errorf("%s %s has no entry in orgRoutes: add its permission policy", m[1], m[2])
		}
	}
	if n != len(orgRoutes) {
		t.Errorf("server.go has %d org routes, the policy lists %d", n, len(orgRoutes))
	}
}

func TestPermissions(t *testing.T) {
	e := setup(t)
	rc := newReceiver(t)
	signup := func(email, org string) (string, string) {
		s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": email, "password": "correct-horse-9", "org_name": org}, 201)
		tok := s["token"].(string)
		return tok, s["user"].(map[string]any)["id"].(string)
	}
	owner, _ := signup("owner@example.com", "Acme")
	adminTok, adminID := signup("admin@example.com", "Admin's own")
	memberTok, _ := signup("member@example.com", "Member's own")
	outsider, _ := signup("outsider@example.com", "Other Co")

	orgID := ""
	for _, o := range e.call("GET", "/v1/me", owner, nil, 200)["orgs"].([]any) {
		orgID = o.(map[string]any)["id"].(string)
	}
	base := "/v1/orgs/" + orgID
	e.call("POST", base+"/members", owner, map[string]any{"email": "admin@example.com", "role": "admin"}, 201)
	e.call("POST", base+"/members", owner, map[string]any{"email": "member@example.com", "role": "member"}, 201)
	memberKey := e.call("POST", base+"/api-keys", owner, map[string]any{"name": "ci", "role": "member"}, 201)
	adminKey := e.call("POST", base+"/api-keys", owner, map[string]any{"name": "ops", "role": "admin"}, 201)

	// One of everything, so every route has a real ID to aim at.
	proj := e.call("POST", base+"/projects", owner, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", owner, map[string]any{"project_id": proj, "name": "w", "provider": "generic"}, 201)
	dest := e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/destinations", owner, map[string]any{"name": "app", "url": rc.srv.URL}, 201)["destination"].(map[string]any)["id"].(string)
	var eventID string
	for i := 0; i < 3; i++ {
		_, res := e.deliver(wh["ingest_url"].(string), fmt.Sprintf(`{"type":"t","amount":%d}`, i), map[string]string{"X-Event-Id": fmt.Sprint(i)})
		eventID = res["id"].(string)
	}
	e.runChecker()
	e.runWorker()
	contract := e.call("GET", base+"/contracts", owner, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	e.call("POST", base+"/contracts/"+contract+"/versions", owner, map[string]any{"critical_fields": []string{"amount"}}, 201)
	e.deliver(wh["ingest_url"].(string), `{"type":"t","amount":"x"}`, map[string]string{"X-Event-Id": "bad"})
	e.runChecker()
	incident := e.call("GET", base+"/incidents", owner, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	delivery := e.call("GET", base+"/deliveries", owner, nil, 200)["data"].([]any)[0].(map[string]any)["id"].(string)
	channel := e.call("POST", base+"/alert-channels", owner, map[string]any{"type": "webhook", "name": "c", "url": rc.srv.URL, "events": []string{"incident_opened"}}, 201)["channel"].(map[string]any)["id"].(string)
	rule := e.call("POST", base+"/repair-rules", owner, map[string]any{"webhook_id": wh["id"], "name": "r", "ops": []any{map[string]any{"op": "remove", "path": "x"}}}, 201)["id"].(string)

	integration := e.call("POST", base+"/integrations", owner, map[string]any{"provider": "hubspot", "client_id": "c", "client_secret": "s"}, 201)["id"].(string)
	var connection string
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO connections (org_id, integration_id, end_user_id, credentials_enc)
		VALUES ($1, $2, 'u1', '\x00') RETURNING id`, orgID, integration).Scan(&connection); err != nil {
		t.Fatal(err)
	}

	ids := map[string]string{
		"{org}": orgID, "{user}": adminID, "{key}": adminKey["api_key"].(map[string]any)["id"].(string), "{project}": proj,
		"{webhook}": wh["id"].(string), "{event}": eventID, "{destination}": dest, "{delivery}": delivery,
		"{contract}": contract, "{incident}": incident, "{channel}": channel, "{rule}": rule,
		"{integration}": integration, "{connection}": connection,
	}
	status := func(method, path, token string) (int, string) {
		req, _ := http.NewRequest(method, e.api.URL+path, bytes.NewBufferString("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	callers := []struct {
		name, token, role string
	}{
		{"member", memberTok, "member"},
		{"member API key", memberKey["key"].(string), "member"},
		{"admin", adminTok, "admin"},
		{"admin API key", adminKey["key"].(string), "admin"},
	}
	rank := map[string]int{"member": 1, "admin": 2, "owner": 3}
	checked := 0
	for _, r := range orgRoutes {
		path := r.path
		for k, v := range ids {
			path = strings.ReplaceAll(path, k, v)
		}
		// Someone from another org can't even tell the org exists.
		if code, body := status(r.method, path, outsider); code != 404 {
			t.Errorf("outsider %s %s: %d %s", r.method, r.path, code, body)
		}
		for _, c := range callers {
			if rank[c.role] >= rank[r.min] {
				continue // allowed: covered by the feature tests
			}
			checked++
			if code, body := status(r.method, path, c.token); code != 403 {
				t.Errorf("%s %s %s (needs %s): %d %s", c.name, r.method, r.path, r.min, code, body)
			}
		}
	}
	if checked < 60 {
		t.Fatalf("only %d forbidden combinations checked", checked)
	}

	// Nothing a forbidden caller tried actually happened.
	if n := len(e.call("GET", base+"/repair-rules", owner, nil, 200)["data"].([]any)); n != 1 {
		t.Fatalf("repair rules: %d", n)
	}
	e.call("GET", base+"/webhooks/"+wh["id"].(string), owner, nil, 200)
	e.call("GET", base+"/projects/"+proj, owner, nil, 200)
	if logs := e.call("GET", base+"/audit-logs", owner, nil, 200)["data"].([]any); strings.Contains(fmt.Sprint(logs), "project.delete") {
		t.Fatal("a forbidden delete was audited as done")
	}
}
