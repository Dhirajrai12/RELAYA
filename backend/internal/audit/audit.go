// Package audit records who did what, to which target, why and with what result.
package audit

import (
	"context"
	"encoding/json"

	"relaya/internal/auth"
)

type Entry struct {
	OrgID      string
	ActorType  string // user, api_key, system
	ActorID    string
	Action     string // e.g. "webhook.create"
	TargetType string
	TargetID   string
	Reason     string
	Result     string // defaults to "success"
	Metadata   map[string]any
}

// ByPrincipal fills the actor fields from an authenticated caller.
func ByPrincipal(p auth.Principal, orgID, action, targetType, targetID string) Entry {
	return Entry{
		OrgID: orgID, ActorType: p.ActorType(), ActorID: p.ActorID(),
		Action: action, TargetType: targetType, TargetID: targetID,
	}
}

// Record writes e using q, which may be a transaction so the audit entry
// commits together with the change it describes.
func Record(ctx context.Context, q auth.Querier, e Entry) error {
	if e.Result == "" {
		e.Result = "success"
	}
	if e.Metadata == nil {
		e.Metadata = map[string]any{}
	}
	meta, err := json.Marshal(e.Metadata)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO audit_logs (org_id, actor_type, actor_id, action, target_type, target_id, reason, result, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.OrgID, e.ActorType, e.ActorID, e.Action, e.TargetType, e.TargetID, e.Reason, e.Result, meta)
	return err
}
