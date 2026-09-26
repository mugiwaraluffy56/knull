package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type actionPlanner interface {
	Create(context.Context, uuid.UUID, actions.Draft) (actions.Created, error)
}

func (s *Server) handleCreateActionPlan(w http.ResponseWriter, r *http.Request) {
	if s.actionPlanner == nil {
		writeError(w, http.StatusServiceUnavailable, "action planning is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	var draft actions.Draft
	if err := decodeJSON(w, r, &draft); err != nil {
		writeError(w, http.StatusBadRequest, "invalid action JSON")
		return
	}
	created, err := s.actionPlanner.Create(r.Context(), id, draft)
	if err != nil {
		switch {
		case errors.Is(err, incidents.ErrNotFound):
			writeError(w, http.StatusNotFound, "incident not found")
		case errors.Is(err, actions.ErrInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.internalError(w, "create action plan", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, created)
}
