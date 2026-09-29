// Package provider knows how each webhook provider signs, identifies and names
// its deliveries. The gateway uses it to verify signatures, pick a dedup key
// and extract the event type without understanding the payload further.
package provider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SignatureResult is stored on every event.
type SignatureResult string

const (
	SigValid         SignatureResult = "valid"
	SigInvalid       SignatureResult = "invalid"
	SigMissing       SignatureResult = "missing"        // secret configured, header absent
	SigNotConfigured SignatureResult = "not_configured" // no secret on the webhook
)

// Request is the inbound delivery as the gateway received it.
type Request struct {
	Header http.Header
	Body   []byte
	Now    time.Time
	// Method and URLs are for providers that sign the request line (Twilio,
	// HubSpot v3, Square). URLs are the addresses the provider may have been given
	// for this webhook, full with query string, most likely first: the configured
	// public base, then the host the request arrived on. A wrong candidate can only
	// fail verification; it can't make a forged request pass.
	Method string
	URLs   []string
}

// Config is the per-webhook configuration a provider may need.
type Config struct {
	Secret          []byte
	SignatureHeader string // generic only
}

// Provider describes one webhook source.
type Provider interface {
	Name() string
	Verify(r Request, c Config) SignatureResult
	// DedupKey returns the provider's delivery or event ID, or "" to fall back to a body hash.
	DedupKey(r Request) string
	EventType(r Request) string
}

var registry = map[string]Provider{}

func register(p Provider) { registry[p.Name()] = p }

func init() {
	register(generic{})
	register(razorpay{})
	register(stripe{tolerance: 5 * time.Minute})
	register(shopify{})
	register(github{})
	register(standardWebhooks{tolerance: 5 * time.Minute})
	register(cashfree{tolerance: 5 * time.Minute})
	register(payu{})
	register(phonepe{})
	register(jiraProvider{})
	register(slack{tolerance: 5 * time.Minute})
	register(twilio{})
	register(hubspot{tolerance: 5 * time.Minute})
	register(square{})
	register(segment{})
	register(sendgrid{})
	register(notion{})
}

// Get returns the named provider, or false if it is unknown.
func Get(name string) (Provider, bool) {
	p, ok := registry[name]
	return p, ok
}

// Names lists supported providers, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DedupKey returns the provider's key, or "sha256:<hex of body>" when it has none.
func DedupKey(p Provider, r Request) string {
	if k := p.DedupKey(r); k != "" {
		return k
	}
	sum := sha256.Sum256(r.Body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ---- generic: hex HMAC-SHA256 of the body in a configurable header ----------

type generic struct{}

func (generic) Name() string { return "generic" }

func (generic) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	header := c.SignatureHeader
	if header == "" {
		header = "X-Signature"
	}
	got := r.Header.Get(header)
	if got == "" {
		return SigMissing
	}
	got = strings.TrimPrefix(got, "sha256=")
	return compareHex(got, hmacSHA256(c.Secret, r.Body))
}

func (generic) DedupKey(r Request) string {
	for _, h := range []string{"Idempotency-Key", "X-Idempotency-Key", "X-Event-Id", "X-Request-Id", "X-Delivery-Id"} {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	return jsonString(r.Body, "id")
}

func (generic) EventType(r Request) string {
	if v := r.Header.Get("X-Event-Type"); v != "" {
		return v
	}
	for _, k := range []string{"type", "event", "event_type", "topic"} {
		if v := jsonString(r.Body, k); v != "" {
			return v
		}
	}
	return ""
}

// ---- Razorpay: X-Razorpay-Signature = hex HMAC-SHA256(body) ------------------

type razorpay struct{}

func (razorpay) Name() string { return "razorpay" }

func (razorpay) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	got := r.Header.Get("X-Razorpay-Signature")
	if got == "" {
		return SigMissing
	}
	return compareHex(got, hmacSHA256(c.Secret, r.Body))
}

func (razorpay) DedupKey(r Request) string  { return r.Header.Get("X-Razorpay-Event-Id") }
func (razorpay) EventType(r Request) string { return jsonString(r.Body, "event") }

// ---- Stripe: Stripe-Signature: t=<unix>,v1=<hex HMAC(t + "." + body)> -------

type stripe struct{ tolerance time.Duration }

func (stripe) Name() string { return "stripe" }

func (s stripe) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	header := r.Header.Get("Stripe-Signature")
	if header == "" {
		return SigMissing
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return SigInvalid
	}
	if age := r.Now.Sub(time.Unix(unix, 0)); age > s.tolerance || age < -s.tolerance {
		return SigInvalid
	}
	want := hmacSHA256(c.Secret, append([]byte(ts+"."), r.Body...))
	for _, sig := range sigs {
		if compareHex(sig, want) == SigValid {
			return SigValid
		}
	}
	return SigInvalid
}

