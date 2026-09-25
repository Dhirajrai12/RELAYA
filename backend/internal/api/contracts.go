package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/alerts"
	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/contract"
	"relaya/internal/httpx"
)

type contractView struct {
	ID            string    `json:"id"`
	WebhookID     string    `json:"webhook_id"`
	WebhookName   string    `json:"webhook_name"`
	EventType     string    `json:"event_type"`
	Status        string    `json:"status"`
	Samples       int       `json:"samples"`     // learned since creation/last activation
	MinSamples    int       `json:"min_samples"` // when learning completes
	ActiveVersion *int      `json:"active_version"`
	Fingerprint   string    `json:"fingerprint"`
	FieldCount    int       `json:"field_count"`
	CriticalCount int       `json:"critical_count"`
	NewFields     int       `json:"new_fields"`
	Suspicious24h int       `json:"suspicious_24h"`
	Breaking24h   int       `json:"breaking_24h"`
	Repaired24h   int       `json:"repaired_24h"` // violations a repair rule fixed
	OpenIncidents int       `json:"open_incidents"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

const contractSelect = `
	SELECT c.id, c.webhook_id, w.name, c.event_type, c.status, (c.observed->>'samples')::int, c.active_version,
	       coalesce(v.fingerprint, ''),
	       coalesce((SELECT count(*) FROM jsonb_object_keys(coalesce(v.schema, c.observed)->'fields')), 0),
	       coalesce(cardinality(v.critical_fields), 0),
	       (SELECT count(*) FROM jsonb_object_keys(c.new_fields)),
	       (SELECT count(*) FROM contract_violations x WHERE x.contract_id = c.id AND x.severity = 'suspicious' AND NOT x.repaired AND x.created_at > now() - interval '24 hours'),
	       (SELECT count(*) FROM contract_violations x WHERE x.contract_id = c.id AND x.severity = 'breaking' AND NOT x.repaired AND x.created_at > now() - interval '24 hours'),
	       (SELECT count(*) FROM contract_violations x WHERE x.contract_id = c.id AND x.repaired AND x.created_at > now() - interval '24 hours'),
	       (SELECT count(*) FROM incidents i WHERE i.contract_id = c.id AND i.status = 'open'),
	       c.first_seen_at, c.last_seen_at
	FROM contracts c
	JOIN webhooks w ON w.id = c.webhook_id
	LEFT JOIN contract_versions v ON v.contract_id = c.id AND v.version = c.active_version`

func (s *Server) scanContract(row pgx.Row) (contractView, error) {
	var v contractView
	err := row.Scan(&v.ID, &v.WebhookID, &v.WebhookName, &v.EventType, &v.Status, &v.Samples, &v.ActiveVersion,
		&v.Fingerprint, &v.FieldCount, &v.CriticalCount, &v.NewFields, &v.Suspicious24h, &v.Breaking24h, &v.Repaired24h, &v.OpenIncidents,
		&v.FirstSeenAt, &v.LastSeenAt)
	v.MinSamples = s.ContractMinSamples
	return v, err
}

func (s *Server) listContracts(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	where, args := "c.org_id = $1", []any{orgID}
	if wid := r.URL.Query().Get("webhook_id"); wid != "" {
		if !uuidRe.MatchString(wid) {
			return httpx.BadRequest("invalid webhook_id")
		}
		where += " AND c.webhook_id = $2"
		args = append(args, wid)
	}
	rows, err := s.Pool.Query(r.Context(), contractSelect+` WHERE `+where+` ORDER BY w.name, c.event_type`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []contractView{}
	for rows.Next() {
		v, err := s.scanContract(rows)
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

// fieldView merges what the active version expects with what's been observed.
type fieldView struct {
	Path          string   `json:"path"`
	Types         []string `json:"types"`
	Required      bool     `json:"required"`
	Enum          []string `json:"enum,omitempty"`
	Critical      bool     `json:"critical"`
	InVersion     bool     `json:"in_version"`     // part of the active version
	ObservedTypes []string `json:"observed_types"` // types seen since activation / during learning
	ObservedSeen  int      `json:"observed_seen"`
}

type versionView struct {
	Version       int       `json:"version"`
	Fingerprint   string    `json:"fingerprint"`
	CriticalCount int       `json:"critical_count"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

type violationView struct {
	EventID   string    `json:"event_id"`
	Severity  string    `json:"severity"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	Expected  string    `json:"expected"`
	Actual    string    `json:"actual"`
	CreatedAt time.Time `json:"created_at"`
	Repaired  bool      `json:"repaired"` // fixed by a repair rule before forwarding
}

// loadSchemas returns the observed schema, the active version's schema (nil if
// none), its critical fields and the contract's new_fields map.
func (s *Server) loadSchemas(ctx context.Context, q rowQuerier, contractID string) (observed, active *contract.Schema, critical []string, newFields map[string]map[string]any, err error) {
	var obsRaw, nfRaw, verRaw []byte
	err = q.QueryRow(ctx, `
		SELECT c.observed, c.new_fields, v.schema, coalesce(v.critical_fields, '{}')
		FROM contracts c LEFT JOIN contract_versions v ON v.contract_id = c.id AND v.version = c.active_version
		WHERE c.id = $1`, contractID).Scan(&obsRaw, &nfRaw, &verRaw, &critical)
	if err != nil {
		return
	}
	observed = contract.NewSchema()
	if err = json.Unmarshal(obsRaw, observed); err != nil {
		return
	}
	if verRaw != nil {
		active = contract.NewSchema()
		if err = json.Unmarshal(verRaw, active); err != nil {
			return
		}
	}
	newFields = map[string]map[string]any{}
	_ = json.Unmarshal(nfRaw, &newFields)
	return
}

func (s *Server) getContract(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "contract")
	if err != nil {
		return err
	}
	c, err := s.scanContract(s.Pool.QueryRow(r.Context(), contractSelect+` WHERE c.id = $1 AND c.org_id = $2`, id, orgID))
	if err != nil {
		return notFoundIfNoRows(err)
	}
	observed, active, critical, newFields, err := s.loadSchemas(r.Context(), s.Pool, id)
	if err != nil {
		return err
	}

	crit := map[string]bool{}
	for _, p := range critical {
		crit[p] = true
	}
	fields := map[string]*fieldView{}
	if active != nil {
		for p, f := range active.Fields {
			fields[p] = &fieldView{Path: p, Types: f.Types, Required: active.Required(f), Enum: enumOf(f), Critical: crit[p], InVersion: true}
		}
	}
	for p, f := range observed.Fields {
		fv := fields[p]
		if fv == nil {
			fv = &fieldView{Path: p}
			if active == nil { // learning/proposed: the observed shape is the candidate contract
				fv.Types, fv.Required, fv.Enum = f.Types, observed.Required(f), enumOf(f)
			}
			fields[p] = fv
		}
		fv.ObservedTypes, fv.ObservedSeen = f.Types, f.Seen
	}
	list := make([]*fieldView, 0, len(fields))
	for _, f := range fields {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })

	rows, err := s.Pool.Query(r.Context(), `
		SELECT version, fingerprint, cardinality(critical_fields), created_by, created_at
		FROM contract_versions WHERE contract_id = $1 ORDER BY version DESC`, id)
	if err != nil {
		return err
	}
	versions, err := pgx.CollectRows(rows, pgx.RowToStructByPos[versionView])
	if err != nil {
		return err
	}
	rows, err = s.Pool.Query(r.Context(), `
		SELECT event_id, severity, kind, path, expected, actual, created_at, repaired
		FROM contract_violations WHERE contract_id = $1 ORDER BY created_at DESC, id DESC LIMIT 50`, id)
	if err != nil {
		return err
	}
	violations, err := pgx.CollectRows(rows, pgx.RowToStructByPos[violationView])
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"contract":         c,
		"fields":           list,
		"observed_samples": observed.Samples,
		"new_fields":       newFields,
		"versions":         versions,
		"violations":       violations,
	})
	return nil
}

// enumOf returns f's values only when they currently count as an enum.
func enumOf(f *contract.Field) []string {
	if contract.IsEnum(f) {
		return f.Enum
	}
	return nil
}

// createContractVersion activates a contract or saves a new version.
//
//	source "observed": the learned shape becomes the contract (first activation,
//	                   or accepting changes; resolves this contract's open incidents)
//	source "active":   keep the current version's shape, only change critical fields
func (s *Server) createContractVersion(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "contract")
	if err != nil {
		return err
	}
	var in struct {
		CriticalFields []string `json:"critical_fields"`
		Source         string   `json:"source"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Source == "" {
		in.Source = "observed"
	}
	if in.Source != "observed" && in.Source != "active" {
		return httpx.BadRequest(`source must be "observed" or "active"`)
	}

	var created int
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var eventType string
		if err := tx.QueryRow(r.Context(), `SELECT event_type FROM contracts WHERE id = $1 AND org_id = $2 FOR UPDATE`, id, orgID).Scan(&eventType); err != nil {
			return notFoundIfNoRows(err)
		}
		observed, active, _, _, err := s.loadSchemas(r.Context(), tx, id)
		if err != nil {
			return err
		}
		schema := observed
		if in.Source == "active" {
			if active == nil {
				return httpx.Conflict("this contract has no active version yet")
			}
			schema = active
		}
		if schema.Samples == 0 {
			return httpx.Conflict("no events observed yet; send a few events first")
		}
		seen := map[string]bool{}
		critical := []string{}
		for _, c := range in.CriticalFields {
			if schema.Fields[c] == nil {
				return httpx.BadRequest("critical field %q is not in the contract", c)
			}
			if !seen[c] {
				seen[c] = true
				critical = append(critical, c)
			}
		}
		sort.Strings(critical)
		raw, err := json.Marshal(schema)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO contract_versions (contract_id, version, schema, fingerprint, critical_fields, created_by)
			VALUES ($1, coalesce((SELECT max(version) FROM contract_versions WHERE contract_id = $1), 0) + 1, $2, $3, $4, $5)
			RETURNING version`, id, raw, schema.Fingerprint(), critical, p.ActorID()).Scan(&created); err != nil {
			return err
		}
		update := `UPDATE contracts SET status = 'active', active_version = $2, updated_at = now() WHERE id = $1`
		if in.Source == "observed" {
			// Start observing afresh, so "accept changes" later reflects only newer events.
			update = `UPDATE contracts SET status = 'active', active_version = $2, updated_at = now(),
				observed = '{"samples": 0, "fields": {}}', new_fields = '{}' WHERE id = $1`
			if err := resolveContractIncidents(r.Context(), tx, id, p.ActorID(), fmt.Sprintf("accepted into contract v%d", created)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(), update, id, created); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "contract.version", "contract", id)
		e.Metadata = map[string]any{"version": created, "source": in.Source, "event_type": eventType, "critical_fields": critical}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"version": created})
	return nil
}

// relearnContract drops the active version and learns the shape from scratch.
func (s *Server) relearnContract(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "contract")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE contracts SET status = 'learning', active_version = NULL, observed = '{"samples": 0, "fields": {}}',
			       new_fields = '{}', first_seen_at = now(), updated_at = now()
			WHERE id = $1 AND org_id = $2`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		if err := resolveContractIncidents(r.Context(), tx, id, p.ActorID(), "contract relearning"); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.ByPrincipal(p, orgID, "contract.relearn", "contract", id))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- incidents ----------------------------------------------------------------------------

type incidentView struct {
	ID            string      `json:"id"`
	WebhookID     string      `json:"webhook_id"`
	WebhookName   string      `json:"webhook_name"`
	ContractID    string      `json:"contract_id"`
	EventType     string      `json:"event_type"`
	Kind          string      `json:"kind"`
	Path          string      `json:"path"`
	Severity      string      `json:"severity"`
	Status        string      `json:"status"`
	Title         string      `json:"title"`
	Expected      string      `json:"expected"`
	Actual        string      `json:"actual"`
	EventCount    int         `json:"event_count"`
	FirstSeenAt   time.Time   `json:"first_seen_at"`
	LastSeenAt    time.Time   `json:"last_seen_at"`
	SampleEventID *string     `json:"sample_event_id"`
	ResolvedAt    *time.Time  `json:"resolved_at"`
	Resolution    string      `json:"resolution"`
	ResolvedBy    string      `json:"resolved_by"` // user/api key id, or "system"
	Replay        *replayView `json:"replay"`      // latest replay, if any
	RepairRule    *struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Enabled      bool   `json:"enabled"`
		AppliedCount int64  `json:"applied_count"`
	} `json:"repair_rule"` // the latest rule created to fix this incident, if any
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	if status != "open" && status != "resolved" {
		return httpx.BadRequest("status must be open or resolved")
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT i.id, i.webhook_id, coalesce(w.name, ''), i.contract_id, c.event_type, i.kind, i.path, i.severity, i.status,
		       i.title, i.expected, i.actual, i.event_count, i.first_seen_at, i.last_seen_at, i.sample_event_id,
		       i.resolved_at, i.resolution, i.resolved_by,
		       rp.id, rp.status, rp.total, rp.succeeded, rp.failed, rp.created_by, rp.created_at, rp.completed_at,
		       rr.id, rr.name, rr.enabled, rr.applied_count
		FROM incidents i
		JOIN contracts c ON c.id = i.contract_id
		LEFT JOIN webhooks w ON w.id = i.webhook_id
		LEFT JOIN LATERAL (
			SELECT * FROM replays WHERE incident_id = i.id ORDER BY created_at DESC LIMIT 1
		) rp ON true
		LEFT JOIN LATERAL (
			SELECT * FROM repair_rules WHERE incident_id = i.id ORDER BY created_at DESC LIMIT 1
		) rr ON true
		WHERE i.org_id = $1 AND i.status = $2
		ORDER BY i.last_seen_at DESC LIMIT 200`, orgID, status)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []incidentView{}
	for rows.Next() {
		var v incidentView
		var rp replayView
		var rpID, rpStatus, rpBy *string
		var rpTotal, rpOK, rpFailed *int
		var rpCreated *time.Time
		var rrID, rrName *string
		var rrEnabled *bool
		var rrApplied *int64
		if err := rows.Scan(&v.ID, &v.WebhookID, &v.WebhookName, &v.ContractID, &v.EventType, &v.Kind, &v.Path, &v.Severity,
			&v.Status, &v.Title, &v.Expected, &v.Actual, &v.EventCount, &v.FirstSeenAt, &v.LastSeenAt, &v.SampleEventID,
			&v.ResolvedAt, &v.Resolution, &v.ResolvedBy,
			&rpID, &rpStatus, &rpTotal, &rpOK, &rpFailed, &rpBy, &rpCreated, &rp.CompletedAt,
			&rrID, &rrName, &rrEnabled, &rrApplied); err != nil {
			return err
		}
		if rrID != nil {
			v.RepairRule = &struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				Enabled      bool   `json:"enabled"`
				AppliedCount int64  `json:"applied_count"`
			}{*rrID, *rrName, *rrEnabled, *rrApplied}
		}
		if rpID != nil {
			rp.ID, rp.Status, rp.Total, rp.Succeeded, rp.Failed, rp.CreatedBy, rp.CreatedAt = *rpID, *rpStatus, *rpTotal, *rpOK, *rpFailed, *rpBy, *rpCreated
			v.Replay = &rp
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data":                       out,
		"auto_resolve_after_seconds": int(s.IncidentAutoResolveAfter.Seconds()),
	})
	return nil
}

func (s *Server) resolveIncident(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "incident")
	if err != nil {
		return err
	}
	var in struct {
		Resolution string `json:"resolution"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.Resolution) > 500 {
		return httpx.BadRequest("resolution is too long")
	}
	if in.Resolution == "" {
		in.Resolution = "resolved manually"
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE incidents SET status = 'resolved', resolved_at = now(), resolved_by = $3, resolution = $4
			WHERE id = $1 AND org_id = $2 AND status = 'open'`, id, orgID, p.ActorID(), in.Resolution)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		e := audit.ByPrincipal(p, orgID, "incident.resolve", "incident", id)
		e.Reason = in.Resolution
		if err := audit.Record(r.Context(), tx, e); err != nil {
			return err
		}
		return alerts.NotifyIncidentsResolved(r.Context(), tx, []string{id})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// resolveContractIncidents closes a contract's open incidents (accept changes,
// relearn) and queues "resolved" alerts for them.
func resolveContractIncidents(ctx context.Context, tx pgx.Tx, contractID, by, resolution string) error {
	rows, err := tx.Query(ctx, `
		UPDATE incidents SET status = 'resolved', resolved_at = now(), resolved_by = $2, resolution = $3
		WHERE contract_id = $1 AND status = 'open' RETURNING id`, contractID, by, resolution)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	return alerts.NotifyIncidentsResolved(ctx, tx, ids)
}
