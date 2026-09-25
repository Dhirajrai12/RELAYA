package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/contract"
	"relaya/internal/httpx"
	"relaya/internal/mask"
	"relaya/internal/repair"
)

type repairRuleView struct {
	ID            string          `json:"id"`
	WebhookID     string          `json:"webhook_id"`
	WebhookName   string          `json:"webhook_name"`
	EventType     string          `json:"event_type"` // "" = every event type
	Name          string          `json:"name"`
	Ops           json.RawMessage `json:"ops"`
	Enabled       bool            `json:"enabled"`
	Position      int             `json:"position"`
	IncidentID    *string         `json:"incident_id"`
	AppliedCount  int64           `json:"applied_count"` // deliveries sent with this rule's changes
	LastAppliedAt *time.Time      `json:"last_applied_at"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

const repairRuleSelect = `
	SELECT r.id, r.webhook_id, w.name, r.event_type, r.name, r.ops, r.enabled, r.position, r.incident_id,
	       r.applied_count, r.last_applied_at, r.created_by, r.created_at, r.updated_at
	FROM repair_rules r JOIN webhooks w ON w.id = r.webhook_id`

func getRepairRule(ctx context.Context, q rowsQuerier, orgID, id string) (repairRuleView, error) {
	rows, err := q.Query(ctx, repairRuleSelect+` WHERE r.id = $1 AND r.org_id = $2`, id, orgID)
	if err != nil {
		return repairRuleView{}, err
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[repairRuleView])
	if errors.Is(err, pgx.ErrNoRows) {
		return v, httpx.ErrNotFound
	}
	return v, err
}

func (s *Server) listRepairRules(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	where, args := "r.org_id = $1", []any{orgID}
	if wid := r.URL.Query().Get("webhook_id"); wid != "" {
		if !uuidRe.MatchString(wid) {
			return httpx.BadRequest("invalid webhook_id")
		}
		args = append(args, wid)
		where += " AND r.webhook_id = $2"
	}
	rows, err := s.Pool.Query(r.Context(), repairRuleSelect+" WHERE "+where+" ORDER BY w.name, r.position, r.created_at", args...)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[repairRuleView])
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

type repairRuleInput struct {
	WebhookID  *string      `json:"webhook_id"`
	EventType  *string      `json:"event_type"`
	Name       *string      `json:"name"`
	Ops        *[]repair.Op `json:"ops"`
	Enabled    *bool        `json:"enabled"`
	IncidentID *string      `json:"incident_id"`
}

func validateOps(ops []repair.Op) ([]byte, error) {
	if err := repair.Validate(ops); err != nil {
		return nil, httpx.BadRequest("%s", err.Error())
	}
	return json.Marshal(ops)
}

func (s *Server) createRepairRule(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in repairRuleInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.WebhookID == nil || in.Name == nil || in.Ops == nil {
		return httpx.BadRequest("webhook_id, name and ops are required")
	}
	if !uuidRe.MatchString(*in.WebhookID) {
		return httpx.BadRequest("invalid webhook_id")
	}
	name, err := requireName(*in.Name, "name", 100)
	if err != nil {
		return err
	}
	opsJSON, err := validateOps(*in.Ops)
	if err != nil {
		return err
	}
	eventType := ""
	if in.EventType != nil {
		eventType = strings.TrimSpace(*in.EventType)
	}
	enabled := in.Enabled == nil || *in.Enabled
	if in.IncidentID != nil && !uuidRe.MatchString(*in.IncidentID) {
		return httpx.BadRequest("invalid incident_id")
	}

	var v repairRuleView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM webhooks WHERE id = $1 AND org_id = $2)`, *in.WebhookID, orgID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return httpx.ErrNotFound
		}
		if in.IncidentID != nil {
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM incidents WHERE id = $1 AND org_id = $2 AND webhook_id = $3)`,
				*in.IncidentID, orgID, *in.WebhookID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return httpx.BadRequest("that incident isn't on this webhook")
			}
		}
		var id string
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO repair_rules (org_id, webhook_id, event_type, name, ops, enabled, incident_id, created_by, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
			        (SELECT coalesce(max(position), 0) + 1 FROM repair_rules WHERE webhook_id = $2))
			RETURNING id`,
			orgID, *in.WebhookID, eventType, name, opsJSON, enabled, in.IncidentID, p.ActorID()).Scan(&id); err != nil {
			return err
		}
		var err error
		if v, err = getRepairRule(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "repair_rule.create", "repair_rule", id)
		e.Metadata = map[string]any{"webhook_id": v.WebhookID, "event_type": eventType, "name": name, "incident_id": in.IncidentID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, v)
	return nil
}

