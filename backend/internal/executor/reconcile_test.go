package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

func eventData(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func priorExecution(t *testing.T) (actions.Contract, uuid.UUID, *fakeHistory) {
	t.Helper()
	action := memoryAction(t)
	planID := uuid.New()
	receipt := Receipt{RequestID: uuid.NewString(), ActionEventID: planID, ActionDigest: action.Digest, Status: Unknown, Target: action.Target, Field: action.Field}
	history := &fakeHistory{stored: []incidents.Event{
		{ID: planID, Seq: 1, Category: incidents.CategoryAction, Source: "action-plan", Data: eventData(t, map[string]any{"contract": action})},
		{ID: uuid.New(), Seq: 2, Category: incidents.CategoryAction, Source: "approval-gate", ToState: incidents.StateRemediating, Data: eventData(t, map[string]any{"actionEventId": planID, "actionDigest": action.Digest})},
		{ID: uuid.New(), Seq: 3, Category: incidents.CategoryAction, Source: "production-executor", Data: eventData(t, map[string]any{"execution": receipt})},
	}}
	return action, planID, history
}

func TestReconcileLostResponseAfterMutationDoesNotRepeatPatch(t *testing.T) {
	action, planID, history := priorExecution(t)
	client := &fakeClient{action: action, live: actions.Preconditions{ResourceUID: action.Preconditions.ResourceUID, ResourceVersion: "9124", CurrentValue: "1Gi"}}
	receipt, err := (Service{Client: client, History: history}).Reconcile(context.Background(), uuid.New(), planID, action.Digest)
	if err != nil || receipt.Status != Completed || client.patches != 0 || len(history.transitions) != 1 || history.transitions[0].To != incidents.StateVerifying {
		t.Fatalf("post-write reconciliation: %v %+v %+v", err, receipt, history)
	}
}

func TestReconcileLostResponseBeforeMutationRetriesWithOriginalPreconditions(t *testing.T) {
	action, planID, history := priorExecution(t)
	client := &fakeClient{action: action, live: action.Preconditions}
	receipt, err := (Service{Client: client, History: history}).Reconcile(context.Background(), uuid.New(), planID, action.Digest)
	if err != nil || receipt.Status != Completed || client.patches != 1 || len(history.transitions) != 1 || history.transitions[0].To != incidents.StateVerifying {
		t.Fatalf("safe retry: %v %+v %+v", err, receipt, history)
	}
}

func TestReconcileConflictingProductionStateEscalatesWithoutMutation(t *testing.T) {
	action, planID, history := priorExecution(t)
	client := &fakeClient{action: action, live: actions.Preconditions{ResourceUID: action.Preconditions.ResourceUID, ResourceVersion: "9124", CurrentValue: "512Mi"}}
	receipt, err := (Service{Client: client, History: history}).Reconcile(context.Background(), uuid.New(), planID, action.Digest)
	if !errors.Is(err, ErrReconcile) || receipt.Status != Failed || client.patches != 0 || len(history.transitions) != 1 || history.transitions[0].To != incidents.StateEscalated {
		t.Fatalf("conflicting state: %v %+v %+v", err, receipt, history)
	}
}

func TestReconcileUnreadableProductionStateEscalatesWithoutMutation(t *testing.T) {
	action, planID, history := priorExecution(t)
	client := &fakeClient{action: action, readErr: context.DeadlineExceeded}
	receipt, err := (Service{Client: client, History: history}).Reconcile(context.Background(), uuid.New(), planID, action.Digest)
	if !errors.Is(err, ErrReconcile) || receipt.Status != Unknown || client.patches != 0 || len(history.transitions) != 1 || history.transitions[0].To != incidents.StateEscalated {
		t.Fatalf("unreadable state: %v %+v %+v", err, receipt, history)
	}
}

func TestReconcileRejectsUnclaimedOrAlteredAction(t *testing.T) {
	action, planID, history := priorExecution(t)
	client := &fakeClient{action: action, live: action.Preconditions}
	for _, bad := range []string{"other-digest", action.Digest} {
		if bad == action.Digest {
			history.stored[1].Data["actionDigest"] = "tampered"
		}
		_, err := (Service{Client: client, History: history}).Reconcile(context.Background(), uuid.New(), planID, bad)
		if !errors.Is(err, ErrReconcile) || client.patches != 0 {
			t.Fatalf("unclaimed action: %v %+v", err, client)
		}
	}
}

func TestReconcileClaimBeforeIntentCanRetryAfterStateRead(t *testing.T) {
	action, planID, history := priorExecution(t)
	history.stored = history.stored[:2]
	client := &fakeClient{action: action, live: action.Preconditions}
	receipt, err := (Service{Client: client, History: history}).Reconcile(context.Background(), uuid.New(), planID, action.Digest)
	if err != nil || receipt.Status != Completed || client.patches != 1 {
		t.Fatalf("claim-only recovery: %v %+v %+v", err, receipt, client)
	}
}
