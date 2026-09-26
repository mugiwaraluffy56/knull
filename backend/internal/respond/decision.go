// Package respond stores Jev's next-action and risk decisions while leaving
// production authorization to the backend approval workflow.
package respond

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/classify"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

var ErrDecision = errors.New("invalid Jev response decision")

type Store interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEvent(context.Context, uuid.UUID, incidents.EventInput) error
	Transition(context.Context, uuid.UUID, incidents.Transition) (incidents.Incident, error)
}

type Decider interface {
	SelectNextAction(context.Context, jev.Input) (jev.NextActionResult, error)
	ClassifyRisk(context.Context, jev.Input) (jev.RiskResult, error)
}

type Service struct {
	store         Store
	decider       Decider
	minConfidence float64
}

func NewService(store Store, decider Decider, minConfidence float64) *Service {
	if minConfidence <= 0 || minConfidence > 1 {
		minConfidence = 0.65
	}
	return &Service{store: store, decider: decider, minConfidence: minConfidence}
}

// Next uses the latest saved classification and the observations it referenced.
// Only the backend changes state; Jev's result can reach PLANNING at most.
func (s *Service) Next(ctx context.Context, id uuid.UUID) (jev.NextActionResult, error) {
	inc, err := s.store.Get(ctx, id)
	if err != nil {
		return jev.NextActionResult{}, err
	}
	if inc.State != incidents.StateInvestigating {
		return jev.NextActionResult{}, fmt.Errorf("next action requires INVESTIGATING state, got %s", inc.State)
	}
	events, err := s.store.Events(ctx, id)
	if err != nil {
		return jev.NextActionResult{}, err
	}
	evidence := classify.EvidenceFromEvents(events)
	classification, err := latestClassification(events)
	if err != nil {
		s.escalate(ctx, inc, "classification missing or invalid")
		return jev.NextActionResult{}, err
	}
	if len(classification.Classes) == 0 || classification.Classes[0].Class == "UNKNOWN" || classification.Classes[0].Confidence < s.minConfidence {
		s.escalate(ctx, inc, "classification uncertain")
		return jev.NextActionResult{}, ErrDecision
	}
	input := jev.Input{Evidence: evidence, Classification: &classification}
	result, err := s.decider.SelectNextAction(ctx, input)
	if err != nil {
		s.escalate(ctx, inc, "next action unavailable")
		return jev.NextActionResult{}, err
	}
	if err := validateNext(result, evidence); err != nil {
		s.escalate(ctx, inc, "next action invalid")
		return jev.NextActionResult{}, err
	}
	refs := make([]string, len(evidence))
	for i, e := range evidence {
		refs[i] = e.ID
	}
	if err := s.store.AppendEvent(ctx, id, incidents.EventInput{Category: incidents.CategoryDecision, Source: "jev-next-action", Actor: "jev", Reason: result.Decision.Rationale, Data: map[string]any{"decision": result.Decision, "metadata": result.Metadata, "inputEvidenceIds": refs}}); err != nil {
		return jev.NextActionResult{}, err
	}
	switch result.Decision.Action {
	case "ESCALATE":
		s.escalate(ctx, inc, "Jev requested escalation")
	case "TEST_REMEDIATION", "REMEDIATE":
		_, err = s.store.Transition(ctx, id, incidents.Transition{To: incidents.StatePlanning, Actor: "knull", Reason: "response planning selected", ExpectedVersion: inc.Version})
		if err != nil {
			return jev.NextActionResult{}, err
		}
	}
	return result, nil
}

// Risk assesses a concrete proposed action event; it cannot approve or run it.
func (s *Service) Risk(ctx context.Context, id, actionID uuid.UUID) (jev.RiskResult, error) {
	inc, err := s.store.Get(ctx, id)
	if err != nil {
		return jev.RiskResult{}, err
	}
	if inc.State != incidents.StatePlanning {
		return jev.RiskResult{}, fmt.Errorf("risk requires PLANNING state, got %s", inc.State)
	}
	events, err := s.store.Events(ctx, id)
	if err != nil {
		return jev.RiskResult{}, err
	}
	var action *incidents.Event
	for i := range events {
		if events[i].ID == actionID && events[i].Category == incidents.CategoryAction {
			action = &events[i]
			break
		}
	}
	if action == nil {
		return jev.RiskResult{}, fmt.Errorf("%w: proposed action not found", ErrDecision)
	}
	input := jev.Input{Evidence: classify.EvidenceFromEvents(events), ActionRef: actionID.String(), Action: action.Reason}
	result, err := s.decider.ClassifyRisk(ctx, input)
	if err != nil {
		s.escalate(ctx, inc, "risk classification unavailable")
		return jev.RiskResult{}, err
	}
	if err := validateRisk(result, input); err != nil {
		s.escalate(ctx, inc, "risk classification invalid")
		return jev.RiskResult{}, err
	}
	if err := s.store.AppendEvent(ctx, id, incidents.EventInput{Category: incidents.CategoryDecision, Source: "jev-risk", Actor: "jev", Reason: "proposed action risk: " + result.Decision.Risk, Data: map[string]any{"decision": result.Decision, "metadata": result.Metadata}}); err != nil {
		return jev.RiskResult{}, err
	}
	return result, nil
}

func latestClassification(events []incidents.Event) (jev.Classification, error) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Source != "jev" || e.Category != incidents.CategoryHypothesis {
			continue
		}
		body, err := json.Marshal(e.Data["decision"])
		if err != nil {
			return jev.Classification{}, ErrDecision
		}
		var decision jev.Classification
		if err := json.Unmarshal(body, &decision); err != nil {
			return jev.Classification{}, ErrDecision
		}
		return decision, nil
	}
	return jev.Classification{}, ErrDecision
}

func validateNext(result jev.NextActionResult, evidence []jev.Evidence) error {
	d := result.Decision
	if !slices.Contains([]string{"INVESTIGATE_MORE", "TEST_REMEDIATION", "REMEDIATE", "ESCALATE"}, d.Action) || d.Rationale == "" || math.IsNaN(d.Confidence) || d.Confidence < 0 || d.Confidence > 1 || result.Metadata.Model == "" || result.Metadata.DecisionVersion == "" {
		return ErrDecision
	}
	return validateRefs(d.EvidenceIDs, evidence)
}

func validateRisk(result jev.RiskResult, input jev.Input) error {
	if !slices.Contains([]string{"LOW", "MEDIUM", "HIGH"}, result.Decision.Risk) || result.Decision.ActionRef != input.ActionRef || result.Metadata.Model == "" || result.Metadata.DecisionVersion == "" {
		return ErrDecision
	}
	return validateRefs(result.Decision.EvidenceIDs, input.Evidence)
}

func validateRefs(refs []string, evidence []jev.Evidence) error {
	known := map[string]bool{}
	for _, e := range evidence {
		known[e.ID] = true
	}
	for _, ref := range refs {
		if !known[ref] {
			return fmt.Errorf("%w: unknown evidence %s", ErrDecision, ref)
		}
	}
	return nil
}

func (s *Service) escalate(ctx context.Context, inc incidents.Incident, reason string) {
	if inc.State.CanTransition(incidents.StateEscalated) {
		_, _ = s.store.Transition(ctx, inc.ID, incidents.Transition{To: incidents.StateEscalated, Actor: "knull", Reason: reason, ExpectedVersion: inc.Version})
	}
}
