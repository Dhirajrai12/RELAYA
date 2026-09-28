package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/httpx"
	"relaya/internal/provider"
)

const ingestTokenPrefix = "in_"

type webhookView struct {
	ID              string    `json:"id"`
	OrgID           string    `json:"org_id"`
	ProjectID       string    `json:"project_id"`
	Name            string    `json:"name"`
	Provider        string    `json:"provider"`
	IngestURL       string    `json:"ingest_url"`
	HasSecret       bool      `json:"has_signing_secret"`
	SignatureHeader string    `json:"signature_header,omitempty"`
	Status          string    `json:"status"`
	Kind            string    `json:"kind"` // inbound, or outbound (an outbound app's messages)
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const webhookCols = `id, org_id, project_id, name, provider, ingest_token,
	signing_secret_enc IS NOT NULL, signature_header, status, kind, created_at, updated_at`

func (s *Server) scanWebhook(row pgx.Row) (webhookView, error) {
	var v webhookView
	var token string
	err := row.Scan(&v.ID, &v.OrgID, &v.ProjectID, &v.Name, &v.Provider, &token,
		&v.HasSecret, &v.SignatureHeader, &v.Status, &v.Kind, &v.CreatedAt, &v.UpdatedAt)
	v.IngestURL = s.IngestBaseURL + "/v1/in/" + token
	return v, err
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	args := []any{orgID}
	q := `SELECT ` + webhookCols + ` FROM webhooks WHERE org_id = $1`
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		if !uuidRe.MatchString(pid) {
			return httpx.BadRequest("invalid project_id")
		}
		args = append(args, pid)
		q += ` AND project_id = $` + itoa(len(args))
	}
	switch k := r.URL.Query().Get("kind"); k {
	case "", "all":
	case "inbound", "outbound":
		args = append(args, k)
		q += ` AND kind = $` + itoa(len(args))
	default:
		return httpx.BadRequest("kind must be inbound, outbound or all")
	}
	rows, err := s.Pool.Query(r.Context(), q+` ORDER BY created_at`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []webhookView{}
	for rows.Next() {
		v, err := s.scanWebhook(rows)
		if err != nil {
			return err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		ProjectID       string `json:"project_id"`
		Name            string `json:"name"`
		Provider        string `json:"provider"`
		SigningSecret   string `json:"signing_secret"`
		SignatureHeader string `json:"signature_header"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	name, err := requireName(in.Name, "name", 100)
	if err != nil {
		return err
	}
	if !uuidRe.MatchString(in.ProjectID) {
		return httpx.BadRequest("project_id is required")
	}
	if in.Provider == "" {
		in.Provider = "generic"
	}
	if _, ok := provider.Get(in.Provider); !ok {
		return httpx.BadRequest("unknown provider %q; supported: %s", in.Provider, strings.Join(provider.Names(), ", "))
	}
	if err := validateSignatureHeader(in.Provider, in.SignatureHeader); err != nil {
		return err
	}
	secretEnc, err := s.encryptSecret(r.Context(), orgID, in.SigningSecret)
	if err != nil {
		return err
	}

	var v webhookView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(r.Context(),
			`SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND org_id = $2)`, in.ProjectID, orgID).
			Scan(&ok); err != nil || !ok {
			return httpx.BadRequest("project_id does not exist in this organization")
		}
		v, err = s.scanWebhook(tx.QueryRow(r.Context(), `
			INSERT INTO webhooks (org_id, project_id, name, provider, ingest_token, signing_secret_enc, signature_header)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+webhookCols,
			orgID, in.ProjectID, name, in.Provider, auth.RandomToken(ingestTokenPrefix), secretEnc, in.SignatureHeader))
		if err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "webhook.create", "webhook", v.ID)
		e.Metadata = map[string]any{"provider": in.Provider, "has_signing_secret": secretEnc != nil}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, v)
	return nil
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "webhook")
	if err != nil {
		return err
	}
	v, err := s.scanWebhook(s.Pool.QueryRow(r.Context(),
		`SELECT `+webhookCols+` FROM webhooks WHERE id = $1 AND org_id = $2`, id, orgID))
	if err != nil {
		return notFoundIfNoRows(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

// updateWebhook changes name, status, signature header or signing secret.
// Send "signing_secret": "" to remove the secret.
func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "webhook")
	if err != nil {
		return err
	}
	var in struct {
		Name            *string `json:"name"`
		Status          *string `json:"status"`
		SigningSecret   *string `json:"signing_secret"`
		SignatureHeader *string `json:"signature_header"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}

	var v webhookView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		cur, err := s.scanWebhook(tx.QueryRow(r.Context(),
			`SELECT `+webhookCols+` FROM webhooks WHERE id = $1 AND org_id = $2 FOR UPDATE`, id, orgID))
		if err != nil {
			return notFoundIfNoRows(err)
		}
		changed := []string{}
		sets := []string{"updated_at = now()"}
		args := []any{id}
		set := func(col string, val any) {
			args = append(args, val)
			sets = append(sets, col+" = $"+itoa(len(args)))
			changed = append(changed, col)
		}

		if in.Name != nil {
			name, err := requireName(*in.Name, "name", 100)
			if err != nil {
				return err
			}
			set("name", name)
		}
		if in.Status != nil {
			if *in.Status != "active" && *in.Status != "paused" {
				return httpx.BadRequest("status must be active or paused")
			}
			set("status", *in.Status)
		}
		if in.SignatureHeader != nil {
			if err := validateSignatureHeader(cur.Provider, *in.SignatureHeader); err != nil {
				return err
			}
			set("signature_header", *in.SignatureHeader)
		}
		if in.SigningSecret != nil {
			enc, err := s.encryptSecret(r.Context(), orgID, *in.SigningSecret)
			if err != nil {
				return err
			}
			set("signing_secret_enc", enc)
		}

		v, err = s.scanWebhook(tx.QueryRow(r.Context(),
			`UPDATE webhooks SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+webhookCols, args...))
		if err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "webhook.update", "webhook", id)
		e.Metadata = map[string]any{"fields": changed} // never the secret itself
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

// rotateWebhookURL issues a new ingest token; the old URL stops working
// (within the ingest cache TTL of 30 seconds).
func (s *Server) rotateWebhookURL(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "webhook")
	if err != nil {
		return err
	}
	var v webhookView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		v, err = s.scanWebhook(tx.QueryRow(r.Context(), `
			UPDATE webhooks SET ingest_token = $3, updated_at = now()
			WHERE id = $1 AND org_id = $2 RETURNING `+webhookCols,
			id, orgID, auth.RandomToken(ingestTokenPrefix)))
		if err != nil {
			return notFoundIfNoRows(err)
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "webhook.rotate_url", "webhook", id))
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "webhook")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM webhooks WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "webhook.delete", "webhook", id))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) encryptSecret(ctx context.Context, orgID, secret string) ([]byte, error) {
	if secret == "" {
		return nil, nil
	}
	if len(secret) > 1024 {
		return nil, httpx.BadRequest("signing_secret is too long")
	}
	return s.Vault.Encrypt(ctx, orgID, []byte(secret))
}

func validateSignatureHeader(providerName, header string) error {
	if header == "" {
		return nil
	}
	if providerName != "generic" {
		return httpx.BadRequest("signature_header can only be set for the generic provider")
	}
	if len(header) > 100 || strings.ContainsAny(header, " :\r\n") {
		return httpx.BadRequest("signature_header must be a valid header name")
	}
	return nil
}
