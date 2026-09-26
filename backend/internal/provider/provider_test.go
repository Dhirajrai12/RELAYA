package provider

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
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

func TestStandardWebhooks(t *testing.T) {
	p, _ := Get("standardwebhooks")
	// The example from the Standard Webhooks spec.
	const (
		whsec = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
		id    = "msg_p5jXN8AQM9LWM0D4loKWxJek"
		ts    = "1614265330"
		body  = `{"test": 2432232314}`
		sig   = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
	)
	at := func(r Request) Request { r.Now = time.Unix(1614265330, 0); return r }
	cfg := Config{Secret: []byte(whsec)}

	cases := []struct {
		name string
		r    Request
		c    Config
		want SignatureResult
	}{
		{"valid", at(req(body, "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", sig)), cfg, SigValid},
		{"svix headers", at(req(body, "svix-id", id, "svix-timestamp", ts, "svix-signature", sig)), cfg, SigValid},
		{"one of several", at(req(body, "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", "v1,Zm9v v2,abc "+sig)), cfg, SigValid},
		{"tampered body", at(req(body+" ", "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", sig)), cfg, SigInvalid},
		{"other id", at(req(body, "webhook-id", "msg_other", "webhook-timestamp", ts, "webhook-signature", sig)), cfg, SigInvalid},
		{"stale", req(body, "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", sig), cfg, SigInvalid},
		{"wrong secret", at(req(body, "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", sig)), Config{Secret: []byte("whsec_c2ho")}, SigInvalid},
		{"missing", at(req(body, "webhook-id", id, "webhook-timestamp", ts)), cfg, SigMissing},
		{"no secret", at(req(body, "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", sig)), Config{}, SigNotConfigured},
	}
	for _, tc := range cases {
		if got := p.Verify(tc.r, tc.c); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}

	r := req(`{"type":"email.delivered","data":{}}`, "webhook-id", "msg_1")
	if p.EventType(r) != "email.delivered" || DedupKey(p, r) != "msg_1" {
		t.Errorf("type=%q dedup=%q", p.EventType(r), DedupKey(p, r))
	}
}

func TestStandardWebhooksEd25519(t *testing.T) {
	p, _ := Get("standardwebhooks")
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	body, id, ts := `{"type":"user.created"}`, "msg_2", "1700000000"
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(id+"."+ts+"."+body)))
	cfg := Config{Secret: []byte("whpk_" + base64.StdEncoding.EncodeToString(pub))}

	if got := p.Verify(req(body, "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", "v1a,"+sig), cfg); got != SigValid {
		t.Errorf("valid: got %s", got)
	}
	if got := p.Verify(req(body+" ", "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", "v1a,"+sig), cfg); got != SigInvalid {
		t.Errorf("tampered: got %s", got)
	}
	// An HMAC signature doesn't satisfy a public key.
	if got := p.Verify(req(body, "webhook-id", id, "webhook-timestamp", ts, "webhook-signature", "v1,"+sig), cfg); got != SigInvalid {
		t.Errorf("v1 against whpk: got %s", got)
	}
}

