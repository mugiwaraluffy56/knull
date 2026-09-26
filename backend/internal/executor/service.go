package executor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/approval"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type Gate interface {
	Check(context.Context, uuid.UUID, uuid.UUID, string, approval.TargetReader) (actions.Contract, error)
	ClaimForExecution(context.Context, uuid.UUID, uuid.UUID, string, approval.TargetReader) (actions.Contract, error)
}

type Client interface {
	approval.TargetReader
	PatchMemory(context.Context, actions.Contract) (*appsv1.Deployment, json.RawMessage, error)
}

type History interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEvent(context.Context, uuid.UUID, incidents.EventInput) error
	Transition(context.Context, uuid.UUID, incidents.Transition) (incidents.Incident, error)
}

type Status string

const (
	Accepted  Status = "ACCEPTED"
	Completed Status = "COMPLETED"
	Failed    Status = "FAILED"
	Unknown   Status = "UNKNOWN"
)

type Receipt struct {
	RequestID       string          `json:"requestId"`
	PriorRequestID  string          `json:"priorRequestId,omitempty"`
	ActionEventID   uuid.UUID       `json:"actionEventId"`
	ActionDigest    string          `json:"actionDigest"`
	Status          Status          `json:"status"`
	Target          actions.Target  `json:"target"`
	Field           string          `json:"field"`
	SubmittedPatch  json.RawMessage `json:"submittedPatch,omitempty"`
	ResourceUID     string          `json:"resourceUid,omitempty"`
	ResourceVersion string          `json:"resourceVersion,omitempty"`
	ResultingValue  string          `json:"resultingValue,omitempty"`
	ObservedAt      time.Time       `json:"observedAt"`
}

type Service struct {
	Gate    Gate
	Client  Client
	History History
}

// Execute consumes a live, unexpired approval and makes one production patch
// attempt. A failed or ambiguous response leaves the incident in REMEDIATING
// for reconciliation; it never retries a possible mutation blindly.
func (s Service) Execute(ctx context.Context, incidentID, actionEventID uuid.UUID, digest string) (Receipt, error) {
	if s.Gate == nil || s.Client == nil || s.History == nil {
		return Receipt{}, errors.New("production executor is not configured")
	}
	preview, err := s.Gate.Check(ctx, incidentID, actionEventID, digest, s.Client)
	if err != nil {
		return Receipt{}, err
	}
	if err := allowedMemory(preview); err != nil {
		return Receipt{}, err
	}
	action, err := s.Gate.ClaimForExecution(ctx, incidentID, actionEventID, digest, s.Client)
	if err != nil {
		return Receipt{}, err
	}
	if action.Digest != preview.Digest {
		return Receipt{}, ErrTarget
	}
	receipt := Receipt{RequestID: uuid.NewString(), ActionEventID: actionEventID, ActionDigest: action.Digest, Status: Unknown, Target: action.Target, Field: action.Field, ObservedAt: time.Now().UTC()}
	// Persist the attempt before the external call. After a process crash the
	// reconciler can inspect production state using the sealed contract.
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	updated, patch, patchErr := s.Client.PatchMemory(ctx, action)
	receipt.SubmittedPatch = patch
	if patchErr != nil {
		receipt.Status = Unknown // A lost response may follow a successful write.
		if errors.Is(patchErr, ErrTarget) {
			receipt.Status = Failed
		}
		if err := s.record(ctx, incidentID, receipt); err != nil {
			return receipt, err
		}
		return receipt, patchErr
	}
	if updated == nil || string(updated.UID) != action.Preconditions.ResourceUID {
		receipt.Status = Unknown
		if err := s.record(ctx, incidentID, receipt); err != nil {
			return receipt, err
		}
		return receipt, errors.New("production mutation response cannot be reconciled")
	}
	receipt.ResourceUID = string(updated.UID)
	receipt.ResourceVersion = updated.ResourceVersion
	receipt.Status = Accepted
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	// A fresh read confirms the API server's persisted value, rather than
	// trusting the patch response as evidence of a completed mutation.
	live, err := s.Client.ReadPreconditions(ctx, action.Target, action.Field)
	if err != nil {
		receipt.Status = Unknown
		if writeErr := s.record(ctx, incidentID, receipt); writeErr != nil {
			return receipt, writeErr
		}
		return receipt, err
	}
	receipt.ResultingValue = live.CurrentValue
	receipt.ResourceVersion = live.ResourceVersion
	if live.ResourceUID != action.Preconditions.ResourceUID || live.CurrentValue != action.DesiredValue {
		receipt.Status = Unknown
		if err := s.record(ctx, incidentID, receipt); err != nil {
			return receipt, err
		}
		return receipt, errors.New("production state conflicts with approved value")
	}
	receipt.Status = Completed
	// Persist the concrete result before changing lifecycle state. A database
	// transition failure must not erase evidence of a successful API write.
	if err := s.record(ctx, incidentID, receipt); err != nil {
		return receipt, err
	}
	inc, err := s.History.Get(ctx, incidentID)
	if err != nil {
		return receipt, err
	}
	_, err = s.History.Transition(ctx, incidentID, incidents.Transition{To: incidents.StateVerifying, Actor: "executor", Reason: "approved memory patch reconciled", ExpectedVersion: inc.Version, Data: map[string]any{"execution": receipt}})
	return receipt, err
}

func allowedMemory(action actions.Contract) error {
	if err := action.Validate(); err != nil {
		return err
	}
	if action.Type != actions.Memory || action.Field != "resources.limits.memory" || action.CurrentValue != "256Mi" || action.DesiredValue != "1Gi" {
		return ErrTarget
	}
	return nil
}

func (s Service) record(ctx context.Context, incidentID uuid.UUID, receipt Receipt) error {
	return s.History.AppendEvent(ctx, incidentID, incidents.EventInput{Category: incidents.CategoryAction, Source: "production-executor", Target: receipt.Target.Cluster + "/" + receipt.Target.Namespace + "/" + receipt.Target.Name, Actor: "executor", Reason: "production memory patch " + string(receipt.Status), Data: map[string]any{"execution": receipt}})
}
