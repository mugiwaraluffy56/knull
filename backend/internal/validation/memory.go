// Package validation reproduces the known memory-limit failure and checks a
// candidate fix inside two separate dedicated-cluster sandbox runs.
package validation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/sandbox"
)

var ErrFailed = errors.New("sandbox validation failed")

type ScriptRequest struct {
	Namespace       string `json:"namespace"`
	ActionDigest    string `json:"actionDigest"`
	ImageDigest     string `json:"imageDigest"`
	MemoryLimit     string `json:"memoryLimit"`
	DurationSeconds int    `json:"durationSeconds"`
	MaxRequests     int    `json:"maxRequests"`
}
type ScriptResult struct {
	ArtifactRef string `json:"artifactRef"`
	ExitCode    int    `json:"exitCode"`
	Output      string `json:"output"`
}
type PodObservation struct {
	Desired    int       `json:"desired"`
	Healthy    int       `json:"healthy"`
	OOMKills   int       `json:"oomKills"`
	ObservedAt time.Time `json:"observedAt"`
}
type MetricObservation struct {
	ErrorRate       float64   `json:"errorRate"`
	P95Milliseconds float64   `json:"p95Milliseconds"`
	Samples         int       `json:"samples"`
	WindowStart     time.Time `json:"windowStart"`
	WindowEnd       time.Time `json:"windowEnd"`
}
type Evidence struct {
	Script  ScriptResult      `json:"script"`
	Pods    PodObservation    `json:"pods"`
	Metrics MetricObservation `json:"metrics"`
}
type Result struct {
	ActionEventID     uuid.UUID   `json:"actionEventId"`
	ActionDigest      string      `json:"actionDigest"`
	Baseline          sandbox.Run `json:"baseline"`
	Candidate         sandbox.Run `json:"candidate"`
	BaselineEvidence  Evidence    `json:"baselineEvidence"`
	CandidateEvidence Evidence    `json:"candidateEvidence"`
	Passed            bool        `json:"passed"`
	Failure           string      `json:"failure,omitempty"`
}

type ScriptEngine interface {
	GenerateAndExecute(context.Context, ScriptRequest) (ScriptResult, error)
}
type PodObserver interface {
	ObservePods(context.Context, string) (PodObservation, error)
}
type MetricObserver interface {
	ObserveMetrics(context.Context, string) (MetricObservation, error)
}
type Runner interface {
	RunWithCheck(context.Context, actions.Contract, sandbox.Workload, func(context.Context, string) error) (sandbox.Run, error)
	RunBaselineWithCheck(context.Context, actions.Contract, sandbox.Workload, func(context.Context, string) error) (sandbox.Run, error)
}
type Store interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEventID(context.Context, uuid.UUID, incidents.EventInput) (uuid.UUID, error)
	Transition(context.Context, uuid.UUID, incidents.Transition) (incidents.Incident, error)
}
type Service struct {
	store   Store
	runner  Runner
	script  ScriptEngine
	pods    PodObserver
	metrics MetricObserver
}

func NewService(store Store, runner Runner, script ScriptEngine, pods PodObserver, metrics MetricObserver) *Service {
	return &Service{store, runner, script, pods, metrics}
}

