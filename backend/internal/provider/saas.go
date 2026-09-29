package provider

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Challenger is implemented by providers that check a new webhook URL with a
// handshake the receiver must answer (Slack's url_verification). When Challenge
// returns ok, ingest answers with body and stores nothing.
type Challenger interface {
	Challenge(r Request) (body []byte, contentType string, ok bool)
}

// ---- Slack (Events API): X-Slack-Signature = "v0=" + hex HMAC-SHA256("v0:ts:body") --
//
// Keyed with the app's signing secret; X-Slack-Request-Timestamp is in seconds.
// Retries carry the same event_id. The first request to a new URL is a signed
// url_verification whose challenge must be echoed back.

type slack struct{ tolerance time.Duration }

func (slack) Name() string { return "slack" }

func (s slack) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	sig, ts := r.Header.Get("X-Slack-Signature"), r.Header.Get("X-Slack-Request-Timestamp")
	if sig == "" {
		return SigMissing
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return SigInvalid
	}
	if age := r.Now.Sub(time.Unix(unix, 0)); age > s.tolerance || age < -s.tolerance {
		return SigInvalid
	}
	hexSig, ok := strings.CutPrefix(sig, "v0=")
	if !ok {
		return SigInvalid
	}
	return compareHex(hexSig, hmacSHA256(c.Secret, append([]byte("v0:"+ts+":"), r.Body...)))
}

func (slack) DedupKey(r Request) string { return jsonString(r.Body, "event_id") }

// EventType is the inner event's type for event_callback ("message", "app_mention"…).
func (slack) EventType(r Request) string {
	var m struct {
		Type  string `json:"type"`
		Event struct {
			Type string `json:"type"`
		} `json:"event"`
	}
	if json.Unmarshal(r.Body, &m) != nil {
		return ""
	}
	if m.Type == "event_callback" && m.Event.Type != "" {
		return m.Event.Type
	}
	return m.Type
}

func (slack) Challenge(r Request) ([]byte, string, bool) {
	var m struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(r.Body, &m) != nil || m.Type != "url_verification" || m.Challenge == "" {
		return nil, "", false
	}
	b, _ := json.Marshal(map[string]string{"challenge": m.Challenge})
	return b, "application/json", true
}

// ---- Twilio: X-Twilio-Signature = base64 HMAC-SHA1(auth token, URL + sorted params) --
//
// The signed string is the full URL Twilio called (with any query string) followed
// by each POST parameter, sorted by name, as name+value. JSON bodies are covered by
// a bodySHA256 query parameter instead (Twilio's newer products).

type twilio struct{}

func (twilio) Name() string { return "twilio" }

func (twilio) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.Header.Get("X-Twilio-Signature")))
	if r.Header.Get("X-Twilio-Signature") == "" {
		return SigMissing
	}
	if err != nil {
		return SigInvalid
	}
	for _, u := range r.URLs {
		if hmac.Equal(got, twilioMAC(c.Secret, u, r)) && twilioBodyOK(u, r.Body) {
			return SigValid
		}
	}
	return SigInvalid
}

func twilioMAC(secret []byte, u string, r Request) []byte {
	var b strings.Builder
	b.WriteString(u)
	if form, err := url.ParseQuery(string(r.Body)); err == nil && !strings.HasPrefix(strings.TrimSpace(string(r.Body)), "{") {
		keys := make([]string, 0, len(form))
		for k := range form {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, v := range form[k] {
				b.WriteString(k)
				b.WriteString(v)
			}
		}
	}
	m := hmac.New(sha1.New, secret)
	m.Write([]byte(b.String()))
	return m.Sum(nil)
}

// twilioBodyOK checks the bodySHA256 query parameter Twilio adds for JSON bodies.
func twilioBodyOK(u string, body []byte) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	want := pu.Query().Get("bodySHA256")
	if want == "" {
		return !strings.HasPrefix(strings.TrimSpace(string(body)), "{") // JSON must be covered by the hash
	}
	sum := sha256.Sum256(body)
	return hmac.Equal([]byte(strings.ToLower(want)), []byte(hex.EncodeToString(sum[:])))
}

func (twilio) DedupKey(r Request) string {
	if k := r.Header.Get("I-Twilio-Idempotency-Token"); k != "" {
		return k
	}
	f := bodyFields(r.Body)
	for _, sid := range []string{"MessageSid", "CallSid", "SmsSid"} {
		if f[sid] != "" {
			return f[sid] + ":" + twilioStatus(f)
		}
	}
	return ""
}

func twilioStatus(f map[string]string) string {
	for _, k := range []string{"MessageStatus", "SmsStatus", "CallStatus"} {
		if f[k] != "" {
			return strings.ToLower(f[k])
		}
	}
	return ""
}

