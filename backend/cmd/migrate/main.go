// Command migrate applies pending database migrations and exits.
package main

import (
	"context"
	"log/slog"
	"os"

	"relaya/internal/config"
	"relaya/internal/db"
)

func main() {
	if err := config.LoadEnvFile(config.EnvFilePath()); err != nil {
		slog.Error("env file", "err", err)
		os.Exit(1)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://relaya:relaya@localhost:5432/relaya?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	if len(applied) == 0 {
		slog.Info("database is up to date")
	}
	for _, name := range applied {
		slog.Info("applied", "migration", name)
	}
}
