package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/alerts"
	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
)

type alertChannelView struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Target    string    `json:"target"` // email address or masked URL
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	// Last 7 days.
	Sent   int `json:"sent_7d"`
	Failed int `json:"failed_7d"`
}

const alertChannelSelect = `
	SELECT c.id, c.type, c.name, c.target, c.events, c.enabled, c.created_at,
	       (SELECT count(*) FROM alerts a WHERE a.channel_id = c.id AND a.status = 'sent' AND a.created_at > now() - interval '7 days'),
	       (SELECT count(*) FROM alerts a WHERE a.channel_id = c.id AND a.status = 'failed' AND a.created_at > now() - interval '7 days')
	FROM alert_channels c`

// rowsQuerier is satisfied by both the pool and a transaction.
type rowsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *Server) getAlertChannel(ctx context.Context, q rowsQuerier, orgID, id string) (alertChannelView, error) {
	rows, err := q.Query(ctx, alertChannelSelect+` WHERE c.id = $1 AND c.org_id = $2`, id, orgID)
	if err != nil {
		return alertChannelView{}, err
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[alertChannelView])
	return v, notFoundIfNoRows(err)
}

// alertSettings tells the dashboard which kinds exist and whether email works here.
func (s *Server) alertSettings(w http.ResponseWriter, r *http.Request) error {
	if _, _, _, err := s.orgAccess(r, auth.RoleMember); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"kinds":         alerts.Kinds,
		"email_enabled": s.AlertSender != nil && s.AlertSender.SMTP.Enabled(),
	})
	return nil
}

func (s *Server) listAlertChannels(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), alertChannelSelect+` WHERE c.org_id = $1 ORDER BY c.created_at`, orgID)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[alertChannelView])
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func validEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return nil, httpx.BadRequest("choose at least one alert type")
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range events {
		ok := false
		for _, k := range alerts.Kinds {
			ok = ok || k == e
		}
		if !ok {
			return nil, httpx.BadRequest("unknown alert type %q", e)
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// maskURL shows host and the last 4 characters: hooks.slack.com/…/AbCd
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "…"
	}
	tail := raw
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return u.Host + "/…" + tail
}

