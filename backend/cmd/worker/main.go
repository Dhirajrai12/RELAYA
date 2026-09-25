// Command worker forwards received events to customer destinations and
// retries failed deliveries. Run as many copies as you like; they coordinate
// through Postgres row locks.
package main

import (
	"log/slog"
	"os"

	"relaya/internal/config"
	"relaya/internal/db"
	"relaya/internal/delivery"
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

	policy := delivery.Policy{AllowHTTP: cfg.DeliveryAllowHTTP, AllowPrivate: cfg.DeliveryAllowPrivate}
	w := &delivery.Worker{
		Pool:        pool,
		Vault:       vault.NewPGVault(pool, wrapper),
		Sender:      delivery.NewSender(policy),
		Concurrency: cfg.WorkerConcurrency,
	}
	slog.Info("worker started", "concurrency", cfg.WorkerConcurrency, "allow_http", policy.AllowHTTP, "allow_private", policy.AllowPrivate)
	w.Run(ctx)
	slog.Info("worker stopped")
}
