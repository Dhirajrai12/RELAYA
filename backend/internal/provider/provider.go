// Package provider knows how each webhook provider signs, identifies and names
// its deliveries. The gateway uses it to verify signatures, pick a dedup key
// and extract the event type without understanding the payload further.
package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
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

// ---- helpers ------------------------------------------------------------------

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
