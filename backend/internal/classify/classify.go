// Package classify turns incident observation events into evidence-backed Jev
// hypotheses. It never authorizes or performs a production action.
package classify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

var ErrNoEvidence = errors.New("no available evidence")
var ErrInvalidDecision = errors.New("invalid classification decision")

type Store interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEvent(context.Context, uuid.UUID, incidents.EventInput) error
	Transition(context.Context, uuid.UUID, incidents.Transition) (incidents.Incident, error)
}

type Decider interface {
	Classify(context.Context, jev.Input) (jev.ClassificationResult, error)
}

type Service struct {
	store         Store
	decider       Decider
	minConfidence float64
	next          interface {
		Next(context.Context, uuid.UUID) (jev.NextActionResult, error)
	}
}

func (s *Service) SetNext(next interface {
	Next(context.Context, uuid.UUID) (jev.NextActionResult, error)
}) {
	s.next = next
}

func NewService(store Store, decider Decider, minConfidence float64) *Service {
	if minConfidence <= 0 || minConfidence > 1 {
		minConfidence = 0.65
	}
	return &Service{store: store, decider: decider, minConfidence: minConfidence}
}

// Classify records the exact evidence IDs used, Jev's ranked output and model
// metadata. Uncertain or invalid decisions cannot move an incident to planning.
func (s *Service) Classify(ctx context.Context, id uuid.UUID) (jev.ClassificationResult, error) {
	inc, err := s.store.Get(ctx, id)
	if err != nil {
		return jev.ClassificationResult{}, err
	}
	if inc.State == incidents.StateReceived {
		inc, err = s.store.Transition(ctx, id, incidents.Transition{To: incidents.StateInvestigating, Actor: "knull", Reason: "evidence classification started", ExpectedVersion: inc.Version})
		if err != nil {
			return jev.ClassificationResult{}, err
		}
	}
	if inc.State != incidents.StateInvestigating {
		return jev.ClassificationResult{}, fmt.Errorf("classification requires INVESTIGATING state, got %s", inc.State)
	}
	events, err := s.store.Events(ctx, id)
	if err != nil {
		return jev.ClassificationResult{}, err
	}
	evidence := EvidenceFromEvents(events)
	if len(evidence) == 0 {
		s.escalate(ctx, inc, "no available evidence for classification")
		return jev.ClassificationResult{}, ErrNoEvidence
	}
	result, err := s.decider.Classify(ctx, jev.Input{Evidence: evidence})
	if err != nil {
		s.escalate(ctx, inc, "classification unavailable or invalid")
		return jev.ClassificationResult{}, err
	}
	if err := validate(result, evidence); err != nil {
		s.escalate(ctx, inc, "classification lacked valid supporting evidence")
		return jev.ClassificationResult{}, err
	}
	ids := make([]string, len(evidence))
	for i, e := range evidence {
		ids[i] = e.ID
	}
	data := map[string]any{"inputEvidenceIds": ids, "decision": result.Decision, "metadata": result.Metadata, "minConfidence": s.minConfidence}
	if err := s.store.AppendEvent(ctx, id, incidents.EventInput{Category: incidents.CategoryHypothesis, Source: "jev", Actor: "jev", Reason: "ranked incident cause hypotheses", Data: data}); err != nil {
		return jev.ClassificationResult{}, err
	}
	leading := result.Decision.Classes[0]
	if leading.Class == "UNKNOWN" || leading.Confidence < s.minConfidence || len(leading.EvidenceIDs) == 0 {
		s.escalate(ctx, inc, "classification uncertain or unsupported")
	} else if s.next != nil {
		if _, err := s.next.Next(ctx, id); err != nil {
			return result, err
		}
	}
	return result, nil
}

// EvidenceFromEvents selects bounded, available observations in event order.
// Event UUIDs are stable references that the UI can link back to the timeline.
func EvidenceFromEvents(events []incidents.Event) []jev.Evidence {
	var out []jev.Evidence
	for _, event := range events {
		if event.Category != incidents.CategoryObservation || event.Data["available"] == false || event.Source == "" {
			continue
		}
		body, err := json.Marshal(event.Data)
		if err != nil {
			continue
		}
		summary := strings.TrimSpace(event.Reason + ": " + string(body))
		if len(summary) > 2000 {
			summary = summary[:2000]
		}
		if summary == "" {
			continue
		}
		out = append(out, jev.Evidence{ID: event.ID.String(), Source: event.Source, Summary: summary})
		if len(out) == 40 {
			break
		}
	}
	return out
}

func validate(result jev.ClassificationResult, evidence []jev.Evidence) error {
	if len(result.Decision.Classes) == 0 || len(result.Decision.Classes) > 6 || result.Metadata.DecisionVersion == "" || result.Metadata.Model == "" {
		return ErrInvalidDecision
	}
	known := map[string]bool{}
	for _, e := range evidence {
		known[e.ID] = true
	}
	for _, h := range result.Decision.Classes {
		if !slices.Contains([]string{"RESOURCE_EXHAUSTION", "BAD_DEPLOYMENT", "DEPENDENCY_FAILURE", "TRAFFIC_SPIKE", "CONFIGURATION_ERROR", "UNKNOWN"}, h.Class) || h.Confidence < 0 || h.Confidence > 1 {
			return ErrInvalidDecision
		}
		if h.Class != "UNKNOWN" && len(h.EvidenceIDs) == 0 {
			return ErrInvalidDecision
		}
		for _, ref := range h.EvidenceIDs {
			if !known[ref] {
				return fmt.Errorf("%w: unknown evidence %s", ErrInvalidDecision, ref)
			}
		}
	}
	for i := 1; i < len(result.Decision.Classes); i++ {
		if result.Decision.Classes[i].Confidence > result.Decision.Classes[i-1].Confidence {
			return fmt.Errorf("%w: classes are not ranked", ErrInvalidDecision)
		}
	}
	return nil
}

func (s *Service) escalate(ctx context.Context, inc incidents.Incident, reason string) {
	if inc.State.CanTransition(incidents.StateEscalated) {
		_, _ = s.store.Transition(ctx, inc.ID, incidents.Transition{To: incidents.StateEscalated, Actor: "jev", Reason: reason, ExpectedVersion: inc.Version})
	}
}
