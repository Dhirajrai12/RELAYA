// Package syncs pulls records from connected apps on a schedule and turns new
// and changed ones into Relaya events (zoho.lead.created, google.sheet_row.updated…).
// Those events then go through the same path as webhooks: deliveries to your
// destinations, contracts, incidents and replay.
package syncs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Field is one setting a model needs, e.g. a spreadsheet ID.
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Help        string   `json:"help,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// Record is one item from the provider, with a stable ID.
type Record struct {
	ID   string
	Data json.RawMessage
}

// Page is one batch of records. Cursor is where the next page (or the next
// run, when More is false) starts.
type Page struct {
	Records []Record
	Cursor  string
	More    bool
}

// Fetcher calls the provider's API as the connected user (through the proxy).
type Fetcher interface {
	Call(ctx context.Context, method, base, path string, query url.Values, body any, header map[string]string) (status int, resp []byte, err error)
}

// Model is one kind of data a sync can pull.
type Model struct {
	Key         string  `json:"key"`
	Provider    string  `json:"provider"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
	// Incremental models only fetch what changed since the cursor; the others
	// look at everything (within their limit) each run.
	Incremental bool `json:"incremental"`
	// Verified: tested against the provider's real API.
	Verified bool `json:"verified"`

	eventPrefix func(cfg map[string]string) string
	fetch       func(ctx context.Context, f Fetcher, cfg map[string]string, cursor string) (Page, error)
}

// EventType is e.g. "zoho.lead.created".
func (m *Model) EventType(cfg map[string]string, change string) string {
	return m.eventPrefix(cfg) + "." + change
}

// Fetch gets the next page after cursor.
func (m *Model) Fetch(ctx context.Context, f Fetcher, cfg map[string]string, cursor string) (Page, error) {
	return m.fetch(ctx, f, cfg, cursor)
}

// Validate checks and fills a config: required fields, allowed options, defaults.
func (m *Model) Validate(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range m.Fields {
		v := strings.TrimSpace(in[f.Key])
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			return nil, fmt.Errorf("%s is required", f.Label)
		}
		if len(v) > 500 {
			return nil, fmt.Errorf("%s is too long", f.Label)
		}
		if len(f.Options) > 0 && v != "" {
			ok := false
			for _, o := range f.Options {
				ok = ok || o == v
			}
			if !ok {
				return nil, fmt.Errorf("%s must be one of %s", f.Label, strings.Join(f.Options, ", "))
			}
		}
		if v != "" {
			out[f.Key] = v
		}
	}
	for k := range in {
		if _, known := out[k]; !known && !m.hasField(k) {
			return nil, fmt.Errorf("unknown setting %q", k)
		}
	}
	return out, nil
}

func (m *Model) hasField(k string) bool {
	for _, f := range m.Fields {
		if f.Key == k {
			return true
		}
	}
	return false
}

var models = map[string]*Model{}

func register(m *Model) { models[m.Key] = m }

// GetModel returns a model by key.
func GetModel(key string) (*Model, bool) { m, ok := models[key]; return m, ok }

// Models lists all models, by provider then name.
func Models() []*Model {
	out := make([]*Model, 0, len(models))
	for _, m := range models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Override adds or replaces a model and returns a function that undoes it (tests).
func Override(m *Model, fetch func(ctx context.Context, f Fetcher, cfg map[string]string, cursor string) (Page, error), prefix string) (restore func()) {
	m.fetch = fetch
	m.eventPrefix = func(map[string]string) string { return prefix }
	old, had := models[m.Key]
	models[m.Key] = m
	return func() {
		if had {
			models[m.Key] = old
		} else {
			delete(models, m.Key)
		}
	}
}

// apiError turns a provider error answer into a short message.
func apiError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return fmt.Errorf("provider answered HTTP %d: %s", status, msg)
}

