package delivery

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Policy decides which destination URLs are allowed. Production blocks plain
// HTTP and every private/internal address, so a customer can't use Relaya to
// reach our own API, database or cloud metadata (SSRF).
type Policy struct {
	AllowHTTP    bool // allow http:// destinations (dev only)
	AllowPrivate bool // allow loopback/private/link-local targets (dev and tests only)
}

// blockedPrefixes are non-public ranges beyond what netip's helpers cover.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),  // documentation
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can map to private IPv4
	netip.MustParsePrefix("2001:db8::/32"),
}

// IsBlockedIP reports whether ip is loopback, private, link-local (incl. cloud
// metadata 169.254.169.254), multicast, unspecified or otherwise non-public.
func IsBlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURL checks a destination URL when it is saved. The real enforcement
// happens again at connect time (after DNS), see Client.
func (p Policy) ValidateURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("URL is too long")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("URL must be absolute, like https://api.example.com/webhooks")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.AllowHTTP {
			return errors.New("URL must use https")
		}
	default:
		return errors.New("URL must use https")
	}
	if u.User != nil {
		return errors.New("URL must not contain a username or password")
	}
	if u.Fragment != "" {
		return errors.New("URL must not contain a #fragment")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		if !p.AllowPrivate {
			return ErrBlockedAddress
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil && IsBlockedIP(ip) && !p.AllowPrivate {
		return ErrBlockedAddress
	}
	return nil
}

// Client returns an HTTP client that refuses to connect to blocked addresses
// (checked on the resolved IP, so DNS tricks and rebinding don't get around it)
// and never follows redirects.
func (p Policy) Client() *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if p.AllowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || IsBlockedIP(ip) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil, // never route customer traffic through an env proxy
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
