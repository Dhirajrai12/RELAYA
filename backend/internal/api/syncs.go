package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/httpx"
	"relaya/internal/syncs"
)

func (s *Server) listSyncModels(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{"data": syncs.Models()})
	return nil
}

type syncView struct {
	ID                  string            `json:"id"`
	ConnectionID        string            `json:"connection_id"`
	EndUserID           string            `json:"end_user_id"`
	IntegrationName     string            `json:"integration_name"`
	Provider            string            `json:"provider"`
	WebhookID           string            `json:"webhook_id"`
	WebhookName         string            `json:"webhook_name"`
	Model               string            `json:"model"`
	ModelName           string            `json:"model_name"`
	Config              map[string]string `json:"config"`
	IntervalMinutes     int               `json:"interval_minutes"`
	Enabled             bool              `json:"enabled"`
	EmitExisting        bool              `json:"emit_existing"`
	BaselineDone        bool              `json:"baseline_done"`
	Running             bool              `json:"running"`
	NextRunAt           time.Time         `json:"next_run_at"`
	LastRunAt           *time.Time        `json:"last_run_at"`
	LastStatus          string            `json:"last_status"`
	LastError           string            `json:"last_error"`
	ConsecutiveFailures int               `json:"consecutive_failures"`
	Records             int               `json:"records"`
	Events              int64             `json:"events"`
	CreatedAt           time.Time         `json:"created_at"`
}

const syncSelect = `
	SELECT s.id, s.connection_id, c.end_user_id, i.name, i.provider, s.webhook_id, w.name, s.model, s.config,
	       s.interval_seconds, s.enabled, s.emit_existing, s.baseline_done,
	       s.run_started_at IS NOT NULL AND s.run_started_at > now() - interval '15 minutes',
	       s.next_run_at, s.last_run_at, s.last_status, s.last_error, s.consecutive_failures, s.records, s.events, s.created_at
	FROM syncs s JOIN connections c ON c.id = s.connection_id JOIN integrations i ON i.id = c.integration_id
	JOIN webhooks w ON w.id = s.webhook_id`

func scanSync(row pgx.Row) (syncView, error) {
	var v syncView
	var cfg []byte
	var secs int
	err := row.Scan(&v.ID, &v.ConnectionID, &v.EndUserID, &v.IntegrationName, &v.Provider, &v.WebhookID, &v.WebhookName, &v.Model, &cfg,
		&secs, &v.Enabled, &v.EmitExisting, &v.BaselineDone, &v.Running, &v.NextRunAt, &v.LastRunAt, &v.LastStatus, &v.LastError,
		&v.ConsecutiveFailures, &v.Records, &v.Events, &v.CreatedAt)
	v.Config = map[string]string{}
	_ = json.Unmarshal(cfg, &v.Config)
	v.IntervalMinutes = secs / 60
	v.ModelName = v.Model
	if m, ok := syncs.GetModel(v.Model); ok {
		v.ModelName = m.Name
	}
	return v, err
}

func (s *Server) listSyncs(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), syncSelect+` WHERE s.org_id = $1 ORDER BY s.created_at`, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []syncView{}
	for rows.Next() {
		v, err := scanSync(rows)
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

func validInterval(minutes int) error {
	if minutes < 5 || minutes > 1440 {
		return httpx.BadRequest("interval_minutes must be between 5 and 1440")
	}
	return nil
}

func (s *Server) createSync(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		ConnectionID    string            `json:"connection_id"`
		Model           string            `json:"model"`
		Config          map[string]string `json:"config"`
		IntervalMinutes int               `json:"interval_minutes"`
		WebhookID       string            `json:"webhook_id"`
		EmitExisting    bool              `json:"emit_existing"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.IntervalMinutes == 0 {
		in.IntervalMinutes = 15
	}
	if err := validInterval(in.IntervalMinutes); err != nil {
		return err
	}
	m, ok := syncs.GetModel(in.Model)
	if !ok {
		return httpx.BadRequest("unknown sync model %q", in.Model)
	}
	if !uuidRe.MatchString(in.ConnectionID) {
		return httpx.BadRequest("connection_id is required")
	}
	var provider, integrationName, endUser string
	err = s.Pool.QueryRow(r.Context(), `
		SELECT i.provider, i.name, c.end_user_id FROM connections c JOIN integrations i ON i.id = c.integration_id
		WHERE c.id = $1 AND c.org_id = $2`, in.ConnectionID, orgID).Scan(&provider, &integrationName, &endUser)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.BadRequest("connection_id does not exist in this organization")
	}
	if err != nil {
		return err
	}
	if m.Provider != provider {
		return httpx.BadRequest("%s syncs need a %s connection; this one is %s", m.Name, m.Provider, provider)
	}
	cfg, err := m.Validate(in.Config)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	cfgJSON, _ := json.Marshal(cfg)

	var v syncView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		webhookID := in.WebhookID
		if webhookID != "" {
			var ok bool
			if !uuidRe.MatchString(webhookID) {
				return httpx.BadRequest("invalid webhook_id")
			}
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM webhooks WHERE id = $1 AND org_id = $2)`, webhookID, orgID).
				Scan(&ok); err != nil || !ok {
				return httpx.BadRequest("webhook_id does not exist in this organization")
			}
		} else if webhookID, err = s.syncWebhook(r, tx, p, orgID, integrationName+" "+strings.ToLower(m.Name)+" ("+endUser+")"); err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO syncs (org_id, connection_id, webhook_id, model, config, interval_seconds, emit_existing)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			orgID, in.ConnectionID, webhookID, m.Key, cfgJSON, in.IntervalMinutes*60, in.EmitExisting).Scan(&id); err != nil {
			return err
		}
		if v, err = scanSync(tx.QueryRow(r.Context(), syncSelect+` WHERE s.id = $1`, id)); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "sync.create", "sync", id)
		e.Metadata = map[string]any{"model": m.Key, "connection_id": in.ConnectionID, "interval_minutes": in.IntervalMinutes}
		if err := audit.Record(r.Context(), tx, e); err != nil {
			return err
		}
		return syncs.Wake(r.Context(), tx)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, v)
	return nil
}

