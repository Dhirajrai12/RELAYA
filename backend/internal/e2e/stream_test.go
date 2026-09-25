package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// openStream connects to the org's realtime stream and authenticates.
func (e *env) openStream(orgID, token string) *websocket.Conn {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(e.api.URL, "http") + "/v1/orgs/" + orgID + "/stream"
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { c.CloseNow() })
	auth, _ := json.Marshal(map[string]string{"type": "auth", "token": token})
	if err := c.Write(ctx, websocket.MessageText, auth); err != nil {
		e.t.Fatal(err)
	}
	return c
}

// next reads messages until one has the given type (or times out).
func next(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["type"] == typ {
			return m
		}
	}
}

func TestRealtimeStream(t *testing.T) {
	e := setup(t)
	rc := newReceiver(t)

	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "live@example.com", "password": "correct-horse-7", "org_name": "Live"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID

	c := e.openStream(orgID, tok)
	next(t, c, "ready")

	// A config change made through the API arrives as a "change" with the audit action.
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	if m := next(t, c, "change"); m["action"] != "project.create" || m["target_id"] != proj {
		t.Fatalf("change: %v", m)
	}
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "w", "provider": "generic"}, 201)
	next(t, c, "change") // webhook.create
	e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/destinations", tok, map[string]any{"name": "d", "url": rc.srv.URL}, 201)
	if m := next(t, c, "change"); m["action"] != "destination.create" {
		t.Fatalf("destination change: %v", m)
	}

	// An incoming webhook arrives as an "event" the moment it is stored.
	_, res := e.deliver(wh["ingest_url"].(string), `{"type":"x","id":"1"}`, map[string]string{"X-Event-Id": "1"})
	if m := next(t, c, "event"); m["event_id"] != res["id"] || m["status"] != "received" {
		t.Fatalf("event: %v", m)
	}

	// The worker's result arrives as a "delivery".
	e.runWorker()
	if m := next(t, c, "delivery"); m["event_id"] != res["id"] || m["status"] != "succeeded" {
		t.Fatalf("delivery: %v", m)
	}

	// Bad token: closed with a policy violation.
	bad := e.openStream(orgID, "rs_not-a-real-token")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := bad.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("bad token: %v", err)
	}

	// A valid user from another organization cannot listen in.
	other := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "spy@example.com", "password": "correct-horse-8", "org_name": "Other"}, 201)
	spy := e.openStream(orgID, other["token"].(string))
	if _, _, err := spy.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("outsider: %v", err)
	}
}
