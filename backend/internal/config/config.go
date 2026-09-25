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

	// Contracts: propose after this many samples, or after LearnWindow with at least 3.
	ContractMinSamples  int
	ContractLearnWindow time.Duration
	// Incidents close themselves after this long without occurrences (0 disables).
	IncidentAutoResolveAfter time.Duration
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
	}

	dev := c.Env == "dev"
	c.DeliveryAllowHTTP = getBool("DELIVERY_ALLOW_HTTP", dev)
	c.DeliveryAllowPrivate = getBool("DELIVERY_ALLOW_PRIVATE", dev)

	var err error
	if c.MaxBodyBytes, err = strconv.ParseInt(get("MAX_BODY_BYTES", "5242880"), 10, 64); err != nil {
		return c, fmt.Errorf("MAX_BODY_BYTES: %w", err)
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
	if c.IncidentAutoResolveAfter, err = time.ParseDuration(get("INCIDENT_AUTO_RESOLVE_AFTER", "1h")); err != nil {
		return c, fmt.Errorf("INCIDENT_AUTO_RESOLVE_AFTER: %w", err)
	}
	if c.SessionTTL, err = time.ParseDuration(get("SESSION_TTL", "720h")); err != nil {
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