// EventType is message.<status> or call.<status>, e.g. message.delivered.
func (twilio) EventType(r Request) string {
	f := bodyFields(r.Body)
	status := twilioStatus(f)
	switch {
	case status == "":
		return ""
	case f["CallSid"] != "" && f["MessageSid"] == "" && f["SmsSid"] == "":
		return "call." + status
	}
	return "message." + status
}

// ---- HubSpot: X-HubSpot-Signature-v3 = base64 HMAC-SHA256(method + URL + body + ts) --
//
// Keyed with the app's client secret; X-HubSpot-Request-Timestamp is in
// milliseconds and must be recent. Older senders use v1 (hex SHA-256 of
// secret + body) or v2 (hex SHA-256 of secret + method + URL + body), named by
// X-HubSpot-Signature-Version. The body is a JSON array of events.

type hubspot struct{ tolerance time.Duration }

func (hubspot) Name() string { return "hubspot" }

func (h hubspot) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	if v3 := r.Header.Get("X-HubSpot-Signature-v3"); v3 != "" {
		ts := r.Header.Get("X-HubSpot-Request-Timestamp")
		ms, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return SigInvalid
		}
		if age := r.Now.Sub(time.UnixMilli(ms)); age > h.tolerance || age < -h.tolerance {
			return SigInvalid
		}
		got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v3))
		if err != nil {
			return SigInvalid
		}
		for _, u := range r.URLs {
			if hmac.Equal(got, hmacSHA256(c.Secret, []byte(r.Method+hubspotURI(u)+string(r.Body)+ts))) {
				return SigValid
			}
		}
		return SigInvalid
	}
	sig := r.Header.Get("X-HubSpot-Signature")
	if sig == "" {
		return SigMissing
	}
	switch r.Header.Get("X-HubSpot-Signature-Version") {
	case "v2":
		for _, u := range r.URLs {
			sum := sha256.Sum256([]byte(string(c.Secret) + r.Method + u + string(r.Body)))
			if compareHex(sig, sum[:]) == SigValid {
				return SigValid
			}
		}
		return SigInvalid
	default: // v1
		sum := sha256.Sum256(append(append([]byte{}, c.Secret...), r.Body...))
		return compareHex(sig, sum[:])
	}
}

// hubspotURI decodes the characters HubSpot decodes before signing v3.
func hubspotURI(u string) string {
	return strings.NewReplacer("%3A", ":", "%2F", "/", "%3F", "?", "%40", "@", "%21", "!", "%24", "$", "%27", "'",
		"%28", "(", "%29", ")", "%2A", "*", "%2C", ",", "%3B", ";").Replace(u)
}

type hubspotEvent struct {
	EventID          json.Number `json:"eventId"`
	SubscriptionType string      `json:"subscriptionType"`
}

func hubspotEvents(body []byte) []hubspotEvent {
	var evs []hubspotEvent
	if json.Unmarshal(body, &evs) != nil {
		var one hubspotEvent // workflow webhooks send a single object
		if json.Unmarshal(body, &one) == nil && one.SubscriptionType != "" {
			evs = []hubspotEvent{one}
		}
	}
	return evs
}

