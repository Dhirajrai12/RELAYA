package main

import (
	"slices"
	"testing"

	"relaya/internal/config"
)

func TestCORSOriginsIncludeDashboard(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"http://localhost:5173"}, DashboardURL: "https://relaya.sbs/"}
	if got, want := corsOrigins(cfg), []string{"http://localhost:5173", "https://relaya.sbs"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	cfg.AllowedOrigins = []string{"https://relaya.sbs"}
	if got := corsOrigins(cfg); len(got) != 1 {
		t.Fatalf("dashboard listed twice: %v", got)
	}
}

func TestStreamOriginsIncludeDashboard(t *testing.T) {
	cfg := config.Config{
		AllowedOrigins: make([]string, 1, 4),
		IngestBaseURL:  "https://api.relaya.sbs",
		DashboardURL:   "https://relaya.sbs",
	}
	cfg.AllowedOrigins[0] = "http://localhost:5173"
	got := streamOrigins(cfg)
	want := []string{"localhost:5173", "api.relaya.sbs", "relaya.sbs"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// The CORS list must not be appended to in place (it has spare capacity here).
	if full := cfg.AllowedOrigins[:cap(cfg.AllowedOrigins)]; full[1] != "" {
		t.Fatalf("AllowedOrigins was modified: %v", full)
	}
}
