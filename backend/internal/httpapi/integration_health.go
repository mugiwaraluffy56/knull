package httpapi

import (
	"context"
	"net/http"

	"github.com/mugiwaraluffy56/knull/backend/internal/operational"
)

type integrationHealth interface {
	Check(context.Context) map[string]operational.State
}

func (s *Server) handleIntegrationHealth(w http.ResponseWriter, r *http.Request) {
	if s.integrationHealth == nil {
		writeError(w, http.StatusServiceUnavailable, "integration health unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": s.integrationHealth.Check(r.Context())})
}