// DedupKey joins the batch's event IDs: a retried batch has the same ones.
func (hubspot) DedupKey(r Request) string {
	var ids []string
	for _, e := range hubspotEvents(r.Body) {
		if e.EventID != "" {
			ids = append(ids, e.EventID.String())
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// EventType is the batch's subscription type, e.g. contact.creation ("batch" if mixed).
func (hubspot) EventType(r Request) string {
	evs := hubspotEvents(r.Body)
	if len(evs) == 0 {
		return ""
	}
	t := evs[0].SubscriptionType
	for _, e := range evs[1:] {
		if e.SubscriptionType != t {
			return "batch"
		}
	}
	return t
}

// ---- Square: x-square-hmacsha256-signature = base64 HMAC-SHA256(key, URL + body) --
//
// Keyed with the subscription's signature key; the URL is the notification URL
// exactly as registered in Square.

type square struct{}

func (square) Name() string { return "square" }

func (square) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	sig := r.Header.Get("X-Square-Hmacsha256-Signature")
	if sig == "" {
		return SigMissing
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil {
		return SigInvalid
	}
	for _, u := range r.URLs {
		if hmac.Equal(got, hmacSHA256(c.Secret, append([]byte(u), r.Body...))) {
			return SigValid
		}
	}
	return SigInvalid
}

func (square) DedupKey(r Request) string  { return jsonString(r.Body, "event_id") }
func (square) EventType(r Request) string { return jsonString(r.Body, "type") }

// ---- Segment (Webhooks destination): X-Signature = hex HMAC-SHA1(shared secret, body) --

type segment struct{}

func (segment) Name() string { return "segment" }

func (segment) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	sig := r.Header.Get("X-Signature")
	if sig == "" {
		return SigMissing
	}
	m := hmac.New(sha1.New, c.Secret)
	m.Write(r.Body)
	return compareHex(sig, m.Sum(nil))
}

func (segment) DedupKey(r Request) string { return jsonString(r.Body, "messageId") }

// EventType is the tracked event's name for track calls ("Order Completed"), else the call type.
func (segment) EventType(r Request) string {
	t := jsonString(r.Body, "type")
	if e := jsonString(r.Body, "event"); t == "track" && e != "" {
		return e
	}
	return t
}

// ---- SendGrid Event Webhook: ECDSA P-256 signature over timestamp + body ------------
//
// X-Twilio-Email-Event-Webhook-Signature is a base64 DER ECDSA signature of
// SHA-256(timestamp + body); the webhook's secret is the verification key SendGrid
// shows (base64 public key). Relaya holds only the public key, so it can check
// SendGrid's events but never sign them. The body is a JSON array of events.

type sendgrid struct{}

func (sendgrid) Name() string { return "sendgrid" }

func (sendgrid) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	sig, ts := r.Header.Get("X-Twilio-Email-Event-Webhook-Signature"), r.Header.Get("X-Twilio-Email-Event-Webhook-Timestamp")
	if sig == "" {
		return SigMissing
	}
	pub, err := sendgridKey(c.Secret)
	if err != nil {
		return SigInvalid
	}
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil {
		return SigInvalid
	}
	sum := sha256.Sum256(append([]byte(ts), r.Body...))
	if !ecdsa.VerifyASN1(pub, sum[:], der) {
		return SigInvalid
	}
	return SigValid
}

// sendgridKey reads the verification key: base64 DER (as SendGrid shows it) or PEM.
func sendgridKey(secret []byte) (*ecdsa.PublicKey, error) {
	s := strings.TrimSpace(string(secret))
	s = strings.TrimPrefix(s, "-----BEGIN PUBLIC KEY-----")
	s = strings.TrimSuffix(s, "-----END PUBLIC KEY-----")
	s = strings.Join(strings.Fields(s), "")
	der, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return nil, x509.ErrUnsupportedAlgorithm
	}
	return pub, nil
}

type sendgridEvent struct {
	Event     string `json:"event"`
	SGEventID string `json:"sg_event_id"`
}

func sendgridEvents(body []byte) []sendgridEvent {
	var evs []sendgridEvent
	_ = json.Unmarshal(body, &evs)
	return evs
}

func (sendgrid) DedupKey(r Request) string {
	var ids []string
	for _, e := range sendgridEvents(r.Body) {
		if e.SGEventID != "" {
			ids = append(ids, e.SGEventID)
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// EventType is the batch's event ("delivered", "open"…), or "batch" when mixed.
func (sendgrid) EventType(r Request) string {
	evs := sendgridEvents(r.Body)
	if len(evs) == 0 {
		return ""
	}
	t := evs[0].Event
	for _, e := range evs[1:] {
		if e.Event != t {
			return "batch"
		}
	}
	return t
}

// ---- Notion: X-Notion-Signature = "sha256=" + hex HMAC-SHA256(verification token, body) --
//
// When a subscription is created, Notion first sends an unsigned request whose
// body carries the verification_token; the user pastes it back into Notion and
// uses it as this webhook's secret. That one request is accepted unsigned.

type notion struct{}

func (notion) Name() string { return "notion" }

// NotionVerificationToken returns the token of Notion's subscription check, if body is one.
func NotionVerificationToken(body []byte) (string, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil || len(m) != 1 {
		return "", false
	}
	var t string
	if json.Unmarshal(m["verification_token"], &t) != nil || t == "" {
		return "", false
	}
	return t, true
}

func (notion) Verify(r Request, c Config) SignatureResult {
	sig := r.Header.Get("X-Notion-Signature")
	if sig == "" {
		if _, ok := NotionVerificationToken(r.Body); ok {
			return SigNotConfigured // the subscription check is unsigned by design
		}
	}
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	if sig == "" {
		return SigMissing
	}
	hexSig, ok := strings.CutPrefix(sig, "sha256=")
	if !ok {
		return SigInvalid
	}
	return compareHex(hexSig, hmacSHA256(c.Secret, r.Body))
}

func (notion) DedupKey(r Request) string { return jsonString(r.Body, "id") }

func (notion) EventType(r Request) string {
	if _, ok := NotionVerificationToken(r.Body); ok {
		return "verification"
	}
	return jsonString(r.Body, "type")
}
