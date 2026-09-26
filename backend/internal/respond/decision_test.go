package respond

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

type fakeStore struct {
	inc    incidents.Incident
	events []incidents.Event
	added  []incidents.EventInput
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
	next  jev.NextActionResult
	risk  jev.RiskResult
	err   error
	input jev.Input
}

func (d *fakeDecider) SelectNextAction(_ context.Context, in jev.Input) (jev.NextActionResult, error) {
	d.input = in
	return d.next, d.err
}
func (d *fakeDecider) ClassifyRisk(_ context.Context, in jev.Input) (jev.RiskResult, error) {
	d.input = in
	return d.risk, d.err
}

func setup() (*fakeStore, string) {
	id := uuid.NewString()
	s := &fakeStore{inc: incidents.Incident{ID: uuid.New(), State: incidents.StateInvestigating, Version: 1}, events: []incidents.Event{
		{ID: uuid.MustParse(id), Category: incidents.CategoryObservation, Source: "kubernetes", Reason: "OOMKilled"},
		{ID: uuid.New(), Category: incidents.CategoryHypothesis, Source: "jev", Data: map[string]any{"decision": map[string]any{"classes": []any{map[string]any{"class": "RESOURCE_EXHAUSTION", "confidence": 0.9, "evidence_ids": []string{id}}}, "rationale": "OOM"}}},
	}}
	return s, id
}

func TestNextActionOnlyMovesToPlanning(t *testing.T) {
	for _, action := range []string{"INVESTIGATE_MORE", "TEST_REMEDIATION", "REMEDIATE", "ESCALATE"} {
		t.Run(action, func(t *testing.T) {
			s, evidenceID := setup()
			d := &fakeDecider{next: jev.NextActionResult{Decision: jev.NextAction{Action: action, Rationale: "evaluate memory limit", Confidence: 0.8, EvidenceIDs: []string{evidenceID}}, Metadata: jev.Metadata{Model: "test", DecisionVersion: "jev-v1"}}}
			if _, err := NewService(s, d, 0.65).Next(context.Background(), s.inc.ID); err != nil {
				t.Fatal(err)
			}
			if d.input.Classification == nil || d.input.Classification.Classes[0].Class != "RESOURCE_EXHAUSTION" {
				t.Fatalf("classification missing from input: %+v", d.input)
			}
			want := incidents.StateInvestigating
			if action == "TEST_REMEDIATION" || action == "REMEDIATE" {
				want = incidents.StatePlanning
			}
			if action == "ESCALATE" {
				want = incidents.StateEscalated
			}
			if s.inc.State != want || len(s.added) != 1 || s.added[0].Category != incidents.CategoryDecision {
				t.Fatalf("unsafe state or missing decision: %+v", s)
			}
		})
	}
}

func TestInvalidNextActionEscalates(t *testing.T) {
	s, _ := setup()
	d := &fakeDecider{next: jev.NextActionResult{Decision: jev.NextAction{Action: "REMEDIATE", Rationale: "go", Confidence: 0.9, EvidenceIDs: []string{"invented"}}, Metadata: jev.Metadata{Model: "test", DecisionVersion: "jev-v1"}}}
	_, err := NewService(s, d, 0.65).Next(context.Background(), s.inc.ID)
	if !errors.Is(err, ErrDecision) || s.inc.State != incidents.StateEscalated || len(s.added) != 0 {
		t.Fatalf("invalid action advanced: err=%v, store=%+v", err, s)
	}
}

func TestRiskRequiresStoredActionAndNeverChangesState(t *testing.T) {
	for _, level := range []string{"LOW", "MEDIUM", "HIGH"} {
		t.Run(level, func(t *testing.T) {
			s, evidenceID := setup()
			s.inc.State = incidents.StatePlanning
			actionID := uuid.New()
			s.events = append(s.events, incidents.Event{ID: actionID, Category: incidents.CategoryAction, Reason: "raise memory limit to 1Gi"})
			d := &fakeDecider{risk: jev.RiskResult{Decision: jev.Risk{Risk: level, ActionRef: actionID.String(), EvidenceIDs: []string{evidenceID}}, Metadata: jev.Metadata{Model: "test", DecisionVersion: "jev-v1"}}}
			if _, err := NewService(s, d, 0.65).Risk(context.Background(), s.inc.ID, actionID); err != nil {
				t.Fatal(err)
			}
			if s.inc.State != incidents.StatePlanning || d.input.ActionRef != actionID.String() || len(s.added) != 1 {
				t.Fatalf("risk mutated state or was not recorded: %+v", s)
			}
			if _, err := NewService(s, d, 0.65).Risk(context.Background(), s.inc.ID, uuid.New()); !errors.Is(err, ErrDecision) {
				t.Fatalf("missing action accepted: %v", err)
			}
		})
	}
}
