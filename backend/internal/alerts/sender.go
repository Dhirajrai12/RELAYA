package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/db"
	"relaya/internal/realtime"
	"relaya/internal/vault"
)

// SMTP settings for email channels. Email is disabled when Host is empty.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // e.g. "Relaya Alerts <alerts@example.com>"
}

func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

// Sender delivers queued alerts. It runs in the worker.
type Sender struct {
	Pool         *pgxpool.Pool
	Vault        vault.Vault
	HTTP         *http.Client // SSRF-safe client for webhook channels
	SMTP         SMTP
	DashboardURL string // prefix for alert links, e.g. https://app.example.com
	// Sign returns the Relaya-Signature header value for webhook channels
	// (delivery.Sign; injected to avoid an import cycle with the delivery worker).
	Sign func(secret []byte, t time.Time, body []byte) string
}

const maxAttempts = 4

// errNoSMTP is permanent: retrying can't help until an admin configures email.
var errNoSMTP = errors.New("email is not configured on this server (SMTP settings missing)")

var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}

// Run sends alerts until ctx is done.
func (s *Sender) Run(ctx context.Context) {
	wake := make(chan struct{}, 1)
	go db.Listen(ctx, s.Pool, Channel, wake)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if _, err := s.RunOnce(ctx, 50); err != nil && ctx.Err() == nil {
			slog.Error("send alerts", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-t.C:
		}
	}
}

type queued struct {
	id                int64
	orgID, kind       string
	title, body, link string
	subject           string
	attempts          int
	channelID         string
	chType, target    string
	urlEnc, secretEnc []byte
	config            []byte
}

const queuedSelect = `
	SELECT al.id, al.org_id, al.kind, al.title, al.body, al.link, al.subject, al.attempts,
	       c.id, c.type, c.target, c.url_enc, c.secret_enc, c.config
	FROM alerts al JOIN alert_channels c ON c.id = al.channel_id`

func scanQueued(row pgx.Row) (queued, error) {
	var a queued
	err := row.Scan(&a.id, &a.orgID, &a.kind, &a.title, &a.body, &a.link, &a.subject, &a.attempts,
		&a.channelID, &a.chType, &a.target, &a.urlEnc, &a.secretEnc, &a.config)
	return a, err
}

