package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://relaya.sbs"})(next)

	// Preflight from the dashboard: credentials, the headers it asks for, PUT.
	req := httptest.NewRequest(http.MethodOptions, "/v1/orgs/x/connections/y/proxy/z", nil)
	req.Header.Set("Origin", "https://relaya.sbs")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	req.Header.Set("Access-Control-Request-Headers", "content-type, relaya-proxy-base-url")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	got := rec.Header()
	if rec.Code != http.StatusNoContent || got.Get("Access-Control-Allow-Origin") != "https://relaya.sbs" ||
		got.Get("Access-Control-Allow-Credentials") != "true" ||
		got.Get("Access-Control-Allow-Headers") != "content-type, relaya-proxy-base-url" ||
		!strings.Contains(got.Get("Access-Control-Allow-Methods"), "PUT") {
		t.Fatalf("preflight: %d %v", rec.Code, got)
	}

	// A real request exposes the proxy's headers to the page.
	req = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Origin", "https://relaya.sbs")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), "Relaya-Proxy-Attempts") {
		t.Fatalf("request: %d %v", rec.Code, rec.Header())
	}

	// Any other origin gets no CORS headers at all.
	req = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if v := rec.Header().Get("Access-Control-Allow-Origin") + rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Fatalf("other origin got CORS headers: %q", v)
	}
}
