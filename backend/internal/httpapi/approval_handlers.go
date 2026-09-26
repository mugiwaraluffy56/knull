package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/approval"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
)

type approvalDecider interface {
	Decide(context.Context, uuid.UUID, uuid.UUID, string, operators.Operator, approval.Decision, string) (approval.Record, error)
}

type approvalRequest struct {
	ActionEventID uuid.UUID `json:"actionEventId"`
	ActionDigest  string    `json:"actionDigest"`
	Reason        string    `json:"reason,omitempty"`
}

func (s *Server) handleApproveAction(w http.ResponseWriter, r *http.Request) {
	s.handleActionDecision(w, r, approval.Approved)
}

func (s *Server) handleDenyAction(w http.ResponseWriter, r *http.Request) {
	s.handleActionDecision(w, r, approval.Denied)
}

func (s *Server) handleActionDecision(w http.ResponseWriter, r *http.Request, decision approval.Decision) {
	if s.approvals == nil {
		writeError(w, http.StatusServiceUnavailable, "approval service is unavailable")
		return
	}
	operator, ok := operatorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	incidentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	var request approvalRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid approval JSON")
		return
	}
	record, err := s.approvals.Decide(r.Context(), incidentID, request.ActionEventID, request.ActionDigest, operator, decision, request.Reason)
	if err != nil {
		switch {
		case errors.Is(err, incidents.ErrNotFound):
			writeError(w, http.StatusNotFound, "incident not found")
		case errors.Is(err, approval.ErrInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, approval.ErrIneligible), errors.Is(err, approval.ErrConflict):
			writeError(w, http.StatusConflict, err.Error())
		default:
			s.internalError(w, "record approval decision", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, record)
}
