package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
	"github.com/mugiwaraluffy56/knull/backend/internal/recoverypolicy"
)

var ErrNotReady = errors.New("recovery observation window is not complete")
var ErrIneligible = errors.New("incident is not eligible for recovery verification")

type History interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEvent(context.Context, uuid.UUID, incidents.EventInput) error
	Transition(context.Context, uuid.UUID, incidents.Transition) (incidents.Incident, error)
}
type Policies interface {
	Current(context.Context, uuid.UUID) (recoverypolicy.Snapshot, error)
	RecordAssessment(context.Context, uuid.UUID, recoverypolicy.Snapshot, recoverypolicy.Observation) (uuid.UUID, recoverypolicy.Assessment, error)
}
type Observer interface {
	Observe(context.Context, actions.Target, recoverypolicy.Policy, time.Time, time.Time) (recoverypolicy.Observation, error)
}
type Decider interface {
	VerifyRecovery(context.Context, jev.Input) (jev.RecoveryResult, error)
}

type Service struct {
	History  History
	Policies Policies
	Observer Observer
	Jev      Decider
	Now      func() time.Time
}

type Result struct {
	AssessmentID uuid.UUID                  `json:"assessmentId"`
	Assessment   recoverypolicy.Assessment  `json:"assessment"`
	Observation  recoverypolicy.Observation `json:"observation"`
	Decision     jev.RecoveryResult         `json:"decision"`
	Outcome      string                     `json:"outcome"`
}

// Verify reads a full post-mutation window. A missing signal, absent Jev
// decision, or conflicting decision cannot make an incident RECOVERED.
func (s Service) Verify(ctx context.Context, incidentID uuid.UUID) (Result, error) {
	if s.History == nil || s.Policies == nil || s.Observer == nil || s.Jev == nil {
		return Result{}, ErrIneligible
	}
	inc, err := s.History.Get(ctx, incidentID)
	if err != nil {
		return Result{}, err
	}
	if inc.State != incidents.StateVerifying || inc.ServiceID == nil {
		return Result{}, ErrIneligible
	}
	policy, err := s.Policies.Current(ctx, *inc.ServiceID)
	if err != nil {
		return Result{}, err
	}
	events, err := s.History.Events(ctx, incidentID)
	if err != nil {
		return Result{}, err
	}
	target, completedAt, evidenceID, err := latestExecution(events)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	start := now.Add(-time.Duration(policy.Policy.WindowSeconds) * time.Second)
	if start.Before(completedAt) {
		return Result{}, ErrNotReady
	}
	obs, err := s.Observer.Observe(ctx, target, policy.Policy, start, now)
	if err != nil {
		return Result{}, err
	}
	obs.WindowStart, obs.WindowEnd = start, now
	assessmentID, assessment, err := s.Policies.RecordAssessment(ctx, incidentID, policy, obs)
	if err != nil {
		return Result{}, err
	}
	result := Result{AssessmentID: assessmentID, Assessment: assessment, Observation: obs, Outcome: string(assessment.Outcome)}
	signals := []string{fmt.Sprintf("workload ready=%v desired=%v", obs.ReadyReplicas, obs.DesiredReplicas)}
	if obs.ErrorRateRatio != nil {
		signals = append(signals, fmt.Sprintf("error rate=%g samples=%v", *obs.ErrorRateRatio, obs.ErrorRateSamples))
	}
	if obs.LatencyP95Milliseconds != nil {
		signals = append(signals, fmt.Sprintf("p95 latency=%gms samples=%v", *obs.LatencyP95Milliseconds, obs.LatencySamples))
	}
	decision, decideErr := s.Jev.VerifyRecovery(ctx, jev.Input{Evidence: []jev.Evidence{{ID: evidenceID, Source: "production-executor", Summary: "approved mutation reconciled"}, {ID: assessmentID.String(), Source: "production-observer", Summary: fmt.Sprintf("policy %d outcome %s over %ds", policy.Version, assessment.Outcome, policy.Policy.WindowSeconds)}}, Signals: signals})
	if decideErr == nil {
		result.Decision = decision
	}
	if decideErr != nil {
		result.Outcome = string(recoverypolicy.Uncertain)
	} else if decision.Decision.Outcome != string(recoverypolicy.Recovered) || assessment.Outcome != recoverypolicy.Recovered {
		result.Outcome = decision.Decision.Outcome
		if assessment.Outcome != recoverypolicy.Recovered {
			result.Outcome = string(assessment.Outcome)
		}
	}
	data := map[string]any{"result": result, "jevError": ""}
	if decideErr != nil {
		data["jevError"] = decideErr.Error()
	}
	if err := s.History.AppendEvent(ctx, incidentID, incidents.EventInput{Category: incidents.CategoryDecision, Source: "recovery-verifier", Actor: "recovery-verifier", Reason: "live recovery assessment", Data: data, ObservedAt: now}); err != nil {
		return result, err
	}
	if result.Outcome == string(recoverypolicy.Recovered) {
		_, err = s.History.Transition(ctx, incidentID, incidents.Transition{To: incidents.StateRecovered, Actor: "recovery-verifier", Reason: "live workload and service signals recovered", ExpectedVersion: inc.Version, Data: map[string]any{"assessmentId": assessmentID}})
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func latestExecution(events []incidents.Event) (actions.Target, time.Time, string, error) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Source != "production-executor" {
			continue
		}
		encoded, err := json.Marshal(e.Data)
		if err != nil {
			return actions.Target{}, time.Time{}, "", err
		}
		var value struct {
			Execution struct {
				Status string         `json:"status"`
				Target actions.Target `json:"target"`
			} `json:"execution"`
		}
		if json.Unmarshal(encoded, &value) == nil && value.Execution.Status == "COMPLETED" && e.ObservedAt.After(time.Time{}) {
			return value.Execution.Target, e.ObservedAt, e.ID.String(), nil
		}
	}
	return actions.Target{}, time.Time{}, "", ErrIneligible
}