func (s *Service) ValidateMemory(ctx context.Context, incidentID, actionEventID uuid.UUID, candidate sandbox.Workload) (result Result, err error) {
	result.ActionEventID = actionEventID
	if s == nil || s.store == nil {
		return result, fmt.Errorf("%w: incident store unavailable", ErrFailed)
	}
	inc, err := s.store.Get(ctx, incidentID)
	if err != nil {
		return result, err
	}
	if inc.State != incidents.StatePlanning {
		return result, fmt.Errorf("%w: incident must be planning", ErrFailed)
	}
	action, err := s.findAction(ctx, incidentID, actionEventID)
	if err != nil {
		return result, err
	}
	if action.Type != actions.Memory || action.CurrentValue != "256Mi" || action.DesiredValue != "1Gi" || candidate.Memory != "1Gi" {
		return result, fmt.Errorf("%w: unsupported memory scenario", ErrFailed)
	}
	if s.runner == nil || s.script == nil || s.pods == nil || s.metrics == nil {
		return result, fmt.Errorf("%w: validation dependencies unavailable", ErrFailed)
	}
	inc, err = s.store.Transition(ctx, incidentID, incidents.Transition{To: incidents.StateValidating, Actor: "sandbox", Reason: "memory-limit validation started", ExpectedVersion: inc.Version})
	if err != nil {
		return result, err
	}
	result.ActionDigest = action.Digest
	defer func() {
		if err != nil {
			result.Failure = err.Error()
		}
		checksPassed := err == nil
		result.Passed = checksPassed
		finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		reason := "memory-limit validation failed"
		if checksPassed {
			reason = "memory-limit validation passed"
		}
		_, recordErr := s.store.AppendEventID(finishCtx, incidentID, incidents.EventInput{Category: incidents.CategoryObservation, Source: "sandbox-validation", Actor: "sandbox", Reason: reason, Data: map[string]any{"result": result, "available": checksPassed}})
		if recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		target := incidents.StateEscalated
		transitionReason := "sandbox validation failed"
		if err == nil {
			target = incidents.StateAwaitingApproval
			transitionReason = "independent sandbox validation passed"
		}
		_, transitionErr := s.store.Transition(finishCtx, incidentID, incidents.Transition{To: target, Actor: "sandbox", Reason: transitionReason, ExpectedVersion: inc.Version})
		err = errors.Join(err, transitionErr)
		result.Passed = err == nil
		if err != nil {
			result.Failure = err.Error()
		}
	}()
	baseline := candidate
	baseline.Memory = action.CurrentValue
	result.Baseline, err = s.runner.RunBaselineWithCheck(ctx, action, baseline, func(checkCtx context.Context, namespace string) error {
		observation, checkErr := s.observe(checkCtx, namespace, action, baseline)
		result.BaselineEvidence = observation
		if checkErr != nil {
			return checkErr
		}
		if observation.Pods.OOMKills < 1 || observation.Pods.Healthy >= observation.Pods.Desired {
			return fmt.Errorf("%w: 256Mi failure did not reproduce", ErrFailed)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	result.Candidate, err = s.runner.RunWithCheck(ctx, action, candidate, func(checkCtx context.Context, namespace string) error {
		observation, checkErr := s.observe(checkCtx, namespace, action, candidate)
		result.CandidateEvidence = observation
		if checkErr != nil {
			return checkErr
		}
		if observation.Script.ExitCode != 0 || observation.Pods.Healthy != observation.Pods.Desired || observation.Pods.OOMKills != 0 || observation.Metrics.ErrorRate > 0.01 || observation.Metrics.P95Milliseconds > 500 {
			return fmt.Errorf("%w: candidate did not meet health thresholds", ErrFailed)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if !result.Baseline.CleanupVerified || !result.Candidate.CleanupVerified || result.Baseline.Status != "checked" || result.Candidate.Status != "checked" {
		return result, fmt.Errorf("%w: sandbox cleanup or checks incomplete", ErrFailed)
	}
	if result.Baseline.ID == uuid.Nil || result.Candidate.ID == uuid.Nil || result.Baseline.ID == result.Candidate.ID || result.Baseline.Namespace == "" || result.Candidate.Namespace == "" || result.Baseline.Namespace == result.Candidate.Namespace || result.Baseline.ClusterUID == "" || result.Baseline.ClusterUID != result.Candidate.ClusterUID || result.Baseline.ActionDigest != action.Digest || result.Candidate.ActionDigest != action.Digest || result.Baseline.Workload.Memory != action.CurrentValue || result.Candidate.Workload.Memory != action.DesiredValue {
		return result, fmt.Errorf("%w: sandbox run identity or action mismatch", ErrFailed)
	}
	return result, nil
}

func (s *Service) observe(ctx context.Context, namespace string, action actions.Contract, workload sandbox.Workload) (Evidence, error) {
	request := ScriptRequest{Namespace: namespace, ActionDigest: action.Digest, ImageDigest: workload.ImageDigest, MemoryLimit: workload.Memory, DurationSeconds: 120, MaxRequests: 1000}
	script, err := s.script.GenerateAndExecute(ctx, request)
	if err != nil {
		return Evidence{}, err
	}
	if script.ArtifactRef == "" || len(script.ArtifactRef) > 1024 || len(script.Output) > 8192 || script.ExitCode < 0 {
		return Evidence{}, fmt.Errorf("%w: script artifact or output invalid", ErrFailed)
	}
	pods, err := s.pods.ObservePods(ctx, namespace)
	if err != nil {
		return Evidence{}, err
	}
	metrics, err := s.metrics.ObserveMetrics(ctx, namespace)
	if err != nil {
		return Evidence{}, err
	}
	if pods.Desired < 1 || pods.Desired > 6 || pods.Healthy < 0 || pods.Healthy > pods.Desired || pods.OOMKills < 0 || pods.ObservedAt.IsZero() || metrics.Samples < 10 || metrics.WindowStart.IsZero() || metrics.WindowEnd.IsZero() || !metrics.WindowEnd.After(metrics.WindowStart) || math.IsNaN(metrics.ErrorRate) || math.IsInf(metrics.ErrorRate, 0) || metrics.ErrorRate < 0 || metrics.ErrorRate > 1 || math.IsNaN(metrics.P95Milliseconds) || math.IsInf(metrics.P95Milliseconds, 0) || metrics.P95Milliseconds < 0 {
		return Evidence{}, fmt.Errorf("%w: independent observations incomplete", ErrFailed)
	}
	return Evidence{Script: script, Pods: pods, Metrics: metrics}, nil
}

func (s *Service) findAction(ctx context.Context, incidentID, actionEventID uuid.UUID) (actions.Contract, error) {
	events, err := s.store.Events(ctx, incidentID)
	if err != nil {
		return actions.Contract{}, err
	}
	for _, event := range events {
		if event.ID != actionEventID || event.Category != incidents.CategoryAction || event.Source != "action-plan" {
			continue
		}
		body, err := json.Marshal(event.Data["contract"])
		if err != nil {
			return actions.Contract{}, err
		}
		var action actions.Contract
		if err := json.Unmarshal(body, &action); err != nil {
			return actions.Contract{}, err
		}
		if err := action.Validate(); err != nil {
			return actions.Contract{}, err
		}
		return action, nil
	}
	return actions.Contract{}, fmt.Errorf("%w: action not found", ErrFailed)
}
