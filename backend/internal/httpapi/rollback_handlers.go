package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/baddeployment"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type rollbackPlanner interface {
	Propose(context.Context, uuid.UUID) (baddeployment.Proposed, error)
}

// handleProposeRollback is proposal-only. A caller cannot use it to validate,
// approve, or execute a rollback; those require their own backend gates.
func (s *Server) handleProposeRollback(w http.ResponseWriter, r *http.Request) {
	if s.rollbackPlanner == nil {
		writeError(w, http.StatusServiceUnavailable, "rollback planning is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	proposed, err := s.rollbackPlanner.Propose(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, incidents.ErrNotFound):
			writeError(w, http.StatusNotFound, "incident not found")
		case errors.Is(err, baddeployment.ErrNoCorrelation):
			writeError(w, http.StatusUnprocessableEntity, "a pinned live image, matching CrashLoop evidence, and a GitHub image change are required")
		case errors.Is(err, actions.ErrInvalid):
			writeError(w, http.StatusConflict, "rollback proposal no longer matches the incident")
		default:
			s.internalError(w, "propose rollback", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, proposed)
}
