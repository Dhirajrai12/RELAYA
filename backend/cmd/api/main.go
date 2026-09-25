// Command api serves the dashboard and public REST API.
package main

import (
	"log/slog"
	"net/url"
	"os"

	"relaya/internal/alerts"
	"relaya/internal/api"
	"relaya/internal/auth"
	"relaya/internal/config"
	"relaya/internal/db"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
	"relaya/internal/realtime"
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
	srv.DeliveryPolicy = delivery.Policy{AllowHTTP: cfg.DeliveryAllowHTTP, AllowPrivate: cfg.DeliveryAllowPrivate}
	srv.Sender = delivery.NewSender(srv.DeliveryPolicy)
	srv.Hub = realtime.NewHub(pool)
	srv.StreamOrigins = streamOrigins(cfg)
	srv.ContractMinSamples = cfg.ContractMinSamples
	srv.IncidentAutoResolveAfter = cfg.IncidentAutoResolveAfter
	srv.AlertSender = &alerts.Sender{
		Pool: pool, Vault: srv.Vault, HTTP: srv.DeliveryPolicy.Client(), Sign: delivery.Sign, DashboardURL: cfg.DashboardURL,
		SMTP: alerts.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom},
	}
	go srv.Hub.Run(ctx)
	h := httpx.Chain(srv.Routes(), httpx.Log, httpx.Recover, httpx.CORS(cfg.AllowedOrigins))
	if err := server.Run(ctx, "api", cfg.APIAddr, h); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// streamOrigins allows the dashboard origins (e.g. the Vite dev server) plus the
// public host. Behind IIS/ARR the Host header is the backend address, so the
// public host must be listed explicitly.
func streamOrigins(cfg config.Config) []string {
	var out []string
	for _, o := range append(cfg.AllowedOrigins, cfg.IngestBaseURL) {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			out = append(out, u.Host)
		}
	}
	return out
}
