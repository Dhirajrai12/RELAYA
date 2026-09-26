// Command ingest is the webhook gateway's receive path. It runs separately from
// the API so the dashboard can go down without dropping provider webhooks.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"relaya/internal/config"
	"relaya/internal/db"
	"relaya/internal/httpx"
	"relaya/internal/ingest"
	"relaya/internal/metrics"
	"relaya/internal/ratelimit"
	"relaya/internal/server"
	"relaya/internal/vault"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(server.Logger(cfg.Env))

	ctx, stop := server.SignalContext()
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	wrapper, err := vault.NewLocalWrapper(cfg.MasterKeyID, cfg.MasterKey)
	if err != nil {
		slog.Error("vault", "err", err)
		os.Exit(1)
	}

	go ingest.Maintain(ctx, pool, 30*24*time.Hour, 6*time.Hour)

	h := &ingest.Handler{
		Pool:              pool,
		Vault:             vault.NewPGVault(pool, wrapper),
		MaxBodyBytes:      cfg.MaxBodyBytes,
		TrustProxyHeaders: cfg.TrustProxyHeaders,
		UnknownIP:         ratelimit.New(60, time.Minute, 30),
	}
	if cfg.IngestPerSecond > 0 {
		h.PerWebhook = ratelimit.New(cfg.IngestPerSecond, time.Second, max(cfg.IngestBurst, cfg.IngestPerSecond))
	}
	reg := metrics.NewRegistry("ingest")
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler(cfg.MetricsToken, reg))
	mux.Handle("/", h.Routes())
	if err := server.Run(ctx, "ingest", cfg.IngestAddr, httpx.Chain(mux, httpx.Log, reg.Middleware, httpx.Recover)); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
