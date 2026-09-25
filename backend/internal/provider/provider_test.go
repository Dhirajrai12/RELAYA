package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

var secret = []byte("shh")

func sign(msg []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(msg)
	return m.Sum(nil)
}

func req(body string, headers ...string) Request {
	h := http.Header{}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Set(headers[i], headers[i+1])
	}
	return Request{Header: h, Body: []byte(body), Now: time.Unix(1_700_000_000, 0)}
}

func TestRazorpay(t *testing.T) {
	p, _ := Get("razorpay")
	body := `{"event":"payment.captured","payload":{}}`
	good := hex.EncodeToString(sign([]byte(body)))

	cases := []struct {
		name string
		r    Request
		c    Config
		want SignatureResult
	}{
		{"valid", req(body, "X-Razorpay-Signature", good), Config{Secret: secret}, SigValid},
		{"tampered body", req(body+" ", "X-Razorpay-Signature", good), Config{Secret: secret}, SigInvalid},
		{"missing header", req(body), Config{Secret: secret}, SigMissing},
		{"no secret", req(body, "X-Razorpay-Signature", good), Config{}, SigNotConfigured},
		{"garbage", req(body, "X-Razorpay-Signature", "zz"), Config{Secret: secret}, SigInvalid},
	}
	for _, tc := range cases {
		if got := p.Verify(tc.r, tc.c); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}

	r := req(body, "X-Razorpay-Event-Id", "evt_1")
	if p.EventType(r) != "payment.captured" || DedupKey(p, r) != "evt_1" {
		t.Errorf("type=%q dedup=%q", p.EventType(r), DedupKey(p, r))
	}
}

func TestStripe(t *testing.T) {
	p, _ := Get("stripe")
	body := `{"id":"evt_9","type":"invoice.paid"}`
	ts := "1700000000"
	sig := hex.EncodeToString(sign([]byte(ts + "." + body)))

	ok := req(body, "Stripe-Signature", fmt.Sprintf("t=%s,v1=deadbeef,v1=%s", ts, sig))
	if got := p.Verify(ok, Config{Secret: secret}); got != SigValid {
		t.Fatalf("valid: got %s", got)
	}

	stale := ok
	stale.Now = stale.Now.Add(10 * time.Minute)
	if got := p.Verify(stale, Config{Secret: secret}); got != SigInvalid {
		t.Errorf("stale timestamp: got %s", got)
	}

	if p.EventType(ok) != "invoice.paid" || DedupKey(p, ok) != "evt_9" {
		t.Error("type/dedup extraction failed")
	}
}

func TestShopifyAndGitHub(t *testing.T) {
	body := `{"id":1}`
	shop, _ := Get("shopify")
	r := req(body,
		"X-Shopify-Hmac-Sha256", base64.StdEncoding.EncodeToString(sign([]byte(body))),
		"X-Shopify-Topic", "orders/create",
		"X-Shopify-Webhook-Id", "w1")
	if shop.Verify(r, Config{Secret: secret}) != SigValid || shop.EventType(r) != "orders/create" || DedupKey(shop, r) != "w1" {
		t.Error("shopify")
	}

	gh, _ := Get("github")
	r = req(body,
		"X-Hub-Signature-256", "sha256="+hex.EncodeToString(sign([]byte(body))),
		"X-GitHub-Event", "push",
		"X-GitHub-Delivery", "d1")
	if gh.Verify(r, Config{Secret: secret}) != SigValid || gh.EventType(r) != "push" || DedupKey(gh, r) != "d1" {
		t.Error("github")
	}
}

func TestGenericFallbacks(t *testing.T) {
	p, _ := Get("generic")
	body := `{"event":"order.shipped"}`
	r := req(body, "X-My-Sig", "sha256="+hex.EncodeToString(sign([]byte(body))))
	if got := p.Verify(r, Config{Secret: secret, SignatureHeader: "X-My-Sig"}); got != SigValid {
		t.Errorf("custom header: %s", got)
	}
	if p.EventType(r) != "order.shipped" {
		t.Error("event type fallback")
	}
	if k := DedupKey(p, r); !strings.HasPrefix(k, "sha256:") {
		t.Errorf("expected body hash dedup key, got %q", k)
	}
}
