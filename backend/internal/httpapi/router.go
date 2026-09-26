// Package httpapi wires the incident API's HTTP routes onto the standard
// library's net/http server, per the SPEC's requirement to use net/http.
package httpapi

import (
	"context"
	"net/http"
	"time"
)

// Server holds the dependencies needed to serve HTTP requests.
type Server struct {
	deps          dependencyChecker
	allowedOrigin string
	healthTimeout time.Duration
}

// New builds a Server. allowedOrigin is the single browser origin permitted for
// CORS; healthTimeout bounds each readiness probe.
func New(deps dependencyChecker, allowedOrigin string, healthTimeout time.Duration) *Server {
	if healthTimeout <= 0 {
		healthTimeout = 3 * time.Second
	}
	return &Server{
		deps:          deps,
		allowedOrigin: allowedOrigin,
		healthTimeout: healthTimeout,
	}
}

// Handler returns the root HTTP handler with all routes and middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleLive)
	mux.HandleFunc("GET /readyz", s.handleReady)
	return s.withCORS(mux)
}

// withCORS allows the configured browser origin to call the API and answers
// preflight requests. Only the single trusted origin is permitted.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin == s.allowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
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
