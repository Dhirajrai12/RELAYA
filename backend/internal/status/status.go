// Package status powers the public status page: the worker records whether
// each customer-facing component works, once a minute, and the API serves the
// current state and 90 days of daily uptime. Only up/down and percentages are
// exposed, never internal numbers.
package status

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Component is one row on the status page.
type Component struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Help string `json:"help"`
}

// Components, in display order.
var Components = []Component{
	{"ingest", "Webhook ingestion", "Receiving events from providers at your webhook URLs."},
	{"delivery", "Event delivery", "Forwarding events to your endpoints, with retries."},
	{"contracts", "Contract checks", "Checking events against their contracts and opening incidents."},
	{"alerts", "Alerts", "Sending incident and failure alerts to Slack, email and webhooks."},
	{"api", "Dashboard and API", "The dashboard, the REST API and live updates."},
}

// Thresholds: a component is down when work waits longer than this.
const (
	deliveryLagLimit = 5 * time.Minute
	contractLagLimit = 10 * time.Minute
	alertAgeLimit    = 15 * time.Minute
	historyDays      = 90
)

// Prober checks the components once. APIURL and IngestURL are the services'
// local addresses (health endpoints are probed over HTTP).
type Prober struct {
	Pool      *pgxpool.Pool
	APIURL    string
	IngestURL string
	HTTP      *http.Client
}

// LocalURL turns a listen address (":8080", "127.0.0.1:8080") into a URL.
func LocalURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return "http://" + addr
}

