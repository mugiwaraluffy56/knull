package actions

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

type IncidentStore interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEventID(context.Context, uuid.UUID, incidents.EventInput) (uuid.UUID, error)
}
type ServiceStore interface {
	Get(context.Context, uuid.UUID) (services.Service, error)
}
type Service struct {
	incidents IncidentStore
	services  ServiceStore
}

func NewService(inc IncidentStore, svc ServiceStore) *Service {
	return &Service{incidents: inc, services: svc}
}

type Created struct {
	EventID  uuid.UUID `json:"eventId"`
	Contract Contract  `json:"contract"`
	Summary  string    `json:"summary"`
}

// Create seals an allowlisted proposal scoped to the incident's configured
// service mapping and stored evidence. It does not validate or execute it.
func (s *Service) Create(ctx context.Context, incidentID uuid.UUID, draft Draft) (Created, error) {
	inc, err := s.incidents.Get(ctx, incidentID)
	if err != nil {
		return Created{}, err
	}
	if inc.State != incidents.StatePlanning || inc.ServiceID == nil {
		return Created{}, fmt.Errorf("%w: incident must be planning with a service mapping", ErrInvalid)
	}
	svc, err := s.services.Get(ctx, *inc.ServiceID)
	if err != nil {
		return Created{}, err
	}
	if !svc.Enabled || svc.Key != inc.ServiceKey || svc.Environment != inc.Environment || draft.Environment != inc.Environment || draft.Target.Cluster != svc.K8sCluster || draft.Target.Namespace != svc.K8sNamespace || draft.Target.Name != svc.K8sWorkload {
		return Created{}, fmt.Errorf("%w: action target differs from incident service mapping", ErrInvalid)
	}
	events, err := s.incidents.Events(ctx, incidentID)
	if err != nil {
		return Created{}, err
	}
	known := map[string]bool{}
	for _, e := range events {
		if e.Category == incidents.CategoryObservation && e.Data["available"] != false {
			known[e.ID.String()] = true
		}
	}
	for _, ref := range draft.EvidenceIDs {
		if !known[ref] {
			return Created{}, fmt.Errorf("%w: unknown or unavailable evidence %s", ErrInvalid, ref)
		}
	}
	contract, err := Seal(draft)
	if err != nil {
		return Created{}, err
	}
	summary, _ := contract.Summary()
	id, err := s.incidents.AppendEventID(ctx, incidentID, incidents.EventInput{Category: incidents.CategoryAction, Source: "action-plan", Actor: "knull", Target: svc.K8sCluster + "/" + svc.K8sNamespace + "/" + svc.K8sWorkload, Reason: summary, Data: map[string]any{"contract": contract, "status": "proposed"}})
	if err != nil {
		return Created{}, err
	}
	return Created{EventID: id, Contract: contract, Summary: summary}, nil
}
