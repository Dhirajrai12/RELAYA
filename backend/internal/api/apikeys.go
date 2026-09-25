package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/httpx"
)

type apiKeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT id, name, prefix, role, last_used_at, revoked_at, created_at
		FROM api_keys WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowToStructByPos[apiKeyView])
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": keys})
	return nil
}

// createAPIKey returns the full key once; only its hash is stored.
func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	orgID, p, callerRole, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	name, err := requireName(in.Name, "name", 100)
	if err != nil {
		return err
	}
	if in.Role == "" {
		in.Role = string(auth.RoleMember)
	}
	role, ok := auth.ParseRole(in.Role)
	if !ok || role == auth.RoleOwner {
		return httpx.BadRequest("role must be admin or member")
	}
	if !callerRole.AtLeast(role) {
		return httpx.ErrForbidden
	}

	key, prefix, hash := auth.NewAPIKey()
	var v apiKeyView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var createdBy *string
		if p.UserID != "" {
			createdBy = &p.UserID
		}
		err := tx.QueryRow(r.Context(), `
			INSERT INTO api_keys (org_id, name, prefix, key_hash, role, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, name, prefix, role, last_used_at, revoked_at, created_at`,
			orgID, name, prefix, hash, role, createdBy).
			Scan(&v.ID, &v.Name, &v.Prefix, &v.Role, &v.LastUsedAt, &v.RevokedAt, &v.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "api_key.create", "api_key", v.ID))
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"api_key": v, "key": key})
	return nil
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	keyID, err := pathID(r, "key")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE api_keys SET revoked_at = now()
			WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL`, keyID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "api_key.revoke", "api_key", keyID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