func (p *Prober) httpOK(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	res, err := p.HTTP.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// Probe returns each component's state now. The worker runs this, so the
// worker-driven components are healthy only if the worker is alive to probe.
func (p *Prober) Probe(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{
		"ingest": p.httpOK(ctx, p.IngestURL+"/healthz"),
		"api":    p.httpOK(ctx, p.APIURL+"/readyz"),
	}
	var deliveryLag, contractLag, alertAge float64
	err := p.Pool.QueryRow(ctx, `
		SELECT coalesce((SELECT extract(epoch FROM now() - min(next_attempt_at)) FROM deliveries
		                 WHERE status IN ('pending', 'retrying') AND next_attempt_at <= now()), 0),
		       coalesce((SELECT extract(epoch FROM now() - min(enqueued_at)) FROM contract_queue), 0),
		       coalesce((SELECT extract(epoch FROM now() - min(created_at)) FROM alerts
		                 WHERE status = 'pending' AND next_attempt_at <= now()), 0)`).
		Scan(&deliveryLag, &contractLag, &alertAge)
	if err != nil {
		// No database: everything that depends on it is down.
		out["delivery"], out["contracts"], out["alerts"] = false, false, false
		return out, err
	}
	out["delivery"] = deliveryLag < deliveryLagLimit.Seconds()
	out["contracts"] = contractLag < contractLagLimit.Seconds()
	out["alerts"] = alertAge < alertAgeLimit.Seconds()
	return out, nil
}

// Record probes and stores one sample per component for the current minute.
func (p *Prober) Record(ctx context.Context, now time.Time) error {
	states, probeErr := p.Probe(ctx)
	at := now.UTC().Truncate(time.Minute)
	for _, c := range Components {
		if _, err := p.Pool.Exec(ctx, `
			INSERT INTO status_samples (component, at, ok) VALUES ($1, $2, $3)
			ON CONFLICT (component, at) DO UPDATE SET ok = EXCLUDED.ok`, c.ID, at, states[c.ID]); err != nil {
			return err
		}
	}
	if _, err := p.Pool.Exec(ctx, `DELETE FROM status_samples WHERE at < $1`, at.AddDate(0, 0, -historyDays-1)); err != nil {
		return err
	}
	return probeErr
}

// Run records a sample every minute until ctx is done.
func (p *Prober) Run(ctx context.Context) {
	tick := func() {
		if err := p.Record(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.Warn("status probe", "err", err)
		}
	}
	tick()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

// ---- the public view --------------------------------------------------------------

// ComponentStatus is a component's current state on the page.
type ComponentStatus struct {
	Component
	Status string `json:"status"` // operational, down, unknown
}

// Day is one day of uptime per component (percent of checked minutes that
// were fine; null when nothing was checked that day).
type Day struct {
	Date   string              `json:"date"` // YYYY-MM-DD, UTC
	Uptime map[string]*float64 `json:"uptime"`
}

// Page is what GET /v1/status returns.
type Page struct {
	Status     string            `json:"status"` // operational, degraded, outage
	Components []ComponentStatus `json:"components"`
	Days       []Day             `json:"days"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Service builds the page from the samples, cached briefly (the endpoint is public).
type Service struct {
	Pool *pgxpool.Pool
	TTL  time.Duration

	mu      sync.Mutex
	cached  *Page
	expires time.Time
}

// staleAfter: without a sample this recent, the worker isn't checking, so the
// components it runs are treated as down.
const staleAfter = 3 * time.Minute

func (s *Service) Page(ctx context.Context) (*Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.cached != nil && now.Before(s.expires) {
		return s.cached, nil
	}
	p, err := s.build(ctx, now)
	if err != nil {
		return nil, err
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	s.cached, s.expires = p, now.Add(ttl)
	return p, nil
}

func (s *Service) build(ctx context.Context, now time.Time) (*Page, error) {
	p := &Page{UpdatedAt: now.UTC()}

	// Current: the latest sample per component, if fresh.
	latest := map[string]bool{}
	fresh := map[string]bool{}
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (component) component, at, ok FROM status_samples
		WHERE at > now() - interval '1 hour' ORDER BY component, at DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var at time.Time
		var ok bool
		if err := rows.Scan(&c, &at, &ok); err != nil {
			rows.Close()
			return nil, err
		}
		latest[c] = ok
		fresh[c] = now.Sub(at) < staleAfter
	}
	rows.Close()
	down := 0
	anyFresh := false
	for _, c := range Components {
		st := "unknown"
		switch {
		case fresh[c.ID] && latest[c.ID]:
			st = "operational"
			anyFresh = true
		case fresh[c.ID]:
			st = "down"
			anyFresh = true
		case c.ID == "api":
			st = "operational" // it is answering this request
		}
		if st != "operational" {
			down++
		}
		p.Components = append(p.Components, ComponentStatus{Component: c, Status: st})
	}
	if !anyFresh {
		// The worker has stopped checking: the parts it runs are not working.
		for i := range p.Components {
			if p.Components[i].ID != "api" && p.Components[i].ID != "ingest" {
				p.Components[i].Status = "down"
			}
		}
	}
	switch {
	case down == 0:
		p.Status = "operational"
	case down == len(Components):
		p.Status = "outage"
	default:
		p.Status = "degraded"
	}

	// History: uptime per component per UTC day.
	start := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(historyDays - 1))
	byDay := map[string]map[string]*float64{}
	rows, err = s.Pool.Query(ctx, `
		SELECT to_char(date_trunc('day', at AT TIME ZONE 'UTC'), 'YYYY-MM-DD'), component,
		       100.0 * count(*) FILTER (WHERE ok) / count(*)
		FROM status_samples WHERE at >= $1 GROUP BY 1, 2`, start)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day, c string
		var pct float64
		if err := rows.Scan(&day, &c, &pct); err != nil {
			rows.Close()
			return nil, err
		}
		if byDay[day] == nil {
			byDay[day] = map[string]*float64{}
		}
		v := pct
		byDay[day][c] = &v
	}
	rows.Close()
	for i := 0; i < historyDays; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		up := map[string]*float64{}
		for _, c := range Components {
			up[c.ID] = byDay[d][c.ID]
		}
		p.Days = append(p.Days, Day{Date: d, Uptime: up})
	}
	return p, nil
}

func (p *Page) String() string { return fmt.Sprintf("%s (%d components)", p.Status, len(p.Components)) }
