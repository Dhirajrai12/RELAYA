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

	var err error
	if c.MaxBodyBytes, err = strconv.ParseInt(get("MAX_BODY_BYTES", "5242880"), 10, 64); err != nil {
		return c, fmt.Errorf("MAX_BODY_BYTES: %w", err)
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
