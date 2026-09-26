package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

// serviceStore is the subset of the services store the API needs.
type serviceStore interface {
	Create(ctx context.Context, in services.Input) (services.Service, error)
	Update(ctx context.Context, id uuid.UUID, in services.Input) (services.Service, error)
	SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (services.Service, error)
	Get(ctx context.Context, id uuid.UUID) (services.Service, error)
	List(ctx context.Context) ([]services.Service, error)
}

// handleListServices returns all configured services.
func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, "service configuration is not available")
		return
	}
	list, err := s.services.List(r.Context())
	if err != nil {
		s.internalError(w, "list services", err)
		return
	}
	if list == nil {
		list = []services.Service{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": list})
}

// handleCreateService creates a new service mapping.
func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, "service configuration is not available")
		return
	}
	var in services.Input
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	svc, err := s.services.Create(r.Context(), in)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, svc)
}

// handleGetService returns one service by id.
func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	id, ok := s.parseServiceID(w, r)
	if !ok {
		return
	}
	svc, err := s.services.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

// handleUpdateService replaces the mutable fields of a service.
func (s *Server) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	id, ok := s.parseServiceID(w, r)
	if !ok {
		return
	}
	var in services.Input
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	svc, err := s.services.Update(r.Context(), id, in)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

// setEnabledRequest toggles a service's enabled flag.
type setEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// handleSetServiceEnabled enables or disables a service.
func (s *Server) handleSetServiceEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := s.parseServiceID(w, r)
	if !ok {
		return
	}
	var req setEnabledRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	svc, err := s.services.SetEnabled(r.Context(), id, req.Enabled)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (s *Server) parseServiceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, "service configuration is not available")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return uuid.Nil, false
	}
	return id, true
}

// writeServiceError maps store errors to HTTP responses. Validation errors are
// returned field-by-field so the UI can guide the operator.
func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	var ve *services.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation failed",
			"fields": ve.Fields,
		})
	case errors.Is(err, services.ErrDuplicate):
		writeError(w, http.StatusConflict, "a service with this key already exists in this environment")
	case errors.Is(err, services.ErrNotFound):
		writeError(w, http.StatusNotFound, "service not found")
	default:
		s.internalError(w, "service operation", err)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst)
}
