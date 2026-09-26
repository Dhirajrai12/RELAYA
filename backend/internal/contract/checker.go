package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/alerts"
	"relaya/internal/db"
	"relaya/internal/realtime"
	"relaya/internal/repair"
)

// QueueChannel is notified by ingest when events are queued for checking.
const QueueChannel = "relaya_contracts"

// Checker learns contracts and checks events. It runs in the worker process,
// off the ingest path, one event per transaction (safe with several workers).
type Checker struct {
	Pool        *pgxpool.Pool
	MinSamples  int           // propose after this many samples…
	LearnWindow time.Duration // …or after this long with at least 3 samples
	PollEvery   time.Duration
	Workers     int // parallel checkers (default 2)
	// Close incidents that stopped this long ago and were followed by a clean event (0 = off).
	AutoResolveAfter time.Duration

	mu       sync.Mutex
	versions map[string]*version // "contractID:version" -> immutable version
}

type version struct {
	schema   *Schema
	critical map[string]bool
}

func (c *Checker) defaults() {
	if c.MinSamples <= 0 {
		c.MinSamples = 20
	}
	if c.LearnWindow <= 0 {
		c.LearnWindow = 24 * time.Hour
	}
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.PollEvery <= 0 {
		c.PollEvery = time.Second
	}
}

// Run processes the queue until ctx is done.
func (c *Checker) Run(ctx context.Context) {
	c.defaults()
	// Several checkers drain the queue in parallel: SKIP LOCKED hands each a
	// different event, and the contract row lock keeps learning per contract serial.
	wakes := make([]chan struct{}, c.Workers)
	for i := range wakes {
		wakes[i] = make(chan struct{}, 1)
		go c.drain(ctx, wakes[i])
	}
	wake := make(chan struct{}, 1)
	go db.Listen(ctx, c.Pool, QueueChannel, wake)
	sweep := time.NewTicker(time.Minute)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			for _, w := range wakes {
				select {
				case w <- struct{}{}:
				default:
				}
			}
		case <-sweep.C:
			if n, err := c.AutoResolve(ctx, c.AutoResolveAfter); err != nil && ctx.Err() == nil {
				slog.Error("auto-resolve incidents", "err", err)
			} else if n > 0 {
				slog.Info("auto-resolved incidents", "count", n)
			}
		}
	}
}

// drain checks queued events until the queue is empty, then waits for a wake-up or the poll tick.
func (c *Checker) drain(ctx context.Context, wake <-chan struct{}) {
	t := time.NewTicker(c.PollEvery)
	defer t.Stop()
	for {
		n, err := c.RunOnce(ctx, 100)
		if err != nil && ctx.Err() == nil {
			slog.Error("contract check", "err", err)
		}
		if n == 100 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-t.C:
		}
	}
}

// RunOnce checks up to max queued events and returns how many it processed.
func (c *Checker) RunOnce(ctx context.Context, max int) (int, error) {
	c.defaults()
	n := 0
	for n < max {
		got, err := c.processBatch(ctx, min(batchSize, max-n))
		n += got
		if err != nil || got == 0 {
			return n, err
		}
	}
	return n, nil
}

// batchSize is how many queued events one transaction claims. Events of the
// same contract share one contract lock, read and write: most of the cost.
const batchSize = 100

type queued struct {
	EventID    string
	ReceivedAt time.Time
	OrgID      string
	WebhookID  string
	EventType  string
}

type item struct {
	q       queued
	payload []byte
	found   bool // false if the event was deleted meanwhile
}