func (stripe) DedupKey(r Request) string  { return jsonString(r.Body, "id") }
func (stripe) EventType(r Request) string { return jsonString(r.Body, "type") }

// ---- Shopify: X-Shopify-Hmac-Sha256 = base64 HMAC-SHA256(body) --------------

type shopify struct{}

func (shopify) Name() string { return "shopify" }

func (shopify) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	got := r.Header.Get("X-Shopify-Hmac-Sha256")
	if got == "" {
		return SigMissing
	}
	gotRaw, err := base64.StdEncoding.DecodeString(got)
	if err != nil || !hmac.Equal(gotRaw, hmacSHA256(c.Secret, r.Body)) {
		return SigInvalid
	}
	return SigValid
}

func (shopify) DedupKey(r Request) string {
	if v := r.Header.Get("X-Shopify-Event-Id"); v != "" {
		return v
	}
	return r.Header.Get("X-Shopify-Webhook-Id")
}
func (shopify) EventType(r Request) string { return r.Header.Get("X-Shopify-Topic") }

// ---- GitHub: X-Hub-Signature-256 = "sha256=" + hex HMAC-SHA256(body) --------

type github struct{}

func (github) Name() string { return "github" }

func (github) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	got := r.Header.Get("X-Hub-Signature-256")
	if got == "" {
		return SigMissing
	}
	hexSig, ok := strings.CutPrefix(got, "sha256=")
	if !ok {
		return SigInvalid
	}
	return compareHex(hexSig, hmacSHA256(c.Secret, r.Body))
}

func (github) DedupKey(r Request) string  { return r.Header.Get("X-GitHub-Delivery") }
func (github) EventType(r Request) string { return r.Header.Get("X-GitHub-Event") }

// ---- Standard Webhooks (standardwebhooks.com; Svix and the senders built on it) --
//
// webhook-id, webhook-timestamp and webhook-signature headers (svix-* on Svix).
// The signature header is a space-separated list of "v1,<base64 HMAC-SHA256>"
// over "<id>.<timestamp>.<body>", keyed with the base64 part of a whsec_ secret,
// or "v1a,<base64 Ed25519>" checked against a whpk_ public key.

type standardWebhooks struct{ tolerance time.Duration }

func (standardWebhooks) Name() string { return "standardwebhooks" }

// swHeader reads a Standard Webhooks header, or its svix- equivalent.
func swHeader(h http.Header, name string) string {
	if v := h.Get("webhook-" + name); v != "" {
		return v
	}
	return h.Get("svix-" + name)
}

func (s standardWebhooks) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	id, ts, header := swHeader(r.Header, "id"), swHeader(r.Header, "timestamp"), swHeader(r.Header, "signature")
	if header == "" {
		return SigMissing
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || id == "" {
		return SigInvalid
	}
	if age := r.Now.Sub(time.Unix(unix, 0)); age > s.tolerance || age < -s.tolerance {
		return SigInvalid
	}
	msg := append([]byte(id+"."+ts+"."), r.Body...)

	secret := strings.TrimSpace(string(c.Secret))
	if pub, ok := strings.CutPrefix(secret, "whpk_"); ok {
		key, err := base64.StdEncoding.DecodeString(pub)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return SigInvalid
		}
		for _, sig := range swSignatures(header, "v1a") {
			if ed25519.Verify(key, msg, sig) {
				return SigValid
			}
		}
		return SigInvalid
	}
	// The key is the base64 after "whsec_"; a secret that isn't base64 is used as is.
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		key = c.Secret
	}
	want := hmacSHA256(key, msg)
	for _, sig := range swSignatures(header, "v1") {
		if hmac.Equal(sig, want) {
			return SigValid
		}
	}
	return SigInvalid
}