func (s *Server) updateRepairRule(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "rule")
	if err != nil {
		return err
	}
	var in repairRuleInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.WebhookID != nil || in.IncidentID != nil {
		return httpx.BadRequest("a rule can't move to another webhook or incident; create a new one")
	}
	sets, args := []string{"updated_at = now()"}, []any{id, orgID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	changed := []string{}
	if in.Name != nil {
		name, err := requireName(*in.Name, "name", 100)
		if err != nil {
			return err
		}
		add("name", name)
		changed = append(changed, "name")
	}
	if in.EventType != nil {
		add("event_type", strings.TrimSpace(*in.EventType))
		changed = append(changed, "event_type")
	}
	if in.Ops != nil {
		opsJSON, err := validateOps(*in.Ops)
		if err != nil {
			return err
		}
		add("ops", opsJSON)
		changed = append(changed, "ops")
	}
	if in.Enabled != nil {
		add("enabled", *in.Enabled)
		changed = append(changed, "enabled")
	}

	var v repairRuleView
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE repair_rules SET `+strings.Join(sets, ", ")+` WHERE id = $1 AND org_id = $2`, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		if v, err = getRepairRule(r.Context(), tx, orgID, id); err != nil {
			return err
		}
		action := "repair_rule.update"
		if len(changed) == 1 && changed[0] == "enabled" {
			action = map[bool]string{true: "repair_rule.enable", false: "repair_rule.disable"}[v.Enabled]
		}
		e := audit.ByPrincipal(p, orgID, action, "repair_rule", id)
		e.Metadata = map[string]any{"changed": changed, "webhook_id": v.WebhookID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) deleteRepairRule(w http.ResponseWriter, r *http.Request) error {
	orgID, p, _, err := s.orgAccess(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r, "rule")
	if err != nil {
		return err
	}
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		var name, webhookID string
		err := tx.QueryRow(r.Context(), `DELETE FROM repair_rules WHERE id = $1 AND org_id = $2 RETURNING name, webhook_id`, id, orgID).Scan(&name, &webhookID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		e := audit.ByPrincipal(p, orgID, "repair_rule.delete", "repair_rule", id)
		e.Metadata = map[string]any{"name": name, "webhook_id": webhookID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- preview ----------------------------------------------------------------------------

type previewFinding struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type contractResult struct {
	Status   string           `json:"status"` // none (no active contract), ok, compatible, suspicious, breaking
	Findings []previewFinding `json:"findings"`
}

// previewRepairRule dry-runs a draft rule (after the webhook's other enabled
// rules, as delivery would) on a stored event and checks the result against
// the active contract. Nothing is saved.
func (s *Server) previewRepairRule(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	var in struct {
		WebhookID string      `json:"webhook_id"`
		EventType string      `json:"event_type"`
		Ops       []repair.Op `json:"ops"`
		EventID   string      `json:"event_id"` // default: the latest event of that webhook (and type)
		RuleID    string      `json:"rule_id"`  // when editing: leave the saved version out
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !uuidRe.MatchString(in.WebhookID) {
		return httpx.BadRequest("webhook_id is required")
	}
	if err := repair.Validate(in.Ops); err != nil {
		return httpx.BadRequest("%s", err.Error())
	}
	ctx := r.Context()

	var eventID, eventType string
	var payload []byte
	q := `SELECT id, type, payload FROM events WHERE org_id = $1 AND webhook_id = $2 AND status = 'received'`
	args := []any{orgID, in.WebhookID}
	switch {
	case in.EventID != "":
		if !uuidRe.MatchString(in.EventID) {
			return httpx.BadRequest("invalid event_id")
		}
		args = append(args, in.EventID)
		q += " AND id = $3"
	case in.EventType != "":
		args = append(args, in.EventType)
		q += " AND type = $3"
	}
	err = s.Pool.QueryRow(ctx, q+" ORDER BY received_at DESC LIMIT 1", args...).Scan(&eventID, &eventType, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewError(http.StatusNotFound, "no_event", "no event to try this on yet: send one to the webhook first")
	}
	if err != nil {
		return err
	}

	existing, err := repair.Load(ctx, s.Pool, in.WebhookID, eventType)
	if err != nil {
		return err
	}
	rules := make([]repair.Rule, 0, len(existing)+1)
	for _, rule := range existing {
		if rule.ID != in.RuleID {
			rules = append(rules, rule)
		}
	}
	draft := repair.Rule{ID: "draft", Name: "this rule", EventType: in.EventType, Ops: in.Ops}
	rules = append(rules, draft)
	res, err := repair.Apply(payload, eventType, rules)
	if err != nil {
		return httpx.BadRequest("%s", err.Error())
	}

	before, err := s.checkAgainstContract(ctx, in.WebhookID, eventType, payload)
	if err != nil {
		return err
	}
	after, err := s.checkAgainstContract(ctx, in.WebhookID, eventType, res.Body)
	if err != nil {
		return err
	}
	counts := res.Counts[len(rules)-1]
	others := []string{}
	for _, id := range res.Applied {
		if id != "draft" {
			others = append(others, repair.Names(existing, []string{id})...)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"event_id":       eventID,
		"event_type":     eventType,
		"before":         json.RawMessage(mask.JSON(payload)),
		"after":          json.RawMessage(mask.JSON(res.Body)),
		"is_json":        json.Valid(payload),
		"changed":        res.Changed,
		"op_changes":     counts, // values each of this rule's changes touched
		"other_rules":    others, // other enabled rules that also changed this event
		"contract":       map[string]any{"before": before, "after": after},
		"applies_to_all": in.EventType == "",
	})
	return nil
}

// checkAgainstContract checks a payload against the active version of the
// webhook's contract for that event type.
func (s *Server) checkAgainstContract(ctx context.Context, webhookID, eventType string, payload []byte) (contractResult, error) {
	res := contractResult{Status: "none", Findings: []previewFinding{}}
	var contractID string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM contracts WHERE webhook_id = $1 AND event_type = $2 AND status = 'active'`,
		webhookID, eventType).Scan(&contractID)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	_, active, critical, _, err := s.loadSchemas(ctx, s.Pool, contractID)
	if err != nil || active == nil {
		return res, err
	}
	o, ok := contract.Observe(payload)
	if !ok {
		return res, nil
	}
	crit := map[string]bool{}
	for _, p := range critical {
		crit[p] = true
	}
	findings, sev := contract.Check(active, crit, o)
	res.Status = sev.String()
	for _, f := range findings {
		if f.Severity >= contract.Suspicious {
			res.Findings = append(res.Findings, previewFinding{f.Severity.String(), f.Kind, f.Path, f.Expected, f.Actual})
		}
	}
	return res, nil
}

