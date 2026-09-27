package connect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrHostNotAllowed: the call would send the user's token somewhere other
// than the provider's own API hosts.
var ErrHostNotAllowed = errors.New("host not allowed")

// ProxyRequest is one API call to make with a connection's token.
type ProxyRequest struct {
	Method   string
	Path     string // appended to the base URL
	RawQuery string
	BaseURL  string // optional; must be one of the provider's API hosts
	Header   http.Header
	Body     []byte
}

// ProxyResult is the provider's answer. The caller must close Resp.Body.
type ProxyResult struct {
	Resp     *http.Response
	Host     string
	Attempts int
}

// NewProxyClient doesn't follow redirects: a redirect could carry the token to another host.
func NewProxyClient() *http.Client {
	return &http.Client{
		Timeout:       100 * time.Second, // per attempt; the handler caps the whole call
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// Proxy makes an API call with the connection's token. It renews the token
// and tries again once when the provider answers 401, and retries idempotent
// requests on 429 and 502-504 (up to 3 attempts, honoring Retry-After up to 5s).
func (s *Service) Proxy(ctx context.Context, client *http.Client, orgID, connID string, in ProxyRequest) (*ProxyResult, error) {
	t, err := s.Token(ctx, orgID, connID)
	if err != nil {
		return nil, err
	}
	p, ok := Get(t.Provider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %s", t.Provider)
	}
	base := strings.TrimRight(in.BaseURL, "/")
	if base == "" {
		base = strings.TrimRight(t.APIBase, "/")
	}
	target, host, err := proxyURL(p, t.APIBase, base, in.Path, in.RawQuery)
	if err != nil {
		return nil, err
	}

	res := &ProxyResult{Host: host}
	renewed := false
	for {
		res.Attempts++
		req, err := http.NewRequestWithContext(ctx, in.Method, target, bytes.NewReader(in.Body))
		if err != nil {
			return nil, err
		}
		for k, v := range in.Header {
			req.Header[k] = v
		}
		scheme := p.AuthScheme
		if scheme == "" {
			scheme = "Bearer"
		}
		req.Header.Set("Authorization", scheme+" "+t.AccessToken)

		resp, err := client.Do(req)
		last := res.Attempts >= 3 || ctx.Err() != nil
		if err != nil {
			if idempotent(in.Method) && !last {
				sleep(ctx, backoff(res.Attempts, ""))
				continue
			}
			return nil, &Error{Message: "could not reach " + host + ": " + errText(err)}
		}
		// 401: the token may have been revoked or rotated early. Renew once.
		if resp.StatusCode == http.StatusUnauthorized && !renewed {
			drain(resp)
			renewed = true
			if err := s.Refresh(ctx, connID, true, false); err != nil {
				return nil, err
			}
			if t, err = s.Token(ctx, orgID, connID); err != nil {
				return nil, err
			}
			continue
		}
		if retryable(resp.StatusCode) && idempotent(in.Method) && !last {
			wait := backoff(res.Attempts, resp.Header.Get("Retry-After"))
			drain(resp)
			sleep(ctx, wait)
			continue
		}
		res.Resp = resp
		return res, nil
	}
}

// proxyURL builds the target URL and checks its origin: the connection's own
// API base (validated when stored) or one of the provider's API hosts.
func proxyURL(p *Provider, apiBase, base, path, rawQuery string) (string, string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", "", fmt.Errorf("%w: invalid base URL", ErrHostNotAllowed)
	}
	origin := u.Scheme + "://" + u.Host
	allowed := false
	if b, err := url.Parse(apiBase); err == nil && b.Host != "" && origin == b.Scheme+"://"+b.Host {
		allowed = true
	}
	for _, re := range p.ProxyHosts {
		allowed = allowed || re.MatchString(origin)
	}
	if !allowed {
		return "", "", fmt.Errorf("%w: %s is not a %s API host", ErrHostNotAllowed, origin, p.Name)
	}
	if strings.Contains(path, "..") || strings.Contains(path, "\\") {
		return "", "", fmt.Errorf("%w: invalid path", ErrHostNotAllowed)
	}
	full := strings.TrimRight(u.String(), "/") + "/" + strings.TrimLeft(path, "/")
	if rawQuery != "" {
		full += "?" + rawQuery
	}
	return full, u.Host, nil
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// backoff is the provider's Retry-After (seconds, capped at 5s) or 300ms, 600msÃ¢â‚¬Â¦
func backoff(attempt int, retryAfter string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, 5*time.Second)
	}
	return time.Duration(300*(1<<(attempt-1))) * time.Millisecond
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
}
