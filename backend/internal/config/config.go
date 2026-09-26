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
	"strings"
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
	// AlertmanagerSecret authenticates the Alertmanager webhook. Empty disables
	// intake (fail closed).
	AlertmanagerSecret string
	// AlertServiceLabel/AlertEnvironmentLabel name the alert labels that carry
	// the service key and environment.
	AlertServiceLabel     string
	AlertEnvironmentLabel string

	// TrueForgeURL is the base URL of the TrueForge workflow runtime. Empty
	// disables durable workflows (incidents are created but not investigated).
	TrueForgeURL string
	// TrueForgeToken authenticates requests to TrueForge.
	TrueForgeToken string
	// These select a dedicated sandbox-only validation agent and connector.
	TrueForgeValidationModel string
	TrueForgeValidationMCP   string
	TrueForgeValidationTools []string

	// K8sMCPURL / K8sMCPToken address the read-only Kubernetes MCP server.
	K8sMCPURL   string
	K8sMCPToken string
	// PrometheusMCPURL / PrometheusMCPToken address the read-only Prometheus MCP server.
	PrometheusMCPURL   string
	PrometheusMCPToken string
	// GitHubMCPURL / GitHubMCPToken address the read-only GitHub MCP server.
	GitHubMCPURL   string
	GitHubMCPToken string
	// JevMCPURL connects the decision-only server. Empty disables classification.
	JevMCPURL              string
	JevMinConfidence       float64
	SandboxKubeconfig      string
	SandboxContext         string
	SandboxClusterUID      string
	ProductionClusterUIDs  []string
	SandboxImageRegistry   string
	SandboxPullSecretName  string
	SandboxPrometheusURL   string
	SandboxPrometheusToken string
	SandboxErrorRateQuery  string
	SandboxP95Query        string
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
		SessionTTL:               8 * time.Hour,
		CookieSecure:             getenvBool("KNULL_COOKIE_SECURE", false),
		AppBaseURL:               getenv("KNULL_APP_BASE_URL", "http://localhost:8080"),
		UIBaseURL:                getenv("KNULL_UI_BASE_URL", "http://localhost:3000"),
		SecretKey:                os.Getenv("KNULL_SECRET_KEY"),
		AlertmanagerSecret:       os.Getenv("KNULL_ALERTMANAGER_SECRET"),
		AlertServiceLabel:        getenv("KNULL_ALERT_SERVICE_LABEL", "service"),
		AlertEnvironmentLabel:    getenv("KNULL_ALERT_ENVIRONMENT_LABEL", "environment"),
		TrueForgeURL:             os.Getenv("KNULL_TRUEFORGE_URL"),
		TrueForgeToken:           os.Getenv("KNULL_TRUEFORGE_TOKEN"),
		TrueForgeValidationModel: os.Getenv("KNULL_TRUEFORGE_VALIDATION_MODEL"),
		TrueForgeValidationMCP:   os.Getenv("KNULL_TRUEFORGE_VALIDATION_MCP"),
		K8sMCPURL:                os.Getenv("KNULL_K8S_MCP_URL"),
		K8sMCPToken:              os.Getenv("KNULL_K8S_MCP_TOKEN"),
		PrometheusMCPURL:         os.Getenv("KNULL_PROMETHEUS_MCP_URL"),
		PrometheusMCPToken:       os.Getenv("KNULL_PROMETHEUS_MCP_TOKEN"),
		GitHubMCPURL:             os.Getenv("KNULL_GITHUB_MCP_URL"),
		GitHubMCPToken:           os.Getenv("KNULL_GITHUB_MCP_TOKEN"),
		JevMCPURL:                os.Getenv("KNULL_JEV_MCP_URL"),
		JevMinConfidence:         0.65,
		SandboxKubeconfig:        os.Getenv("KNULL_SANDBOX_KUBECONFIG"),
		SandboxContext:           os.Getenv("KNULL_SANDBOX_CONTEXT"),
		SandboxClusterUID:        os.Getenv("KNULL_SANDBOX_CLUSTER_UID"),
		ProductionClusterUIDs:    strings.Split(os.Getenv("KNULL_PRODUCTION_CLUSTER_UIDS"), ","),
		SandboxImageRegistry:     os.Getenv("KNULL_SANDBOX_IMAGE_REGISTRY"),
		SandboxPullSecretName:    os.Getenv("KNULL_SANDBOX_PULL_SECRET_NAME"),
		SandboxPrometheusURL:     os.Getenv("KNULL_SANDBOX_PROMETHEUS_URL"),
		SandboxPrometheusToken:   os.Getenv("KNULL_SANDBOX_PROMETHEUS_TOKEN"),
		SandboxErrorRateQuery:    os.Getenv("KNULL_SANDBOX_ERROR_RATE_QUERY"),
		SandboxP95Query:          os.Getenv("KNULL_SANDBOX_P95_QUERY"),
	}
	if raw := os.Getenv("KNULL_TRUEFORGE_VALIDATION_TOOLS"); raw != "" {
		for _, tool := range strings.Split(raw, ",") {
			c.TrueForgeValidationTools = append(c.TrueForgeValidationTools, strings.TrimSpace(tool))
		}
	}
	if raw := os.Getenv("KNULL_JEV_MIN_CONFIDENCE"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value <= 0 || value > 1 {
			return Config{}, fmt.Errorf("KNULL_JEV_MIN_CONFIDENCE must be in (0, 1]")
		}
		c.JevMinConfidence = value
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
