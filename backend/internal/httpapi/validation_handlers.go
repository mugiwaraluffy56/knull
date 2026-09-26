package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/sandbox"
	"github.com/mugiwaraluffy56/knull/backend/internal/validation"
)

type memoryValidator interface {
	ValidateMemory(context.Context, uuid.UUID, uuid.UUID, sandbox.Workload) (validation.Result, error)
}

func (s *Server) handleValidateMemory(w http.ResponseWriter, r *http.Request) {
	if s.memoryValidator == nil {
		writeError(w, http.StatusServiceUnavailable, "memory validation dependencies are not configured")
		return
	}
	incidentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	var request prepareSandboxRequest
	if err := decodeJSON(w, r, &request); err != nil || request.ActionEventID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "actionEventId and workload required")
		return
	}
	result, err := s.memoryValidator.ValidateMemory(r.Context(), incidentID, request.ActionEventID, request.Workload)
	if err != nil {
		if errors.Is(err, incidents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "result": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
