package classify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

type fakeStore struct {
	inc    incidents.Incident
	events []incidents.Event
	added  []incidents.EventInput
}

type providerStub struct{ evidenceID string }

func (p providerStub) Decide(context.Context, string, string, map[string]any, jev.Input) (jev.ModelResponse, error) {
	return jev.ModelResponse{Text: []byte(`{"classes":[{"class":"RESOURCE_EXHAUSTION","confidence":0.9,"evidence_ids":["` + p.evidenceID + `"]}],"rationale":"OOM"}`), Model: "test", ResponseID: "resp-1"}, nil
}

type handlerTransport struct{ handler http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res := httptest.NewRecorder()
	t.handler.ServeHTTP(res, req)
	return res.Result(), nil
}

func TestMCPClientCallsJevServer(t *testing.T) {
	id := uuid.NewString()
	server := jev.NewServer(jev.NewService(providerStub{evidenceID: id}, "test"))
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	client := &MCPClient{Endpoint: "http://jev.test/mcp", HTTPClient: &http.Client{Transport: handlerTransport{handler: handler}}}
	result, err := client.Classify(context.Background(), jev.Input{Evidence: []jev.Evidence{{ID: id, Source: "kubernetes", Summary: "OOMKilled"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Classes[0].EvidenceIDs[0] != id {
		t.Fatalf("wrong result: %+v", result)
	}
}

func (s *fakeStore) Get(context.Context, uuid.UUID) (incidents.Incident, error) { return s.inc, nil }
func (s *fakeStore) Events(context.Context, uuid.UUID) ([]incidents.Event, error) {
	return s.events, nil
}
func (s *fakeStore) AppendEvent(_ context.Context, _ uuid.UUID, e incidents.EventInput) error {
	s.added = append(s.added, e)
	return nil
}
func (s *fakeStore) Transition(_ context.Context, _ uuid.UUID, tr incidents.Transition) (incidents.Incident, error) {
	s.inc.State = tr.To
	s.inc.Version++
	return s.inc, nil
}

type fakeDecider struct {
	result jev.ClassificationResult
	err    error
	input  jev.Input
}

func (d *fakeDecider) Classify(_ context.Context, input jev.Input) (jev.ClassificationResult, error) {
	d.input = input
	return d.result, d.err
}

func TestClassificationUsesStoredEvidenceAndEscalatesUnknown(t *testing.T) {
	evidenceID := uuid.New()
	changeID := uuid.New()
	s := &fakeStore{inc: incidents.Incident{ID: uuid.New(), State: incidents.StateInvestigating, Version: 1}, events: []incidents.Event{
		{ID: evidenceID, Category: incidents.CategoryObservation, Source: "kubernetes", Reason: "OOMKilled", Data: map[string]any{"reason": "container out of memory"}},
		{ID: changeID, Category: incidents.CategoryObservation, Source: "github", Reason: "memory limit changed", Data: map[string]any{"before": "1Gi", "after": "256Mi"}},
		{ID: uuid.New(), Category: incidents.CategoryObservation, Source: "github", Data: map[string]any{"available": false}},
	}}
	d := &fakeDecider{result: jev.ClassificationResult{Decision: jev.Classification{Classes: []jev.Hypothesis{{Class: "RESOURCE_EXHAUSTION", Confidence: 0.9, EvidenceIDs: []string{evidenceID.String(), changeID.String()}}}, Rationale: "OOM after memory limit cut"}, Metadata: jev.Metadata{Model: "test", DecisionVersion: "jev-v1"}}}
	if _, err := NewService(s, d, 0.65).Classify(context.Background(), s.inc.ID); err != nil {
		t.Fatal(err)
	}
	if len(d.input.Evidence) != 2 || d.input.Evidence[0].ID != evidenceID.String() || d.input.Evidence[1].ID != changeID.String() {
		t.Fatalf("wrong input: %+v", d.input)
	}
	if s.inc.State != incidents.StateInvestigating || len(s.added) != 1 || s.added[0].Category != incidents.CategoryHypothesis {
		t.Fatalf("classification not persisted: %+v", s)
	}
	refs := s.added[0].Data["inputEvidenceIds"].([]string)
	if len(refs) != 2 || refs[0] != evidenceID.String() || refs[1] != changeID.String() {
		t.Fatalf("wrong stored references: %v", refs)
	}
	d.result.Decision.Classes = []jev.Hypothesis{{Class: "UNKNOWN", Confidence: 0.8}}
	if _, err := NewService(s, d, 0.65).Classify(context.Background(), s.inc.ID); err != nil {
		t.Fatal(err)
	}
	if s.inc.State != incidents.StateEscalated {
		t.Fatalf("unknown class did not escalate: %s", s.inc.State)
	}
}

func TestInvalidReferencesAndProviderErrorsEscalate(t *testing.T) {
	e := incidents.Event{ID: uuid.New(), Category: incidents.CategoryObservation, Source: "kubernetes", Reason: "OOM"}
	for name, decider := range map[string]*fakeDecider{
		"invented reference": {result: jev.ClassificationResult{Decision: jev.Classification{Classes: []jev.Hypothesis{{Class: "RESOURCE_EXHAUSTION", Confidence: 0.9, EvidenceIDs: []string{"invented"}}}}, Metadata: jev.Metadata{Model: "test", DecisionVersion: "jev-v1"}}},
		"provider error":     {err: errors.New("unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			s := &fakeStore{inc: incidents.Incident{ID: uuid.New(), State: incidents.StateInvestigating, Version: 1}, events: []incidents.Event{e}}
			if _, err := NewService(s, decider, 0.65).Classify(context.Background(), s.inc.ID); err == nil {
				t.Fatal("expected error")
			}
			if s.inc.State != incidents.StateEscalated || len(s.added) != 0 {
				t.Fatalf("unsafe result persisted or not escalated: %+v", s)
			}
		})
	}
}
