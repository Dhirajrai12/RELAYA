package syncs

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// canned answers calls with a fixed status and body, and records the calls.
type canned struct {
	status int
	body   string
	calls  []call
}

type call struct {
	method, base, path string
	query              url.Values
	body               any
	header             map[string]string
}

func (c *canned) Call(_ context.Context, method, base, path string, query url.Values, body any, header map[string]string) (int, []byte, error) {
	c.calls = append(c.calls, call{method, base, path, query, body, header})
	return c.status, []byte(c.body), nil
}

func mustModel(t *testing.T, key string) *Model {
	t.Helper()
	m, ok := GetModel(key)
	if !ok {
		t.Fatalf("no model %s", key)
	}
	return m
}

func TestHubSpotContacts(t *testing.T) {
	m := mustModel(t, "hubspot.crm_objects")
	cfg, err := m.Validate(map[string]string{"properties": "email, firstname"})
	if err != nil || cfg["object"] != "contacts" {
		t.Fatalf("validate: %v %v", cfg, err)
	}
	f := &canned{status: 200, body: `{"results":[
		{"id":"101","updatedAt":"2026-09-27T05:00:00.000Z","properties":{"email":"a@x.in"}},
		{"id":"102","updatedAt":"2026-09-27T05:10:00.000Z","properties":{"email":"b@x.in"}}],
		"paging":{"next":{"after":"2"}}}`}
	p, err := m.Fetch(context.Background(), f, cfg, "")
	if err != nil || len(p.Records) != 2 || p.Records[0].ID != "101" || !p.More || p.Cursor != "0|2" {
		t.Fatalf("page 1: %+v %v", p, err)
	}
	c := f.calls[0]
	b, _ := json.Marshal(c.body)
	if c.method != "POST" || c.path != "/crm/v3/objects/contacts/search" || !strings.Contains(string(b), `"propertyName":"lastmodifieddate"`) ||
		!strings.Contains(string(b), `"operator":"GTE"`) || !strings.Contains(string(b), `"properties":["email","firstname","lastmodifieddate"]`) {
		t.Fatalf("request: %s %s %s", c.method, c.path, b)
	}
	// Last page: the cursor becomes the newest time seen, in milliseconds.
	f.body = `{"results":[{"id":"103","updatedAt":"2026-09-27T05:20:00.000Z","properties":{}}]}`
	p, _ = m.Fetch(context.Background(), f, cfg, "0|2")
	b, _ = json.Marshal(f.calls[1].body)
	if p.More || p.Cursor != "1790486400000" || !strings.Contains(string(b), `"after":"2"`) {
		t.Fatalf("page 2: %+v %s", p, b)
	}
	// Companies use hs_lastmodifieddate.
	cfg2, _ := m.Validate(map[string]string{"object": "companies"})
	m.Fetch(context.Background(), f, cfg2, "")
	b, _ = json.Marshal(f.calls[2].body)
	if !strings.Contains(string(b), `"hs_lastmodifieddate"`) || m.EventType(cfg2, "updated") != "hubspot.company.updated" {
		t.Fatalf("companies: %s", b)
	}
	f.status, f.body = 429, `{"message":"You have reached your secondly limit."}`
	if _, err := m.Fetch(context.Background(), f, cfg, ""); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error answer: %v", err)
	}
}

func TestZohoRecords(t *testing.T) {
	m := mustModel(t, "zoho.crm_records")
	cfg, _ := m.Validate(map[string]string{})
	if cfg["module"] != "Leads" || m.EventType(cfg, "created") != "zoho.lead.created" {
		t.Fatalf("defaults: %v", cfg)
	}
	f := &canned{status: 200, body: `{"data":[{"id":"5001","Last_Name":"Rao","Modified_Time":"2026-09-27T10:00:00+05:30"}],"info":{"more_records":true}}`}
	p, err := m.Fetch(context.Background(), f, cfg, "2026-09-26T10:00:00+05:30")
	if err != nil || p.Records[0].ID != "5001" || !p.More || p.Cursor != "2026-09-26T10:00:00+05:30|2" {
		t.Fatalf("page: %+v %v", p, err)
	}
	c := f.calls[0]
	if c.path != "/crm/v2/Leads" || c.query.Get("sort_by") != "Modified_Time" || c.query.Get("sort_order") != "asc" ||
		c.header["If-Modified-Since"] != "2026-09-26T10:00:00+05:30" {
		t.Fatalf("request: %+v", c)
	}
	// After 10 pages: restart from the newest modified time.
	p, _ = m.Fetch(context.Background(), f, cfg, "2026-09-26T10:00:00+05:30|10")
	if p.Cursor != "2026-09-27T10:00:00+05:30" || !p.More {
		t.Fatalf("page 10: %+v", p)
	}
	// 304: nothing changed, the cursor stays.
	f.status, f.body = 304, ""
	p, err = m.Fetch(context.Background(), f, cfg, "2026-09-27T10:00:00+05:30")
	if err != nil || len(p.Records) != 0 || p.More || p.Cursor != "2026-09-27T10:00:00+05:30" {
		t.Fatalf("304: %+v %v", p, err)
	}
	if _, err := m.Fetch(context.Background(), f, map[string]string{"module": "Leads/../users"}, ""); err == nil {
		t.Fatal("bad module name accepted")
	}
}

