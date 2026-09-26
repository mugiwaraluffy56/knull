package actions

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

type incidentStoreStub struct {
	inc    incidents.Incident
	events []incidents.Event
	added  incidents.EventInput
}

func (s *incidentStoreStub) Get(context.Context, uuid.UUID) (incidents.Incident, error) {
	return s.inc, nil
}
func (s *incidentStoreStub) Events(context.Context, uuid.UUID) ([]incidents.Event, error) {
	return s.events, nil
}
func (s *incidentStoreStub) AppendEventID(_ context.Context, _ uuid.UUID, event incidents.EventInput) (uuid.UUID, error) {
	s.added = event
	return uuid.New(), nil
}

type serviceStoreStub struct{ service services.Service }

func (s serviceStoreStub) Get(context.Context, uuid.UUID) (services.Service, error) {
	return s.service, nil
}

func TestCreateScopedAction(t *testing.T) {
	draft := baseDraft()
	serviceID := uuid.New()
	evidenceID := uuid.New()
	draft.EvidenceIDs = []string{evidenceID.String()}
	inc := &incidentStoreStub{inc: incidents.Incident{ID: uuid.New(), ServiceID: &serviceID, ServiceKey: "checkout-api", Environment: "production", State: incidents.StatePlanning}, events: []incidents.Event{{ID: evidenceID, Category: incidents.CategoryObservation, Source: "kubernetes"}}}
	svc := serviceStoreStub{service: services.Service{ID: serviceID, Key: "checkout-api", Environment: "production", K8sCluster: "prod-east", K8sNamespace: "checkout", K8sWorkload: "checkout-api", Enabled: true}}
	created, err := NewService(inc, svc).Create(context.Background(), inc.inc.ID, draft)
	if err != nil {
		t.Fatal(err)
	}
	if created.EventID == uuid.Nil || created.Contract.Digest == "" || inc.added.Category != incidents.CategoryAction || inc.added.Reason != created.Summary {
		t.Fatalf("plan not sealed and recorded: %+v", created)
	}
	draft.Target.Name = "other-api"
	if _, err := NewService(inc, svc).Create(context.Background(), inc.inc.ID, draft); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-service plan accepted: %v", err)
	}
	draft.Target.Name = "checkout-api"
	draft.EvidenceIDs = []string{uuid.NewString()}
	if _, err := NewService(inc, svc).Create(context.Background(), inc.inc.ID, draft); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invented evidence accepted: %v", err)
	}
}