// singular turns "Leads" into "lead", "companies" into "company".
func singular(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

var zohoModule = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,99}$`)

func init() {
	// ---- HubSpot CRM objects: search sorted by last-modified time ----------------
	register(&Model{
		Key: "hubspot.crm_objects", Provider: "hubspot", Name: "CRM records",
		Description: "Contacts, companies or deals created or changed in HubSpot.",
		Incremental: true,
		Fields: []Field{
			{Key: "object", Label: "Object", Required: true, Options: []string{"contacts", "companies", "deals"}, Default: "contacts"},
			{Key: "properties", Label: "Properties", Help: "Comma-separated; empty = HubSpot's default set.", Placeholder: "email,firstname,lastname,phone"},
		},
		eventPrefix: func(cfg map[string]string) string { return "hubspot." + singular(cfg["object"]) },
		fetch: func(ctx context.Context, f Fetcher, cfg map[string]string, cursor string) (Page, error) {
			modified := "hs_lastmodifieddate"
			if cfg["object"] == "contacts" {
				modified = "lastmodifieddate"
			}
			since, after := "0", ""
			if c := strings.SplitN(cursor, "|", 2); cursor != "" {
				since = c[0]
				if len(c) == 2 {
					after = c[1]
				}
			}
			body := map[string]any{
				"filterGroups": []any{map[string]any{"filters": []any{map[string]any{"propertyName": modified, "operator": "GTE", "value": since}}}},
				"sorts":        []any{map[string]any{"propertyName": modified, "direction": "ASCENDING"}},
				"limit":        100,
			}
			if p := strings.TrimSpace(cfg["properties"]); p != "" {
				props := []string{}
				for _, x := range strings.Split(p, ",") {
					if x = strings.TrimSpace(x); x != "" {
						props = append(props, x)
					}
				}
				body["properties"] = append(props, modified)
			}
			if after != "" {
				body["after"] = after
			}
			status, raw, err := f.Call(ctx, "POST", "", "/crm/v3/objects/"+cfg["object"]+"/search", nil, body, nil)
			if err != nil {
				return Page{}, err
			}
			if status != 200 {
				return Page{}, apiError(status, raw)
			}
			var res struct {
				Results []struct {
					ID         string         `json:"id"`
					UpdatedAt  string         `json:"updatedAt"`
					Properties map[string]any `json:"properties"`
				} `json:"results"`
				Paging *struct {
					Next struct {
						After string `json:"after"`
					} `json:"next"`
				} `json:"paging"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return Page{}, fmt.Errorf("unexpected HubSpot answer: %w", err)
			}
			page := Page{Cursor: since}
			last := since
			for _, r := range res.Results {
				data, _ := json.Marshal(r)
				page.Records = append(page.Records, Record{ID: r.ID, Data: data})
				if t, err := time.Parse(time.RFC3339Nano, r.UpdatedAt); err == nil {
					last = strconv.FormatInt(t.UnixMilli(), 10)
				}
			}
			// Search stops at 10,000 results: restart from the newest time seen
			// instead of paging deeper. Records at that exact time come again and
			// are dropped as unchanged.
			if res.Paging != nil && res.Paging.Next.After != "" {
				if n, _ := strconv.Atoi(res.Paging.Next.After); n < 9900 {
					page.Cursor, page.More = since+"|"+res.Paging.Next.After, true
					return page, nil
				}
				page.Cursor, page.More = last, true
				return page, nil
			}
			page.Cursor = last
			return page, nil
		},
	})

	// ---- Zoho CRM modules: records modified since the cursor ----------------------
	register(&Model{
		Key: "zoho.crm_records", Provider: "zoho", Name: "CRM records",
		Description: "Leads, contacts, deals or any module's records created or changed in Zoho CRM.",
		Incremental: true,
		Fields: []Field{
			{Key: "module", Label: "Module", Required: true, Default: "Leads", Placeholder: "Leads", Help: "The module's API name: Leads, Contacts, Deals, Accounts, or a custom module."},
			{Key: "fields", Label: "Fields", Help: "Comma-separated API names; empty = all fields.", Placeholder: "Last_Name,Email,Phone"},
		},
		eventPrefix: func(cfg map[string]string) string { return "zoho." + singular(cfg["module"]) },
		fetch: func(ctx context.Context, f Fetcher, cfg map[string]string, cursor string) (Page, error) {
			if !zohoModule.MatchString(cfg["module"]) {
				return Page{}, fmt.Errorf("invalid module name %q", cfg["module"])
			}
			since, pageNo := "", 1
			if c := strings.SplitN(cursor, "|", 2); cursor != "" {
				since = c[0]
				if len(c) == 2 {
					pageNo, _ = strconv.Atoi(c[1])
				}
			}
			q := url.Values{"sort_by": {"Modified_Time"}, "sort_order": {"asc"}, "per_page": {"200"}, "page": {strconv.Itoa(max(pageNo, 1))}}
			if fl := strings.TrimSpace(cfg["fields"]); fl != "" {
				q.Set("fields", fl+",Modified_Time")
			}
			hdr := map[string]string{}
			if since != "" {
				hdr["If-Modified-Since"] = since
			}
			status, raw, err := f.Call(ctx, "GET", "", "/crm/v2/"+cfg["module"], q, nil, hdr)
			if err != nil {
				return Page{}, err
			}
			if status == 304 || status == 204 { // nothing changed / no records
				return Page{Cursor: since}, nil
			}
			if status != 200 {
				return Page{}, apiError(status, raw)
			}
			var res struct {
				Data []map[string]any `json:"data"`
				Info struct {
					MoreRecords bool `json:"more_records"`
				} `json:"info"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return Page{}, fmt.Errorf("unexpected Zoho answer: %w", err)
			}
			page := Page{}
			last := since
			for _, r := range res.Data {
				id := fmt.Sprint(r["id"])
				data, _ := json.Marshal(r)
				page.Records = append(page.Records, Record{ID: id, Data: data})
				if m, ok := r["Modified_Time"].(string); ok && m != "" {
					last = m
				}
			}
			// Pages go up to 2,000 records (10 pages); after that restart from the
			// newest modified time seen.
			switch {
			case res.Info.MoreRecords && pageNo < 10:
				page.Cursor, page.More = since+"|"+strconv.Itoa(pageNo+1), true
			case res.Info.MoreRecords:
				page.Cursor, page.More = last, true
			default:
				page.Cursor = last
			}
			return page, nil
		},
	})

	// ---- Google Sheets: rows of a range, first row as headers ----------------------
	register(&Model{
		Key: "google.sheet_rows", Provider: "google", Name: "Sheet rows",
		Description: "Rows added or changed in a Google Sheet. The first row holds the column names.",
		Verified:    true, // live-tested 2026-09-27 with a real Google account
		Fields: []Field{
			{Key: "spreadsheet_id", Label: "Spreadsheet ID", Required: true, Help: "From the sheet's URL: docs.google.com/spreadsheets/d/<ID>/edit", Placeholder: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms"},
			{Key: "range", Label: "Sheet and range", Default: "Sheet1", Placeholder: "Sheet1 or Orders!A1:F", Help: "A sheet name, or a range in A1 notation."},
			{Key: "key_column", Label: "Key column", Help: "Column name that identifies a row (e.g. Order ID). Empty = the row number.", Placeholder: "Order ID"},
		},
		eventPrefix: func(map[string]string) string { return "google.sheet_row" },
		fetch: func(ctx context.Context, f Fetcher, cfg map[string]string, _ string) (Page, error) {
			id := cfg["spreadsheet_id"]
			if !regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`).MatchString(id) {
				return Page{}, fmt.Errorf("invalid spreadsheet ID")
			}
			path := "/v4/spreadsheets/" + id + "/values/" + url.PathEscape(cfg["range"])
			status, raw, err := f.Call(ctx, "GET", "https://sheets.googleapis.com", path,
				url.Values{"majorDimension": {"ROWS"}, "valueRenderOption": {"FORMATTED_VALUE"}}, nil, nil)
			if err != nil {
				return Page{}, err
			}
			if status != 200 {
				return Page{}, apiError(status, raw)
			}
			var res struct {
				Values [][]any `json:"values"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return Page{}, fmt.Errorf("unexpected Google answer: %w", err)
			}
			if len(res.Values) == 0 {
				return Page{}, nil
			}
			headers := make([]string, len(res.Values[0]))
			for i, h := range res.Values[0] {
				headers[i] = strings.TrimSpace(fmt.Sprint(h))
				if headers[i] == "" {
					headers[i] = "column_" + strconv.Itoa(i+1)
				}
			}
			page := Page{}
			for n, row := range res.Values[1:] {
				if len(page.Records) >= 10000 {
					break
				}
				obj := map[string]any{}
				empty := true
				for i, h := range headers {
					v := ""
					if i < len(row) {
						v = fmt.Sprint(row[i])
					}
					empty = empty && v == ""
					obj[h] = v
				}
				if empty {
					continue
				}
				rowNo := n + 2 // 1-based, after the header row
				id := "row-" + strconv.Itoa(rowNo)
				if k := cfg["key_column"]; k != "" {
					v, _ := obj[k].(string)
					if v == "" {
						continue // no key yet: picked up once it's filled in
					}
					id = v
				}
				data, _ := json.Marshal(map[string]any{"row_number": rowNo, "values": obj})
				page.Records = append(page.Records, Record{ID: id, Data: data})
			}
			return page, nil
		},
	})

	// ---- Shiprocket: the latest orders ---------------------------------------------
	register(&Model{
		Key: "shiprocket.orders", Provider: "shiprocket", Name: "Orders",
		Description: "New orders and status changes among the latest orders in Shiprocket.",
		Fields: []Field{
			{Key: "pages", Label: "How many recent orders", Options: []string{"100", "300", "500", "1000"}, Default: "300",
				Help: "Orders further back than this aren't watched."},
		},
		eventPrefix: func(map[string]string) string { return "shiprocket.order" },
		fetch: func(ctx context.Context, f Fetcher, cfg map[string]string, cursor string) (Page, error) {
			limit, _ := strconv.Atoi(cfg["pages"])
			pageNo := 1
			if cursor != "" {
				pageNo, _ = strconv.Atoi(cursor)
			}
			status, raw, err := f.Call(ctx, "GET", "", "/orders", url.Values{"per_page": {"100"}, "page": {strconv.Itoa(max(pageNo, 1))}}, nil, nil)
			if err != nil {
				return Page{}, err
			}
			if status != 200 {
				return Page{}, apiError(status, raw)
			}
			var res struct {
				Data []map[string]any `json:"data"`
				Meta struct {
					Pagination struct {
						TotalPages int `json:"total_pages"`
					} `json:"pagination"`
				} `json:"meta"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return Page{}, fmt.Errorf("unexpected Shiprocket answer: %w", err)
			}
			page := Page{}
			for _, o := range res.Data {
				data, _ := json.Marshal(o)
				page.Records = append(page.Records, Record{ID: fmt.Sprint(o["id"]), Data: data})
			}
			// Each run walks pages 1..limit/100, newest first.
			if pageNo*100 < limit && pageNo < res.Meta.Pagination.TotalPages {
				page.Cursor, page.More = strconv.Itoa(pageNo+1), true
			}
			return page, nil
		},
	})
}
