package validation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/sandbox"
)

type memoryStore struct {
	incident      incidents.Incident
	events        []incidents.Event
	added         []incidents.EventInput
	recordErr     error
	transitionErr error
}

func (s *memoryStore) Get(context.Context, uuid.UUID) (incidents.Incident, error) {
	return s.incident, nil
}
func (s *memoryStore) Events(context.Context, uuid.UUID) ([]incidents.Event, error) {
	return s.events, nil
}
func (s *memoryStore) AppendEventID(_ context.Context, _ uuid.UUID, in incidents.EventInput) (uuid.UUID, error) {
	if s.recordErr != nil {
		return uuid.Nil, s.recordErr
	}
	s.added = append(s.added, in)
	return uuid.New(), nil
}
func (s *memoryStore) Transition(_ context.Context, _ uuid.UUID, tr incidents.Transition) (incidents.Incident, error) {
	if s.transitionErr != nil && tr.To == incidents.StateAwaitingApproval {
		return incidents.Incident{}, s.transitionErr
	}
	if s.incident.Version != tr.ExpectedVersion || !s.incident.State.CanTransition(tr.To) {
		return incidents.Incident{}, incidents.ErrConflict
	}
	s.incident.State = tr.To
	s.incident.Version++
	return s.incident, nil
}

type memoryRunner struct {
	checked []string
	cleanup bool
}

func (r *memoryRunner) run(ctx context.Context, a actions.Contract, w sandbox.Workload, check func(context.Context, string) error) (sandbox.Run, error) {
	ns := "run-" + w.Memory
	r.checked = append(r.checked, w.Memory)
	err := check(ctx, ns)
	status := "checked"
	if err != nil {
		status = "failed"
	}
	return sandbox.Run{ID: uuid.New(), Namespace: ns, ClusterUID: "sandbox-uid", ActionDigest: a.Digest, Workload: w, Status: status, CleanupVerified: r.cleanup}, err
}
func (r *memoryRunner) RunWithCheck(ctx context.Context, a actions.Contract, w sandbox.Workload, check func(context.Context, string) error) (sandbox.Run, error) {
	return r.run(ctx, a, w, check)
}
func (r *memoryRunner) RunBaselineWithCheck(ctx context.Context, a actions.Contract, w sandbox.Workload, check func(context.Context, string) error) (sandbox.Run, error) {
	return r.run(ctx, a, w, check)
}

type memoryScript struct{ err error }

func (s memoryScript) GenerateAndExecute(_ context.Context, _ ScriptRequest) (ScriptResult, error) {
	return ScriptResult{ArtifactRef: "sha256:" + strings.Repeat("a", 64), ExitCode: 0, Output: "bounded output"}, s.err
}

type memoryPods struct{ candidateOOM bool }

func (p memoryPods) ObservePods(_ context.Context, ns string) (PodObservation, error) {
	if strings.HasSuffix(ns, "256Mi") {
		return PodObservation{Desired: 2, Healthy: 1, OOMKills: 1, ObservedAt: time.Now()}, nil
	}
	oom := 0
	if p.candidateOOM {
		oom = 1
	}
	return PodObservation{Desired: 2, Healthy: 2, OOMKills: oom, ObservedAt: time.Now()}, nil
}

type memoryMetrics struct{}

func (memoryMetrics) ObserveMetrics(context.Context, string) (MetricObservation, error) {
	now := time.Now()
	return MetricObservation{ErrorRate: 0.005, P95Milliseconds: 190, Samples: 100, WindowStart: now.Add(-time.Minute), WindowEnd: now}, nil
}

func setupMemoryValidation(t *testing.T) (*memoryStore, *memoryRunner, uuid.UUID, sandbox.Workload) {
	t.Helper()
	action, err := actions.Seal(actions.Draft{Type: actions.Memory, Environment: "production", Target: actions.Target{Cluster: "prod", Namespace: "checkout", Kind: "Deployment", Name: "checkout-api", Container: "api"}, Field: "resources.limits.memory", CurrentValue: "256Mi", DesiredValue: "1Gi", Preconditions: actions.Preconditions{ResourceUID: uuid.NewString(), ResourceVersion: "1", CurrentValue: "256Mi"}, ExpectedImpact: "restart pods", Risk: "MEDIUM", EvidenceIDs: []string{"ev-1"}})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	store := &memoryStore{incident: incidents.Incident{ID: uuid.New(), State: incidents.StatePlanning, Version: 1}, events: []incidents.Event{{ID: id, Category: incidents.CategoryAction, Source: "action-plan", Data: map[string]any{"contract": action}}}}
	runner := &memoryRunner{cleanup: true}
	return store, runner, id, sandbox.Workload{ImageDigest: "registry.example/checkout@sha256:" + strings.Repeat("b", 64), Container: "api", Replicas: 2, CPU: "500m", Memory: "1Gi"}
}

func TestMemoryValidationRequiresTwoCheckedRunsAndAudit(t *testing.T) {
	store, runner, id, workload := setupMemoryValidation(t)
	result, err := NewService(store, runner, memoryScript{}, memoryPods{}, memoryMetrics{}).ValidateMemory(context.Background(), store.incident.ID, id, workload)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || store.incident.State != incidents.StateAwaitingApproval || len(store.added) != 1 || len(runner.checked) != 2 || runner.checked[0] != "256Mi" || runner.checked[1] != "1Gi" {
		t.Fatalf("invalid validation: %+v, state=%s, runs=%v", result, store.incident.State, runner.checked)
	}
}

func TestMemoryValidationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*memoryStore, *memoryRunner, *memoryScript, *memoryPods)
	}{
		{"candidate OOM", func(_ *memoryStore, _ *memoryRunner, _ *memoryScript, p *memoryPods) { p.candidateOOM = true }},
		{"script unavailable", func(_ *memoryStore, _ *memoryRunner, s *memoryScript, _ *memoryPods) {
			s.err = errors.New("unavailable")
		}},
		{"cleanup missing", func(_ *memoryStore, r *memoryRunner, _ *memoryScript, _ *memoryPods) { r.cleanup = false }},
		{"audit unavailable", func(st *memoryStore, _ *memoryRunner, _ *memoryScript, _ *memoryPods) {
			st.recordErr = errors.New("audit unavailable")
		}},
		{"state conflict", func(st *memoryStore, _ *memoryRunner, _ *memoryScript, _ *memoryPods) {
			st.transitionErr = incidents.ErrConflict
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, runner, id, workload := setupMemoryValidation(t)
			script, pods := memoryScript{}, memoryPods{}
			tc.setup(store, runner, &script, &pods)
			result, err := NewService(store, runner, script, pods, memoryMetrics{}).ValidateMemory(context.Background(), store.incident.ID, id, workload)
			if err == nil || result.Passed || store.incident.State == incidents.StateAwaitingApproval {
				t.Fatalf("failure passed: result=%+v state=%s err=%v", result, store.incident.State, err)
			}
		})
	}
}
