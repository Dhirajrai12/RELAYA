package delivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relaya/internal/provider"
)

// Outbound signatures must pass a Standard Webhooks verifier: Relaya's own
// inbound one, which is tested against the spec's published example.
func TestStandardSignatureVerifies(t *testing.T) {
	secret := NewStandardSecret()
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("secret %q", secret)
	}
	var got http.Header
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	s := NewSender(Policy{AllowHTTP: true, AllowPrivate: true})
	res := s.Send(context.Background(), Request{
		URL: srv.URL, Secret: []byte(secret), Timeout: 5 * time.Second, Scheme: "standard",
		EventID: "msg_123", DeliveryID: "d1", EventType: "invoice.paid", Attempt: 2,
		Body: []byte(`{"type":"invoice.paid","data":{"id":"in_1"}}`),
	})
	if res.StatusCode != 200 {
		t.Fatalf("send: %+v", res)
	}
	if got.Get("webhook-id") != "msg_123" || got.Get("webhook-timestamp") == "" || !strings.HasPrefix(got.Get("webhook-signature"), "v1,") {
		t.Fatalf("headers: %v", got)
	}
	for k := range got {
		if strings.HasPrefix(strings.ToLower(k), "relaya-") || strings.EqualFold(k, "Idempotency-Key") {
			t.Errorf("outbound request carries %s", k)
		}
	}
	p, _ := provider.Get("standardwebhooks")
	req := provider.Request{Header: got, Body: body, Now: time.Now()}
	if v := p.Verify(req, provider.Config{Secret: []byte(secret)}); v != provider.SigValid {
		t.Fatalf("verify: %s", v)
	}
	if v := p.Verify(req, provider.Config{Secret: []byte(NewStandardSecret())}); v != provider.SigInvalid {
		t.Fatalf("other secret: %s", v)
	}
}
