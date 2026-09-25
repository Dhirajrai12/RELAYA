package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"relaya/internal/delivery"
)

// receiver is a fake customer endpoint whose response we control.
type receiver struct {
	mu       sync.Mutex
	status   int
	requests []received
	srv      *httptest.Server
}

type received struct {
	header http.Header
	body   []byte
}

func newReceiver(t *testing.T) *receiver {
	rc := &receiver{status: http.StatusOK}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.requests = append(rc.requests, received{r.Header.Clone(), body})
		status := rc.status
		rc.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) set(status int) { rc.mu.Lock(); rc.status = status; rc.mu.Unlock() }

func (rc *receiver) last() received {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.requests[len(rc.requests)-1]
}

func (rc *receiver) count() int { rc.mu.Lock(); defer rc.mu.Unlock(); return len(rc.requests) }

func (e *env) runWorker() int {
	e.t.Helper()
	n, err := e.worker.RunOnce(context.Background(), 50)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestDeliveryEndToEnd(t *testing.T) {
	e := setup(t)
	rc := newReceiver(t)

	s := e.call("POST", "/v1/auth/signup", "", map[string]any{
		"email": "ops@example.com", "password": "correct-horse-9", "org_name": "Delivery Co",
	}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "Shop"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "orders", "provider": "generic"}, 201)
	whID, ingestURL := wh["id"].(string), wh["ingest_url"].(string)

	// Create a destination: the signing secret is returned once.
	created := e.call("POST", base+"/webhooks/"+whID+"/destinations", tok, map[string]any{
		"name": "Order service", "url": rc.srv.URL + "/hooks", "max_attempts": 3,
	}, 201)
	secret := []byte(created["signing_secret"].(string))
	dest := created["destination"].(map[string]any)
	destID := dest["id"].(string)
	if !strings.HasPrefix(string(secret), "rsec_") || dest["enabled"] != true {
		t.Fatalf("created: %v", created)
	}
	e.call("POST", base+"/webhooks/"+whID+"/destinations", tok, map[string]any{"name": "x", "url": "ftp://nope"}, 400)

	// Send test: synchronous, signed, nothing queued.
	test := e.call("POST", base+"/destinations/"+destID+"/test", tok, nil, 200)
	if test["ok"] != true || test["status_code"] != 200.0 {
		t.Fatalf("test delivery: %v", test)
	}
	got := rc.last()
	if !strings.Contains(string(got.body), `"relaya.test"`) ||
		!delivery.Verify(secret, got.header.Get(delivery.SignatureHeader), got.body, time.Now(), time.Minute) {
		t.Fatalf("test request not signed correctly: %s %v", got.body, got.header)
	}

	// Endpoint down: the delivery is queued with the event, fails with 500 and is scheduled for retry.
	rc.set(http.StatusInternalServerError)
	body := `{"type":"order.created","id":"ord_1","total":999}`
	status, res := e.deliver(ingestURL, body, map[string]string{"X-Event-Id": "ord_1", "X-Shop-Domain": "acme.myshop"})
	if status != 200 {
		t.Fatalf("ingest %d", status)
	}
	eventID := res["id"].(string)
	if n := e.runWorker(); n != 1 {
		t.Fatalf("worker processed %d, want 1", n)
	}
	ds := e.call("GET", base+"/deliveries?event_id="+eventID, tok, nil, 200)["data"].([]any)
	d := ds[0].(map[string]any)
	if d["status"] != "retrying" || d["attempts"] != 1.0 || d["last_status_code"] != 500.0 || d["next_attempt_at"] == nil {
		t.Fatalf("after 500: %v", d)
	}
	deliveryID := d["id"].(string)
	if n := e.runWorker(); n != 0 {
		t.Fatalf("retry should wait for its backoff, but worker took %d", n)
	}
	list := e.call("GET", base+"/events", tok, nil, 200)["data"].([]any)
	if list[0].(map[string]any)["delivery"] != "pending" {
		t.Fatalf("events list delivery state: %v", list[0])
	}

	// Endpoint recovers; "Retry now" delivers it.
	rc.set(http.StatusOK)
	retried := e.call("POST", base+"/deliveries/"+deliveryID+"/retry", tok, nil, 200)
	if retried["status"] != "pending" {
		t.Fatalf("retry: %v", retried)
	}
	e.runWorker()
	got = rc.last()
	if string(got.body) != body {
		t.Fatalf("body changed in transit: %s", got.body)
	}
	if got.header.Get("Idempotency-Key") != deliveryID || got.header.Get("Relaya-Event-Id") != eventID ||
		got.header.Get("Relaya-Attempt") != "2" || got.header.Get("X-Shop-Domain") != "acme.myshop" {
		t.Fatalf("forwarded headers: %v", got.header)
	}
	if !delivery.Verify(secret, got.header.Get(delivery.SignatureHeader), got.body, time.Now(), time.Minute) {
		t.Fatal("forwarded delivery signature invalid")
	}
	detail := e.call("GET", base+"/deliveries/"+deliveryID, tok, nil, 200)
	attempts := detail["attempts"].([]any)
	if detail["delivery"].(map[string]any)["status"] != "succeeded" || len(attempts) != 2 ||
		attempts[0].(map[string]any)["outcome"] != "succeeded" || attempts[1].(map[string]any)["outcome"] != "retry" {
		t.Fatalf("delivery detail: %v", detail)
	}
	ev := e.call("GET", base+"/events/"+eventID, tok, nil, 200)
	if ev["delivery"] != "delivered" || len(ev["deliveries"].([]any)) != 1 {
		t.Fatalf("event detail deliveries: %v", ev["deliveries"])
	}
	e.call("POST", base+"/deliveries/"+deliveryID+"/retry", tok, nil, 409) // already succeeded

	// The same provider delivery again is a duplicate: nothing new is forwarded.
	before := rc.count()
	e.deliver(ingestURL, body, map[string]string{"X-Event-Id": "ord_1"})
	if n := e.runWorker(); n != 0 || rc.count() != before {
		t.Fatalf("duplicate was forwarded (worker %d, requests %d→%d)", n, before, rc.count())
	}

	// A 4xx means the destination rejected it: failed at once, no retries.
	rc.set(http.StatusUnprocessableEntity)
	_, res = e.deliver(ingestURL, `{"type":"order.created","id":"ord_2"}`, map[string]string{"X-Event-Id": "ord_2"})
	e.runWorker()
	d = e.call("GET", base+"/deliveries?event_id="+res["id"].(string), tok, nil, 200)["data"].([]any)[0].(map[string]any)
	if d["status"] != "failed" || d["attempts"] != 1.0 || d["last_status_code"] != 422.0 {
		t.Fatalf("after 422: %v", d)
	}

	// Disabled destinations get nothing queued.
	e.call("PATCH", base+"/destinations/"+destID, tok, map[string]any{"enabled": false}, 200)
	_, res = e.deliver(ingestURL, `{"type":"order.created","id":"ord_3"}`, map[string]string{"X-Event-Id": "ord_3"})
	if ds := e.call("GET", base+"/deliveries?event_id="+res["id"].(string), tok, nil, 200)["data"].([]any); len(ds) != 0 {
		t.Fatalf("disabled destination got a delivery: %v", ds)
	}

	// Destination list shows health; stats show forwarded totals.
	dl := e.call("GET", base+"/webhooks/"+whID+"/destinations", tok, nil, 200)["data"].([]any)[0].(map[string]any)
	st := dl["stats"].(map[string]any)
	if st["succeeded_24h"] != 1.0 || st["failed_24h"] != 1.0 || st["last_success_at"] == nil {
		t.Fatalf("destination stats: %v", st)
	}
	fwd := e.call("GET", base+"/events/stats", tok, nil, 200)["forwarded"].(map[string]any)
	if fwd["succeeded"] != 1.0 || fwd["failed"] != 1.0 {
		t.Fatalf("forwarded stats: %v", fwd)
	}

	// Secret rotation returns a new secret, and the audit log saw everything.
	rot := e.call("POST", base+"/destinations/"+destID+"/rotate-secret", tok, nil, 200)
	if rot["signing_secret"] == string(secret) {
		t.Fatal("secret not rotated")
	}
	logs := mustJSON(e.call("GET", base+"/audit-logs", tok, nil, 200))
	for _, a := range []string{"destination.create", "destination.test", "delivery.retry", "destination.update", "destination.rotate_secret"} {
		if !strings.Contains(logs, `"`+a+`"`) {
			t.Errorf("audit log missing %s", a)
		}
	}
	if strings.Contains(logs, string(secret)) {
		t.Fatal("secret leaked into audit log")
	}
	e.call("DELETE", base+"/destinations/"+destID, tok, nil, 204)
}
