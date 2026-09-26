package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

// evidenceCollector runs the connected read-only investigators for an incident.
type evidenceCollector interface {
	Collect(ctx context.Context, incidentID uuid.UUID) error
	Enabled() bool
}

type incidentClassifier interface {
	Classify(ctx context.Context, incidentID uuid.UUID) (jev.ClassificationResult, error)
}

// EvidenceCollector is the exported alias used when wiring the server.
type EvidenceCollector = evidenceCollector

// handleCollectEvidence runs read-only investigation for an incident on operator
// request, recording the gathered evidence on its timeline.
func (s *Server) handleCollectEvidence(w http.ResponseWriter, r *http.Request) {
	if s.collector == nil || !s.collector.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "no investigation integrations are connected")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	if err := s.collector.Collect(r.Context(), id); err != nil {
		if errors.Is(err, incidents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.classifier != nil {
		if _, err := s.classifier.Classify(r.Context(), id); err != nil {
			writeError(w, http.StatusBadGateway, "evidence collected, but classification failed: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "collected"})
}
