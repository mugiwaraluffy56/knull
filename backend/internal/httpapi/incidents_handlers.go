package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

// incidentStore is the subset of the incident store the API needs.
type incidentStore interface {
	Create(ctx context.Context, in incidents.NewIncident) (incidents.Incident, error)
	FindActiveByAlert(ctx context.Context, serviceKey, environment, alertIdentity string) (incidents.Incident, error)
	Get(ctx context.Context, id uuid.UUID) (incidents.Incident, error)
	List(ctx context.Context) ([]incidents.Incident, error)
	Events(ctx context.Context, id uuid.UUID) ([]incidents.Event, error)
}

// workflowStarter begins a durable investigation for a newly created incident.
type workflowStarter interface {
	StartForIncident(ctx context.Context, inc incidents.Incident) error
}

// WorkflowStarter is the exported alias used when wiring the server.
type WorkflowStarter = workflowStarter

// manualAlertIdentity marks incidents opened by an operator rather than an alert.
const manualAlertIdentity = "operator"

// startInvestigationRequest is the body for an operator-initiated investigation.
type startInvestigationRequest struct {
	ServiceID string            `json:"serviceId"`
	Summary   string            `json:"summary"`
	Symptoms  map[string]string `json:"symptoms"`
	Force     bool              `json:"force"`
}

// handleStartInvestigation opens an incident for a configured service on operator
// request. It validates the service reference before any workflow starts and, by
// default, refuses to open a second active manual incident for the same service
// unless force is set.
func (s *Server) handleStartInvestigation(w http.ResponseWriter, r *http.Request) {
	if s.incidents == nil || s.services == nil {
		writeError(w, http.StatusServiceUnavailable, "incidents are not available")
		return
	}
	op, ok := operatorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req startInvestigationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Summary) == "" {
		writeError(w, http.StatusBadRequest, "summary is required")
		return
	}
	svcID, err := uuid.Parse(req.ServiceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid serviceId")
		return
	}

	// Validate the service reference before starting anything.
	svc, err := s.services.Get(r.Context(), svcID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown service")
		return
	}

	if !req.Force {
		if existing, err := s.incidents.FindActiveByAlert(r.Context(), svc.Key, svc.Environment, manualAlertIdentity); err == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":               "an active investigation already exists for this service",
				"activeIncidentId":    existing.ID,
				"activeIncidentState": existing.State,
			})
			return
		} else if !errors.Is(err, incidents.ErrNotFound) {
			s.internalError(w, "dedup check", err)
			return
		}
	}

	inc, err := s.incidents.Create(r.Context(), incidents.NewIncident{
		ServiceID:     &svcID,
		ServiceKey:    svc.Key,
		Environment:   svc.Environment,
		AlertIdentity: manualAlertIdentity,
		Summary:       req.Summary,
		Symptoms:      req.Symptoms,
		Actor:         op.Email,
	})
	if err != nil {
		s.internalError(w, "create incident", err)
		return
	}
	if s.workflow != nil {
		// Start the durable investigation without blocking the response.
		go func(started incidents.Incident) {
			_ = s.workflow.StartForIncident(context.Background(), started)
		}(inc)
	}
	writeJSON(w, http.StatusCreated, inc)
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
