package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

var ErrReconcile = errors.New("production execution cannot be reconciled")

// Reconcile reviews an already claimed execution. It never asks the approval
// gate for new mutation authority: a prior REMEDIATING claim, sealed action,
// and recorded attempt must agree before it reads or retries production.
// Repeated retries use the original UID, resource version, and value as an
// API-server-side compare-and-swap key, so only one patch can take effect.
func (s Service) Reconcile(ctx context.Context, incidentID, actionEventID uuid.UUID, digest string) (Receipt, error) {
	if s.Client == nil || s.History == nil {
		return Receipt{}, ErrReconcile
	}
	inc, err := s.History.Get(ctx, incidentID)
	if err != nil {
		return Receipt{}, err
	}
	if inc.State != incidents.StateRemediating {
		return Receipt{}, ErrReconcile
	}
	events, err := s.History.Events(ctx, incidentID)
	if err != nil {
		return Receipt{}, err
	}
	action, prior, err := claimedAttempt(events, actionEventID, digest)
	if err != nil {
		return Receipt{}, err
	}
	if err := allowedMemory(action); err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{RequestID: uuid.NewString(), ActionEventID: actionEventID, ActionDigest: digest, Status: Unknown, Target: action.Target, Field: action.Field, ObservedAt: time.Now().UTC(), PriorRequestID: prior.RequestID}
	live, err := s.Client.ReadPreconditions(ctx, action.Target, action.Field)
	if err != nil {
		return s.escalateUnknown(ctx, incidentID, inc.Version, receipt, "production state could not be read for reconciliation")
	}
	receipt.ResourceUID, receipt.ResourceVersion, receipt.ResultingValue = live.ResourceUID, live.ResourceVersion, live.CurrentValue
	if live.ResourceUID != action.Preconditions.ResourceUID {
		return s.escalate(ctx, incidentID, inc.Version, receipt, "production resource identity changed")
	}
	if live.CurrentValue == action.DesiredValue {
		return s.complete(ctx, incidentID, inc.Version, receipt)
	}
	if live.ResourceVersion != action.Preconditions.ResourceVersion || live.CurrentValue != action.CurrentValue {
		return s.escalate(ctx, incidentID, inc.Version, receipt, "production state conflicts with approved action")
	}
	// The exact old UID, version, and 256Mi value prove that the previous
	// attempt did not change this Deployment. JSON Patch repeats these tests
	// atomically, guarding a race between this read and the retry.
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	updated, patch, err := s.Client.PatchMemory(ctx, action)
	receipt.SubmittedPatch = patch
	if err != nil {
		if writeErr := s.record(ctx, incidentID, receipt); writeErr != nil {
			return receipt, writeErr
		}
		return receipt, err
	}
	if updated == nil || string(updated.UID) != action.Preconditions.ResourceUID {
		if writeErr := s.record(ctx, incidentID, receipt); writeErr != nil {
			return receipt, writeErr
		}
		return receipt, ErrReconcile
	}
	receipt.Status = Accepted
	receipt.ResourceVersion = updated.ResourceVersion
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	live, err = s.Client.ReadPreconditions(ctx, action.Target, action.Field)
	if err != nil {
		receipt.Status = Unknown
		if writeErr := s.record(ctx, incidentID, receipt); writeErr != nil {
			return receipt, writeErr
		}
		return receipt, err
	}
	receipt.ResourceUID, receipt.ResourceVersion, receipt.ResultingValue = live.ResourceUID, live.ResourceVersion, live.CurrentValue
	if live.ResourceUID == action.Preconditions.ResourceUID && live.CurrentValue == action.DesiredValue {
		return s.complete(ctx, incidentID, inc.Version, receipt)
	}
	return s.escalate(ctx, incidentID, inc.Version, receipt, "production result conflicts with approved action")
}

