// Package metrics exposes Prometheus text-format metrics without extra
// dependencies: HTTP request counts and latencies recorded by the services,
// plus gauges collected from the database at scrape time.
package metrics

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Latency buckets in seconds.
var buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type histogram struct {
	counts []uint64 // per bucket, cumulative at export
	count  uint64
	sum    float64
}

// Registry holds one service's request metrics.
type Registry struct {
	Service string

	mu       sync.Mutex
	requests map[[3]string]uint64     // method, route, status class
	latency  map[[2]string]*histogram // method, route
	started  time.Time
}

func NewRegistry(service string) *Registry {
	return &Registry{Service: service, requests: map[[3]string]uint64{}, latency: map[[2]string]*histogram{}, started: time.Now()}
}

// Observe records one finished request. route should be low-cardinality
// (a pattern, never a raw path with IDs or tokens).
func (r *Registry) Observe(method, route string, status int, d time.Duration) {
	if r == nil {
		return
	}
	class := strconv.Itoa(status/100) + "xx"
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[[3]string{method, route, class}]++
	h := r.latency[[2]string{method, route}]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(buckets))}
		r.latency[[2]string{method, route}] = h
	}
	s := d.Seconds()
	for i, b := range buckets {
		if s <= b {
			h.counts[i]++
			break
		}
	}
	h.count++
	h.sum += s
}

// Gauge is one sample from a Collector.
type Gauge struct {
	Name   string
	Help   string
	Labels map[string]string
	Value  float64
}

// Collector produces gauges at scrape time (e.g. from the database).
type Collector func(ctx context.Context) ([]Gauge, error)

// Handler serves the metrics to callers presenting the bearer token. With no
// token configured the endpoint answers 404, so it is never public by accident.
func Handler(token string, reg *Registry, collectors ...Collector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		var gauges []Gauge
		up := 1.0
		for _, c := range collectors {
			g, err := c(ctx)
			if err != nil {
				up = 0
				continue
			}
			gauges = append(gauges, g...)
		}
		reg.write(w)
		if len(collectors) > 0 {
			gauges = append(gauges, Gauge{Name: "relaya_collect_up", Help: "Whether database metrics could be collected.", Value: up})
		}
		writeGauges(w, gauges)
	})
}

func (r *Registry) write(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	svc := fmt.Sprintf("service=%q", r.Service)
	fmt.Fprintf(w, "# HELP relaya_uptime_seconds Seconds since the service started.\n# TYPE relaya_uptime_seconds gauge\nrelaya_uptime_seconds{%s} %g\n",
		svc, math.Round(time.Since(r.started).Seconds()))

	fmt.Fprint(w, "# HELP relaya_http_requests_total HTTP requests by route and status class.\n# TYPE relaya_http_requests_total counter\n")
	keys := make([][3]string, 0, len(r.requests))
	for k := range r.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.Join(keys[i][:], "|") < strings.Join(keys[j][:], "|") })
	for _, k := range keys {
		fmt.Fprintf(w, "relaya_http_requests_total{%s,method=%q,route=%q,status=%q} %d\n", svc, k[0], k[1], k[2], r.requests[k])
	}

	fmt.Fprint(w, "# HELP relaya_http_request_duration_seconds HTTP request latency.\n# TYPE relaya_http_request_duration_seconds histogram\n")
	lkeys := make([][2]string, 0, len(r.latency))
	for k := range r.latency {
		lkeys = append(lkeys, k)
	}
	sort.Slice(lkeys, func(i, j int) bool { return lkeys[i][0]+lkeys[i][1] < lkeys[j][0]+lkeys[j][1] })
	for _, k := range lkeys {
		h := r.latency[k]
		labels := fmt.Sprintf("%s,method=%q,route=%q", svc, k[0], k[1])
		var cum uint64
		for i, b := range buckets {
			cum += h.counts[i]
			fmt.Fprintf(w, "relaya_http_request_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(b, 'g', -1, 64), cum)
		}
		fmt.Fprintf(w, "relaya_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, h.count)
		fmt.Fprintf(w, "relaya_http_request_duration_seconds_sum{%s} %g\n", labels, h.sum)
		fmt.Fprintf(w, "relaya_http_request_duration_seconds_count{%s} %d\n", labels, h.count)
	}
}

func writeGauges(w io.Writer, gauges []Gauge) {
	seen := map[string]bool{}
	for _, g := range gauges {
		if !seen[g.Name] {
			seen[g.Name] = true
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", g.Name, g.Help, g.Name)
		}
		names := make([]string, 0, len(g.Labels))
		for k := range g.Labels {
			names = append(names, k)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, k := range names {
			parts = append(parts, fmt.Sprintf("%s=%q", k, g.Labels[k]))
		}
		lbl := ""
		if len(parts) > 0 {
			lbl = "{" + strings.Join(parts, ",") + "}"
		}
		fmt.Fprintf(w, "%s%s %g\n", g.Name, lbl, g.Value)
	}
}

// Middleware records every request's route, status and latency.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, req)
		route := Route(req.URL.Path)
		if rec.status == http.StatusNotFound {
			route = "unmatched" // scanners must not create new series
		}
		r.Observe(req.Method, route, rec.status, time.Since(start))
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap keeps http.ResponseController (flush, hijack for WebSockets) working.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Route turns a path into a low-cardinality label: IDs become ":id", ingest
// tokens ":token", numbers ":n".
func Route(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segs {
		switch {
		case i > 0 && segs[i-1] == "in" && s != "":
			segs[i] = ":token"
		case isUUID(s):
			segs[i] = ":id"
		case s != "" && strings.Trim(s, "0123456789") == "":
			segs[i] = ":n"
		}
	}
	return "/" + strings.Join(segs, "/")
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
