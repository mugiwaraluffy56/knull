// Command api is the Knull incident API server.
//
// It loads configuration, connects to PostgreSQL and Redis, applies database
// migrations, and serves the HTTP API (health endpoints in this foundation
// slice) until it receives an interrupt, then shuts down gracefully.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mugiwaraluffy56/knull/backend/internal/config"
	"github.com/mugiwaraluffy56/knull/backend/internal/httpapi"
	"github.com/mugiwaraluffy56/knull/backend/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer st.Close()
	logger.Info("connected to dependencies")

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	logger.Info("migrations applied")

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(st, cfg.AllowedOrigin, cfg.HealthTimeout).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("server stopped cleanly")
	return nil
}
