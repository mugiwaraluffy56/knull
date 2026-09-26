package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/approval"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type fakeGate struct {
	action actions.Contract
	err    error
	claims int
}

func (g *fakeGate) Check(_ context.Context, _, _ uuid.UUID, _ string, _ approval.TargetReader) (actions.Contract, error) {
	return g.action, g.err
}
func (g *fakeGate) ClaimForExecution(_ context.Context, _, _ uuid.UUID, _ string, _ approval.TargetReader) (actions.Contract, error) {
	g.claims++
	return g.action, g.err
}

type fakeClient struct {
	action   actions.Contract
	patchErr error
	patches  int
	live     actions.Preconditions
}

func (c *fakeClient) ReadPreconditions(context.Context, actions.Target, string) (actions.Preconditions, error) {
	return c.live, nil
}
func (c *fakeClient) PatchMemory(context.Context, actions.Contract) (*appsv1.Deployment, json.RawMessage, error) {
	c.patches++
	if c.patchErr != nil {
		return nil, json.RawMessage(`[{"op":"replace"}]`), c.patchErr
	}
	return deployment(c.action), json.RawMessage(`[{"op":"replace"}]`), nil
}

type fakeHistory struct {
	events      []incidents.EventInput
	transitions []incidents.Transition
}

func (h *fakeHistory) Get(context.Context, uuid.UUID) (incidents.Incident, error) {
	return incidents.Incident{State: incidents.StateRemediating, Version: 5}, nil
}
func (h *fakeHistory) AppendEvent(_ context.Context, _ uuid.UUID, event incidents.EventInput) error {
	h.events = append(h.events, event)
	return nil
}
func (h *fakeHistory) Transition(_ context.Context, _ uuid.UUID, transition incidents.Transition) (incidents.Incident, error) {
	h.transitions = append(h.transitions, transition)
	return incidents.Incident{}, nil
}

func TestExecuteRequiresApprovalBeforeClaimOrPatch(t *testing.T) {
	action := memoryAction(t)
	gate := &fakeGate{action: action, err: approval.ErrIneligible}
	client := &fakeClient{action: action}
	history := &fakeHistory{}
	_, err := (Service{Gate: gate, Client: client, History: history}).Execute(context.Background(), uuid.New(), uuid.New(), action.Digest)
	if !errors.Is(err, approval.ErrIneligible) || gate.claims != 0 || client.patches != 0 || len(history.events) != 0 {
		t.Fatalf("approval bypass: %v %+v %+v", err, gate, client)
	}
}

func TestExecuteRecordsCompletedOnlyAfterLiveValue(t *testing.T) {
	action := memoryAction(t)
	gate := &fakeGate{action: action}
	client := &fakeClient{action: action, live: actions.Preconditions{ResourceUID: action.Preconditions.ResourceUID, ResourceVersion: "9124", CurrentValue: "1Gi"}}
	history := &fakeHistory{}
	receipt, err := (Service{Gate: gate, Client: client, History: history}).Execute(context.Background(), uuid.New(), uuid.New(), action.Digest)
	if err != nil || receipt.Status != Completed || gate.claims != 1 || client.patches != 1 || len(history.events) != 1 || len(history.transitions) != 1 || history.transitions[0].To != incidents.StateVerifying {
		t.Fatalf("completion: %v %+v %+v %+v", err, receipt, gate, history)
	}
}

func TestExecuteLeavesAmbiguousWriteForReconciliation(t *testing.T) {
	action := memoryAction(t)
	gate := &fakeGate{action: action}
	client := &fakeClient{action: action, patchErr: context.DeadlineExceeded}
	history := &fakeHistory{}
	receipt, err := (Service{Gate: gate, Client: client, History: history}).Execute(context.Background(), uuid.New(), uuid.New(), action.Digest)
	if !errors.Is(err, context.DeadlineExceeded) || receipt.Status != Unknown || len(history.events) != 1 || len(history.transitions) != 0 {
		t.Fatalf("ambiguous result: %v %+v %+v", err, receipt, history)
	}
}