// ---- suggestion from an incident --------------------------------------------------------

// repairSuggestion proposes a rule that undoes an incident's breaking change:
// convert a retyped field back, rename a field the provider renamed, or fill
// a missing/null field with a default the user confirms.
func (s *Server) repairSuggestion(w http.ResponseWriter, r *http.Request) error {
	orgID, _, _, err := s.orgAccess(r, auth.RoleMember)
	if err != nil {
		return err
	}
	id, err := pathID(r, "incident")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var webhookID, contractID, eventType, kind, path, expected, actual string
	var sample *string
	err = s.Pool.QueryRow(ctx, `
		SELECT i.webhook_id, i.contract_id, c.event_type, i.kind, i.path, i.expected, i.actual, i.sample_event_id
		FROM incidents i JOIN contracts c ON c.id = i.contract_id
		WHERE i.id = $1 AND i.org_id = $2`, id, orgID).
		Scan(&webhookID, &contractID, &eventType, &kind, &path, &expected, &actual, &sample)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}

	target := pickType(expected)
	out := map[string]any{
		"webhook_id": webhookID, "event_type": eventType, "incident_id": id, "sample_event_id": sample,
		"needs_value": false,
	}
	short := path[strings.LastIndex(path, ".")+1:]
	switch kind {
	case contract.KindTypeChanged:
		if target == "" {
			return httpx.NewError(http.StatusUnprocessableEntity, "no_suggestion", "no automatic fix for this change; build a rule by hand")
		}
		out["name"] = fmt.Sprintf("Convert %s back to %s", short, target)
		out["ops"] = []repair.Op{{Op: repair.OpConvert, Path: path, Type: target}}
		out["explanation"] = fmt.Sprintf("The provider now sends %s as %s. This converts it back to %s before forwarding, when that's possible without losing data (\"100\" becomes 100, but \"12.5\" is left as it is for an integer).", path, actual, target)
	case contract.KindMissingField:
		if from := s.renamedFrom(ctx, contractID, path, expected); from != "" {
			out["name"] = fmt.Sprintf("Rename %s back to %s", from[strings.LastIndex(from, ".")+1:], short)
			out["ops"] = []repair.Op{{Op: repair.OpRename, From: from, To: path}}
			out["explanation"] = fmt.Sprintf("%s stopped arriving and %s started arriving with the same type, so the provider probably renamed it. This moves the value back to %s.", path, from, path)
			break
		}
		out["name"] = fmt.Sprintf("Fill in %s when missing", short)
		out["ops"] = []repair.Op{{Op: repair.OpDefault, Path: path, Value: placeholder(target)}}
		out["needs_value"] = true
		out["explanation"] = fmt.Sprintf("%s is missing. This fills it in with a value you choose; check that your system handles that value correctly.", path)
	case contract.KindNullValue:
		out["name"] = fmt.Sprintf("Fill in %s when null", short)
		out["ops"] = []repair.Op{{Op: repair.OpDefault, Path: path, Value: placeholder(target)}}
		out["needs_value"] = true
		out["explanation"] = fmt.Sprintf("%s now arrives as null. This replaces null with a value you choose.", path)
	default:
		return httpx.NewError(http.StatusUnprocessableEntity, "no_suggestion", "no automatic fix for this change; build a rule by hand")
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// pickType chooses the conversion target from an "integer | null"-style label.
func pickType(label string) string {
	parts := strings.Split(label, " | ")
	for _, t := range []string{contract.TInteger, contract.TNumber, contract.TString, contract.TBoolean} {
		for _, p := range parts {
			if strings.TrimSpace(p) == t {
				return t
			}
		}
	}
	return ""
}

func placeholder(t string) json.RawMessage {
	switch t {
	case contract.TInteger, contract.TNumber:
		return json.RawMessage(`0`)
	case contract.TBoolean:
		return json.RawMessage(`false`)
	}
	return json.RawMessage(`""`)
}

// renamedFrom looks for a field the contract has started seeing (a new field)
// in the same object, with the same type as the missing one.
func (s *Server) renamedFrom(ctx context.Context, contractID, missing, expected string) string {
	var raw []byte
	if err := s.Pool.QueryRow(ctx, `SELECT new_fields FROM contracts WHERE id = $1`, contractID).Scan(&raw); err != nil {
		return ""
	}
	var nf map[string]struct {
		Types string  `json:"types"`
		Count float64 `json:"count"`
	}
	if json.Unmarshal(raw, &nf) != nil {
		return ""
	}
	parent := ""
	if i := strings.LastIndex(missing, "."); i >= 0 {
		parent = missing[:i]
	}
	best, bestCount := "", 0.0
	for p, f := range nf {
		pp := ""
		if i := strings.LastIndex(p, "."); i >= 0 {
			pp = p[:i]
		}
		if pp != parent || f.Types != expected || f.Count <= bestCount {
			continue
		}
		best, bestCount = p, f.Count
	}
	return best
}
