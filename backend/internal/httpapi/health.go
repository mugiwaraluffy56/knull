package httpapi

import (
	"context"
	"net/http"
	"time"
)

// dependencyChecker probes a single backing dependency within ctx.
type dependencyChecker interface {
	PingPostgres(ctx context.Context) error
	PingRedis(ctx context.Context) error
}

// healthStatus is the JSON body returned by the health endpoints.
type healthStatus struct {
	Status       string            `json:"status"`
	Dependencies map[string]string `json:"dependencies"`
	CheckedAt    string            `json:"checkedAt"`
}

// handleLive answers a liveness probe. It reports the process is up without
// touching any dependency, so an unhealthy database cannot cause a restart loop.
func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthStatus{
		Status:    "ok",
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// handleReady answers a readiness probe by checking every dependency. It
// returns 200 only when all dependencies respond, and 503 with per-dependency
// detail otherwise, so operators can see exactly what is down.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.healthTimeout)
	defer cancel()

	deps := map[string]string{}
	healthy := true

	if err := s.deps.PingPostgres(ctx); err != nil {
		deps["postgres"] = "unavailable"
		healthy = false
	} else {
		deps["postgres"] = "ok"
	}
	if err := s.deps.PingRedis(ctx); err != nil {
		deps["redis"] = "unavailable"
		healthy = false
	} else {
		deps["redis"] = "ok"
	}

	status := healthStatus{
		Status:       "ok",
		Dependencies: deps,
		CheckedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	code := http.StatusOK
	if !healthy {
		status.Status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, status)
}
