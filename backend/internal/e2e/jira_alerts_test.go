package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"relaya/internal/alerts"
)

// fakeJira is a Jira Cloud REST API just big enough for alert channels.
type fakeJira struct {
	srv      *httptest.Server
	mu       sync.Mutex
	issues   []map[string]any    // created issues' fields
	comments map[string][]string // issue key -> comment texts
	closed   map[string]bool
}

func newFakeJira(t *testing.T) *fakeJira {
	f := &fakeJira{comments: map[string][]string{}, closed: map[string]bool{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("ops@example.com:good-token")) {
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.Path
		switch {
		case path == "/rest/api/3/myself":
			json.NewEncoder(w).Encode(map[string]string{"accountId": "abc"})
		case path == "/rest/api/3/project/OPS":
			json.NewEncoder(w).Encode(map[string]any{"issueTypes": []map[string]string{{"name": "Task"}, {"name": "Bug"}}})
		case strings.HasPrefix(path, "/rest/api/3/project/"):
			w.WriteHeader(404)
			io.WriteString(w, `{"errorMessages":["No project could be found"]}`)
		case path == "/rest/api/3/issue" && r.Method == "POST":
			f.issues = append(f.issues, body["fields"].(map[string]any))
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"key": fmt.Sprintf("OPS-%d", len(f.issues))})
		case strings.HasSuffix(path, "/comment"):
			key := strings.Split(path, "/")[5]
			f.comments[key] = append(f.comments[key], adfText(body["body"]))
			w.WriteHeader(201)
		case strings.HasSuffix(path, "/transitions") && r.Method == "GET":
			io.WriteString(w, `{"transitions":[{"id":"11","to":{"statusCategory":{"key":"indeterminate"}}},{"id":"31","to":{"statusCategory":{"key":"done"}}}]}`)
		case strings.HasSuffix(path, "/transitions"):
			if body["transition"].(map[string]any)["id"] == "31" {
				f.closed[strings.Split(path, "/")[5]] = true
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// adfText flattens an Atlassian Document Format document to its text.
func adfText(v any) string {
	var sb strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if t, ok := n["text"].(string); ok {
				sb.WriteString(t + "\n")
			}
			walk(n["content"])
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(v)
	return sb.String()
}

func TestJiraAlerts(t *testing.T) {
	e := setup(t)
	jira := newFakeJira(t)
	// The fake uses a self-signed certificate; trust it for this test.
	prev := e.alerts.HTTP
	e.alerts.HTTP = jira.srv.Client()
	t.Cleanup(func() { e.alerts.HTTP = prev })
	app := newReceiver(t)

	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "jira@example.com", "password": "correct-horse-9", "org_name": "Jira shop"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	events := []string{"destination_failing", "destination_recovered", "signature_failures"}
	channel := func(token, project, issueType string, want int) map[string]any {
		t.Helper()
		return e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "jira", "name": "Ops Jira", "events": events, "jira": map[string]any{
			"site": jira.srv.URL + "/some/path", "email": "ops@example.com", "api_token": token, "project": project, "issue_type": issueType}}, want)
	}

	// Settings are checked with Jira when the channel is saved.
	if r := channel("bad-token", "OPS", "", 400); !strings.Contains(fmt.Sprint(r), "401") {
		t.Fatalf("bad token: %v", r)
	}
	if r := channel("good-token", "NOPE", "", 400); !strings.Contains(fmt.Sprint(r), "no Jira project NOPE") {
		t.Fatalf("bad project: %v", r)
	}
	if r := channel("good-token", "OPS", "Epic", 400); !strings.Contains(fmt.Sprint(r), "Task, Bug") {
		t.Fatalf("bad issue type: %v", r)
	}
	e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "jira", "name": "x", "events": events,
		"jira": map[string]any{"site": "http://jira.example", "email": "ops@example.com", "api_token": "t", "project": "OPS"}}, 400)
	ch := channel("good-token", "ops", "", 201)["channel"].(map[string]any)
	chID := ch["id"].(string)
	if !strings.HasPrefix(ch["target"].(string), "OPS · 127.0.0.1:") {
		t.Fatalf("target %v", ch["target"])
	}

	// Send test: checks the settings, creates no issue.
	if r := e.call("POST", base+"/alert-channels/"+chID+"/test", tok, nil, 200); r["ok"] != true {
		t.Fatalf("test: %v", r)
	}
	if len(jira.issues) != 0 {
		t.Fatal("the test created an issue")
	}

	// A failing destination opens an issue.
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "Orders", "provider": "generic"}, 201)
	dest := e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/destinations", tok, map[string]any{"name": "Order app", "url": app.srv.URL}, 201)
	destID := dest["destination"].(map[string]any)["id"].(string)
	n := 0
	send := func() {
		t.Helper()
		n++
		e.deliver(wh["ingest_url"].(string), `{"id":1}`, map[string]string{"X-Event-Id": fmt.Sprintf("j%d", n)})
		e.runWorker()
		e.sendAlerts()
	}
	app.set(http.StatusInternalServerError)
	for i := 0; i < 3; i++ {
		send()
	}
	if len(jira.issues) != 1 {
		t.Fatalf("issues after failing: %d", len(jira.issues))
	}
	f := jira.issues[0]
	if f["summary"] != "Deliveries to Order app are failing" || f["project"].(map[string]any)["key"] != "OPS" ||
		f["issuetype"].(map[string]any)["name"] != "Task" || fmt.Sprint(f["labels"]) != "[relaya]" ||
		!strings.Contains(adfText(f["description"]), "HTTP 500") || !strings.Contains(fmt.Sprint(f["description"]), "https://dash.example/events") {
		t.Fatalf("issue fields: %v", f)
	}

	// The same problem again, while the issue is open: a comment, not a second issue.
	if err := alerts.Enqueue(context.Background(), e.pool, orgID, alerts.DestinationFailingAlert(destID, "Order app", app.srv.URL, 502, "", 9)); err != nil {
		t.Fatal(err)
	}
	e.sendAlerts()
	if len(jira.issues) != 1 || len(jira.comments["OPS-1"]) != 1 || !strings.HasPrefix(jira.comments["OPS-1"][0], "Happened again: ") {
		t.Fatalf("repeat: %d issues, comments %v", len(jira.issues), jira.comments)
	}

	// Recovery comments on the issue and moves it to Done.
	app.set(http.StatusOK)
	send()
	if c := jira.comments["OPS-1"]; len(c) != 2 || !strings.HasPrefix(c[1], "Recovered: Deliveries to Order app have recovered") || !jira.closed["OPS-1"] {
		t.Fatalf("recovery: comments %v, closed %v", c, jira.closed)
	}

	// Failing again later opens a fresh issue.
	app.set(http.StatusInternalServerError)
	for i := 0; i < 3; i++ {
		send()
	}
	if len(jira.issues) != 2 {
		t.Fatalf("issues after failing again: %d", len(jira.issues))
	}

	// The alert log links each alert to its issue.
	log := e.call("GET", base+"/alerts", tok, nil, 200)["data"].([]any)
	byRef := map[string]int{}
	for _, a := range log {
		m := a.(map[string]any)
		if m["status"] != "sent" {
			t.Fatalf("alert not sent: %v", m)
		}
		if ref := m["external_ref"].(string); ref != "" {
			byRef[ref]++
			if m["external_url"] != jira.srv.URL+"/browse/"+ref {
				t.Fatalf("external_url %v", m["external_url"])
			}
		}
	}
	if byRef["OPS-1"] != 3 || byRef["OPS-2"] != 1 { // opened, again, recovered; opened
		t.Fatalf("alert refs: %v", byRef)
	}
}