// swSignatures returns the decoded signatures of one version from a signature header.
func swSignatures(header, version string) [][]byte {
	var out [][]byte
	for _, part := range strings.Fields(header) {
		v, sig, ok := strings.Cut(part, ",")
		if !ok || v != version {
			continue
		}
		if raw, err := base64.StdEncoding.DecodeString(sig); err == nil {
			out = append(out, raw)
		}
	}
	return out
}

func (standardWebhooks) DedupKey(r Request) string { return swHeader(r.Header, "id") }

func (standardWebhooks) EventType(r Request) string {
	for _, k := range []string{"type", "event_type", "event"} {
		if v := jsonString(r.Body, k); v != "" {
			return v
		}
	}
	return ""
}

// ---- Cashfree (PG): x-webhook-signature = base64 HMAC-SHA256(timestamp + body) --
//
// Keyed with the merchant's PG secret key; x-webhook-timestamp is in milliseconds.

type cashfree struct{ tolerance time.Duration }

func (cashfree) Name() string { return "cashfree" }

func (s cashfree) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	got, ts := r.Header.Get("X-Webhook-Signature"), r.Header.Get("X-Webhook-Timestamp")
	if got == "" {
		return SigMissing
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return SigInvalid
	}
	sent := time.UnixMilli(n)
	if n < 1e12 { // seconds, not milliseconds
		sent = time.Unix(n, 0)
	}
	if age := r.Now.Sub(sent); age > s.tolerance || age < -s.tolerance {
		return SigInvalid
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(got))
	if err != nil || !hmac.Equal(raw, hmacSHA256(c.Secret, append([]byte(ts), r.Body...))) {
		return SigInvalid
	}
	return SigValid
}

// DedupKey uses Cashfree's idempotency header (webhook version 2025-01-01 on),
// else the body hash: Cashfree retries resend the same payload.
func (cashfree) DedupKey(r Request) string  { return r.Header.Get("X-Idempotency-Key") }
func (cashfree) EventType(r Request) string { return jsonString(r.Body, "type") }

// ---- PayU (India): reverse hash in the body -------------------------------------
//
// PayU posts the transaction (form-encoded or JSON) with a "hash" field:
// sha512 hex of [additional_charges|]SALT|status|udf10..udf1|email|firstname|
// productinfo|amount|txnid|key. The webhook's secret is the merchant salt.

type payu struct{}

func (payu) Name() string { return "payu" }

func (payu) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	f := bodyFields(r.Body)
	if f["hash"] == "" {
		return SigMissing
	}
	got, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(f["hash"])))
	if err != nil {
		return SigInvalid
	}
	parts := []string{strings.TrimSpace(string(c.Secret)), f["status"]}
	for i := 10; i >= 1; i-- {
		parts = append(parts, f["udf"+strconv.Itoa(i)])
	}
	parts = append(parts, f["email"], f["firstname"], f["productinfo"], f["amount"], f["txnid"], f["key"])
	s := strings.Join(parts, "|")
	if f["additional_charges"] != "" {
		s = f["additional_charges"] + "|" + s
	}
	want := sha512.Sum512([]byte(s))
	if !hmac.Equal(got, want[:]) {
		return SigInvalid
	}
	return SigValid
}

// DedupKey is PayU's payment ID plus status: a payment's success and a later
// refund or failure update are different events.
func (payu) DedupKey(r Request) string {
	f := bodyFields(r.Body)
	if f["mihpayid"] == "" {
		return ""
	}
	return f["mihpayid"] + ":" + f["status"]
}

func (payu) EventType(r Request) string {
	if s := bodyFields(r.Body)["status"]; s != "" {
		return "payment." + strings.ToLower(s)
	}
	return ""
}

