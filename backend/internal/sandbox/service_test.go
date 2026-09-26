package sandbox

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type incidentStoreStub struct {
	inc      incidents.Incident
	events   []incidents.Event
	recorded incidents.EventInput
}

func (s *incidentStoreStub) Get(context.Context, uuid.UUID) (incidents.Incident, error) {
	return s.inc, nil
}
func (s *incidentStoreStub) Events(context.Context, uuid.UUID) ([]incidents.Event, error) {
	return s.events, nil
}
func (s *incidentStoreStub) AppendEventID(_ context.Context, _ uuid.UUID, e incidents.EventInput) (uuid.UUID, error) {
	s.recorded = e
	return uuid.New(), nil
}

func TestPrepareLinksSealedActionAndRun(t *testing.T) {
	action, workload := candidate()
	actionEventID := uuid.New()
	store := &incidentStoreStub{inc: incidents.Incident{ID: uuid.New(), State: incidents.StatePlanning}, events: []incidents.Event{{ID: actionEventID, Category: incidents.CategoryAction, Source: "action-plan", Data: map[string]any{"contract": action}}}}
	runner := NewRunner(&fakeCluster{uid: "sandbox-uid"}, Config{ClusterUID: "sandbox-uid", ProductionClusterUIDs: []string{"production-uid"}, AllowedImageRegistry: "registry.example"})
	result, err := NewService(store, runner).Prepare(context.Background(), store.inc.ID, actionEventID, workload)
	if err != nil {
		t.Fatal(err)
	}
	if result.EventID == uuid.Nil || result.Run.ActionDigest != action.Digest || store.recorded.Category != incidents.CategoryObservation || store.recorded.Source != "sandbox" || store.recorded.Data["actionEventId"] != actionEventID.String() {
		t.Fatalf("run not linked: %+v", result)
	}
	if _, err := NewService(store, runner).Prepare(context.Background(), store.inc.ID, uuid.New(), workload); err == nil {
		t.Fatal("invented action accepted")
	}
}
