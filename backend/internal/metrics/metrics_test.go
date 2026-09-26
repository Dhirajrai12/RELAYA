package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRoute(t *testing.T) {
	cases := map[string]string{
		"/v1/orgs/3F2504E0-4F89-41D3-9A0C-0305E82C3301/events/0f8fad5b-d9cb-469f-a165-70867728950e": "/v1/orgs/:id/events/:id",
		"/v1/in/in_abcdef123":  "/v1/in/:token",
		"/v1/orgs/x/alerts/42": "/v1/orgs/x/alerts/:n",
		"/healthz":             "/healthz",
		"/":                    "/",
	}
	for in, want := range cases {
		if got := Route(in); got != want {
			t.Errorf("Route(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandlerNeedsToken(t *testing.T) {
	reg := NewRegistry("api")
	for _, c := range []struct {
		token, auth string
		want        int
	}{{"", "", 404}, {"", "Bearer ", 404}, {"s3cret", "Bearer nope", 404}, {"s3cret", "Bearer s3cret", 200}} {
		req := httptest.NewRequest("GET", "/metrics", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rec := httptest.NewRecorder()
		Handler(c.token, reg).ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("token %q auth %q: %d, want %d", c.token, c.auth, rec.Code, c.want)
		}
	}
}

func TestMiddlewareAndOutput(t *testing.T) {
	reg := NewRegistry("ingest")
	app := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/in/") {
			time.Sleep(20 * time.Millisecond)
			return
		}
		http.NotFound(w, r)
	}))
	for _, p := range []string{"/v1/in/in_a", "/v1/in/in_b", "/wp-admin", "/.env"} {
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", p, nil))
	}
	gauge := func(ctx context.Context) ([]Gauge, error) {
		return []Gauge{{Name: "relaya_delivery_queue", Help: "h", Labels: map[string]string{"status": "pending"}, Value: 3}}, nil
	}
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	Handler("t", reg, gauge).ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`relaya_http_requests_total{service="ingest",method="POST",route="/v1/in/:token",status="2xx"} 2`,
		`relaya_http_requests_total{service="ingest",method="POST",route="unmatched",status="4xx"} 2`,
		`relaya_http_request_duration_seconds_bucket{service="ingest",method="POST",route="/v1/in/:token",le="0.01"} 0`,
		`relaya_http_request_duration_seconds_count{service="ingest",method="POST",route="/v1/in/:token"} 2`,
		`relaya_delivery_queue{status="pending"} 3`,
		`relaya_collect_up 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "wp-admin") || strings.Contains(body, "in_a") {
		t.Error("raw paths or tokens leaked into labels")
	}
}