func TestSheetRows(t *testing.T) {
	m := mustModel(t, "google.sheet_rows")
	if _, err := m.Validate(map[string]string{}); err == nil {
		t.Fatal("spreadsheet_id not required")
	}
	cfg, _ := m.Validate(map[string]string{"spreadsheet_id": "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms", "range": "Orders!A1:C"})
	f := &canned{status: 200, body: `{"values":[["Order ID","Customer",""],["A-1","Asha","x"],[],["A-2","Ravi"],["","Nobody"]]}`}
	p, err := m.Fetch(context.Background(), f, cfg, "")
	if err != nil || len(p.Records) != 3 {
		t.Fatalf("rows: %+v %v", p, err)
	}
	if f.calls[0].base != "https://sheets.googleapis.com" || f.calls[0].path != "/v4/spreadsheets/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/values/Orders%21A1:C" {
		t.Fatalf("request: %+v", f.calls[0])
	}
	var r0 map[string]any
	json.Unmarshal(p.Records[0].Data, &r0)
	if p.Records[0].ID != "row-2" || r0["values"].(map[string]any)["Customer"] != "Asha" || r0["values"].(map[string]any)["column_3"] != "x" {
		t.Fatalf("row: %s %v", p.Records[0].ID, r0)
	}
	if p.Records[1].ID != "row-4" { // the empty row 3 is skipped, numbering kept
		t.Fatalf("row numbers: %s", p.Records[1].ID)
	}
	// Key column: rows without a key wait until it's filled in.
	cfg["key_column"] = "Order ID"
	p, _ = m.Fetch(context.Background(), f, cfg, "")
	if len(p.Records) != 2 || p.Records[0].ID != "A-1" || p.Records[1].ID != "A-2" {
		t.Fatalf("keyed rows: %+v", p.Records)
	}
	if _, err := m.Fetch(context.Background(), f, map[string]string{"spreadsheet_id": "../../x"}, ""); err == nil {
		t.Fatal("bad spreadsheet id accepted")
	}
}

func TestShiprocketOrders(t *testing.T) {
	m := mustModel(t, "shiprocket.orders")
	cfg, _ := m.Validate(map[string]string{})
	f := &canned{status: 200, body: `{"data":[{"id":9001,"status":"NEW"},{"id":9002,"status":"DELIVERED"}],"meta":{"pagination":{"total_pages":7}}}`}
	p, _ := m.Fetch(context.Background(), f, cfg, "")
	if len(p.Records) != 2 || p.Records[0].ID != "9001" || p.Cursor != "2" || !p.More {
		t.Fatalf("page 1: %+v", p)
	}
	p, _ = m.Fetch(context.Background(), f, cfg, "3") // 300 orders watched: page 3 is the last
	if p.More || p.Cursor != "" {
		t.Fatalf("page 3: %+v", p)
	}
	if _, err := m.Validate(map[string]string{"pages": "7"}); err == nil {
		t.Fatal("pages outside the options accepted")
	}
}

func TestCanonicalHashIgnoresKeyOrder(t *testing.T) {
	a := canonical([]byte(`{"b":1,"a":{"y":2,"x":1.50}}`))
	b := canonical([]byte(`{"a":{"x":1.50,"y":2},"b":1}`))
	if string(a) != string(b) || !strings.Contains(string(a), "1.50") {
		t.Fatalf("%s vs %s", a, b)
	}
}

