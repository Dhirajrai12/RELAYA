// Command worker forwards received events to customer destinations, retries
// failed deliveries, and learns/checks integration contracts. Run as many copies as you like; they coordinate
// through Postgres row locks.
package main

import (
	"log/slog"
	"os"
	"time"

	"relaya/internal/alerts"
	"relaya/internal/config"
	"relaya/internal/contract"
	"relaya/internal/db"
	"relaya/internal/delivery"
	"relaya/internal/retention"
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
	checker := &contract.Checker{Pool: pool, MinSamples: cfg.ContractMinSamples, LearnWindow: cfg.ContractLearnWindow,
		AutoResolveAfter: cfg.IncidentAutoResolveAfter}
	go checker.Run(ctx)

	alertSender := &alerts.Sender{
		Pool: pool, Vault: vault.NewPGVault(pool, wrapper), HTTP: policy.Client(), Sign: delivery.Sign, DashboardURL: cfg.DashboardURL,
		SMTP: alerts.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom},
	}
	go alertSender.Run(ctx)
	go retention.Run(ctx, pool, retention.Policy{Events: cfg.EventRetention, Alerts: cfg.AlertRetention}, time.Hour)

	slog.Info("worker started", "concurrency", cfg.WorkerConcurrency, "allow_http", policy.AllowHTTP, "allow_private", policy.AllowPrivate)
	w.Run(ctx)
	slog.Info("worker stopped")
}
