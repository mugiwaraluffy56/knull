package sandbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type IncidentStore interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEventID(context.Context, uuid.UUID, incidents.EventInput) (uuid.UUID, error)
}

type Service struct {
	incidents IncidentStore
	runner    *Runner
}

func NewService(store IncidentStore, runner *Runner) *Service {
	return &Service{incidents: store, runner: runner}
}

type RecordedRun struct {
	EventID uuid.UUID `json:"eventId"`
	Run     Run       `json:"run"`
}

// Prepare looks up the exact sealed action event and records the sandbox
// provenance and cleanup result. A prepared run is not a passing validation.
func (s *Service) Prepare(ctx context.Context, incidentID, actionEventID uuid.UUID, workload Workload) (RecordedRun, error) {
	inc, err := s.incidents.Get(ctx, incidentID)
	if err != nil {
		return RecordedRun{}, err
	}
	if inc.State != incidents.StatePlanning {
		return RecordedRun{}, fmt.Errorf("sandbox run requires PLANNING state")
	}
	events, err := s.incidents.Events(ctx, incidentID)
	if err != nil {
		return RecordedRun{}, err
	}
	var contract actions.Contract
	found := false
	for _, event := range events {
		if event.ID != actionEventID || event.Category != incidents.CategoryAction || event.Source != "action-plan" {
			continue
		}
		body, err := json.Marshal(event.Data["contract"])
		if err != nil {
			return RecordedRun{}, err
		}
		if err := json.Unmarshal(body, &contract); err != nil {
			return RecordedRun{}, err
		}
		found = true
		break
	}
	if !found {
		return RecordedRun{}, fmt.Errorf("action event not found")
	}
	if err := contract.Validate(); err != nil {
		return RecordedRun{}, err
	}
	run, runErr := s.runner.Run(ctx, contract, workload)
	status := "sandbox prepared"
	if runErr != nil {
		status = "sandbox failed"
	}
	eventID, recordErr := s.incidents.AppendEventID(ctx, incidentID, incidents.EventInput{Category: incidents.CategoryObservation, Source: "sandbox", Actor: "sandbox", Target: run.Namespace, Reason: status, Data: map[string]any{"run": run, "actionEventId": actionEventID.String(), "available": runErr == nil}})
	if recordErr != nil {
		return RecordedRun{}, recordErr
	}
	if runErr != nil {
		return RecordedRun{EventID: eventID, Run: run}, runErr
	}
	return RecordedRun{EventID: eventID, Run: run}, nil
}