// processBatch claims up to max queued events and checks them, grouped by contract.
func (c *Checker) processBatch(ctx context.Context, max int) (n int, err error) {
	err = pgx.BeginFunc(ctx, c.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT event_id, event_received_at, org_id, webhook_id, event_type FROM contract_queue
			ORDER BY enqueued_at LIMIT $1 FOR UPDATE SKIP LOCKED`, max)
		if err != nil {
			return err
		}
		qs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[queued])
		if err != nil || len(qs) == 0 {
			return err
		}
		n = len(qs)

		ids := make([]string, len(qs))
		ats := make([]time.Time, len(qs))
		for i, q := range qs {
			ids[i], ats[i] = q.EventID, q.ReceivedAt
		}
		payloads := map[string][]byte{}
		prows, err := tx.Query(ctx, `
			SELECT e.id, e.payload FROM events e
			JOIN unnest($1::uuid[], $2::timestamptz[]) AS k(id, at) ON e.id = k.id AND e.received_at = k.at`, ids, ats)
		if err != nil {
			return err
		}
		for prows.Next() {
			var id string
			var p []byte
			if err := prows.Scan(&id, &p); err != nil {
				prows.Close()
				return err
			}
			payloads[id] = p
		}
		prows.Close()
		if err := prows.Err(); err != nil {
			return err
		}

		// Group by contract (webhook + event type), keeping queue order within a group.
		type group struct {
			key   string
			items []item
		}
		var groups []*group
		byKey := map[string]*group{}
		for _, q := range qs {
			k := q.WebhookID + "\x00" + q.EventType
			g := byKey[k]
			if g == nil {
				g = &group{key: k}
				byKey[k] = g
				groups = append(groups, g)
			}
			p, ok := payloads[q.EventID]
			g.items = append(g.items, item{q: q, payload: p, found: ok})
		}
		// Lock contracts in one fixed order so parallel checkers can't deadlock.
		sort.Slice(groups, func(i, j int) bool { return groups[i].key < groups[j].key })

		for _, g := range groups {
			statuses, contractID, err := c.checkGroup(ctx, tx, g.items)
			if err != nil {
				return err
			}
			for i, it := range g.items {
				if it.found {
					if _, err := tx.Exec(ctx, `UPDATE events SET contract_status = $3 WHERE id = $1 AND received_at = $2`,
						it.q.EventID, it.q.ReceivedAt, statuses[i]); err != nil {
						return err
					}
				}
				if err := realtime.Notify(ctx, tx, realtime.Message{
					Type: "contract", OrgID: it.q.OrgID, WebhookID: it.q.WebhookID, EventID: it.q.EventID, Status: statuses[i], TargetID: contractID,
				}); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM contract_queue WHERE event_id = ANY($1)`, ids)
		return err
	})
	return n, err
}

