package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

type collectorStub struct{ called bool }

func (s *collectorStub) Enabled() bool                            { return true }
func (s *collectorStub) Collect(context.Context, uuid.UUID) error { s.called = true; return nil }

type classifierStub struct{ called bool }

func (s *classifierStub) Classify(context.Context, uuid.UUID) (jev.ClassificationResult, error) {
	s.called = true
	return jev.ClassificationResult{}, nil
}

func TestCollectClassifiesAfterEvidence(t *testing.T) {
	collector := &collectorStub{}
	classifier := &classifierStub{}
	s := &Server{collector: collector, classifier: classifier}
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/id/collect", nil)
	req.SetPathValue("id", uuid.NewString())
	res := httptest.NewRecorder()
	s.handleCollectEvidence(res, req)
	if res.Code != http.StatusAccepted || !collector.called || !classifier.called {
		t.Fatalf("status %d, collected %v, classified %v", res.Code, collector.called, classifier.called)
	}
}
