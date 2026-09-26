package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type resolutionStore interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Transition(context.Context, uuid.UUID, incidents.Transition) (incidents.Incident, error)
}

type resolutionRequest struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	Reason          string `json:"reason"`
}

func (s *Server) handleCloseIncident(w http.ResponseWriter, r *http.Request) {
	s.resolveIncident(w, r, incidents.StateClosed)
}
func (s *Server) handleEscalateIncident(w http.ResponseWriter, r *http.Request) {
	s.resolveIncident(w, r, incidents.StateEscalated)
}

func (s *Server) resolveIncident(w http.ResponseWriter, r *http.Request, to incidents.State) {
	if s.resolutions == nil {
		writeError(w, http.StatusServiceUnavailable, "incident resolution unavailable")
		return
	}
	if s.allowedOrigin == "" || r.Header.Get("Origin") != s.allowedOrigin {
		writeError(w, http.StatusForbidden, "untrusted origin")
		return
	}
	op, ok := operatorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	var request resolutionRequest
	if decodeJSON(w, r, &request) != nil || request.ExpectedVersion < 1 || len(request.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "invalid resolution request")
		return
	}
	inc, err := s.resolutions.Get(r.Context(), id)
	if errors.Is(err, incidents.ErrNotFound) {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		s.internalError(w, "get incident for resolution", err)
		return
	}
	if inc.Version != request.ExpectedVersion || !inc.State.CanTransition(to) {
		writeError(w, http.StatusConflict, "incident state changed or transition unavailable")
		return
	}
	if to == incidents.StateClosed && inc.State == incidents.StateVerifying && strings.TrimSpace(request.Reason) == "" {
		writeError(w, http.StatusBadRequest, "manual closure before verified recovery requires a reason")
		return
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		if to == incidents.StateClosed {
			reason = "operator closed incident"
		} else {
			reason = "operator escalated incident"
		}
	}
	updated, err := s.resolutions.Transition(r.Context(), id, incidents.Transition{To: to, Actor: op.Email, Reason: reason, ExpectedVersion: request.ExpectedVersion, Data: map[string]any{"manual": true, "operatorId": op.ID}})
	if errors.Is(err, incidents.ErrConflict) || errors.Is(err, incidents.ErrInvalidTransition) {
		writeError(w, http.StatusConflict, "incident changed while resolving")
		return
	}
	if err != nil {
		s.internalError(w, "resolve incident", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
