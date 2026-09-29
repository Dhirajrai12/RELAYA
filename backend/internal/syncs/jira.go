package syncs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // JQL dates are in the user's time zone; Windows has no zoneinfo of its own
)

// ---- Jira issues: JQL search sorted by last-updated time ------------------------
//
// JQL compares dates in the Jira user's time zone, to the minute. Each run
// asks from the newest update seen (in that zone, rounded down to the minute);
// issues at that same minute come again and are dropped as unchanged.
// Cursor: "<since unix ms>" between runs, "<since>|<nextPageToken>" within one.

var (
	jiraProjectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)
	jiraFieldName  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,100}$`)
)

const jiraDefaultFields = "summary,status,issuetype,priority,assignee,reporter,labels,created,updated"

func init() {
	register(&Model{
		Key: "jira.issues", Provider: "jira", Name: "Issues",
		Description: "Jira issues created or changed, optionally only some projects or a JQL filter.",
		Incremental: true,
		Fields: []Field{
			{Key: "projects", Label: "Projects", Help: "Comma-separated project keys; empty = every project the user can see.", Placeholder: "OPS, SUPPORT"},
			{Key: "jql", Label: "Extra JQL filter", Help: "Optional, added with AND.", Placeholder: "issuetype = Bug AND priority in (High, Highest)"},
			{Key: "fields", Label: "Fields", Help: "Comma-separated; empty = summary, status, type, priority, people, labels and dates.", Placeholder: "summary,status,customfield_10020"},
		},
		eventPrefix: func(map[string]string) string { return "jira.issue" },
		fetch:       fetchJiraIssues,
	})
}

func jiraJQL(cfg map[string]string, since string) (string, error) {
	var parts []string
	if p := strings.TrimSpace(cfg["projects"]); p != "" {
		var keys []string
		for _, k := range strings.Split(p, ",") {
			k = strings.ToUpper(strings.TrimSpace(k))
			if k == "" {
				continue
			}
			if !jiraProjectKey.MatchString(k) {
				return "", fmt.Errorf("%q is not a Jira project key", k)
			}
			keys = append(keys, k)
		}
		if len(keys) > 0 {
			parts = append(parts, "project in ("+strings.Join(keys, ", ")+")")
		}
	}
	if q := strings.TrimSpace(cfg["jql"]); q != "" {
		if strings.Contains(strings.ToLower(q), "order by") {
			return "", fmt.Errorf("leave ORDER BY out of the JQL filter")
		}
		parts = append(parts, "("+q+")")
	}
	if since != "" {
		parts = append(parts, `updated >= "`+since+`"`)
	}
	return strings.Join(parts, " AND ") + " ORDER BY updated ASC, key ASC", nil
}

func jiraFields(cfg map[string]string) (string, error) {
	list := strings.TrimSpace(cfg["fields"])
	if list == "" {
		return jiraDefaultFields, nil
	}
	var out []string
	hasUpdated := false
	for _, f := range strings.Split(list, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if !jiraFieldName.MatchString(f) {
			return "", fmt.Errorf("%q is not a Jira field name", f)
		}
		hasUpdated = hasUpdated || f == "updated"
		out = append(out, f)
	}
	if !hasUpdated {
		out = append(out, "updated") // the cursor needs it
	}
	return strings.Join(out, ","), nil
}

// jiraZone is the time zone the connected user's JQL dates are read in.
func jiraZone(ctx context.Context, f Fetcher) *time.Location {
	status, raw, err := f.Call(ctx, "GET", "", "/rest/api/3/myself", nil, nil, nil)
	if err != nil || status != 200 {
		return time.UTC
	}
	var me struct {
		TimeZone string `json:"timeZone"`
	}
	if json.Unmarshal(raw, &me) != nil || me.TimeZone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(me.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// jiraTime parses Jira's "2026-09-28T10:15:30.123+0530".
func jiraTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func fetchJiraIssues(ctx context.Context, f Fetcher, cfg map[string]string, cursor string) (Page, error) {
	since, token, _ := strings.Cut(cursor, "|")
	sinceMS, _ := strconv.ParseInt(since, 10, 64)

	jqlSince := ""
	if sinceMS > 0 {
		jqlSince = time.UnixMilli(sinceMS).In(jiraZone(ctx, f)).Format("2006-01-02 15:04")
	}
	jql, err := jiraJQL(cfg, jqlSince)
	if err != nil {
		return Page{}, err
	}
	fields, err := jiraFields(cfg)
	if err != nil {
		return Page{}, err
	}
	q := url.Values{"jql": {jql}, "fields": {fields}, "maxResults": {"100"}}
	if token != "" {
		q.Set("nextPageToken", token)
	}
	status, raw, err := f.Call(ctx, "GET", "", "/rest/api/3/search/jql", q, nil, nil)
	if err != nil {
		return Page{}, err
	}
	if status != 200 {
		return Page{}, apiError(status, raw)
	}
	var res struct {
		Issues []struct {
			ID     string                     `json:"id"`
			Key    string                     `json:"key"`
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"issues"`
		NextPageToken string `json:"nextPageToken"`
		IsLast        *bool  `json:"isLast"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return Page{}, fmt.Errorf("unexpected Jira answer: %w", err)
	}

	page := Page{}
	newest := sinceMS
	for _, is := range res.Issues {
		data, _ := json.Marshal(map[string]any{"id": is.ID, "key": is.Key, "fields": is.Fields})
		page.Records = append(page.Records, Record{ID: is.ID, Data: data})
		var updated string
		if json.Unmarshal(is.Fields["updated"], &updated) == nil {
			if t, ok := jiraTime(updated); ok && t.UnixMilli() > newest {
				newest = t.UnixMilli()
			}
		}
	}
	last := res.NextPageToken == "" || (res.IsLast != nil && *res.IsLast)
	if !last {
		// More of this search: keep asking from the same time, with Jira's page token.
		page.Cursor, page.More = since+"|"+res.NextPageToken, true
		return page, nil
	}
	page.Cursor = strconv.FormatInt(newest, 10)
	if newest == 0 {
		page.Cursor = ""
	}
	return page, nil
}
