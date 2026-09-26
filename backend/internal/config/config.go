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
