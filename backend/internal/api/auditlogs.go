package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"relaya/internal/auth"
	"relaya/internal/httpx"
)

type auditView struct {
	ID         int64           `json:"id"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Reason     string          `json:"reason"`
	Result     string          `json:"result"`
	Metadata   json.RawMessage `json:"metadata"`
	At         time.Time       `json:"at"`
}

// listAuditLogs returns entries newest first. Paginate with ?before=<id>.
func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	before := int64(1<<63 - 1)
	if v := r.URL.Query().Get("before"); v != "" {
		if before, err = strconv.ParseInt(v, 10, 64); err != nil {
			return httpx.BadRequest("before must be an audit log id")
		}
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT id, actor_type, actor_id, action, target_type, target_id, reason, result, metadata, at
		FROM audit_logs WHERE org_id = $1 AND id < $2 ORDER BY id DESC LIMIT 100`, orgID, before)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []auditView{}
	for rows.Next() {
		var a auditView
		if err := rows.Scan(&a.ID, &a.ActorType, &a.ActorID, &a.Action, &a.TargetType, &a.TargetID,
			&a.Reason, &a.Result, &a.Metadata, &a.At); err != nil {
			return err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}
