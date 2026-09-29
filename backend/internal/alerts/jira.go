package alerts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// JiraConfig is a Jira alert channel's non-secret settings; the API token is kept encrypted apart.
type JiraConfig struct {
	Site      string `json:"site"`       // https://yourco.atlassian.net
	Email     string `json:"email"`      // the Atlassian account the token belongs to
	Project   string `json:"project"`    // project key, e.g. OPS
	IssueType string `json:"issue_type"` // e.g. Task or Bug
}

var jiraProjectRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)

// Normalize checks the settings and reduces Site to scheme://host.
func (c *JiraConfig) Normalize() error {
	u, err := url.Parse(strings.TrimSpace(c.Site))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("site must be your Jira address, e.g. https://yourco.atlassian.net")
	}
	c.Site = "https://" + u.Host
	c.Email = strings.TrimSpace(c.Email)
	if c.Email == "" || !strings.Contains(c.Email, "@") {
		return errors.New("email must be the Atlassian account the API token belongs to")
	}
	c.Project = strings.ToUpper(strings.TrimSpace(c.Project))
	if !jiraProjectRe.MatchString(c.Project) {
		return errors.New("project must be a Jira project key, e.g. OPS")
	}
	c.IssueType = strings.TrimSpace(c.IssueType)
	if c.IssueType == "" {
		c.IssueType = "Task"
	}
	if len(c.IssueType) > 60 {
		return errors.New("issue type is too long")
	}
	return nil
}

// jira calls one Jira Cloud site's REST API (v3) with an email and API token.
type jira struct {
	http  *http.Client
	cfg   JiraConfig
	token string
}

func (j jira) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, j.cfg.Site+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(j.cfg.Email+":"+j.token)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Relaya-Alerts/1.0")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return errors.New("could not reach Jira at " + j.cfg.Site)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return jiraError(resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// jiraError turns Jira's error body into one readable line.
func jiraError(status int, data []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return errors.New("Jira refused the email and API token (HTTP 401); create a new token at id.atlassian.com")
	case http.StatusForbidden:
		return errors.New("the Jira account isn't allowed to do this (HTTP 403); it needs to create and edit issues in the project")
	}
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	var parts []string
	if json.Unmarshal(data, &e) == nil {
		parts = append(parts, e.ErrorMessages...)
		for k, v := range e.Errors {
			parts = append(parts, k+": "+v)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, strings.TrimSpace(string(data)))
	}
	msg := strings.Join(parts, "; ")
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return fmt.Errorf("Jira answered HTTP %d: %s", status, msg)
}

// Check confirms the token works and the project takes the configured issue type.
func (j jira) Check(ctx context.Context) error {
	var me struct {
		AccountID string `json:"accountId"`
	}
	if err := j.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &me); err != nil {
		return err
	}
	var project struct {
		IssueTypes []struct {
			Name string `json:"name"`
		} `json:"issueTypes"`
	}
	if err := j.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(j.cfg.Project), nil, &project); err != nil {
		if isJiraNotFound(err) {
			return fmt.Errorf("no Jira project %s that this account can see", j.cfg.Project)
		}
		return err
	}
	var names []string
	for _, t := range project.IssueTypes {
		if strings.EqualFold(t.Name, j.cfg.IssueType) {
			return nil
		}
		names = append(names, t.Name)
	}
	return fmt.Errorf("project %s has no issue type %q; it has: %s", j.cfg.Project, j.cfg.IssueType, strings.Join(names, ", "))
}

// adf builds an Atlassian Document Format document: one paragraph per line, then a link.
func adf(text, linkText, link string) map[string]any {
	var content []any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			content = append(content, map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": line}}})
		}
	}
	if link != "" {
		content = append(content, map[string]any{"type": "paragraph", "content": []any{map[string]any{
			"type": "text", "text": linkText, "marks": []any{map[string]any{"type": "link", "attrs": map[string]string{"href": link}}},
		}}})
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// Create opens an issue and returns its key, e.g. OPS-12.
func (j jira) Create(ctx context.Context, summary, body, link string) (string, error) {
	if len([]rune(summary)) > 250 {
		summary = string([]rune(summary)[:250]) + "…"
	}
	var out struct {
		Key string `json:"key"`
	}
	err := j.do(ctx, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": map[string]any{
		"project":     map[string]string{"key": j.cfg.Project},
		"issuetype":   map[string]string{"name": j.cfg.IssueType},
		"summary":     summary,
		"description": adf(body, "Open in Relaya", link),
		"labels":      []string{"relaya"},
	}}, &out)
	if err == nil && out.Key == "" {
		err = errors.New("Jira did not return an issue key")
	}
	return out.Key, err
}

