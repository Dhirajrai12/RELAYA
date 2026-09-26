package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"relaya/internal/api"
	"relaya/internal/ingest"
	"relaya/internal/status"
)

func TestPublicStatus(t *testing.T) {
	svc := &status.Service{TTL: time.Nanosecond}
	e := setupWith(t, func(s *api.Server, _ *ingest.Handler) { s.Status = svc })
	svc.Pool = e.pool
	ctx := context.Background()
	p := &status.Prober{Pool: e.pool, APIURL: e.api.URL, IngestURL: e.ingest.URL, HTTP: http.DefaultClient}
	get := func() map[string]any { return e.call("GET", "/v1/status", "", nil, 200) }
	comp := func(page map[string]any, id string) string {
		for _, c := range page["components"].([]any) {
			if m := c.(map[string]any); m["id"] == id {
				return m["status"].(string)
			}
		}
		return "missing"
	}

	// Nothing recorded yet: the worker isn't checking, so what it runs is down.
	page := get()
	if page["status"] != "degraded" || comp(page, "delivery") != "down" || comp(page, "api") != "operational" {
		t.Fatalf("no samples: %v", page)
	}

	// All healthy.
	if err := p.Record(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	page = get()
	if page["status"] != "operational" {
		t.Fatalf("healthy: %v", page)
	}
	days := page["days"].([]any)
	today := days[len(days)-1].(map[string]any)
	if len(days) != 90 || today["uptime"].(map[string]any)["delivery"] != 100.0 || days[0].(map[string]any)["uptime"].(map[string]any)["delivery"] != nil {
		t.Fatalf("history: %d days, today %v", len(days), today)
	}

	// A delivery 10 minutes overdue: delivery is down, the rest fine.
	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "status@example.com", "password": "correct-horse-10", "org_name": "S"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	proj := e.call("POST", "/v1/orgs/"+orgID+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", "/v1/orgs/"+orgID+"/webhooks", tok, map[string]any{"project_id": proj, "name": "w", "provider": "generic"}, 201)
	rc := newReceiver(t)
	e.call("POST", "/v1/orgs/"+orgID+"/webhooks/"+wh["id"].(string)+"/destinations", tok, map[string]any{"name": "d", "url": rc.srv.URL}, 201)
	e.deliver(wh["ingest_url"].(string), `{"a":1}`, map[string]string{"X-Event-Id": "s1"})
	if _, err := e.pool.Exec(ctx, `UPDATE deliveries SET next_attempt_at = now() - interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	if err := p.Record(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	page = get()
	if page["status"] != "degraded" || comp(page, "delivery") != "down" || comp(page, "ingest") != "operational" || comp(page, "contracts") != "operational" {
		t.Fatalf("stuck delivery: %v", page)
	}
	days = page["days"].([]any)
	if up := days[len(days)-1].(map[string]any)["uptime"].(map[string]any)["delivery"]; up != 50.0 {
		t.Fatalf("today's delivery uptime: %v", up)
	}
	// Nothing internal leaks: only ids, names, help text and states.
	for _, c := range page["components"].([]any) {
		if len(c.(map[string]any)) != 4 {
			t.Fatalf("component has extra fields: %v", c)
		}
	}
}
