package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/httpx"
)

type projectView struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

const projectCols = `id, org_id, name, slug, created_at`

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(),
		`SELECT `+projectCols+` FROM projects WHERE org_id = $1 ORDER BY created_at`, orgID)
	if err != nil {
		return err
	}
	projects, err := pgx.CollectRows(rows, pgx.RowToStructByPos[projectView])
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": projects})
	return nil
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
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
	var v projectView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO projects (org_id, name, slug) VALUES ($1, $2, $3)
			RETURNING `+projectCols, orgID, name, slugify(name)).
			Scan(&v.ID, &v.OrgID, &v.Name, &v.Slug, &v.CreatedAt)
		if isUniqueViolation(err) {
			return httpx.Conflict("a project with this name already exists")
		}
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "project.create", "project", v.ID))
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, v)
	return nil
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	projectID, err := pathID(r, "project")
	if err != nil {
		return err
	}
	var v projectView
	err = s.Pool.QueryRow(r.Context(),
		`SELECT `+projectCols+` FROM projects WHERE id = $1 AND org_id = $2`, projectID, orgID).
		Scan(&v.ID, &v.OrgID, &v.Name, &v.Slug, &v.CreatedAt)
	if err != nil {
		return notFoundIfNoRows(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

// deleteProject removes the project and its webhooks. Stored events stay until
// retention removes them, so the audit trail keeps its evidence.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleOwner)
	if err != nil {
		return err
	}
	projectID, err := pathID(r, "project")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM projects WHERE id = $1 AND org_id = $2`, projectID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "project.delete", "project", projectID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
