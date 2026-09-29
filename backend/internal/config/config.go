// Package config loads service configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env               string // dev, staging, prod
	DatabaseURL       string
	APIAddr           string
	IngestAddr        string
	IngestBaseURL     string // public base URL shown to users, e.g. https://in.example.com
	MasterKey         []byte // 32 bytes; wraps each organization's data key
	MasterKeyID       string
	MaxBodyBytes      int64
	SessionTTL        time.Duration
	AllowedOrigins    []string
	TrustProxyHeaders bool // trust X-Real-IP/X-Forwarded-For; enable only behind our own proxy

	// Outbound delivery (SSRF policy + worker). Dev defaults allow http://localhost receivers.
	DeliveryAllowHTTP    bool
	DeliveryAllowPrivate bool
	WorkerConcurrency    int

	// Rate limits (0 = off). Sign-in and sign-up limits are fixed.
	IngestPerSecond int // sustained requests per ingest URL
	IngestBurst     int
	APIPerMinute    int // authenticated API requests per user or API key

	// Contracts: propose after this many samples, or after LearnWindow with at least 3.
	ContractMinSamples  int
	ContractLearnWindow time.Duration
	// Incidents close themselves after this long without occurrences (0 disables).
	IncidentAutoResolveAfter time.Duration
	// Bearer token for GET /metrics (Prometheus). Empty = metrics off.
	MetricsToken string
	// Retention: event payloads (with deliveries and findings) and the alert log.
	EventRetention time.Duration
	AlertRetention time.Duration

	// Alerts. DashboardURL prefixes links in alerts (defaults to INGEST_BASE_URL).
	DashboardURL string
	SMTPHost     string // email alerts are disabled when empty
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// Connections: the OAuth callback URL organizations register in their OAuth
	// apps. Defaults to DASHBOARD_URL + /api/v1/connect/callback (IIS routes /api to the API).
	ConnectRedirectURI string
}

// Load reads configuration from the environment, after loading an env file:
// $ENV_FILE if set, otherwise .env next to the executable (Windows services
// start in System32 and inherit no shell environment). Variables already set
// in the environment win over the file.
func Load() (Config, error) {
	if err := LoadEnvFile(EnvFilePath()); err != nil {
		return Config{}, err
	}
	c := Config{
		Env:               get("APP_ENV", "dev"),
		DatabaseURL:       get("DATABASE_URL", "postgres://relaya:relaya@localhost:5432/relaya?sslmode=disable"),
		APIAddr:           get("API_ADDR", ":8080"),
		IngestAddr:        get("INGEST_ADDR", ":8081"),
		IngestBaseURL:     strings.TrimRight(get("INGEST_BASE_URL", "http://localhost:8081"), "/"),
		MasterKeyID:       get("MASTER_KEY_ID", "local-1"),
		AllowedOrigins:    splitList(get("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),
		TrustProxyHeaders: get("TRUST_PROXY_HEADERS", "false") == "true",
		MetricsToken:      get("METRICS_TOKEN", ""),
	}

	dev := c.Env == "dev"
	c.DeliveryAllowHTTP = getBool("DELIVERY_ALLOW_HTTP", dev)
	c.DeliveryAllowPrivate = getBool("DELIVERY_ALLOW_PRIVATE", dev)

	var err error
	if c.MaxBodyBytes, err = strconv.ParseInt(get("MAX_BODY_BYTES", "5242880"), 10, 64); err != nil {
		return c, fmt.Errorf("MAX_BODY_BYTES: %w", err)
	}
	for _, v := range []struct {
		name string
		def  string
		dst  *int
	}{{"RATE_LIMIT_INGEST_PER_SECOND", "100", &c.IngestPerSecond}, {"RATE_LIMIT_INGEST_BURST", "1000", &c.IngestBurst}, {"RATE_LIMIT_API_PER_MINUTE", "600", &c.APIPerMinute}} {
		if *v.dst, err = strconv.Atoi(get(v.name, v.def)); err != nil || *v.dst < 0 {
			return c, fmt.Errorf("%s must be a whole number (0 turns the limit off)", v.name)
		}
	}
	if c.WorkerConcurrency, err = strconv.Atoi(get("WORKER_CONCURRENCY", "8")); err != nil || c.WorkerConcurrency < 1 {
		return c, errors.New("WORKER_CONCURRENCY must be a positive integer")
	}
	if c.ContractMinSamples, err = strconv.Atoi(get("CONTRACT_MIN_SAMPLES", "20")); err != nil || c.ContractMinSamples < 1 {
		return c, errors.New("CONTRACT_MIN_SAMPLES must be a positive integer")
	}
	if c.ContractLearnWindow, err = time.ParseDuration(get("CONTRACT_LEARN_WINDOW", "24h")); err != nil {
		return c, fmt.Errorf("CONTRACT_LEARN_WINDOW: %w", err)
	}
	if c.EventRetention, err = time.ParseDuration(get("EVENT_RETENTION", "720h")); err != nil || c.EventRetention < 24*time.Hour {
		return c, errors.New("EVENT_RETENTION must be a duration of at least 24h (e.g. 720h for 30 days)")
	}
	if c.AlertRetention, err = time.ParseDuration(get("ALERT_RETENTION", "2160h")); err != nil || c.AlertRetention < 24*time.Hour {
		return c, errors.New("ALERT_RETENTION must be a duration of at least 24h")
	}
	if c.IncidentAutoResolveAfter, err = time.ParseDuration(get("INCIDENT_AUTO_RESOLVE_AFTER", "1h")); err != nil {
		return c, fmt.Errorf("INCIDENT_AUTO_RESOLVE_AFTER: %w", err)
	}
	c.DashboardURL = strings.TrimRight(get("DASHBOARD_URL", c.IngestBaseURL), "/")
	c.ConnectRedirectURI = get("CONNECT_REDIRECT_URI", c.DashboardURL+"/api/v1/connect/callback")
	c.SMTPHost, c.SMTPUsername, c.SMTPPassword, c.SMTPFrom = os.Getenv("SMTP_HOST"), os.Getenv("SMTP_USERNAME"), os.Getenv("SMTP_PASSWORD"), os.Getenv("SMTP_FROM")
	if c.SMTPPort, err = strconv.Atoi(get("SMTP_PORT", "587")); err != nil {
		return c, fmt.Errorf("SMTP_PORT: %w", err)
	}
	if c.SessionTTL, err = time.ParseDuration(get("SESSION_TTL", "168h")); err != nil {
		return c, fmt.Errorf("SESSION_TTL: %w", err)
	}

	raw := os.Getenv("MASTER_KEY")
	if raw == "" {
		return c, errors.New("MASTER_KEY is required (base64 of 32 random bytes; generate with `openssl rand -base64 32`)")
	}
	if c.MasterKey, err = base64.StdEncoding.DecodeString(raw); err != nil || len(c.MasterKey) != 32 {
		return c, errors.New("MASTER_KEY must be base64 of exactly 32 bytes")
	}
	return c, nil
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getBool(key string, def bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return def
}
