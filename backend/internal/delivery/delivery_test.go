package delivery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		r    Result
		want Outcome
	}{
		{Result{StatusCode: 200}, Succeeded},
		{Result{StatusCode: 204}, Succeeded},
		{Result{StatusCode: 500}, Retry},
		{Result{StatusCode: 503}, Retry},
		{Result{StatusCode: 429}, Retry},
		{Result{StatusCode: 408}, Retry},
		{Result{StatusCode: 400}, Failed},
		{Result{StatusCode: 401}, Failed},
		{Result{StatusCode: 404}, Failed},
		{Result{StatusCode: 301, Err: errors.New("redirects are not followed")}, Failed},
		{Result{Err: errors.New("timed out")}, Retry},
		{Result{Err: ErrBlockedAddress}, Failed},
	}
	for _, c := range cases {
		if got := Classify(c.r); got != c.want {
			t.Errorf("Classify(%d, %v) = %s, want %s", c.r.StatusCode, c.r.Err, got, c.want)
		}
	}
}

func TestNextDelay(t *testing.T) {
	for attempt, base := range map[int]time.Duration{1: 30 * time.Second, 3: 10 * time.Minute, 7: 6 * time.Hour, 50: 6 * time.Hour} {
		d := NextDelay(attempt, 0)
		if d < base*8/10 || d > base*12/10 {
			t.Errorf("attempt %d: %v not within 20%% of %v", attempt, d, base)
		}
	}
	if d := NextDelay(1, 10*time.Minute); d != 10*time.Minute {
		t.Errorf("Retry-After should win when longer: %v", d)
	}
	if d := NextDelay(1, 48*time.Hour); d != maxRetryAfter {
		t.Errorf("Retry-After should be capped: %v", d)
	}
}

func TestSignVerify(t *testing.T) {
	secret := []byte(NewSecret())
	body := []byte(`{"a":1}`)
	now := time.Unix(1_800_000_000, 0)
	h := Sign(secret, now, body)
	if !strings.HasPrefix(h, "t=1800000000,v1=") {
		t.Fatalf("header %q", h)
	}
	if !Verify(secret, h, body, now.Add(time.Minute), 5*time.Minute) {
		t.Fatal("valid signature rejected")
	}
	if Verify(secret, h, []byte(`{"a":2}`), now, 5*time.Minute) {
		t.Fatal("tampered body accepted")
	}
	if Verify(secret, h, body, now.Add(10*time.Minute), 5*time.Minute) {
		t.Fatal("stale signature accepted")
	}
	if Verify([]byte("other"), h, body, now, 5*time.Minute) {
		t.Fatal("wrong secret accepted")
	}
}

func TestBlockedIPs(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "64:ff9b::a00:1"}
	public := []string{"8.8.8.8", "162.246.23.85", "2606:4700:4700::1111"}
	for _, s := range blocked {
		if !IsBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range public {
		if IsBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestValidateURL(t *testing.T) {
	prod := Policy{}
	for _, bad := range []string{"http://example.com/x", "ftp://example.com", "https://user:pw@example.com", "https://127.0.0.1/x", "https://localhost/x", "https://169.254.169.254/latest", "/relative", "https://[::1]/x"} {
		if prod.ValidateURL(bad) == nil {
			t.Errorf("%q should be rejected in production", bad)
		}
	}
	if err := prod.ValidateURL("https://api.example.com/webhooks/relaya?x=1"); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
	dev := Policy{AllowHTTP: true, AllowPrivate: true}
	if err := dev.ValidateURL("http://localhost:3000/hook"); err != nil {
		t.Errorf("dev should allow local http: %v", err)
	}
}

// The client must refuse loopback targets at connect time, even when the URL
// passed validation (e.g. a hostname that resolves to 127.0.0.1).
func TestClientBlocksPrivateAtDial(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()

	s := NewSender(Policy{AllowHTTP: true})
	res := s.Send(context.Background(), Request{URL: srv.URL, Secret: []byte("s"), Timeout: 2 * time.Second, Body: []byte("{}")})
	if !errors.Is(res.Err, ErrBlockedAddress) || hit {
		t.Fatalf("expected blocked dial, got %v (hit=%v)", res.Err, hit)
	}
	if Classify(res) != Failed {
		t.Fatal("blocked address must not be retried")
	}
}

func TestSendForwardsHeadersAndSigns(t *testing.T) {
	var got *http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"ok":true,"token":"secret-token"}`))
	}))
	defer srv.Close()

	secret := []byte("rsec_test")
	s := NewSender(Policy{AllowHTTP: true, AllowPrivate: true})
	res := s.Send(context.Background(), Request{
		URL: srv.URL, Secret: secret, Timeout: 2 * time.Second,
		EventID: "evt-1", DeliveryID: "dlv-1", EventType: "payment.captured", Attempt: 2,
		Body: []byte(`{"event":"payment.captured"}`), ContentType: "application/json",
		Headers: map[string]string{
			"x-razorpay-signature": "abc", "x-forwarded-for": "1.2.3.4", "host": "in.example",
			"x-api-key": "[REDACTED]", "user-agent": "Razorpay-Webhook/v1",
		},
	})
	if res.StatusCode != 202 || res.Err != nil || Classify(res) != Succeeded {
		t.Fatalf("result %+v", res)
	}
	h := got.Header
	if h.Get("X-Razorpay-Signature") != "abc" {
		t.Error("provider signature header not forwarded")
	}
	if h.Get("X-Forwarded-For") != "" || h.Get("X-Api-Key") != "" {
		t.Error("infrastructure or redacted headers forwarded")
	}
	if h.Get("Idempotency-Key") != "dlv-1" || h.Get("Relaya-Attempt") != "2" || h.Get("Relaya-Event-Type") != "payment.captured" {
		t.Errorf("relaya headers: %v", h)
	}
	if !Verify(secret, h.Get(SignatureHeader), gotBody, time.Now(), time.Minute) {
		t.Error("forwarded request signature does not verify")
	}
	if strings.Contains(res.Body, "secret-token") {
		t.Error("response body should be masked")
	}
}

func TestSendRedirectAndRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/elsewhere", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	s := NewSender(Policy{AllowHTTP: true, AllowPrivate: true})

	res := s.Send(context.Background(), Request{URL: srv.URL + "/moved", Secret: []byte("s"), Timeout: time.Second})
	if res.StatusCode != 301 || Classify(res) != Failed {
		t.Errorf("redirect: %+v %s", res, Classify(res))
	}
	res = s.Send(context.Background(), Request{URL: srv.URL, Secret: []byte("s"), Timeout: time.Second})
	if res.StatusCode != 429 || res.RetryAfter != 2*time.Minute || Classify(res) != Retry {
		t.Errorf("429: %+v", res)
	}
}

func TestCleanErrorsAreFriendly(t *testing.T) {
	s := NewSender(Policy{AllowHTTP: true, AllowPrivate: true})
	// Closed port on loopback: refused on every OS, but the message must not leak OS text.
	res := s.Send(context.Background(), Request{URL: "http://127.0.0.1:1/x", Secret: []byte("s"), Timeout: 3 * time.Second})
	if res.Err == nil || res.Err.Error() != "could not connect: connection refused or host unreachable" {
		t.Fatalf("refused: %v", res.Err)
	}
	if Classify(res) != Retry {
		t.Fatal("connection refused should be retried")
	}
	res = s.Send(context.Background(), Request{URL: "http://no-such-host.invalid/x", Secret: []byte("s"), Timeout: 3 * time.Second})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "DNS") {
		t.Fatalf("dns: %v", res.Err)
	}
}