// syncWebhook creates the webhook a sync's events go to, in the org's first
// project (or a new "Syncs" project). It gets a random signing secret nobody
// knows, so nothing from outside can post into it.
func (s *Server) syncWebhook(r *http.Request, tx pgx.Tx, p auth.Principal, orgID, name string) (string, error) {
	ctx := r.Context()
	var projectID string
	err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE org_id = $1 ORDER BY created_at LIMIT 1`, orgID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO projects (org_id, name, slug) VALUES ($1, 'Syncs', $2) RETURNING id`,
			orgID, "syncs-"+randomSuffix()).Scan(&projectID)
	}
	if err != nil {
		return "", err
	}
	secret, err := s.encryptSecret(ctx, orgID, auth.RandomToken("sync_"))
	if err != nil {
		return "", err
	}
	if len(name) > 94 {
		name = name[:94]
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO webhooks (org_id, project_id, name, provider, ingest_token, signing_secret_enc)
		VALUES ($1, $2, $3, 'generic', $4, $5) RETURNING id`,
		orgID, projectID, "Sync: "+name, auth.RandomToken(ingestTokenPrefix), secret).Scan(&id); err != nil {
		return "", err
	}
	e := audit.ByPrincipal(p, orgID, "webhook.create", "webhook", id)
	e.Metadata = map[string]any{"provider": "generic", "for": "sync"}
	return id, audit.Record(ctx, tx, e)
}

func (s *Server) updateSync(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "sync")
	if err != nil {
		return err
	}
	var in struct {
		Enabled         *bool              `json:"enabled"`
		IntervalMinutes *int               `json:"interval_minutes"`
		Config          *map[string]string `json:"config"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var v syncView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		cur, err := scanSync(tx.QueryRow(r.Context(), syncSelect+` WHERE s.id = $1 AND s.org_id = $2 FOR UPDATE OF s`, id, orgID))
		if err != nil {
			return notFoundIfNoRows(err)
		}
		changed := []string{}
		if in.Enabled != nil {
			if _, err := tx.Exec(r.Context(), `UPDATE syncs SET enabled = $2, next_run_at = CASE WHEN $2 THEN now() ELSE next_run_at END, updated_at = now() WHERE id = $1`,
				id, *in.Enabled); err != nil {
				return err
			}
			changed = append(changed, "enabled")
		}
		if in.IntervalMinutes != nil {
			if err := validInterval(*in.IntervalMinutes); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `UPDATE syncs SET interval_seconds = $2, updated_at = now() WHERE id = $1`, id, *in.IntervalMinutes*60); err != nil {
				return err
			}
			changed = append(changed, "interval")
		}
		if in.Config != nil {
			m, ok := syncs.GetModel(cur.Model)
			if !ok {
				return httpx.BadRequest("unknown sync model")
			}
			cfg, err := m.Validate(*in.Config)
			if err != nil {
				return httpx.BadRequest("%v", err)
			}
			cfgJSON, _ := json.Marshal(cfg)
			// Different data: start over with a fresh baseline.
			if _, err := tx.Exec(r.Context(), `DELETE FROM sync_records WHERE sync_id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `
				UPDATE syncs SET config = $2, cursor = '', baseline_done = false, records = 0, next_run_at = now(), updated_at = now()
				WHERE id = $1`, id, cfgJSON); err != nil {
				return err
			}
			changed = append(changed, "config")
		}
		if v, err = scanSync(tx.QueryRow(r.Context(), syncSelect+` WHERE s.id = $1`, id)); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "sync.update", "sync", id)
		e.Metadata = map[string]any{"fields": changed}
		if err := audit.Record(r.Context(), tx, e); err != nil {
			return err
		}
		return syncs.Wake(r.Context(), tx)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteSync(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "sync")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM syncs WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "sync.delete", "sync", id))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// runSync makes a sync due now; a worker picks it up within seconds.
func (s *Server) runSync(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "sync")
	if err != nil {
		return err
	}
	var v syncView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE syncs SET next_run_at = now() WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		if v, err = scanSync(tx.QueryRow(r.Context(), syncSelect+` WHERE s.id = $1`, id)); err != nil {
			return err
		}
		if err := audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "sync.run", "sync", id)); err != nil {
			return err
		}
		return syncs.Wake(r.Context(), tx)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusAccepted, v)
	return nil
}

type syncRunView struct {
	ID         int64      `json:"id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Status     string     `json:"status"`
	Fetched    int        `json:"fetched"`
	Created    int        `json:"created"`
	Updated    int        `json:"updated"`
	Error      string     `json:"error"`
}

func (s *Server) listSyncRuns(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "sync")
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT id, started_at, finished_at, status, fetched, created, updated, error FROM sync_runs
		WHERE sync_id = $1 AND org_id = $2 ORDER BY started_at DESC LIMIT 50`, id, orgID)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[syncRunView])
	if err != nil {
		return err
	}
	if out == nil {
		out = []syncRunView{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}