func TestCashfree(t *testing.T) {
	p, _ := Get("cashfree")
	body := `{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{"order":{"order_id":"o1"}}}`
	ts := "1700000000000" // milliseconds, as Cashfree sends it
	good := base64.StdEncoding.EncodeToString(sign([]byte(ts + body)))

	cases := []struct {
		name string
		r    Request
		c    Config
		want SignatureResult
	}{
		{"valid", req(body, "x-webhook-signature", good, "x-webhook-timestamp", ts), Config{Secret: secret}, SigValid},
		{"seconds timestamp", req(body, "x-webhook-signature", base64.StdEncoding.EncodeToString(sign([]byte("1700000000"+body))), "x-webhook-timestamp", "1700000000"), Config{Secret: secret}, SigValid},
		{"tampered body", req(body+" ", "x-webhook-signature", good, "x-webhook-timestamp", ts), Config{Secret: secret}, SigInvalid},
		{"other timestamp", req(body, "x-webhook-signature", good, "x-webhook-timestamp", "1700000001000"), Config{Secret: secret}, SigInvalid},
		{"stale", req(body, "x-webhook-signature", base64.StdEncoding.EncodeToString(sign([]byte("1699999000000"+body))), "x-webhook-timestamp", "1699999000000"), Config{Secret: secret}, SigInvalid},
		{"missing", req(body, "x-webhook-timestamp", ts), Config{Secret: secret}, SigMissing},
		{"no secret", req(body, "x-webhook-signature", good, "x-webhook-timestamp", ts), Config{}, SigNotConfigured},
	}
	for _, tc := range cases {
		if got := p.Verify(tc.r, tc.c); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
	r := req(body, "x-idempotency-key", "idem1")
	if p.EventType(r) != "PAYMENT_SUCCESS_WEBHOOK" || DedupKey(p, r) != "idem1" {
		t.Errorf("type=%q dedup=%q", p.EventType(r), DedupKey(p, r))
	}
	if k := DedupKey(p, req(body)); !strings.HasPrefix(k, "sha256:") {
		t.Errorf("no idempotency header: dedup %q", k)
	}
}

func TestPayU(t *testing.T) {
	p, _ := Get("payu")
	salt := []byte("salt1")
	// PayU's reverse hash: SALT|status||||||udf5|udf4|udf3|udf2|udf1|email|firstname|productinfo|amount|txnid|key
	hashOf := func(s string) string { h := sha512.Sum512([]byte(s)); return hex.EncodeToString(h[:]) }
	plain := "salt1|success||||||||||u1|a@b.in|Asha|Deposit|10.00|t1|KEY"
	form := func(extra string, hash string) string {
		return "mihpayid=403993715523&status=success&txnid=t1&amount=10.00&productinfo=Deposit&firstname=Asha&email=a%40b.in&udf1=u1&key=KEY" + extra + "&hash=" + hash
	}

	if got := p.Verify(req(form("", hashOf(plain))), Config{Secret: salt}); got != SigValid {
		t.Errorf("form: got %s", got)
	}
	jsonBody := `{"mihpayid":"403993715523","status":"success","txnid":"t1","amount":"10.00","productinfo":"Deposit","firstname":"Asha","email":"a@b.in","udf1":"u1","key":"KEY","hash":"` + hashOf(plain) + `"}`
	if got := p.Verify(req(jsonBody), Config{Secret: salt}); got != SigValid {
		t.Errorf("json: got %s", got)
	}
	// JSON numbers keep their exact text.
	jsonNum := strings.Replace(jsonBody, `"amount":"10.00"`, `"amount":10.00`, 1)
	if got := p.Verify(req(jsonNum), Config{Secret: salt}); got != SigValid {
		t.Errorf("json number: got %s", got)
	}
	if got := p.Verify(req(form("&additional_charges=2.50", hashOf("2.50|"+plain))), Config{Secret: salt}); got != SigValid {
		t.Errorf("additional charges: got %s", got)
	}
	if got := p.Verify(req(strings.Replace(form("", hashOf(plain)), "amount=10.00", "amount=1000.00", 1)), Config{Secret: salt}); got != SigInvalid {
		t.Errorf("tampered amount: got %s", got)
	}
	if got := p.Verify(req(form("", hashOf(plain))), Config{Secret: []byte("other")}); got != SigInvalid {
		t.Errorf("wrong salt: got %s", got)
	}
	if got := p.Verify(req("status=success&txnid=t1"), Config{Secret: salt}); got != SigMissing {
		t.Errorf("no hash: got %s", got)
	}
	r := req(form("", "x"))
	if p.EventType(r) != "payment.success" || DedupKey(p, r) != "403993715523:success" {
		t.Errorf("type=%q dedup=%q", p.EventType(r), DedupKey(p, r))
	}
}

func TestPhonePe(t *testing.T) {
	p, _ := Get("phonepe")
	body := `{"event":"checkout.order.completed","payload":{"orderId":"OMO1","state":"COMPLETED"}}`
	sum := sha256.Sum256([]byte("merchant:pa55"))
	auth := hex.EncodeToString(sum[:])
	cfg := Config{Secret: []byte("merchant:pa55")}

	for name, tc := range map[string]struct {
		header string
		c      Config
		want   SignatureResult
	}{
		"valid":          {auth, cfg, SigValid},
		"upper-case hex": {strings.ToUpper(auth), cfg, SigValid},
		"with prefix":    {"SHA256 " + auth, cfg, SigValid},
		"wrong password": {auth, Config{Secret: []byte("merchant:other")}, SigInvalid},
		"short":          {"abc", cfg, SigInvalid},
		"missing":        {"", cfg, SigMissing},
		"no secret":      {auth, Config{}, SigNotConfigured},
	} {
		r := req(body)
		if tc.header != "" {
			r = req(body, "Authorization", tc.header)
		}
		if got := p.Verify(r, tc.c); got != tc.want {
			t.Errorf("%s: got %s want %s", name, got, tc.want)
		}
	}
	r := req(body)
	if p.EventType(r) != "checkout.order.completed" || DedupKey(p, r) != "checkout.order.completed:OMO1::COMPLETED" {
		t.Errorf("type=%q dedup=%q", p.EventType(r), DedupKey(p, r))
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
