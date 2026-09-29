package provider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"
)

func hexOf(b []byte) string { return hex.EncodeToString(b) }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Slack's own example (api.slack.com/authentication/verifying-requests-from-slack).
func TestSlackDocumentedExample(t *testing.T) {
	p, _ := Get("slack")
	body := "token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c"
	r := req(body, "X-Slack-Request-Timestamp", "1531420618", "X-Slack-Signature", "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503")
	r.Now = time.Unix(1531420618, 0).Add(time.Minute)
	if got := p.Verify(r, Config{Secret: []byte("8f742231b10e8888abcd99yyyzzz85a5")}); got != SigValid {
		t.Fatalf("documented example: %s", got)
	}
	r.Now = time.Unix(1531420618, 0).Add(10 * time.Minute)
	if got := p.Verify(r, Config{Secret: []byte("8f742231b10e8888abcd99yyyzzz85a5")}); got != SigInvalid {
		t.Fatalf("replayed 10 minutes later: %s", got)
	}
}

// Twilio's own example (twilio.com/docs/usage/security#validating-requests).
func TestTwilioDocumentedExample(t *testing.T) {
	p, _ := Get("twilio")
	body := "CallSid=CA1234567890ABCDE&Caller=%2B12349013030&Digits=1234&From=%2B12349013030&To=%2B18005551212"
	r := req(body, "X-Twilio-Signature", "0/KCTR6DLpKmkAf8muzZqo1nDgQ=", "Content-Type", "application/x-www-form-urlencoded")
	r.Method, r.URLs = "POST", []string{"https://mycompany.com/myapp.php?foo=1&bar=2"}
	if got := p.Verify(r, Config{Secret: []byte("12345")}); got != SigValid {
		t.Fatalf("documented example: %s", got)
	}
}

func TestSlackURLVerification(t *testing.T) {
	p, _ := Get("slack")
	ch := p.(Challenger)
	body, ct, ok := ch.Challenge(Request{Body: []byte(`{"token":"x","challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P","type":"url_verification"}`)})
	if !ok || ct != "application/json" || string(body) != `{"challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"}` {
		t.Fatalf("challenge: %s %s %v", body, ct, ok)
	}
	if _, _, ok := ch.Challenge(Request{Body: []byte(`{"type":"event_callback","event_id":"Ev1","event":{"type":"message"}}`)}); ok {
		t.Fatal("an event was treated as the handshake")
	}
	if got := p.EventType(Request{Body: []byte(`{"type":"event_callback","event":{"type":"app_mention"}}`)}); got != "app_mention" {
		t.Fatalf("event type %q", got)
	}
}

func TestNotionVerificationHandshake(t *testing.T) {
	p, _ := Get("notion")
	check := req(`{"verification_token":"test-verification-token"}`)
	// Accepted unsigned, even when a secret is set, so the subscription can be (re)verified.
	if got := p.Verify(check, Config{Secret: []byte("old")}); got != SigNotConfigured {
		t.Fatalf("handshake: %s", got)
	}
	if p.EventType(check) != "verification" {
		t.Fatalf("type %q", p.EventType(check))
	}
	// Anything else unsigned is refused once a secret is set.
	if got := p.Verify(req(`{"verification_token":"x","type":"page.created"}`), Config{Secret: []byte("old")}); got != SigMissing {
		t.Fatalf("unsigned event: %s", got)
	}
	// "sha256=" prefix required.
	body := `{"id":"e1","type":"page.created"}`
	sig := hexOf(hmacSHA256([]byte("s"), []byte(body)))
	if got := p.Verify(req(body, "X-Notion-Signature", sig), Config{Secret: []byte("s")}); got != SigInvalid {
		t.Fatalf("bare hex: %s", got)
	}
	if got := p.Verify(req(body, "X-Notion-Signature", "sha256="+sig), Config{Secret: []byte("s")}); got != SigValid {
		t.Fatalf("sha256=: %s", got)
	}
}

func TestHubSpotLegacyVersions(t *testing.T) {
	p, _ := Get("hubspot")
	body := `[{"eventId":"1","subscriptionType":"contact.creation"}]`
	secret := "yyyyyyyy-yyyy-yyyy-yyyy-yyyyyyyyyyyy"
	v1 := sha256Hex(secret + body)
	if got := p.Verify(req(body, "X-HubSpot-Signature", v1, "X-HubSpot-Signature-Version", "v1"), Config{Secret: []byte(secret)}); got != SigValid {
		t.Fatalf("v1: %s", got)
	}
	u := "https://relaya.test/v1/in/in_x"
	r := req(body, "X-HubSpot-Signature", sha256Hex(secret+"POST"+u+body), "X-HubSpot-Signature-Version", "v2")
	r.Method, r.URLs = "POST", []string{u}
	if got := p.Verify(r, Config{Secret: []byte(secret)}); got != SigValid {
		t.Fatalf("v2: %s", got)
	}
	// v3 older than 5 minutes is refused.
	old := req(body, "X-HubSpot-Signature-v3", "AAAA", "X-HubSpot-Request-Timestamp", "1000")
	old.Now, old.Method, old.URLs = time.Now(), "POST", []string{u}
	if got := p.Verify(old, Config{Secret: []byte(secret)}); got != SigInvalid {
		t.Fatalf("stale v3: %s", got)
	}
}

func TestSendGridKeyFormats(t *testing.T) {
	// SendGrid shows the verification key as base64 DER; PEM is accepted too.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	b64 := base64.StdEncoding.EncodeToString(der)
	if _, err := sendgridKey([]byte(b64)); err != nil {
		t.Fatalf("base64: %v", err)
	}
	pem := "-----BEGIN PUBLIC KEY-----\n" + b64[:64] + "\n" + b64[64:] + "\n-----END PUBLIC KEY-----\n"
	if _, err := sendgridKey([]byte(pem)); err != nil {
		t.Fatalf("pem: %v", err)
	}
	if _, err := sendgridKey([]byte("not a key")); err == nil {
		t.Fatal("garbage accepted")
	}
	p, _ := Get("sendgrid")
	if got := p.Verify(req(`[]`, "X-Twilio-Email-Event-Webhook-Signature", "MEUCIQ==", "X-Twilio-Email-Event-Webhook-Timestamp", "1"), Config{Secret: []byte(b64)}); got != SigInvalid {
		t.Fatalf("bad signature: %s", got)
	}
}
