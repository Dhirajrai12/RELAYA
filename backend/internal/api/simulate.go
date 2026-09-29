package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/httpx"
	"relaya/internal/ingest"
	"relaya/internal/mask"
	"relaya/internal/provider"
	"relaya/internal/simulate"
)

// The event simulator: sample events for a webhook's provider, signed with the
// webhook's own secret exactly as the provider signs them, run through ingest
// (verification, dedup, deliveries) and marked simulated.

const maxSimulatedBody = 256 << 10

// Provider event types, including Shopify's "orders/create".
var simEventTypeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,99}$`)

type simWebhook struct {
	id, orgID, projectID, provider, kind, signatureHeader, ingestToken string
	secretEnc                                                          []byte
}

func (s *Server) simWebhook(r *http.Request, min auth.Role) (auth.Principal, simWebhook, error) {
	orgID, p, _, err := s.orgAccess(r, min)
	if err != nil {
		return p, simWebhook{}, err
	}
	id, err := pathID(r, "webhook")
	if err != nil {
		return p, simWebhook{}, err
	}
	wh := simWebhook{id: id, orgID: orgID}
	err = s.Pool.QueryRow(r.Context(), `
		SELECT project_id, provider, kind, signature_header, signing_secret_enc, ingest_token FROM webhooks WHERE id = $1 AND org_id = $2`, id, orgID).
		Scan(&wh.projectID, &wh.provider, &wh.kind, &wh.signatureHeader, &wh.secretEnc, &wh.ingestToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, wh, httpx.ErrNotFound
	}
	if err == nil && wh.kind != "inbound" {
		err = httpx.BadRequest("outbound webhooks take messages from the outbound API; send one there instead")
	}
	return p, wh, err
}

func (s *Server) listSimulationSamples(w http.ResponseWriter, r *http.Request) error {
	_, wh, err := s.simWebhook(r, auth.RoleMember)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"provider": wh.provider,
		"signed":   len(wh.secretEnc) > 0,
		"data":     simulate.Samples(wh.provider, time.Now()),
	})
	return nil
}

func (s *Server) simulateEvent(w http.ResponseWriter, r *http.Request) error {
	p, wh, err := s.simWebhook(r, auth.RoleAdmin)
	if err != nil {
		return err
	}
	var in struct {
		EventType string `json:"event_type"`
		Payload   string `json:"payload"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.EventType = strings.TrimSpace(in.EventType)
	if in.EventType != "" && !simEventTypeRe.MatchString(in.EventType) {
		return httpx.BadRequest("event_type: letters, digits, _ . : / -; up to 100")
	}
	body := []byte(strings.TrimSpace(in.Payload))
	if len(body) == 0 {
		// No payload: send the first sample of that type (or the provider's first).
		samples := simulate.Samples(wh.provider, time.Now())
		body = []byte(samples[0].Payload)
		for _, sm := range samples {
			if sm.Type == in.EventType {
				body = []byte(sm.Payload)
			}
		}
	}
	if len(body) > maxSimulatedBody {
		return httpx.BadRequest("payload is larger than 256 KB")
	}
	if body[0] == '{' || body[0] == '[' {
		if !json.Valid(body) {
			return httpx.BadRequest("payload is not valid JSON")
		}
	}

	cfg := provider.Config{SignatureHeader: wh.signatureHeader}
	if len(wh.secretEnc) > 0 {
		if cfg.Secret, err = s.Vault.Decrypt(r.Context(), wh.orgID, wh.secretEnc); err != nil {
			return err
		}
	}
	name := wh.provider
	prov, ok := provider.Get(name)
	if !ok {
		name = "generic"
		prov, _ = provider.Get(name)
	}
	now := time.Now().UTC()
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	// Providers that sign the URL (Twilio, HubSpot, Square) sign the webhook's own ingest URL.
	ingestURL := s.IngestBaseURL + "/v1/in/" + wh.ingestToken
	out, err := provider.Sign(name, body, cfg, now, in.EventType, "sim_"+hex.EncodeToString(b), ingestURL)
	if errors.Is(err, provider.ErrCannotSign) {
		return httpx.BadRequest("%v: send a test from the provider instead", err)
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	out.Header.Set("User-Agent", "Relaya-Simulator/1.0")
	out.Header.Set("Relaya-Simulated", "true")

	// The same checks a real delivery gets.
	preq := provider.Request{Header: out.Header, Body: out.Body, Now: now, Method: http.MethodPost, URLs: []string{ingestURL}}
	sig := prov.Verify(preq, cfg)
	accepted := sig == provider.SigValid || sig == provider.SigNotConfigured
	status := "received"
	if !accepted {
		status = "rejected"
	}
	headers, err := json.Marshal(mask.Headers(out.Header))
	if err != nil {
		return err
	}
	ev := ingest.Event{
		OrgID: wh.orgID, ProjectID: wh.projectID, WebhookID: wh.id,
		DedupKey: provider.DedupKey(prov, preq), Type: prov.EventType(preq), Status: status, Signature: string(sig),
		ContentType: out.Header.Get("Content-Type"), Headers: headers, Payload: out.Body, ReceivedAt: now, Simulated: true,
	}
	var id string
	var duplicate bool
	var deliveries int
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		if id, duplicate, err = ingest.StoreTx(r.Context(), tx, ev, accepted); err != nil {
			return err
		}
		if !duplicate {
			if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM deliveries WHERE event_id = $1`, id).Scan(&deliveries); err != nil {
				return err
			}
		}
		e := audit.ByPrincipal(p, wh.orgID, "webhook.simulate", "webhook", wh.id)
		e.Metadata = map[string]any{"event_id": id, "event_type": ev.Type, "duplicate": duplicate}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": id, "duplicate": duplicate, "event_type": ev.Type, "status": status, "signature": string(sig), "deliveries": deliveries,
	})
	return nil
}