// Comment adds a comment to an issue.
func (j jira) Comment(ctx context.Context, key, text, link string) error {
	return j.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", map[string]any{"body": adf(text, "Open in Relaya", link)}, nil)
}

// Close moves an issue to the first transition into a "done" status. It reports false when the
// workflow has none from the issue's current status (the issue is left as it is).
func (j jira) Close(ctx context.Context, key string) (bool, error) {
	var tr struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				StatusCategory struct {
					Key string `json:"key"`
				} `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := j.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", nil, &tr); err != nil {
		return false, err
	}
	for _, t := range tr.Transitions {
		if t.To.StatusCategory.Key == "done" {
			return true, j.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", map[string]any{"transition": map[string]string{"id": t.ID}}, nil)
		}
	}
	return false, nil
}

func isJiraNotFound(err error) bool { return err != nil && strings.Contains(err.Error(), "HTTP 404") }

// sendJira turns an alert into Jira activity and returns the issue key it touched:
//   - a failure opens an issue, or comments on the one still open for the same subject;
//   - a recovery comments on that issue and moves it to Done;
//   - a test only checks the settings, so it leaves no junk issue behind.
func (s *Sender) sendJira(ctx context.Context, q Querier, a queued) (string, error) {
	var cfg JiraConfig
	if err := json.Unmarshal(a.config, &cfg); err != nil {
		return "", err
	}
	token, err := s.Vault.Decrypt(ctx, a.orgID, a.secretEnc)
	if err != nil {
		return "", err
	}
	j := jira{http: s.HTTP, cfg: cfg, token: string(token)}
	if a.kind == Test {
		return "", j.Check(ctx)
	}
	link := s.link(a.link)
	if a.subject == "" {
		return j.Create(ctx, a.title, a.body, link)
	}

	// One open issue per (channel, subject): serialize senders working on the same subject.
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "alert_issue:"+a.channelID+":"+a.subject); err != nil {
		return "", err
	}
	var issueID int64
	var key string
	err = q.QueryRow(ctx, `SELECT id, issue_key FROM alert_issues WHERE channel_id = $1 AND subject = $2 AND closed_at IS NULL`,
		a.channelID, a.subject).Scan(&issueID, &key)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	open := err == nil
	markClosed := func() error {
		_, err := q.Exec(ctx, `UPDATE alert_issues SET closed_at = now() WHERE id = $1`, issueID)
		return err
	}

	if Recovers(a.kind) {
		if !open {
			return "", nil // the failure never made an issue here (channel added later, or not subscribed)
		}
		err := j.Comment(ctx, key, "Recovered: "+a.title+"\n"+a.body, link)
		if err == nil {
			_, err = j.Close(ctx, key)
		}
		if err != nil && !isJiraNotFound(err) { // deleted in Jira meanwhile: nothing left to close
			return key, err
		}
		return key, markClosed()
	}

	if open {
		err := j.Comment(ctx, key, "Happened again: "+a.title+"\n"+a.body, link)
		if !isJiraNotFound(err) {
			return key, err
		}
		if err := markClosed(); err != nil { // the issue was deleted in Jira: open a new one
			return "", err
		}
	}
	key, err = j.Create(ctx, a.title, a.body, link)
	if err != nil {
		return "", err
	}
	_, err = q.Exec(ctx, `INSERT INTO alert_issues (channel_id, subject, issue_key) VALUES ($1, $2, $3)`, a.channelID, a.subject, key)
	return key, err
}

// IssueURL is the browser link to an issue.
func (c JiraConfig) IssueURL(key string) string { return c.Site + "/browse/" + key }
