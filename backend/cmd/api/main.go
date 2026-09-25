// Command api serves the dashboard and public REST API.
package main

import (
	"log/slog"
	"os"

	"relaya/internal/api"
	"relaya/internal/auth"
	"relaya/internal/config"
	"relaya/internal/db"
	"relaya/internal/httpx"
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

	srv := &api.Server{
		Pool:          pool,
		Auth:          &auth.Service{Pool: pool, SessionTTL: cfg.SessionTTL},
		Vault:         vault.NewPGVault(pool, wrapper),
		IngestBaseURL: cfg.IngestBaseURL,
	}
	h := httpx.Chain(srv.Routes(), httpx.Log, httpx.Recover, httpx.CORS(cfg.AllowedOrigins))
	if err := server.Run(ctx, "api", cfg.APIAddr, h); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
