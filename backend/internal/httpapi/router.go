// Package httpapi wires the incident API's HTTP routes onto the standard
// library's net/http server, per the SPEC's requirement to use net/http.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/auth"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
	"github.com/mugiwaraluffy56/knull/backend/internal/secrets"
)

// operatorStore is the subset of the operators store the API needs.
type operatorStore interface {
	Get(ctx context.Context, id uuid.UUID) (operators.Operator, error)
	Upsert(ctx context.Context, issuer, subject, email, name string) (operators.Operator, error)
}

// secretStore is the subset of the credential store the API needs. Note it has
// no reveal method: plaintext credentials are never served over HTTP.
type secretStore interface {
	Put(ctx context.Context, kind secrets.Kind, scope secrets.Scope, plaintext []byte, createdBy uuid.UUID) (secrets.Metadata, error)
	List(ctx context.Context) ([]secrets.Metadata, error)
}

// Options bundles the dependencies used to build a Server.
type Options struct {
	Deps          dependencyChecker
	AllowedOrigin string
	HealthTimeout time.Duration
	Sessions      *auth.SessionManager
	Authn         *auth.Authenticator // nil when OIDC is not configured/reachable
	Operators     operatorStore
	Secrets       secretStore
	AppBaseURL    string
	UIBaseURL     string
	Logger        *slog.Logger
}

// Server holds the dependencies needed to serve HTTP requests.
type Server struct {
	deps          dependencyChecker
	allowedOrigin string
	healthTimeout time.Duration
	sessions      *auth.SessionManager
	authn         *auth.Authenticator
	operators     operatorStore
	secrets       secretStore
	appBaseURL    string
	uiBaseURL     string
	logger        *slog.Logger
}

// New builds a Server from Options, applying safe defaults.
func New(opts Options) *Server {
	if opts.HealthTimeout <= 0 {
		opts.HealthTimeout = 3 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{
		deps:          opts.Deps,
		allowedOrigin: opts.AllowedOrigin,
		healthTimeout: opts.HealthTimeout,
		sessions:      opts.Sessions,
		authn:         opts.Authn,
		operators:     opts.Operators,
		secrets:       opts.Secrets,
		appBaseURL:    opts.AppBaseURL,
		uiBaseURL:     opts.UIBaseURL,
		logger:        opts.Logger,
	}
}

// Handler returns the root HTTP handler with all routes and middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public endpoints.
	mux.HandleFunc("GET /healthz", s.handleLive)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/auth/callback", s.handleCallback)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	// Operator-only endpoints.
	mux.Handle("GET /api/me", s.requireOperator(http.HandlerFunc(s.handleMe)))
	mux.Handle("GET /api/integrations/credentials", s.requireOperator(http.HandlerFunc(s.handleListCredentials)))
	mux.Handle("PUT /api/integrations/credentials", s.requireOperator(http.HandlerFunc(s.handlePutCredential)))

	return s.withCORS(mux)
}

// withCORS allows the configured browser origin to call the API with cookies and
// answers preflight requests. Only the single trusted origin is permitted.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin == s.allowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// staticChecker is a fixed dependencyChecker used where no live store exists,
// such as in tests.
type staticChecker struct {
	postgresErr error
	redisErr    error
}

func (c staticChecker) PingPostgres(context.Context) error { return c.postgresErr }
func (c staticChecker) PingRedis(context.Context) error    { return c.redisErr }
