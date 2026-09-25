package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/httpx"
	"relaya/internal/provider"
)

type orgView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Plan      string    `json:"plan"`
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) orgsForUser(ctx context.Context, userID string) ([]orgView, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT o.id, o.name, o.slug, o.plan, m.role, o.created_at
		FROM organizations o JOIN memberships m ON m.org_id = o.id
		WHERE m.user_id = $1 ORDER BY o.created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[orgView])
}

func (s *Server) listOrgs(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.FromContext(r.Context())
	var orgs []orgView
	var err error
	if p.APIKeyID != "" {
		var o orgView
		err = s.Pool.QueryRow(r.Context(),
			`SELECT id, name, slug, plan, $2, created_at FROM organizations WHERE id = $1`,
			p.APIKeyOrgID, string(p.APIKeyRole)).Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &o.Role, &o.CreatedAt)
		orgs = []orgView{o}
	} else {
		orgs, err = s.orgsForUser(r.Context(), p.UserID)
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": orgs})
	return nil
}

func (s *Server) createOrg(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.FromContext(r.Context())
	if p.UserID == "" {
		return httpx.NewError(http.StatusForbidden, "forbidden", "API keys cannot create organizations")
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	name, err := requireName(in.Name, "name", 100)
	if err != nil {
		return err
	}
	var o orgView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		id, err := createOrgTx(r.Context(), tx, name, p.UserID)
		if err != nil {
			return err
		}
		if err := audit.Record(r.Context(), tx, audit.ByPrincipal(p, id, "org.create", "organization", id)); err != nil {
			return err
		}
		o.Role = string(auth.RoleOwner)
		return tx.QueryRow(r.Context(), `SELECT id, name, slug, plan, created_at FROM organizations WHERE id = $1`, id).
			Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &o.CreatedAt)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, o)
	return nil
}

func (s *Server) getOrg(w http.ResponseWriter, r *http.Request) error {
	orgID, _, role, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	o := orgView{Role: string(role)}
	if err := s.Pool.QueryRow(r.Context(),
		`SELECT id, name, slug, plan, created_at FROM organizations WHERE id = $1`, orgID).
		Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &o.CreatedAt); err != nil {
		return notFoundIfNoRows(err)
	}
	httpx.JSON(w, http.StatusOK, o)
	return nil
}

// ---- members -------------------------------------------------------------------

type memberView struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT u.id, u.email, u.name, m.role, m.created_at
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1 ORDER BY m.created_at`, orgID)
	if err != nil {
		return err
	}
	members, err := pgx.CollectRows(rows, pgx.RowToStructByPos[memberView])
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": members})
	return nil
}

// addMember adds an existing user by email. Email invitations come later.
func (s *Server) addMember(w http.ResponseWriter, r *http.Request) error {
	orgID, p, callerRole, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	role, ok := auth.ParseRole(in.Role)
	if !ok {
		return httpx.BadRequest("role must be owner, admin or member")
	}
	if !callerRole.AtLeast(role) {
		return httpx.ErrForbidden
	}

	var m memberView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `SELECT id, email, name FROM users WHERE lower(email) = lower($1)`,
			strings.TrimSpace(in.Email)).Scan(&m.UserID, &m.Email, &m.Name)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.BadRequest("no user with that email; ask them to sign up first")
			}
			return err
		}
		err = tx.QueryRow(r.Context(), `
			INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)
			RETURNING role, created_at`, orgID, m.UserID, role).Scan(&m.Role, &m.CreatedAt)
		if isUniqueViolation(err) {
			return httpx.Conflict("user is already a member")
		}
		if err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "member.add", "user", m.UserID)
		e.Metadata = map[string]any{"role": role}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, m)
	return nil
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) error {
	orgID, p, callerRole, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	userID, err := pathID(r, "user")
	if err != nil {
		return err
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	role, ok := auth.ParseRole(in.Role)
	if !ok {
		return httpx.BadRequest("role must be owner, admin or member")
	}

	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		current, err := lockMember(r.Context(), tx, orgID, userID)
		if err != nil {
			return err
		}
		// Admins cannot promote to owner or change an owner.
		if !callerRole.AtLeast(role) || !callerRole.AtLeast(current) {
			return httpx.ErrForbidden
		}
		if current == auth.RoleOwner && role != auth.RoleOwner {
			if err := ensureAnotherOwner(r.Context(), tx, orgID, userID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2`, orgID, userID, role); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "member.update_role", "user", userID)
		e.Metadata = map[string]any{"from": current, "to": role}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) error {
	orgID, p, callerRole, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	userID, err := pathID(r, "user")
	if err != nil {
		return err
	}
	self := p.UserID == userID // anyone may leave; removing others needs admin

	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		current, err := lockMember(r.Context(), tx, orgID, userID)
		if err != nil {
			return err
		}
		if !self && (!callerRole.AtLeast(auth.RoleAdmin) || !callerRole.AtLeast(current)) {
			return httpx.ErrForbidden
		}
		if current == auth.RoleOwner {
			if err := ensureAnotherOwner(r.Context(), tx, orgID, userID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(),
			`DELETE FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "member.remove", "user", userID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func lockMember(ctx context.Context, tx pgx.Tx, orgID, userID string) (auth.Role, error) {
	var role string
	err := tx.QueryRow(ctx,
		`SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2 FOR UPDATE`, orgID, userID).Scan(&role)
	return auth.Role(role), notFoundIfNoRows(err)
}

// ensureAnotherOwner prevents an organization from losing its last owner.
func ensureAnotherOwner(ctx context.Context, tx pgx.Tx, orgID, exceptUserID string) error {
	// Lock all owner rows so two concurrent demotions cannot both pass.
	rows, err := tx.Query(ctx,
		`SELECT user_id FROM memberships WHERE org_id = $1 AND role = 'owner' FOR UPDATE`, orgID)
	if err != nil {
		return err
	}
	owners, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range owners {
		if id != exceptUserID {
			return nil
		}
	}
	return httpx.Conflict("an organization must keep at least one owner")
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{"data": provider.Names()})
	return nil
}