// RunOnce sends up to max due alerts and returns how many it attempted.
func (s *Sender) RunOnce(ctx context.Context, max int) (int, error) {
	n := 0
	for n < max {
		done, err := s.sendNext(ctx)
		if err != nil || !done {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Sender) sendNext(ctx context.Context) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	a, err := scanQueued(tx.QueryRow(ctx, queuedSelect+`
		WHERE al.status = 'pending' AND al.next_attempt_at <= now()
		ORDER BY al.next_attempt_at LIMIT 1 FOR UPDATE OF al SKIP LOCKED`))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	ref, sendErr := s.send(ctx, tx, a)
	a.attempts++
	switch {
	case sendErr == nil:
		_, err = tx.Exec(ctx, `UPDATE alerts SET status = 'sent', attempts = $2, sent_at = now(), last_error = '', external_ref = $3 WHERE id = $1`,
			a.id, a.attempts, ref)
	case a.attempts >= maxAttempts || errors.Is(sendErr, errNoSMTP):
		_, err = tx.Exec(ctx, `UPDATE alerts SET status = 'failed', attempts = $2, last_error = $3 WHERE id = $1`, a.id, a.attempts, sendErr.Error())
	default:
		delay := retryDelays[min(a.attempts-1, len(retryDelays)-1)]
		_, err = tx.Exec(ctx, `UPDATE alerts SET attempts = $2, last_error = $3, next_attempt_at = now() + $4::interval WHERE id = $1`,
			a.id, a.attempts, sendErr.Error(), fmt.Sprintf("%d seconds", int(delay.Seconds())))
	}
	if err != nil {
		return false, err
	}
	if err := notifyLog(ctx, tx, a.orgID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// SendNow sends one queued alert immediately and reports the result (used by "Send test").
func (s *Sender) SendNow(ctx context.Context, alertID int64) error {
	a, err := scanQueued(s.Pool.QueryRow(ctx, queuedSelect+` WHERE al.id = $1`, alertID))
	if err != nil {
		return err
	}
	ref, sendErr := s.send(ctx, s.Pool, a)
	status, msg := "sent", ""
	if sendErr != nil {
		status, msg = "failed", sendErr.Error()
	}
	_, err = s.Pool.Exec(ctx, `
		UPDATE alerts SET status = $2, attempts = 1, last_error = $3, external_ref = $4, sent_at = CASE WHEN $2 = 'sent' THEN now() END
		WHERE id = $1`, alertID, status, msg, ref)
	if err == nil {
		err = notifyLog(ctx, s.Pool, a.orgID)
	}
	if err != nil {
		return err
	}
	return sendErr
}

// notifyLog tells open dashboards that the alert log changed.
func notifyLog(ctx context.Context, q realtime.Execer, orgID string) error {
	return realtime.Notify(ctx, q, realtime.Message{Type: "change", OrgID: orgID, Action: "alert.sent"})
}

func (s *Sender) link(path string) string {
	if path == "" {
		return strings.TrimRight(s.DashboardURL, "/")
	}
	return strings.TrimRight(s.DashboardURL, "/") + path
}

// send delivers a to its channel and returns a reference to what it created there (a Jira
// issue key), if anything. q is the transaction the alert row is locked in.
func (s *Sender) send(ctx context.Context, q Querier, a queued) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch a.chType {
	case "slack":
		url, err := s.Vault.Decrypt(ctx, a.orgID, a.urlEnc)
		if err != nil {
			return "", err
		}
		return "", s.postJSON(ctx, string(url), SlackPayload(a.title, a.body, s.link(a.link)), nil)
	case "webhook":
		url, err := s.Vault.Decrypt(ctx, a.orgID, a.urlEnc)
		if err != nil {
			return "", err
		}
		secret, err := s.Vault.Decrypt(ctx, a.orgID, a.secretEnc)
		if err != nil {
			return "", err
		}
		body, _ := json.Marshal(map[string]any{
			"type": a.kind, "title": a.title, "body": a.body, "link": s.link(a.link),
			"org_id": a.orgID, "alert_id": a.id, "sent_at": time.Now().UTC(),
		})
		return "", s.postJSON(ctx, string(url), body, secret)
	case "email":
		return "", s.sendEmail(a.target, a.title, a.body+"\n\nOpen in Relaya: "+s.link(a.link))
	case "jira":
		return s.sendJira(ctx, q, a)
	}
	return "", fmt.Errorf("unknown channel type %q", a.chType)
}

// CheckJira confirms Jira settings and a token work (used when a channel is created).
func (s *Sender) CheckJira(ctx context.Context, cfg JiraConfig, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return jira{http: s.HTTP, cfg: cfg, token: token}.Check(ctx)
}

// SlackPayload builds an Incoming Webhook message: bold title, body, link.
func SlackPayload(title, body, link string) []byte {
	esc := func(s string) string { // Slack mrkdwn escaping
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
	}
	text := fmt.Sprintf("*%s*\n%s\n<%s|Open in Relaya>", esc(title), esc(body), link)
	b, _ := json.Marshal(map[string]any{
		"text": title, // notification preview
		"blocks": []any{
			map[string]any{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": text}},
		},
	})
	return b
}

func (s *Sender) postJSON(ctx context.Context, url string, body []byte, secret []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Relaya-Alerts/1.0")
	if secret != nil {
		req.Header.Set("Relaya-Signature", s.Sign(secret, time.Now(), body))
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return errors.New("could not reach the channel URL (unreachable, or a private address that is not allowed)")
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("channel answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (s *Sender) sendEmail(to, subject, body string) error {
	if !s.SMTP.Enabled() {
		return errNoSMTP
	}
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(subject, "\r\n") {
		return errors.New("invalid email header value")
	}
	msg := "From: " + s.SMTP.From + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: [Relaya] " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"

	addr := net.JoinHostPort(s.SMTP.Host, fmt.Sprint(s.SMTP.Port))
	from := s.SMTP.From
	if i := strings.LastIndex(from, "<"); i >= 0 {
		from = strings.TrimSuffix(from[i+1:], ">")
	}
	var auth smtp.Auth
	if s.SMTP.Username != "" {
		auth = smtp.PlainAuth("", s.SMTP.Username, s.SMTP.Password, s.SMTP.Host)
	}
	if s.SMTP.Port == 465 { // implicit TLS
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: s.SMTP.Host})
		if err != nil {
			return fmt.Errorf("smtp: %w", err)
		}
		c, err := smtp.NewClient(conn, s.SMTP.Host)
		if err != nil {
			return err
		}
		defer c.Close()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
		if err := c.Mail(from); err != nil {
			return err
		}
		if err := c.Rcpt(to); err != nil {
			return err
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(msg)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return c.Quit()
	}
	// 587/25: smtp.SendMail upgrades with STARTTLS when offered.
	if err := smtp.SendMail(addr, auth, from, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return nil
}