// ---- PhonePe (PG): Authorization = hex SHA256("username:password") ------------
//
// The webhook's secret is "username:password" as configured in the PhonePe
// dashboard. This proves the sender but not the body: PhonePe signs no payload.

type phonepe struct{}

func (phonepe) Name() string { return "phonepe" }

func (phonepe) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	if got == "" {
		return SigMissing
	}
	if len(got) > 7 && strings.EqualFold(got[:7], "SHA256 ") {
		got = strings.TrimSpace(got[7:])
	}
	want := sha256.Sum256([]byte(strings.TrimSpace(string(c.Secret))))
	return compareHex(got, want[:])
}

// DedupKey is the event plus the order (or refund) and its state.
func (phonepe) DedupKey(r Request) string {
	var m struct {
		Event   string `json:"event"`
		Payload struct {
			OrderID  string `json:"orderId"`
			RefundID string `json:"refundId"`
			State    string `json:"state"`
		} `json:"payload"`
	}
	if json.Unmarshal(r.Body, &m) != nil || (m.Payload.OrderID == "" && m.Payload.RefundID == "") {
		return ""
	}
	return strings.Join([]string{m.Event, m.Payload.OrderID, m.Payload.RefundID, m.Payload.State}, ":")
}

func (phonepe) EventType(r Request) string { return jsonString(r.Body, "event") }

// ---- Jira Cloud: X-Hub-Signature = "sha256=" + hex HMAC-SHA256(secret, body) ---
//
// Jira system webhooks (Settings → System → WebHooks) sign the body when a secret
// is set. X-Atlassian-Webhook-Identifier names the delivery and stays the same on
// retries; the event type is the body's webhookEvent, e.g. "jira:issue_created".

type jiraProvider struct{}

func (jiraProvider) Name() string { return "jira" }

func (jiraProvider) Verify(r Request, c Config) SignatureResult {
	if len(c.Secret) == 0 {
		return SigNotConfigured
	}
	got := r.Header.Get("X-Hub-Signature")
	if got == "" {
		return SigMissing
	}
	method, hexSig, ok := strings.Cut(got, "=")
	if !ok || !strings.EqualFold(method, "sha256") {
		return SigInvalid
	}
	return compareHex(hexSig, hmacSHA256(c.Secret, r.Body))
}

func (jiraProvider) DedupKey(r Request) string {
	if id := r.Header.Get("X-Atlassian-Webhook-Identifier"); id != "" {
		return id
	}
	// Older senders: the event, its time and what it is about.
	var m struct {
		Event     string              `json:"webhookEvent"`
		Timestamp json.Number         `json:"timestamp"`
		Issue     struct{ ID string } `json:"issue"`
		Comment   struct{ ID string } `json:"comment"`
	}
	if json.Unmarshal(r.Body, &m) != nil || m.Event == "" || m.Timestamp == "" {
		return ""
	}
	return strings.Join([]string{m.Event, m.Timestamp.String(), m.Issue.ID, m.Comment.ID}, ":")
}

func (jiraProvider) EventType(r Request) string { return jsonString(r.Body, "webhookEvent") }

// ---- helpers ------------------------------------------------------------------

// bodyFields reads a flat form-encoded or JSON object body into strings.
func bodyFields(body []byte) map[string]string {
	out := map[string]string{}
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // keep "10.00" as sent: hashes cover the exact text
	if dec.Decode(&m) == nil {
		for k, v := range m {
			switch x := v.(type) {
			case string:
				out[k] = x
			case json.Number:
				out[k] = x.String()
			}
		}
		return out
	}
	if q, err := url.ParseQuery(string(body)); err == nil {
		for k := range q {
			out[k] = q.Get(k)
		}
	}
	return out
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

func compareHex(gotHex string, want []byte) SignatureResult {
	got, err := hex.DecodeString(strings.TrimSpace(gotHex))
	if err != nil || !hmac.Equal(got, want) {
		return SigInvalid
	}
	return SigValid
}

// jsonString returns a top-level string field of a JSON object body, or "".
func jsonString(body []byte, key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return s
}