// checkGroup learns from and checks the events of one contract (same webhook
// and event type) under a single lock, read and write of the contract. It
// returns each event's contract status, and the contract's ID.
func (c *Checker) checkGroup(ctx context.Context, tx pgx.Tx, items []item) ([]string, string, error) {
	statuses := make([]string, len(items))
	obs := make([]Obs, len(items))
	valid := make([]bool, len(items))
	anyValid := false
	for i, it := range items {
		statuses[i] = "none"
		if !it.found {
			continue
		}
		if obs[i], valid[i] = Observe(it.payload); valid[i] {
			anyValid = true
		}
	}
	if !anyValid {
		return statuses, "", nil
	}
	q0 := items[0].q

	if _, err := tx.Exec(ctx, `
		INSERT INTO contracts (org_id, webhook_id, event_type) VALUES ($1, $2, $3)
		ON CONFLICT (webhook_id, event_type) DO NOTHING`, q0.OrgID, q0.WebhookID, q0.EventType); err != nil {
		return nil, "", err
	}
	var (
		id, status         string
		observedRaw, nfRaw []byte
		activeVersion      *int
		firstSeen          time.Time
	)
	// FOR UPDATE serializes learning per contract across workers.
	if err := tx.QueryRow(ctx, `
		SELECT id, status, observed, new_fields, active_version, first_seen_at FROM contracts
		WHERE webhook_id = $1 AND event_type = $2 FOR UPDATE`, q0.WebhookID, q0.EventType).
		Scan(&id, &status, &observedRaw, &nfRaw, &activeVersion, &firstSeen); err != nil {
		return nil, "", err
	}

	observed := NewSchema()
	if err := json.Unmarshal(observedRaw, observed); err != nil {
		return nil, "", err
	}
	if observed.Fields == nil {
		observed.Fields = map[string]*Field{}
	}
	newFields := map[string]map[string]any{}
	_ = json.Unmarshal(nfRaw, &newFields)

	var v *version
	if status == "active" && activeVersion != nil {
		var err error
		if v, err = c.version(ctx, tx, id, *activeVersion); err != nil {
			return nil, "", err
		}
	}
	var rules []repair.Rule
	rulesLoaded := false

	for i, it := range items {
		if !valid[i] {
			continue
		}
		o := obs[i]
		observed.Merge(o)
		if status == "learning" &&
			(observed.Samples >= c.MinSamples || (time.Since(firstSeen) >= c.LearnWindow && observed.Samples >= 3)) {
			status = "proposed"
		}
		statuses[i] = "learning"
		if v == nil {
			continue
		}

		findings, sev := Check(v.schema, v.critical, o)
		statuses[i] = sev.String()
		// Findings a repair rule fixes are kept for visibility but open no incident.
		var fixed map[string]bool
		if sev >= Suspicious {
			if !rulesLoaded {
				var err error
				if rules, err = repair.Load(ctx, tx, q0.WebhookID, q0.EventType); err != nil {
					return nil, "", err
				}
				rulesLoaded = true
			}
			var repairedSev Severity
			fixed, repairedSev = repairedFindings(it.q, it.payload, v, findings, rules)
			if len(fixed) > 0 {
				statuses[i] = StatusRepaired
				if repairedSev >= Suspicious {
					statuses[i] = repairedSev.String()
				}
			}
		}
		for _, f := range findings {
			if fixed[f.Kind+"|"+f.Path] {
				if err := c.recordRepaired(ctx, tx, it.q, id, *activeVersion, f); err != nil {
					return nil, "", err
				}
				continue
			}
			switch f.Severity {
			case Compatible:
				nf := newFields[f.Path]
				if nf == nil {
					nf = map[string]any{"first_seen": time.Now().UTC(), "count": 0.0}
					newFields[f.Path] = nf
				}
				count, _ := nf["count"].(float64)
				nf["count"] = count + 1
				nf["types"] = f.Actual
			case Suspicious, Breaking:
				if err := c.recordFinding(ctx, tx, it.q, id, *activeVersion, f); err != nil {
					return nil, "", err
				}
			}
		}
	}

	obsJSON, err := json.Marshal(observed)
	if err != nil {
		return nil, "", err
	}
	nfJSON, err := json.Marshal(newFields)
	if err != nil {
		return nil, "", err
	}
	_, err = tx.Exec(ctx, `
		UPDATE contracts SET observed = $2, new_fields = $3, status = $4, last_seen_at = now(), updated_at = now()
		WHERE id = $1`, id, obsJSON, nfJSON, status)
	return statuses, id, err
}

