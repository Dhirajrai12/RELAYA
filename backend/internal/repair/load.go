package repair

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Load returns a webhook's enabled rules for an event type, in run order.
func Load(ctx context.Context, q Querier, webhookID, eventType string) ([]Rule, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, event_type, ops FROM repair_rules
		WHERE webhook_id = $1 AND enabled AND (event_type = '' OR event_type = $2)
		ORDER BY position, created_at`, webhookID, eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var ops []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.EventType, &ops); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ops, &r.Ops); err != nil {
			return nil, fmt.Errorf("repair rule %s: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Names returns the names of the rules with the given IDs, in the same order.
func Names(rules []Rule, ids []string) []string {
	byID := make(map[string]string, len(rules))
	for _, r := range rules {
		byID[r.ID] = r.Name
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}