func (s *Server) createAlertChannel(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Type   string   `json:"type"`
		Name   string   `json:"name"`
		URL    string   `json:"url"`
		Email  string   `json:"email"`
		Events []string `json:"events"`
		Jira   *struct {
			alerts.JiraConfig
			APIToken string `json:"api_token"`
		} `json:"jira"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	name, err := requireName(in.Name, "name", 100)
	if err != nil {
		return err
	}
	events, err := validEvents(in.Events)
	if err != nil {
		return err
	}

	var target string
	var urlEnc, secretEnc []byte
	var secret string
	config := []byte("{}")
	switch in.Type {
	case "jira":
		if in.Jira == nil {
			return httpx.BadRequest("jira settings are required: site, email, api_token, project")
		}
		cfg := in.Jira.JiraConfig
		if err := cfg.Normalize(); err != nil {
			return httpx.BadRequest("%v", err)
		}
		if err := s.DeliveryPolicy.ValidateURL(cfg.Site); err != nil {
			return httpx.BadRequest("site: %v", err)
		}
		token := strings.TrimSpace(in.Jira.APIToken)
		if token == "" || len(token) > 1000 {
			return httpx.BadRequest("api_token is required (create one at id.atlassian.com → Security → API tokens)")
		}
		// Check with Jira now, so a typo shows up here rather than in a failed alert later.
		if err := s.AlertSender.CheckJira(r.Context(), cfg, token); err != nil {
			return httpx.BadRequest("%v", err)
		}
		target = cfg.Project + " · " + strings.TrimPrefix(cfg.Site, "https://")
		if secretEnc, err = s.Vault.Encrypt(r.Context(), orgID, []byte(token)); err != nil {
			return err
		}
		config, _ = json.Marshal(cfg)
	case "slack":
		u := strings.TrimSpace(in.URL)
		if !strings.HasPrefix(u, "https://hooks.slack.com/") || len(u) > 500 {
			return httpx.BadRequest("paste a Slack Incoming Webhook URL (it starts with https://hooks.slack.com/)")
		}
		target = maskURL(u)
		if urlEnc, err = s.Vault.Encrypt(r.Context(), orgID, []byte(u)); err != nil {
			return err
		}
	case "webhook":
		u := strings.TrimSpace(in.URL)
		if err := s.DeliveryPolicy.ValidateURL(u); err != nil {
			return httpx.BadRequest("url: %v", err)
		}
		target = maskURL(u)
		if urlEnc, err = s.Vault.Encrypt(r.Context(), orgID, []byte(u)); err != nil {
			return err
		}
		secret = delivery.NewSecret()
		if secretEnc, err = s.Vault.Encrypt(r.Context(), orgID, []byte(secret)); err != nil {
			return err
		}
	case "email":
		addr, err := mail.ParseAddress(strings.TrimSpace(in.Email))
		if err != nil {
			return httpx.BadRequest("a valid email address is required")
		}
		// Only members of this organization can receive alert emails.
		var isMember bool
		if err := s.Pool.QueryRow(r.Context(), `
			SELECT EXISTS (SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
			               WHERE m.org_id = $1 AND lower(u.email) = lower($2))`, orgID, addr.Address).Scan(&isMember); err != nil {
			return err
		}
		if !isMember {
			return httpx.BadRequest("email alerts can only go to members of this organization")
		}
		target = addr.Address
	default:
		return httpx.BadRequest("type must be slack, email, webhook or jira")
	}

	var v alertChannelView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO alert_channels (org_id, type, name, target, url_enc, secret_enc, events, config)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			orgID, in.Type, name, target, urlEnc, secretEnc, events, config).Scan(&id); err != nil {
			return err
		}
		if v, err = s.getAlertChannel(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "alert_channel.create", "alert_channel", id)
		e.Metadata = map[string]any{"type": in.Type, "target": target, "events": events}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	out := map[string]any{"channel": v}
	if secret != "" {
		out["signing_secret"] = secret // webhook channels: shown once
	}
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

func (s *Server) updateAlertChannel(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "channel")
	if err != nil {
		return err
	}
	var in struct {
		Name    *string  `json:"name"`
		Events  []string `json:"events"`
		Enabled *bool    `json:"enabled"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	sets, args := []string{"updated_at = now()"}, []any{id, orgID}
	if in.Name != nil {
		name, err := requireName(*in.Name, "name", 100)
		if err != nil {
			return err
		}
		args = append(args, name)
		sets = append(sets, "name = $"+itoa(len(args)))
	}
	if in.Events != nil {
		events, err := validEvents(in.Events)
		if err != nil {
			return err
		}
		args = append(args, events)
		sets = append(sets, "events = $"+itoa(len(args)))
	}
	if in.Enabled != nil {
		args = append(args, *in.Enabled)
		sets = append(sets, "enabled = $"+itoa(len(args)))
	}
	var v alertChannelView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE alert_channels SET `+strings.Join(sets, ", ")+` WHERE id = $1 AND org_id = $2`, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		if v, err = s.getAlertChannel(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "alert_channel.update", "alert_channel", id))
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteAlertChannel(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "channel")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM alert_channels WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "alert_channel.delete", "alert_channel", id))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// testAlertChannel sends a test alert now and reports whether the channel accepted it.
func (s *Server) testAlertChannel(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "channel")
	if err != nil {
		return err
	}
	if _, err := s.getAlertChannel(r.Context(), s.Pool, orgID, id); err != nil {
		return err
	}
	alertID, err := alerts.EnqueueTo(r.Context(), s.Pool, orgID, id, alerts.Alert{
		Kind:  alerts.Test,
		Title: "Test alert from Relaya",
		Body:  "If you can read this, alerts to this channel work. Incidents, failing deliveries and signature problems will arrive here.",
		Link:  "/settings/alerts",
	})
	if err != nil {
		return err
	}
	sendErr := s.AlertSender.SendNow(r.Context(), alertID)
	e := audit.ByPrincipal(p, orgID, "alert_channel.test", "alert_channel", id)
	out := map[string]any{"ok": sendErr == nil, "error": ""}
	if sendErr != nil {
		out["error"] = sendErr.Error()
		e.Result = "failure"
	}
	if err := audit.Record(r.Context(), s.Pool, e); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type alertLogView struct {
	ID          int64      `json:"id"`
	ChannelID   string     `json:"channel_id"`
	ChannelName string     `json:"channel_name"`
	ChannelType string     `json:"channel_type"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
	SentAt      *time.Time `json:"sent_at"`
	// What the channel made of it, e.g. a Jira issue key, and its link.
	ExternalRef string `json:"external_ref"`
	ExternalURL string `json:"external_url"`
	config      []byte
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT a.id, a.channel_id, c.name, c.type, a.kind, a.title, a.status, a.attempts, a.last_error, a.created_at, a.sent_at,
		       a.external_ref, c.config
		FROM alerts a JOIN alert_channels c ON c.id = a.channel_id
		WHERE a.org_id = $1 ORDER BY a.created_at DESC, a.id DESC LIMIT 100`, orgID)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (alertLogView, error) {
		var v alertLogView
		err := row.Scan(&v.ID, &v.ChannelID, &v.ChannelName, &v.ChannelType, &v.Kind, &v.Title, &v.Status, &v.Attempts,
			&v.LastError, &v.CreatedAt, &v.SentAt, &v.ExternalRef, &v.config)
		if err == nil && v.ChannelType == "jira" && v.ExternalRef != "" {
			var cfg alerts.JiraConfig
			if json.Unmarshal(v.config, &cfg) == nil {
				v.ExternalURL = cfg.IssueURL(v.ExternalRef)
			}
		}
		return v, err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}