func claimedAttempt(events []incidents.Event, actionEventID uuid.UUID, digest string) (actions.Contract, Receipt, error) {
	if actionEventID == uuid.Nil || digest == "" {
		return actions.Contract{}, Receipt{}, ErrReconcile
	}
	var plan incidents.Event
	var claim incidents.Event
	var attempt incidents.Event
	var latestPlanSeq int64
	for _, event := range events {
		if event.Source == "action-plan" && event.Category == incidents.CategoryAction && event.Seq > latestPlanSeq {
			latestPlanSeq, plan = event.Seq, event
		}
		if event.Source == "approval-gate" && event.ToState == incidents.StateRemediating && event.Seq > claim.Seq {
			claim = event
		}
		if event.Source == "production-executor" && event.Category == incidents.CategoryAction && event.Seq > attempt.Seq {
			attempt = event
		}
	}
	if plan.ID != actionEventID || claim.ID == uuid.Nil || claim.Seq <= plan.Seq {
		return actions.Contract{}, Receipt{}, ErrReconcile
	}
	var body struct {
		Contract actions.Contract `json:"contract"`
	}
	if err := decodeEventValue(plan.Data, &body); err != nil || body.Contract.Validate() != nil || body.Contract.Digest != digest {
		return actions.Contract{}, Receipt{}, ErrReconcile
	}
	var claimData struct {
		ActionEventID uuid.UUID `json:"actionEventId"`
		ActionDigest  string    `json:"actionDigest"`
	}
	if err := decodeEventValue(claim.Data, &claimData); err != nil || claimData.ActionEventID != actionEventID || claimData.ActionDigest != digest {
		return actions.Contract{}, Receipt{}, ErrReconcile
	}
	// A crash after the durable claim but before the first intent event is
	// also recoverable. The current state still decides whether retry is safe.
	if attempt.ID == uuid.Nil {
		return body.Contract, Receipt{RequestID: claim.ID.String(), Status: Unknown}, nil
	}
	if attempt.Seq <= claim.Seq {
		return actions.Contract{}, Receipt{}, ErrReconcile
	}
	var result struct {
		Execution Receipt `json:"execution"`
	}
	if err := decodeEventValue(attempt.Data, &result); err != nil || result.Execution.ActionEventID != actionEventID || result.Execution.ActionDigest != digest || result.Execution.RequestID == "" {
		return actions.Contract{}, Receipt{}, ErrReconcile
	}
	if result.Execution.Status != Unknown && result.Execution.Status != Accepted && result.Execution.Status != Completed && result.Execution.Status != Failed {
		return actions.Contract{}, Receipt{}, ErrReconcile
	}
	return body.Contract, result.Execution, nil
}

func decodeEventValue(value map[string]any, out any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (s Service) complete(ctx context.Context, incidentID uuid.UUID, version int64, receipt Receipt) (Receipt, error) {
	receipt.Status = Completed
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	_, err := s.History.Transition(ctx, incidentID, incidents.Transition{To: incidents.StateVerifying, Actor: "executor", Reason: "production memory patch reconciled", ExpectedVersion: version, Data: map[string]any{"execution": receipt}})
	return receipt, err
}

func (s Service) escalate(ctx context.Context, incidentID uuid.UUID, version int64, receipt Receipt, reason string) (Receipt, error) {
	receipt.Status = Failed
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	_, err := s.History.Transition(ctx, incidentID, incidents.Transition{To: incidents.StateEscalated, Actor: "executor", Reason: reason, ExpectedVersion: version, Data: map[string]any{"execution": receipt}})
	if err != nil {
		return receipt, err
	}
	return receipt, fmt.Errorf("%w: %s", ErrReconcile, reason)
}

func (s Service) escalateUnknown(ctx context.Context, incidentID uuid.UUID, version int64, receipt Receipt, reason string) (Receipt, error) {
	receipt.Status = Unknown
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	_, err := s.History.Transition(ctx, incidentID, incidents.Transition{To: incidents.StateEscalated, Actor: "executor", Reason: reason, ExpectedVersion: version, Data: map[string]any{"execution": receipt}})
	if err != nil {
		return receipt, err
	}
	return receipt, fmt.Errorf("%w: %s", ErrReconcile, reason)
}