// routed answers calls by path, and records them.
type routed struct {
	answers map[string]string
	calls   []call
}

func (r *routed) Call(_ context.Context, method, base, path string, query url.Values, body any, header map[string]string) (int, []byte, error) {
	r.calls = append(r.calls, call{method, base, path, query, body, header})
	if b, ok := r.answers[path]; ok {
		return 200, []byte(b), nil
	}
	return 404, []byte(`{"errorMessages":["not found"]}`), nil
}

func TestJiraIssues(t *testing.T) {
	m := mustModel(t, "jira.issues")
	cfg, err := m.Validate(map[string]string{"projects": "ops, support", "jql": "issuetype = Bug"})
	if err != nil {
		t.Fatal(err)
	}
	f := &routed{answers: map[string]string{
		"/rest/api/3/myself": `{"timeZone":"Asia/Kolkata"}`,
		"/rest/api/3/search/jql": `{"issues":[
			{"id":"10001","key":"OPS-1","fields":{"summary":"Checkout down","updated":"2026-09-28T10:15:30.123+0530"}},
			{"id":"10002","key":"OPS-2","fields":{"summary":"Slow","updated":"2026-09-28T10:20:00.000+0530"}}],
			"nextPageToken":"tok2","isLast":false}`,
	}}

	// First run: no time filter, the whole (filtered) list, page by page.
	p, err := m.Fetch(context.Background(), f, cfg, "")
	if err != nil || len(p.Records) != 2 || p.Records[0].ID != "10001" || !p.More || p.Cursor != "|tok2" {
		t.Fatalf("page 1: %+v %v", p, err)
	}
	q := f.calls[0].query
	if f.calls[0].path != "/rest/api/3/search/jql" || q.Get("jql") != "project in (OPS, SUPPORT) AND (issuetype = Bug) ORDER BY updated ASC, key ASC" ||
		q.Get("maxResults") != "100" || !strings.Contains(q.Get("fields"), "summary") {
		t.Fatalf("request 1: %s %v", f.calls[0].path, q)
	}
	var rec map[string]any
	_ = json.Unmarshal(p.Records[0].Data, &rec)
	if rec["key"] != "OPS-1" || rec["fields"].(map[string]any)["summary"] != "Checkout down" {
		t.Fatalf("record: %v", rec)
	}

	// Last page: the cursor is the newest update seen.
	f.answers["/rest/api/3/search/jql"] = `{"issues":[{"id":"10003","key":"SUPPORT-9","fields":{"updated":"2026-09-28T10:25:00.000+0530"}}],"isLast":true}`
	p, err = m.Fetch(context.Background(), f, cfg, p.Cursor)
	want := time.Date(2026, 9, 28, 4, 55, 0, 0, time.UTC).UnixMilli() // 10:25 IST
	if err != nil || p.More || p.Cursor != strconv.FormatInt(want, 10) || f.calls[1].query.Get("nextPageToken") != "tok2" {
		t.Fatalf("page 2: %+v %v", p, err)
	}

	// Next run: from that time, written in the user's time zone to the minute.
	f.calls = nil
	f.answers["/rest/api/3/search/jql"] = `{"issues":[],"isLast":true}`
	p, err = m.Fetch(context.Background(), f, cfg, p.Cursor)
	if err != nil || len(f.calls) != 2 || f.calls[0].path != "/rest/api/3/myself" {
		t.Fatalf("next run: %+v %v %v", p, err, f.calls)
	}
	if jql := f.calls[1].query.Get("jql"); !strings.Contains(jql, `updated >= "2026-09-28 10:25"`) {
		t.Fatalf("jql: %s", jql)
	}
	if p.Cursor != strconv.FormatInt(want, 10) {
		t.Fatalf("nothing new must keep the cursor: %q", p.Cursor)
	}

	// Settings are checked.
	for _, bad := range []map[string]string{{"projects": "ops; drop"}, {"jql": "project = X ORDER BY created"}, {"fields": "summary, bad field"}} {
		c, _ := m.Validate(bad)
		if _, err := m.Fetch(context.Background(), &routed{answers: map[string]string{}}, c, ""); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if m.EventType(cfg, "created") != "jira.issue.created" {
		t.Fatal(m.EventType(cfg, "created"))
	}
}