func (c *Checker) recordFinding(ctx context.Context, tx pgx.Tx, q queued, contractID string, ver int, f Finding) error {
	sev := f.Severity.String()
	if _, err := tx.Exec(ctx, `
		INSERT INTO contract_violations (org_id, contract_id, version, event_id, event_received_at, severity, kind, path, expected, actual)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		q.OrgID, contractID, ver, q.EventID, q.ReceivedAt, sev, f.Kind, f.Path, f.Expected, f.Actual); err != nil {
		return err
	}
	if f.Severity != Breaking {
		return nil
	}
	// One open incident per (contract, kind, path); repeats bump its count.
	var incidentID string
	var created bool
	err := tx.QueryRow(ctx, `
		INSERT INTO incidents (org_id, webhook_id, contract_id, kind, path, title, expected, actual, event_count, sample_event_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9)
		ON CONFLICT (contract_id, kind, path) WHERE status = 'open'
		DO UPDATE SET event_count = incidents.event_count + 1, last_seen_at = now(),
		              actual = EXCLUDED.actual, sample_event_id = EXCLUDED.sample_event_id
		RETURNING id, (xmax = 0)`,
		q.OrgID, q.WebhookID, contractID, f.Kind, f.Path, Title(q.EventType, f), f.Expected, f.Actual, q.EventID).
		Scan(&incidentID, &created)
	if err != nil || !created {
		return err
	}
	// One alert per incident, not per event.
	orgID, a, err := alerts.IncidentOpenedAlert(ctx, tx, incidentID)
	if err != nil {
		return err
	}
	return alerts.Enqueue(ctx, tx, orgID, a)
}

// StatusRepaired marks an event that broke its contract but that the webhook's
// repair rules turn back into a valid payload before it is forwarded.
const StatusRepaired = "repaired"

// repairedFindings applies the webhook's repair rules to the payload and
// checks the result. It returns the (kind|path) keys of suspicious and
// breaking findings that the repair fixes, and the repaired payload's severity.
func repairedFindings(q queued, payload []byte, v *version, raw []Finding, rules []repair.Rule) (map[string]bool, Severity) {
	if len(rules) == 0 {
		return nil, OK
	}
	res, err := repair.Apply(payload, q.EventType, rules)
	if err != nil || !res.Changed {
		return nil, OK
	}
	o, ok := Observe(res.Body)
	if !ok {
		return nil, OK
	}
	after, sev := Check(v.schema, v.critical, o)
	remaining := map[string]bool{}
	for _, f := range after {
		if f.Severity >= Suspicious {
			remaining[f.Kind+"|"+f.Path] = true
		}
	}
	fixed := map[string]bool{}
	for _, f := range raw {
		if key := f.Kind + "|" + f.Path; f.Severity >= Suspicious && !remaining[key] {
			fixed[key] = true
		}
	}
	return fixed, sev
}

// recordRepaired records a finding that a repair rule fixed. It opens no
// incident: the endpoint gets a valid payload.
func (c *Checker) recordRepaired(ctx context.Context, tx pgx.Tx, q queued, contractID string, ver int, f Finding) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO contract_violations (org_id, contract_id, version, event_id, event_received_at, severity, kind, path, expected, actual, repaired)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true)`,
		q.OrgID, contractID, ver, q.EventID, q.ReceivedAt, f.Severity.String(), f.Kind, f.Path, f.Expected, f.Actual)
	return err
}

// Title is the one-line incident summary shown in alerts and the dashboard.
func Title(eventType string, f Finding) string {
	switch f.Kind {
	case KindMissingField:
		s := fmt.Sprintf("%s: critical field %s is missing", eventType, f.Path)
		if f.Nested > 0 {
			s += fmt.Sprintf(" (with %d nested fields)", f.Nested)
		}
		return s
	case KindTypeChanged:
		return fmt.Sprintf("%s: %s changed type from %s to %s", eventType, f.Path, f.Expected, f.Actual)
	case KindNullValue:
		return fmt.Sprintf("%s: %s is null (expected %s)", eventType, f.Path, f.Expected)
	}
	return fmt.Sprintf("%s: %s (%s)", eventType, f.Path, f.Kind)
}

// version loads an immutable contract version, cached in memory.
func (c *Checker) version(ctx context.Context, tx pgx.Tx, contractID string, ver int) (*version, error) {
	key := fmt.Sprintf("%s:%d", contractID, ver)
	c.mu.Lock()
	if c.versions == nil {
		c.versions = map[string]*version{}
	}
	v := c.versions[key]
	c.mu.Unlock()
	if v != nil {
		return v, nil
	}
	var raw []byte
	var critical []string
	if err := tx.QueryRow(ctx, `SELECT schema, critical_fields FROM contract_versions WHERE contract_id = $1 AND version = $2`,
		contractID, ver).Scan(&raw, &critical); err != nil {
		return nil, fmt.Errorf("load contract version: %w", err)
	}
	v = &version{schema: NewSchema(), critical: map[string]bool{}}
	if err := json.Unmarshal(raw, v.schema); err != nil {
		return nil, err
	}
	for _, p := range critical {
		v.critical[p] = true
	}
	c.mu.Lock()
	c.versions[key] = v
	c.mu.Unlock()
	return v, nil
}
