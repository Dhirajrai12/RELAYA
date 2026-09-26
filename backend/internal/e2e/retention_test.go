package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"relaya/internal/retention"
)

func TestRetention(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rc := newReceiver(t)

	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "keep@example.com", "password": "correct-horse-8", "org_name": "Keep"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	wh := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "w", "provider": "generic"}, 201)
	e.call("POST", base+"/webhooks/"+wh["id"].(string)+"/destinations", tok, map[string]any{"name": "app", "url": rc.srv.URL}, 201)

	ids := make([]string, 3)
	for i := range ids {
		_, res := e.deliver(wh["ingest_url"].(string), fmt.Sprintf(`{"type":"t","n":%d}`, i), map[string]string{"X-Event-Id": fmt.Sprintf("r%d", i)})
		ids[i] = res["id"].(string)
	}
	e.runWorker()

	// Age two events (and everything pointing at them): one into an old monthly
	// partition (dropped whole), one into the default partition (deleted in batches).
	ago := func(days int) time.Time { return time.Now().Add(-time.Duration(days) * 24 * time.Hour) }
	if _, err := e.pool.Exec(ctx, `SELECT ensure_event_partitions($1, 0)`, ago(100)); err != nil {
		t.Fatal(err)
	}
	var oldPartition string
	if err := e.pool.QueryRow(ctx, `SELECT format('events_%s', to_char($1::timestamptz, 'YYYY_MM'))`, ago(100)).Scan(&oldPartition); err != nil {
		t.Fatal(err)
	}
	for i, days := range map[int]int{0: 100, 1: 40} {
		at := ago(days)
		for _, q := range []string{
			`UPDATE events SET received_at = $2 WHERE id = $1`,
			`UPDATE deliveries SET event_received_at = $2 WHERE event_id = $1`,
		} {
			if _, err := e.pool.Exec(ctx, q, ids[i], at); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO sessions (user_id, token_hash, expires_at) SELECT id, '\x01', now() - interval '1 day' FROM users LIMIT 1`); err != nil {
		t.Fatal(err)
	}

	st, err := retention.RunOnce(ctx, e.pool, time.Now(), retention.Policy{Events: 30 * 24 * time.Hour, Alerts: 90 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.PartitionsDropped) != 1 || st.PartitionsDropped[0] != oldPartition || st.Events != 1 || st.Deliveries != 2 || st.Sessions != 1 {
		t.Fatalf("stats: %+v (want partition %s)", st, oldPartition)
	}
	var left, deliveries, attempts int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&left)
	e.pool.QueryRow(ctx, `SELECT count(*) FROM deliveries`).Scan(&deliveries)
	e.pool.QueryRow(ctx, `SELECT count(*) FROM delivery_attempts`).Scan(&attempts)
	if left != 1 || deliveries != 1 || attempts != 1 {
		t.Fatalf("left: events %d, deliveries %d, attempts %d", left, deliveries, attempts)
	}
	e.call("GET", base+"/events/"+ids[2], tok, nil, 200)
	e.call("GET", base+"/events/"+ids[0], tok, nil, 404)
	e.call("GET", "/v1/me", tok, nil, 200) // the live session survives

	// A second run finds nothing.
	if st, err := retention.RunOnce(ctx, e.pool, time.Now(), retention.Policy{Events: 30 * 24 * time.Hour}); err != nil || !st.Empty() {
		t.Fatalf("second run: %+v %v", st, err)
	}
}
