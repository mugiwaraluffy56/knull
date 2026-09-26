package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/recoverypolicy"
)

type recoveryPolicyStore interface {
	Put(context.Context, uuid.UUID, recoverypolicy.Policy, uuid.UUID) (recoverypolicy.Snapshot, error)
	Current(context.Context, uuid.UUID) (recoverypolicy.Snapshot, error)
	Version(context.Context, uuid.UUID, int64) (recoverypolicy.Snapshot, error)
}

func (s *Server) handleGetRecoveryPolicy(w http.ResponseWriter, r *http.Request) {
	if s.recoveryPolicies == nil {
		writeError(w, http.StatusServiceUnavailable, "recovery policy configuration is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}
	policy, err := s.recoveryPolicies.Current(r.Context(), id)
	if err != nil {
		s.writeRecoveryPolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handleGetRecoveryPolicyVersion(w http.ResponseWriter, r *http.Request) {
	if s.recoveryPolicies == nil {
		writeError(w, http.StatusServiceUnavailable, "recovery policy configuration is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "invalid policy version")
		return
	}
	policy, err := s.recoveryPolicies.Version(r.Context(), id, version)
	if err != nil {
		s.writeRecoveryPolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handlePutRecoveryPolicy(w http.ResponseWriter, r *http.Request) {
	if s.recoveryPolicies == nil {
		writeError(w, http.StatusServiceUnavailable, "recovery policy configuration is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}
	op, ok := operatorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var policy recoverypolicy.Policy
	if err := decodeJSON(w, r, &policy); err != nil {
		writeError(w, http.StatusBadRequest, "invalid recovery policy JSON")
		return
	}
	snapshot, err := s.recoveryPolicies.Put(r.Context(), id, policy, op.ID)
	if err != nil {
		s.writeRecoveryPolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) writeRecoveryPolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, recoverypolicy.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, recoverypolicy.ErrNotFound), errors.Is(err, recoverypolicy.ErrServiceNotFound):
		writeError(w, http.StatusNotFound, "recovery policy or service not found")
	default:
		s.internalError(w, "recovery policy operation", err)
	}
}
