package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/recovery"
)

type recoveryVerifier interface {
	Verify(context.Context, uuid.UUID) (recovery.Result, error)
}

func (s *Server) handleVerifyRecovery(w http.ResponseWriter, r *http.Request) {
	if s.recoveryVerifier == nil {
		writeError(w, http.StatusServiceUnavailable, "live recovery verifier is not configured")
		return
	}
	if s.allowedOrigin == "" || r.Header.Get("Origin") != s.allowedOrigin {
		writeError(w, http.StatusForbidden, "untrusted origin")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	result, err := s.recoveryVerifier.Verify(r.Context(), id)
	if err == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	if errors.Is(err, recovery.ErrNotReady) || errors.Is(err, recovery.ErrIneligible) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.internalError(w, "verify live recovery", err)
}
