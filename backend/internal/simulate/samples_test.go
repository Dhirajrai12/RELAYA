package simulate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"relaya/internal/provider"
)

const target = "https://relaya.test/v1/in/in_abc123?src=test"

// sendgridKeys is a key pair like SendGrid's: Relaya is given the public key only.
func sendgridKeys(t *testing.T) (*ecdsa.PrivateKey, string) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	return priv, base64.StdEncoding.EncodeToString(der)
}

// signAsSendGrid signs the way SendGrid does, with its private key.
func signAsSendGrid(t *testing.T, priv *ecdsa.PrivateKey, body []byte, now time.Time) provider.Outgoing {
	ts := now.Format("20060102150405")
	sum := sha256.Sum256(append([]byte(ts), body...))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-Twilio-Email-Event-Webhook-Timestamp", ts)
	h.Set("X-Twilio-Email-Event-Webhook-Signature", base64.StdEncoding.EncodeToString(sig))
	return provider.Outgoing{Header: h, Body: body}
}

// Every sample of every provider, signed by Sign, must pass that provider's own
// Verify, carry the right event type, and have a dedup key.
func TestSamplesSignAndVerify(t *testing.T) {
	now := time.Now()
	priv, sendgridPub := sendgridKeys(t)
	secrets := map[string]string{"standardwebhooks": "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", "phonepe": "merchant:pa55", "sendgrid": sendgridPub}
	for _, name := range provider.Names() {
		p, _ := provider.Get(name)
		samples := Samples(name, now)
		if len(samples) == 0 || (name != "generic" && samples[0].Payload == Samples("generic", now)[0].Payload) {
			t.Errorf("%s: no samples of its own", name)
		}
		secret := secrets[name]
		if secret == "" {
			secret = "test-secret-" + name
		}
		cfg := provider.Config{Secret: []byte(secret)}
		for _, s := range samples {
			form := name == "payu" || name == "twilio"
			if !form && !json.Valid([]byte(s.Payload)) {
				t.Errorf("%s %s: invalid JSON", name, s.Type)
			}
			out, err := provider.Sign(name, []byte(s.Payload), cfg, now, s.Type, "dlv_1", target)
			if name == "sendgrid" { // only SendGrid holds the private key
				if !errors.Is(err, provider.ErrCannotSign) {
					t.Fatalf("sendgrid sign with a public key: %v", err)
				}
				out, err = signAsSendGrid(t, priv, []byte(s.Payload), now), nil
			}
			if err != nil {
				t.Fatalf("%s %s: sign: %v", name, s.Type, err)
			}
			req := provider.Request{Header: out.Header, Body: out.Body, Now: now, Method: "POST", URLs: []string{"https://other.test/x", target}}
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
			other := "whsec_b3RoZXItc2VjcmV0LW90aGVyLXNlY3JldA=="
			if name == "sendgrid" {
				_, other = sendgridKeys(t)
			}
			if got := p.Verify(req, provider.Config{Secret: []byte(other)}); got == provider.SigValid {
				t.Errorf("%s %s: verified with another secret", name, s.Type)
			}
			// A changed body doesn't pass (PhonePe signs the sender, not the body).
			if name != "phonepe" {
				tampered := req
				tampered.Body = []byte(strings.Replace(string(req.Body), "e", "E", 1))
				if name == "payu" { // PayU's hash covers the payment fields, not every field
					tampered.Body = []byte(strings.Replace(string(req.Body), "amount=499.00", "amount=1.00", 1))
				}
				if got := p.Verify(tampered, cfg); got == provider.SigValid {
					t.Errorf("%s %s: tampered body verified", name, s.Type)
				}
			}
			// Unsigned (a webhook without a secret) is built and accepted as not configured.
			plain, err := provider.Sign(name, []byte(s.Payload), provider.Config{}, now, s.Type, "dlv_2", target)
			if err != nil || p.Verify(provider.Request{Header: plain.Header, Body: plain.Body, Now: now, Method: "POST", URLs: []string{target}}, provider.Config{}) != provider.SigNotConfigured {
				t.Errorf("%s %s: unsigned: %v", name, s.Type, err)
			}
		}
	}
	// Fresh IDs every time.
	if a, b := Samples("stripe", now)[0].Payload, Samples("stripe", now)[0].Payload; a == b {
		t.Error("samples repeat their IDs")
	}
	// A public key can't sign.
	if _, err := provider.Sign("standardwebhooks", []byte(`{}`), provider.Config{Secret: []byte("whpk_abc")}, now, "x", "id", target); !errors.Is(err, provider.ErrCannotSign) {
		t.Errorf("whpk_: %v", err)
	}
}

// Providers that sign the URL only pass with the URL they were given.
func TestURLSignedProviders(t *testing.T) {
	now := time.Now()
	for _, name := range []string{"twilio", "hubspot", "square"} {
		p, _ := provider.Get(name)
		s := Samples(name, now)[0]
		cfg := provider.Config{Secret: []byte("url-secret")}
		out, _ := provider.Sign(name, []byte(s.Payload), cfg, now, s.Type, "d", target)
		if got := p.Verify(provider.Request{Header: out.Header, Body: out.Body, Now: now, Method: "POST", URLs: []string{"https://relaya.test/v1/in/in_other"}}, cfg); got == provider.SigValid {
			t.Errorf("%s: verified against another URL", name)
		}
		if got := p.Verify(provider.Request{Header: out.Header, Body: out.Body, Now: now, Method: "POST", URLs: []string{target}}, cfg); got != provider.SigValid {
			t.Errorf("%s: %s", name, got)
		}
	}
}
