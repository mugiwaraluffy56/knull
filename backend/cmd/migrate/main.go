// Command migrate applies all pending database migrations and exits. It is used
// by `make migrate`, by CI before database-backed tests, and for operational
// schema upgrades.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/mugiwaraluffy56/knull/backend/internal/config"
	"github.com/mugiwaraluffy56/knull/backend/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.RedisURL)
	if err != nil {
		logger.Error("connect", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		logger.Error("migrate", "error", err)
		os.Exit(1)
	}
	logger.Info("migrations applied")
}
