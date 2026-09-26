package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/approval"
	"github.com/mugiwaraluffy56/knull/backend/internal/executor"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type productionExecutor interface {
	Execute(context.Context, uuid.UUID, uuid.UUID, string) (executor.Receipt, error)
}

func (s *Server) handleExecuteMemory(w http.ResponseWriter, r *http.Request) {
	if s.executor == nil {
		writeError(w, http.StatusServiceUnavailable, "production execution is disabled")
		return
	}
	// This endpoint consumes an authenticated browser cookie. Require the
	// configured UI origin before allowing a production mutation.
	if r.Header.Get("Origin") != s.allowedOrigin || s.allowedOrigin == "" {
		writeError(w, http.StatusForbidden, "untrusted origin")
		return
	}
	incidentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	var request approvalRequest
	if err := decodeJSON(w, r, &request); err != nil || request.ActionEventID == uuid.Nil || request.ActionDigest == "" {
		writeError(w, http.StatusBadRequest, "invalid execution request")
		return
	}
	receipt, err := s.executor.Execute(r.Context(), incidentID, request.ActionEventID, request.ActionDigest)
	if err == nil {
		writeJSON(w, http.StatusOK, receipt)
		return
	}
	if receipt.RequestID != "" {
		// A write attempt may have succeeded despite an error. Return its
		// receipt and prevent operators from assuming a safe retry.
		writeJSON(w, http.StatusAccepted, receipt)
		return
	}
	switch {
	case errors.Is(err, incidents.ErrNotFound):
		writeError(w, http.StatusNotFound, "incident not found")
	case errors.Is(err, approval.ErrIneligible), errors.Is(err, approval.ErrExpired), errors.Is(err, approval.ErrStale), errors.Is(err, executor.ErrTarget):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.internalError(w, "execute approved memory patch", err)
	}
}
