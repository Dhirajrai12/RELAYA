// Command api serves the dashboard and public REST API.
package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"relaya/internal/alerts"
	"relaya/internal/api"
	"relaya/internal/auth"
	"relaya/internal/config"
	"relaya/internal/connect"
	"relaya/internal/db"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
	"relaya/internal/metrics"
	"relaya/internal/ratelimit"
	"relaya/internal/realtime"
	"relaya/internal/server"
	"relaya/internal/status"
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
	srv.TrustProxyHeaders = cfg.TrustProxyHeaders
	srv.Status = &status.Service{Pool: pool}
	srv.DashboardURL = cfg.DashboardURL
	srv.Connect = &connect.Service{Pool: pool, Vault: srv.Vault, Client: connect.NewClient(), RedirectURI: cfg.ConnectRedirectURI}
	srv.ProxyHTTP = connect.NewProxyClient()
	srv.Limits = api.Limits{
		LoginIP:    ratelimit.New(20, time.Minute, 20),
		LoginEmail: ratelimit.New(10, 15*time.Minute, 10), // failed attempts only
		SignupIP:   ratelimit.New(10, time.Hour, 10),
		StatusIP:   ratelimit.New(120, time.Minute, 60),
		ConnectIP:  ratelimit.New(120, time.Minute, 60), // connect.js polls every 2s while a popup is open
	}
	if cfg.APIPerMinute > 0 {
		srv.Limits.Caller = ratelimit.New(cfg.APIPerMinute, time.Minute, max(cfg.APIPerMinute/2, 10))
	}
	srv.AlertSender = &alerts.Sender{
		Pool: pool, Vault: srv.Vault, HTTP: srv.DeliveryPolicy.Client(), Sign: delivery.Sign, DashboardURL: cfg.DashboardURL,
		SMTP: alerts.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom},
	}
	go srv.Hub.Run(ctx)
	reg := metrics.NewRegistry("api")
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler(cfg.MetricsToken, reg, metrics.DBCollector(pool)))
	mux.Handle("/", srv.Routes())
	h := httpx.Chain(mux, httpx.Log, reg.Middleware, httpx.Recover, httpx.CORS(cfg.AllowedOrigins))
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
