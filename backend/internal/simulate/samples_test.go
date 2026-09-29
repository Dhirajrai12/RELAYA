package simulate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"relaya/internal/provider"
)

// Every sample of every provider, signed by Sign, must pass that provider's own
// Verify, carry the right event type, and have a dedup key.
func TestSamplesSignAndVerify(t *testing.T) {
	now := time.Now()
	secrets := map[string]string{"standardwebhooks": "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", "phonepe": "merchant:pa55"}
	for _, name := range provider.Names() {
		p, _ := provider.Get(name)
		samples := Samples(name, now)
		if len(samples) == 0 {
			t.Errorf("%s: no samples", name)
		}
		secret := secrets[name]
		if secret == "" {
			secret = "test-secret-" + name
		}
		cfg := provider.Config{Secret: []byte(secret)}
		for _, s := range samples {
			if !strings.HasPrefix(s.Payload, "{") && name != "payu" {
				t.Errorf("%s %s: payload is not JSON", name, s.Type)
			}
			if strings.HasPrefix(s.Payload, "{") && !json.Valid([]byte(s.Payload)) {
				t.Errorf("%s %s: invalid JSON", name, s.Type)
			}
			out, err := provider.Sign(name, []byte(s.Payload), cfg, now, s.Type, "dlv_1")
			if err != nil {
				t.Fatalf("%s %s: sign: %v", name, s.Type, err)
			}
			req := provider.Request{Header: out.Header, Body: out.Body, Now: now}
			if got := p.Verify(req, cfg); got != provider.SigValid {
				t.Errorf("%s %s: verify = %s", name, s.Type, got)
			}
			if got := p.EventType(req); got != s.Type {
				t.Errorf("%s %s: event type = %q", name, s.Type, got)
			}
			if provider.DedupKey(p, req) == "" {
				t.Errorf("%s %s: no dedup key", name, s.Type)
			}
			// Someone else's secret doesn't pass.
			if got := p.Verify(req, provider.Config{Secret: []byte("whsec_b3RoZXItc2VjcmV0LW90aGVyLXNlY3JldA==")}); got == provider.SigValid {
				t.Errorf("%s %s: verified with another secret", name, s.Type)
			}
			// Unsigned (a webhook without a secret) is built and accepted as not configured.
			plain, err := provider.Sign(name, []byte(s.Payload), provider.Config{}, now, s.Type, "dlv_2")
			if err != nil || p.Verify(provider.Request{Header: plain.Header, Body: plain.Body, Now: now}, provider.Config{}) != provider.SigNotConfigured {
				t.Errorf("%s %s: unsigned: %v", name, s.Type, err)
			}
		}
	}
	// Fresh IDs every time.
	if a, b := Samples("stripe", now)[0].Payload, Samples("stripe", now)[0].Payload; a == b {
		t.Error("samples repeat their IDs")
	}
	// A public key can't sign.
	if _, err := provider.Sign("standardwebhooks", []byte(`{}`), provider.Config{Secret: []byte("whpk_abc")}, now, "x", "id"); !errors.Is(err, provider.ErrCannotSign) {
		t.Errorf("whpk_: %v", err)
	}
}
