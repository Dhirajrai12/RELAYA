package delivery

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"relaya/internal/mask"
)

// Request is one outbound delivery attempt.
type Request struct {
	URL         string
	Secret      []byte
	Timeout     time.Duration
	EventID     string // platform event ID
	DeliveryID  string // stable across retries; sent as Idempotency-Key
	EventType   string
	Attempt     int
	Body        []byte
	ContentType string
	Headers     map[string]string // original (masked) provider headers, lowercase names
}

// Sender performs attempts with a policy-enforcing HTTP client.
type Sender struct {
	Client *http.Client
	Now    func() time.Time
}

func NewSender(p Policy) *Sender {
	return &Sender{Client: p.Client(), Now: time.Now}
}

const maxResponseBody = 4 << 10

// skipHeaders are never forwarded: hop-by-hop, proxy/infrastructure, or set by us.
var skipHeaders = map[string]bool{
	"host": true, "content-length": true, "connection": true, "keep-alive": true,
	"transfer-encoding": true, "te": true, "trailer": true, "upgrade": true,
	"proxy-connection": true, "proxy-authenticate": true, "proxy-authorization": true,
	"forwarded": true, "via": true, "x-real-ip": true, "max-forwards": true,
	"accept-encoding": true, "content-type": true, "user-agent": true, "idempotency-key": true,
	"x-arr-log-id": true, "x-arr-ssl": true, "x-original-url": true,
}

// Send makes one attempt. It never returns an error: failures are in Result.
func (s *Sender) Send(ctx context.Context, r Request) Result {
	now := s.Now()
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return Result{Err: err}
	}

	// Forward the provider's headers so existing receivers (and their own
	// provider-signature checks) keep working unchanged.
	for name, v := range r.Headers {
		if skipHeaders[name] || strings.HasPrefix(name, "x-forwarded-") || strings.HasPrefix(name, "relaya-") || v == mask.Redacted {
			continue
		}
		req.Header.Set(name, v)
	}
	ct := r.ContentType
	if ct == "" {
		ct = "application/json"
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("User-Agent", "Relaya-Webhooks/1.0")
	req.Header.Set("Idempotency-Key", r.DeliveryID)
	req.Header.Set("Relaya-Event-Id", r.EventID)
	req.Header.Set("Relaya-Delivery-Id", r.DeliveryID)
	req.Header.Set("Relaya-Attempt", strconv.Itoa(r.Attempt))
	if r.EventType != "" {
		req.Header.Set("Relaya-Event-Type", r.EventType)
	}
	req.Header.Set(SignatureHeader, Sign(r.Secret, now, r.Body))

	start := time.Now()
	resp, err := s.Client.Do(req)
	res := Result{Duration: time.Since(start)}
	if err != nil {
		res.Err = cleanError(err)
		return res
	}
	defer resp.Body.Close()

	res.StatusCode = resp.StatusCode
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	res.Body = strings.ToValidUTF8(string(mask.JSON(raw)), "�")
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		res.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), now)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		res.Err = errors.New("redirects are not followed; update the destination URL to the final address")
	}
	return res
}

// cleanError turns transport errors into short, OS-independent messages that
// are safe to show customers (no raw Go/Windows error text).
func cleanError(err error) error {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var opErr *net.OpError
	switch {
	case errors.Is(err, ErrBlockedAddress):
		return ErrBlockedAddress
	case errors.Is(err, context.DeadlineExceeded), os.IsTimeout(err):
		return errors.New("timed out waiting for a response")
	case errors.As(err, &dnsErr):
		return errors.New("could not resolve the destination hostname (DNS lookup failed)")
	case errors.As(err, &certErr), strings.Contains(err.Error(), "x509:"), strings.Contains(err.Error(), "tls:"):
		return errors.New("TLS error: the destination's certificate is invalid or the handshake failed")
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return errors.New("could not connect: connection refused or host unreachable")
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET), errors.As(err, &opErr):
		return errors.New("connection closed before a response was received")
	}
	return errors.New("request failed before a response was received")
}
