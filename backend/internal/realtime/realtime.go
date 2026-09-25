// Package realtime pushes change notifications to connected dashboards.
//
// Anything that changes data calls Notify inside its transaction; Postgres
// delivers the NOTIFY only when that transaction commits, so a dashboard is
// never told about something that was rolled back. Each API process runs one
// Hub that LISTENs on the channel and fans messages out to the WebSocket
// clients of the affected organization.
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel is the Postgres NOTIFY channel for change messages.
const Channel = "relaya_changes"

// Message is what dashboards receive. Keep it small: it tells the client what
// to refetch, it never carries payloads or secrets.
type Message struct {
	Type          string `json:"type"` // event, delivery, change
	OrgID         string `json:"org_id"`
	WebhookID     string `json:"webhook_id,omitempty"`
	EventID       string `json:"event_id,omitempty"`
	DeliveryID    string `json:"delivery_id,omitempty"`
	DestinationID string `json:"destination_id,omitempty"`
	Status        string `json:"status,omitempty"`
	Action        string `json:"action,omitempty"` // for type=change: the audit action, e.g. webhook.create
	TargetID      string `json:"target_id,omitempty"`
}

// Execer is satisfied by *pgxpool.Pool, pgx.Tx and *pgx.Conn.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Notify queues m for delivery when the surrounding transaction commits.
func Notify(ctx context.Context, q Execer, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, string(b))
	return err
}

// ---- hub ------------------------------------------------------------------------------

// Subscriber receives messages for one organization.
type Subscriber struct {
	C      chan []byte
	orgID  string
	mu     sync.Mutex
	lagged bool // a message was dropped because C was full: client must resync
}

// TakeLagged reports (and clears) whether messages were dropped.
func (s *Subscriber) TakeLagged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.lagged
	s.lagged = false
	return l
}

// Hub fans out Postgres notifications to subscribers by organization.
type Hub struct {
	Pool *pgxpool.Pool

	mu   sync.RWMutex
	subs map[string]map[*Subscriber]struct{}
}

func NewHub(pool *pgxpool.Pool) *Hub {
	return &Hub{Pool: pool, subs: map[string]map[*Subscriber]struct{}{}}
}

// Subscribe registers a subscriber for orgID; call the returned func to leave.
func (h *Hub) Subscribe(orgID string) (*Subscriber, func()) {
	s := &Subscriber{C: make(chan []byte, 256), orgID: orgID}
	h.mu.Lock()
	if h.subs[orgID] == nil {
		h.subs[orgID] = map[*Subscriber]struct{}{}
	}
	h.subs[orgID][s] = struct{}{}
	h.mu.Unlock()
	return s, func() {
		h.mu.Lock()
		delete(h.subs[orgID], s)
		if len(h.subs[orgID]) == 0 {
			delete(h.subs, orgID)
		}
		h.mu.Unlock()
	}
}

// Subscribers returns the number of connected clients (for health/metrics).
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, m := range h.subs {
		n += len(m)
	}
	return n
}

func (h *Hub) publish(payload string) {
	var m struct {
		OrgID string `json:"org_id"`
	}
	if json.Unmarshal([]byte(payload), &m) != nil || m.OrgID == "" {
		return
	}
	b := []byte(payload)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs[m.OrgID] {
		select {
		case s.C <- b:
		default: // slow client: tell it to refetch everything instead of blocking others
			s.mu.Lock()
			s.lagged = true
			s.mu.Unlock()
		}
	}
}

// Run LISTENs until ctx is done, reconnecting on errors. After a reconnect
// every subscriber is marked lagged, because notifications may have been missed.
func (h *Hub) Run(ctx context.Context) {
	first := true
	for ctx.Err() == nil {
		if err := h.listen(ctx, first); err != nil && ctx.Err() == nil {
			slog.Warn("realtime listen", "err", err)
			time.Sleep(time.Second)
		}
		first = false
	}
}

func (h *Hub) listen(ctx context.Context, first bool) error {
	conn, err := h.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// A LISTENing connection must not go back to the pool. (Wrapped in a func:
	// `defer conn.Hijack().Close(...)` would hijack immediately.)
	defer func() { conn.Hijack().Close(context.Background()) }()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	if !first {
		h.markAllLagged()
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.publish(n.Payload)
	}
}

func (h *Hub) markAllLagged() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, m := range h.subs {
		for s := range m {
			s.mu.Lock()
			s.lagged = true
			s.mu.Unlock()
		}
	}
}
