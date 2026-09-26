package investigate

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

type fakeIncidentStore struct {
	inc    incidents.Incident
	events []incidents.EventInput
}

func (f *fakeIncidentStore) Get(context.Context, uuid.UUID) (incidents.Incident, error) {
	return f.inc, nil
}
func (f *fakeIncidentStore) AppendEvent(_ context.Context, _ uuid.UUID, in incidents.EventInput) error {
	f.events = append(f.events, in)
	return nil
}

type fakeServiceStore struct{ svc services.Service }

func (f *fakeServiceStore) Get(context.Context, uuid.UUID) (services.Service, error) {
	return f.svc, nil
}

func TestCollectorRunsKubernetesAndRecordsEvidence(t *testing.T) {
	svcID := uuid.New()
	svc := services.Service{
		ID: svcID, Key: "checkout-api", Environment: "production",
		K8sCluster: "prod-eks", K8sNamespace: "shop", K8sWorkload: "checkout-api",
	}
	inc := incidents.Incident{ID: uuid.New(), ServiceID: &svcID, ServiceKey: "checkout-api", Environment: "production"}
	incStore := &fakeIncidentStore{inc: inc}
	svcStore := &fakeServiceStore{svc: svc}

	caller := &fakeCaller{results: map[string]string{"list_pods": `{"pods":1}`}}
	col := NewCollector(incStore, svcStore, NewKubernetesInvestigator(caller))

	if !col.Enabled() {
		t.Fatal("collector should be enabled with a k8s investigator")
	}
	if err := col.Collect(context.Background(), inc.ID); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(incStore.events) != len(k8sReads) {
		t.Fatalf("recorded %d evidence events, want %d", len(incStore.events), len(k8sReads))
	}
	if caller.lastArgs["namespace"] != "shop" {
		t.Fatalf("k8s call not scoped to service namespace: %v", caller.lastArgs)
	}
}

func TestCollectorRunsPrometheusWhenKubernetesIsNotConfigured(t *testing.T) {
	svcID := uuid.New()
	svc := services.Service{
		ID: svcID, Key: "checkout-api", Environment: "production",
		PrometheusLabels: map[string]string{"app": "checkout-api", "environment": "production"},
	}
	inc := incidents.Incident{ID: uuid.New(), ServiceID: &svcID, ServiceKey: "checkout-api", Environment: "production"}
	incStore := &fakeIncidentStore{inc: inc}
	calls := &recordingCaller{result: `{"result":"{app=\"checkout-api\"} => 2 @[1790424000]","warnings":[]}`}
	col := NewCollector(incStore, &fakeServiceStore{svc: svc}, nil, NewPrometheusInvestigator(calls))

	if !col.Enabled() {
		t.Fatal("collector should be enabled with a Prometheus investigator")
	}
	if err := col.Collect(context.Background(), inc.ID); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(calls.calls) != 13 {
		t.Fatalf("made %d Prometheus calls, want 13", len(calls.calls))
	}
	if len(incStore.events) != 13 {
		t.Fatalf("recorded %d evidence events, want 13", len(incStore.events))
	}
}

func TestCollectorErrorsWhenNoServiceMapping(t *testing.T) {
	inc := incidents.Incident{ID: uuid.New()} // ServiceID nil
	col := NewCollector(&fakeIncidentStore{inc: inc}, &fakeServiceStore{}, NewKubernetesInvestigator(&fakeCaller{}))
	if err := col.Collect(context.Background(), inc.ID); err == nil {
		t.Fatal("expected error for incident without a service mapping")
	}
}
