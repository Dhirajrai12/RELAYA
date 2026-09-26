// Package httpx holds small HTTP helpers shared by the API and ingest services.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Error is an API error with a stable machine-readable code.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func NewError(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

var (
	ErrUnauthorized = NewError(http.StatusUnauthorized, "unauthorized", "missing or invalid credentials")
	ErrForbidden    = NewError(http.StatusForbidden, "forbidden", "your role does not allow this action")
	ErrNotFound     = NewError(http.StatusNotFound, "not_found", "resource not found")
)

func BadRequest(format string, args ...any) *Error {
	return NewError(http.StatusBadRequest, "bad_request", fmt.Sprintf(format, args...))
}

func Conflict(format string, args ...any) *Error {
	return NewError(http.StatusConflict, "conflict", fmt.Sprintf(format, args...))
}

// JSON writes v as a JSON response.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// WriteError writes err as {"error": {...}}. Unknown errors become 500s and are
// logged; their text is never sent to the client.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.ErrorContext(r.Context(), "internal error", "err", err, "path", r.URL.Path, "request_id", RequestID(r))
		e = NewError(http.StatusInternalServerError, "internal", "internal server error")
	}
	JSON(w, e.Status, map[string]any{"error": e})
}

// HandlerFunc is an http.HandlerFunc that returns an error.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

func (h HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}

// Decode reads a JSON body (max 1 MiB) into v and rejects unknown fields.
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return BadRequest("invalid JSON body: %v", err)
	}
	return nil
}

// ---- middleware ----------------------------------------------------------------

type ctxKey int

const requestIDKey ctxKey = iota

func RequestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Log assigns a request ID and logs method, path, status and latency.
// It never logs headers or bodies, so secrets cannot leak into logs.
func Log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-Id", id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.InfoContext(r.Context(), "http",
			"method", r.Method,
			"path", redactPath(r.URL.Path),
			"status", rec.status,
			"ms", time.Since(start).Milliseconds(),
			"request_id", id)
	})
}

// redactPath hides ingest tokens (/v1/in/<token>) in logs.
func redactPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "/v1/in/"); ok && rest != "" {
		return "/v1/in/***"
	}
	return p
}

// Recover turns panics into 500s.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				WriteError(w, r, fmt.Errorf("panic: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORS allows the dashboard origins to call the API.
func CORS(origins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && slices.Contains(origins, origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Vary", "Origin")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Chain applies middleware so the first one listed runs first.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// ---- client IP and rate limiting ---------------------------------------------------

// ClientIP returns the caller's IP, or "" if it can't be parsed. Behind our
// proxy (IIS/ARR), X-Real-IP is overwritten by the proxy's rewrite rule, so
// clients cannot spoof it. The X-Forwarded-For fallback takes the last entry,
// the one our proxy appended; earlier entries come from the client. Only
// trust these headers when the service is reachable solely through the proxy.
func ClientIP(r *http.Request, trustProxy bool) string {
	ip := ""
	if trustProxy {
		if v := r.Header.Get("X-Real-IP"); v != "" {
			ip = v
		} else if v := r.Header.Get("X-Forwarded-For"); v != "" {
			ip = v[strings.LastIndex(v, ",")+1:]
		}
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	ip = stripPort(strings.TrimSpace(ip))
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

// stripPort handles "1.2.3.4:5678" and "[::1]:5678" (ARR includes the port in
// X-Forwarded-For by default) as well as bare IPv4 and IPv6 addresses.
func stripPort(s string) string {
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}
	return strings.Trim(s, "[]")
}

// TooManyRequests sets Retry-After and returns a 429 error for the handler to return.
func TooManyRequests(w http.ResponseWriter, wait time.Duration, msg string) *Error {
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	return NewError(http.StatusTooManyRequests, "rate_limited", msg)
}
