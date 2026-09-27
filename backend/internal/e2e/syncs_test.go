package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/api"
	"relaya/internal/connect"
	"relaya/internal/ingest"
	"relaya/internal/syncs"
)

// fakeItems is a provider API listing items, two per page; the test edits them.
type fakeItems struct {
	mu    sync.Mutex
	srv   *httptest.Server
	items []map[string]any
	fail  bool
	calls int
}

func newFakeItems(t *testing.T) *fakeItems {
	f := &fakeItems{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if r.Header.Get("Authorization") != "Bearer access-seed" {
			w.WriteHeader(401)
			return
		}
		if f.fail {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"message":"database unavailable"}`))
			return
		}
		page := 0
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		end := min(page*2+2, len(f.items))
		out := map[string]any{"items": f.items[min(page*2, end):end]}
		if end < len(f.items) {
			out["next"] = fmt.Sprint(page + 1)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeItems) set(items ...map[string]any) { f.mu.Lock(); f.items = items; f.mu.Unlock() }
func (f *fakeItems) failing(v bool)              { f.mu.Lock(); f.fail = v; f.mu.Unlock() }

func item(id, name string) map[string]any { return map[string]any{"id": id, "name": name} }

func TestSyncsEndToEnd(t *testing.T) {
	fake := newFakeProvider(t)
	api_ := newFakeItems(t)
	defer connect.Override(&connect.Provider{Key: "fakeoauth", Name: "FakeOAuth", Auth: connect.OAuth2,
		AuthURL: fake.srv.URL + "/authorize", TokenURL: fake.srv.URL + "/token", APIBase: api_.srv.URL,
		ProxyHosts: []*regexp.Regexp{regexp.MustCompile(`^http://127\.0\.0\.1:\d+$`)}})()
	defer syncs.Override(&syncs.Model{Key: "fakeoauth.items", Provider: "fakeoauth", Name: "Items",
		Fields: []syncs.Field{{Key: "list", Label: "List", Required: true}, {Key: "note", Label: "Note"}}},
		func(ctx context.Context, f syncs.Fetcher, cfg map[string]string, cursor string) (syncs.Page, error) {
			if cursor == "" {
				cursor = "0"
			}
			status, raw, err := f.Call(ctx, "GET", "", "/items", url.Values{"page": {cursor}, "list": {cfg["list"]}}, nil, nil)
			if err != nil {
				return syncs.Page{}, err
			}
			if status != 200 {
				return syncs.Page{}, fmt.Errorf("provider answered HTTP %d: %s", status, raw)
			}
			var res struct {
				Items []map[string]any `json:"items"`
				Next  string           `json:"next"`
			}
			_ = json.Unmarshal(raw, &res)
			p := syncs.Page{Cursor: res.Next, More: res.Next != ""}
			for _, it := range res.Items {
				b, _ := json.Marshal(it)
				p.Records = append(p.Records, syncs.Record{ID: it["id"].(string), Data: b})
			}
			return p, nil
		}, "fake.item")()

	var svc *connect.Service
	e := setupWith(t, func(s *api.Server, _ *ingest.Handler) { svc = s.Connect })
	runner := &syncs.Runner{Pool: e.pool, Connect: svc, HTTP: connect.NewProxyClient()}
	ctx := context.Background()
	rc := newReceiver(t)

	owner := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "s@example.com", "password": "correct-horse-1", "org_name": "Sync Co"}, 201)
	tok := owner["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	e.call("POST", base+"/alert-channels", tok, map[string]any{"type": "webhook", "name": "Ops", "url": rc.srv.URL, "events": []string{"sync_failing", "sync_recovered"}}, 201)

	integ := e.call("POST", base+"/integrations", tok, map[string]any{"provider": "fakeoauth", "client_id": "cid-1", "client_secret": "client-secret-1"}, 201)
	hub := e.call("POST", base+"/integrations", tok, map[string]any{"provider": "hubspot", "client_id": "c", "client_secret": "s"}, 201)
	save := func(integrationID, user string) string {
		exp := time.Now().Add(time.Hour)
		var id string
		if err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) (err error) {
			id, err = svc.Save(ctx, tx, orgID, integrationID, user, connect.Credentials{AccessToken: "access-seed", RefreshToken: "refresh-1", ExpiresAt: &exp}, connect.Metadata{})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	connID := save(integ["id"].(string), "shop-1")
	hubConn := save(hub["id"].(string), "shop-1")

	models := e.call("GET", "/v1/connect/sync-models", tok, nil, 200)
	if s := mustJSON(models); !strings.Contains(s, `"zoho.crm_records"`) || !strings.Contains(s, `"google.sheet_rows"`) || !strings.Contains(s, `"hubspot.crm_objects"`) {
		t.Fatalf("models: %s", s)
	}

	// Validation.
	e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": hubConn, "model": "fakeoauth.items", "config": map[string]string{"list": "a"}}, 400) // wrong provider
	e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": connID, "model": "fakeoauth.items", "config": map[string]string{}}, 400)             // required
	e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": connID, "model": "fakeoauth.items", "config": map[string]string{"list": "a", "x": "1"}}, 400)
	e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": connID, "model": "fakeoauth.items", "config": map[string]string{"list": "a"}, "interval_minutes": 1}, 400)
	e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": hubConn, "model": "hubspot.crm_objects", "config": map[string]string{"object": "tickets"}}, 400)

	// A sync with its own new webhook, which gets a destination.
	sy := e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": connID, "model": "fakeoauth.items", "config": map[string]string{"list": "main"}}, 201)
	syncID, whID := sy["id"].(string), sy["webhook_id"].(string)
	if sy["interval_minutes"] != float64(15) || sy["last_status"] != "never" || !strings.HasPrefix(sy["webhook_name"].(string), "Sync: ") {
		t.Fatalf("sync: %v", sy)
	}
	wh := e.call("GET", base+"/webhooks/"+whID, tok, nil, 200)
	if wh["has_signing_secret"] != true {
		t.Fatal("sync webhook has no secret: anyone could post into it")
	}
	e.call("POST", base+"/webhooks/"+whID+"/destinations", tok, map[string]any{"name": "app", "url": rc.srv.URL}, 201)

	run := func() {
		t.Helper()
		e.pool.Exec(ctx, `UPDATE syncs SET next_run_at = now()`)
		if _, err := runner.RunDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	events := func() []map[string]any {
		t.Helper()
		rows, err := e.pool.Query(ctx, `SELECT type, convert_from(payload, 'UTF8') FROM events WHERE webhook_id = $1 ORDER BY received_at`, whID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []map[string]any
		for rows.Next() {
			var typ, payload string
			_ = rows.Scan(&typ, &payload)
			m := map[string]any{}
			_ = json.Unmarshal([]byte(payload), &m)
			m["_type"] = typ
			out = append(out, m)
		}
		return out
	}

	// First run: 3 existing items are remembered, not announced.
	api_.set(item("1", "Asha"), item("2", "Ravi"), item("3", "Meera"))
	run()
	s1 := e.call("GET", base+"/syncs", tok, nil, 200)["data"].([]any)[0].(map[string]any)
	if s1["last_status"] != "ok" || s1["records"] != float64(3) || s1["events"] != float64(0) || s1["baseline_done"] != true {
		t.Fatalf("after baseline: %v", s1)
	}
	if n := len(events()); n != 0 {
		t.Fatalf("baseline produced %d events", n)
	}

	// A change and a new item: exactly two events, delivered to the destination.
	api_.set(item("1", "Asha"), item("2", "Ravi Kumar"), item("3", "Meera"), item("4", "Kiran"))
	run()
	ev := events()
	// In page order: item 2 (page 1) changed, item 4 (page 2) is new.
	if len(ev) != 2 || ev[0]["_type"] != "fake.item.updated" || ev[1]["_type"] != "fake.item.created" || ev[1]["record_id"] != "4" {
		t.Fatalf("events: %v", ev)
	}
	upd := ev[0]
	if upd["record_id"] != "2" || upd["change"] != "updated" || upd["end_user_id"] != "shop-1" || upd["sync_id"] != syncID ||
		upd["record"].(map[string]any)["name"] != "Ravi Kumar" {
		t.Fatalf("updated event: %v", upd)
	}
	e.runWorker()
	if rc.count() != 2 || !strings.Contains(string(rc.last().body), `"fake.item.`) {
		t.Fatalf("delivered %d", rc.count())
	}
	// The events are checked against contracts like webhooks.
	e.runChecker()
	if cs := e.call("GET", base+"/contracts", tok, nil, 200)["data"].([]any); len(cs) != 2 {
		t.Fatalf("contracts: %v", cs)
	}

	// Nothing changed: nothing new. Calls went through the proxy log.
	run()
	if n := len(events()); n != 2 {
		t.Fatalf("unchanged run made events: %d", n)
	}
	if calls := e.call("GET", base+"/proxy-calls?connection="+connID, tok, nil, 200)["data"].([]any); len(calls) < 6 {
		t.Fatalf("proxy calls logged: %d", len(calls))
	}
	runs := e.call("GET", base+"/syncs/"+syncID+"/runs", tok, nil, 200)["data"].([]any)
	if len(runs) != 3 || runs[1].(map[string]any)["created"] != float64(1) || runs[1].(map[string]any)["updated"] != float64(1) || runs[1].(map[string]any)["fetched"] != float64(4) {
		t.Fatalf("runs: %v", runs)
	}

	// Provider failing: 3 failed runs send one alert; recovery sends another, and catches up.
	api_.failing(true)
	for i := 0; i < 4; i++ {
		run()
	}
	s2 := e.call("GET", base+"/syncs", tok, nil, 200)["data"].([]any)[0].(map[string]any)
	if s2["last_status"] != "error" || s2["consecutive_failures"] != float64(4) || !strings.Contains(s2["last_error"].(string), "500") {
		t.Fatalf("failing: %v", s2)
	}
	if n := countAlerts(t, e, orgID, "sync_failing"); n != 1 {
		t.Fatalf("failing alerts: %d", n)
	}
	api_.failing(false)
	api_.set(item("1", "Asha"), item("2", "Ravi Kumar"), item("3", "Meera"), item("4", "Kiran"), item("5", "Dev"))
	run()
	if n := countAlerts(t, e, orgID, "sync_recovered"); n != 1 {
		t.Fatalf("recovered alerts: %d", n)
	}
	if ev = events(); len(ev) != 3 || ev[2]["record_id"] != "5" {
		t.Fatalf("after recovery: %v", ev)
	}

	// Paused syncs don't run; "Run now" is refused for other orgs' syncs (404).
	e.call("PATCH", base+"/syncs/"+syncID, tok, map[string]any{"enabled": false}, 200)
	before := api_.calls
	e.pool.Exec(ctx, `UPDATE syncs SET next_run_at = now()`)
	runner.RunDue(ctx)
	if api_.calls != before {
		t.Fatal("paused sync ran")
	}
	e.call("POST", base+"/syncs/00000000-0000-0000-0000-000000000000/run", tok, nil, 404)

	// A limit per run: the first run stops early and continues soon, and the
	// baseline is only complete at the end (no "created" for existing items).
	small := &syncs.Runner{Pool: e.pool, Connect: svc, HTTP: connect.NewProxyClient(), MaxRecords: 2}
	sy2 := e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": connID, "model": "fakeoauth.items", "config": map[string]string{"list": "second"}, "webhook_id": whID}, 201)
	e.pool.Exec(ctx, `UPDATE syncs SET next_run_at = now() WHERE id = $1`, sy2["id"])
	small.RunDue(ctx)
	var baseline bool
	var next time.Time
	e.pool.QueryRow(ctx, `SELECT baseline_done, next_run_at FROM syncs WHERE id = $1`, sy2["id"]).Scan(&baseline, &next)
	if baseline || time.Until(next) > 2*time.Minute {
		t.Fatalf("partial first run: baseline %v, next in %s", baseline, time.Until(next))
	}
	for i := 0; i < 3; i++ {
		e.pool.Exec(ctx, `UPDATE syncs SET next_run_at = now() WHERE id = $1`, sy2["id"])
		small.RunDue(ctx)
	}
	e.pool.QueryRow(ctx, `SELECT baseline_done FROM syncs WHERE id = $1`, sy2["id"]).Scan(&baseline)
	if !baseline || len(events()) != 3 {
		t.Fatalf("baseline after catching up: %v, %d events", baseline, len(events()))
	}

	// emit_existing: the first run announces everything.
	sy3 := e.call("POST", base+"/syncs", tok, map[string]any{"connection_id": connID, "model": "fakeoauth.items", "config": map[string]string{"list": "third"},
		"webhook_id": whID, "emit_existing": true}, 201)
	e.pool.Exec(ctx, `UPDATE syncs SET next_run_at = now() WHERE id = $1`, sy3["id"])
	runner.RunDue(ctx)
	if n := len(events()); n != 8 {
		t.Fatalf("emit_existing: %d events", n)
	}

	// Broken connection: the run fails with that reason, and no sync alert (the connection alerts).
	e.pool.Exec(ctx, `UPDATE connections SET status = 'broken', last_error = 'invalid_grant' WHERE id = $1`, connID)
	for i := 0; i < 3; i++ {
		e.pool.Exec(ctx, `UPDATE syncs SET next_run_at = now() WHERE id = $1`, sy3["id"])
		runner.RunDue(ctx)
	}
	var lastErr string
	e.pool.QueryRow(ctx, `SELECT last_error FROM syncs WHERE id = $1`, sy3["id"]).Scan(&lastErr)
	if !strings.Contains(lastErr, "broken") || countAlerts(t, e, orgID, "sync_failing") != 1 {
		t.Fatalf("broken connection: %q, %d failing alerts", lastErr, countAlerts(t, e, orgID, "sync_failing"))
	}

	// Changing what a sync reads starts it over; deleting the connection deletes its syncs.
	e.call("PATCH", base+"/syncs/"+syncID, tok, map[string]any{"config": map[string]string{"list": "other"}}, 200)
	var records int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM sync_records WHERE sync_id = $1`, syncID).Scan(&records)
	if records != 0 {
		t.Fatalf("records kept after config change: %d", records)
	}
	e.call("DELETE", base+"/connections/"+connID, tok, nil, 204)
	if n := len(e.call("GET", base+"/syncs", tok, nil, 200)["data"].([]any)); n != 0 {
		t.Fatalf("syncs left: %d", n)
	}
}
