package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/sandbox"
)

type sandboxPreparer interface {
	Prepare(context.Context, uuid.UUID, uuid.UUID, sandbox.Workload) (sandbox.RecordedRun, error)
}
type prepareSandboxRequest struct {
	ActionEventID uuid.UUID        `json:"actionEventId"`
	Workload      sandbox.Workload `json:"workload"`
}

func (s *Server) handlePrepareSandbox(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		writeError(w, http.StatusServiceUnavailable, "dedicated sandbox cluster is not configured")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	var req prepareSandboxRequest
	if err := decodeJSON(w, r, &req); err != nil || req.ActionEventID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "actionEventId and workload required")
		return
	}
	result, err := s.sandbox.Prepare(r.Context(), id, req.ActionEventID, req.Workload)
	if err != nil {
		if errors.Is(err, incidents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "run": result})
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}
