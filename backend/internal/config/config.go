// Package config loads runtime configuration from the environment.
//
// Every value has a documented default so a clean checkout can boot against the
// local docker-compose stack without any prior setup. Secrets are only ever
// read from the environment; they are never logged or echoed back through the
// API.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime settings for the incident API.
type Config struct {
	// HTTPAddr is the listen address for the API server, e.g. ":8080".
	HTTPAddr string
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string
	// RedisURL is the Redis connection string used by the shared runtime.
	RedisURL string
	// AllowedOrigin is the single browser origin permitted for CORS.
	AllowedOrigin string
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration
	// HealthTimeout bounds each dependency health probe.
	HealthTimeout time.Duration

	// OIDC holds operator sign-in settings. When OIDC.Issuer is empty, auth is
	// unconfigured and protected endpoints reject every request.
	OIDC OIDCConfig
	// SessionTTL bounds how long an operator session stays valid.
	SessionTTL time.Duration
	// CookieSecure marks session cookies Secure; disable only for local http.
	CookieSecure bool
	// AppBaseURL is the browser-facing base URL of this backend, used to build
	// the OIDC redirect URL and to redirect back to the UI after login.
	AppBaseURL string
	// UIBaseURL is where operators are sent after a successful login.
	UIBaseURL string
	// SecretKey is the 32-byte AES-256 key (base64 or hex) used to encrypt
	// stored integration credentials at rest. Required to store credentials.
	SecretKey string
}

// OIDCConfig holds the OpenID Connect settings for operator sign-in.
type OIDCConfig struct {
	// Issuer is the OIDC discovery issuer URL (e.g. the Keycloak realm URL).
	Issuer string
	// ClientID identifies this application to the identity provider.
	ClientID string
	// ClientSecret authenticates a confidential client.
	ClientSecret string
}

// Configured reports whether OIDC sign-in has the minimum settings to run.
func (o OIDCConfig) Configured() bool {
	return o.Issuer != "" && o.ClientID != ""
}

// Load reads configuration from the environment, applying defaults that target
// the local docker-compose development stack. It returns an error only when a
// provided value is malformed, never for a missing value.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:        getenv("KNULL_HTTP_ADDR", ":8080"),
		DatabaseURL:     getenv("KNULL_DATABASE_URL", "postgres://knull:knull@localhost:55432/knull?sslmode=disable"),
		RedisURL:        getenv("KNULL_REDIS_URL", "redis://localhost:6379/0"),
		AllowedOrigin:   getenv("KNULL_ALLOWED_ORIGIN", "http://localhost:3000"),
		ShutdownTimeout: 10 * time.Second,
		HealthTimeout:   3 * time.Second,
		OIDC: OIDCConfig{
			Issuer:       getenv("KNULL_OIDC_ISSUER", "http://localhost:8081/realms/knull"),
			ClientID:     getenv("KNULL_OIDC_CLIENT_ID", "knull-backend"),
			ClientSecret: getenv("KNULL_OIDC_CLIENT_SECRET", "knull-local-secret"),
		},
		SessionTTL:   8 * time.Hour,
		CookieSecure: getenvBool("KNULL_COOKIE_SECURE", false),
		AppBaseURL:   getenv("KNULL_APP_BASE_URL", "http://localhost:8080"),
		UIBaseURL:    getenv("KNULL_UI_BASE_URL", "http://localhost:3000"),
		SecretKey:    os.Getenv("KNULL_SECRET_KEY"),
	}

	if d, err := durationSeconds("KNULL_SESSION_TTL_SECONDS"); err != nil {
		return Config{}, err
	} else if d > 0 {
		c.SessionTTL = d
	}

	if d, err := durationSeconds("KNULL_SHUTDOWN_TIMEOUT_SECONDS"); err != nil {
		return Config{}, err
	} else if d > 0 {
		c.ShutdownTimeout = d
	}
	if d, err := durationSeconds("KNULL_HEALTH_TIMEOUT_SECONDS"); err != nil {
		return Config{}, err
	} else if d > 0 {
		c.HealthTimeout = d
	}
	return c, nil
}

func durationSeconds(key string) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return 0, nil
	}
	secs, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if secs < 0 {
		return 0, fmt.Errorf("parse %s: must not be negative", key)
	}
	return time.Duration(secs) * time.Second, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
