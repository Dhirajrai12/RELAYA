package ingest

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		trust  bool
		remote string
		hdr    map[string]string
		want   string
	}{
		{"direct", false, "203.0.113.9:4000", nil, "203.0.113.9"},
		{"untrusted headers ignored", false, "127.0.0.1:4000", map[string]string{"X-Real-IP": "6.6.6.6"}, "127.0.0.1"},
		{"x-real-ip from proxy", true, "127.0.0.1:4000", map[string]string{"X-Real-IP": "203.0.113.9"}, "203.0.113.9"},
		{"x-real-ip with port", true, "127.0.0.1:4000", map[string]string{"X-Real-IP": "203.0.113.9:51234"}, "203.0.113.9"},
		{"arr xff with port", true, "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "203.0.113.9:51234"}, "203.0.113.9"},
		{"xff spoof prefix ignored", true, "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.9:51234"}, "203.0.113.9"},
		{"ipv6 with port", true, "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "[2001:db8::1]:51234"}, "2001:db8::1"},
		{"ipv6 bare", true, "127.0.0.1:4000", map[string]string{"X-Real-IP": "2001:db8::1"}, "2001:db8::1"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("POST", "/v1/in/x", nil)
		r.RemoteAddr = tc.remote
		for k, v := range tc.hdr {
			r.Header.Set(k, v)
		}
		got := (&Handler{TrustProxyHeaders: tc.trust}).clientIP(r)
		if got == nil || *got != tc.want {
			t.Errorf("%s: got %v, want %s", tc.name, got, tc.want)
		}
	}
}
