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

	"github.com/mugiwaraluffy56/knull/backend/internal/auth"
	"github.com/mugiwaraluffy56/knull/backend/internal/config"
	"github.com/mugiwaraluffy56/knull/backend/internal/httpapi"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
	"github.com/mugiwaraluffy56/knull/backend/internal/secrets"
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

	sessions := auth.NewSessionManager(st.Redis, cfg.SessionTTL, cfg.CookieSecure)
	operatorStore := operators.NewStore(st.Pool)

	var cipher *secrets.Cipher
	if cfg.SecretKey != "" {
		cipher, err = secrets.NewCipher(cfg.SecretKey)
		if err != nil {
			return err
		}
	} else {
		logger.Warn("KNULL_SECRET_KEY not set; storing integration credentials is disabled")
	}
	secretStore := secrets.NewStore(st.Pool, cipher)

	var authn *auth.Authenticator
	if cfg.OIDC.Configured() {
		redirectURL := cfg.AppBaseURL + "/api/auth/callback"
		authn, err = auth.NewAuthenticator(ctx, cfg.OIDC.Issuer, cfg.OIDC.ClientID, cfg.OIDC.ClientSecret, redirectURL)
		if err != nil {
			// A missing identity provider must not crash the whole service; it
			// degrades to health-only. Protected endpoints reject requests.
			logger.Error("oidc initialization failed; sign-in disabled", "error", err)
		} else {
			logger.Info("oidc sign-in enabled", "issuer", cfg.OIDC.Issuer)
		}
	} else {
		logger.Warn("OIDC not configured; operator sign-in is disabled")
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.New(httpapi.Options{
			Deps:          st,
			AllowedOrigin: cfg.AllowedOrigin,
			HealthTimeout: cfg.HealthTimeout,
			Sessions:      sessions,
			Authn:         authn,
			Operators:     operatorStore,
			Secrets:       secretStore,
			AppBaseURL:    cfg.AppBaseURL,
			UIBaseURL:     cfg.UIBaseURL,
			Logger:        logger,
		}).Handler(),
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
