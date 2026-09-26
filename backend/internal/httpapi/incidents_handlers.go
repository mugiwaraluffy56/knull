package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

// incidentStore is the subset of the incident store the read API needs.
type incidentStore interface {
	Get(ctx context.Context, id uuid.UUID) (incidents.Incident, error)
	List(ctx context.Context) ([]incidents.Incident, error)
	Events(ctx context.Context, id uuid.UUID) ([]incidents.Event, error)
}

// handleListIncidents returns all incidents, most recently updated first.
func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	if s.incidents == nil {
		writeError(w, http.StatusServiceUnavailable, "incidents are not available")
		return
	}
	list, err := s.incidents.List(r.Context())
	if err != nil {
		s.internalError(w, "list incidents", err)
		return
	}
	if list == nil {
		list = []incidents.Incident{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": list})
}

// handleGetIncident returns one incident by id.
func (s *Server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := s.parseIncidentID(w, r)
	if !ok {
		return
	}
	inc, err := s.incidents.Get(r.Context(), id)
	if err != nil {
		s.writeIncidentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

// handleListIncidentEvents returns an incident's full audit history.
func (s *Server) handleListIncidentEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := s.parseIncidentID(w, r)
	if !ok {
		return
	}
	events, err := s.incidents.Events(r.Context(), id)
	if err != nil {
		s.writeIncidentError(w, err)
		return
	}
	if events == nil {
		events = []incidents.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) parseIncidentID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if s.incidents == nil {
		writeError(w, http.StatusServiceUnavailable, "incidents are not available")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return uuid.Nil, false
	}
	return id, true
}

func (s *Server) writeIncidentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, incidents.ErrNotFound):
		writeError(w, http.StatusNotFound, "incident not found")
	default:
		s.internalError(w, "incident operation", err)
	}
}
