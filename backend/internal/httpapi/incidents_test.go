package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

// fakeIncidents implements incidentStore for handler tests.
type fakeIncidents struct {
	created []incidents.NewIncident
	active  map[string]incidents.Incident // key: serviceKey|env|identity
	nextID  uuid.UUID
}

func (f *fakeIncidents) Create(_ context.Context, in incidents.NewIncident) (incidents.Incident, error) {
	f.created = append(f.created, in)
	id := f.nextID
	if id == uuid.Nil {
		id = uuid.New()
	}
	return incidents.Incident{ID: id, ServiceKey: in.ServiceKey, Environment: in.Environment, State: incidents.StateReceived}, nil
}
func (f *fakeIncidents) FindActiveByAlert(_ context.Context, sk, env, ident string) (incidents.Incident, error) {
	if inc, ok := f.active[sk+"|"+env+"|"+ident]; ok {
		return inc, nil
	}
	return incidents.Incident{}, incidents.ErrNotFound
}
func (f *fakeIncidents) Get(context.Context, uuid.UUID) (incidents.Incident, error) {
	return incidents.Incident{}, incidents.ErrNotFound
}
func (f *fakeIncidents) List(context.Context) ([]incidents.Incident, error) { return nil, nil }
func (f *fakeIncidents) Events(context.Context, uuid.UUID) ([]incidents.Event, error) {
	return nil, nil
}

// fakeServices implements serviceStore for handler tests.
type fakeServices struct{ svc *services.Service }

func (f *fakeServices) Create(context.Context, services.Input) (services.Service, error) {
	return services.Service{}, nil
}
func (f *fakeServices) Update(context.Context, uuid.UUID, services.Input) (services.Service, error) {
	return services.Service{}, nil
}
func (f *fakeServices) SetEnabled(context.Context, uuid.UUID, bool) (services.Service, error) {
	return services.Service{}, nil
}
func (f *fakeServices) Get(_ context.Context, id uuid.UUID) (services.Service, error) {
	if f.svc != nil && f.svc.ID == id {
		return *f.svc, nil
	}
	return services.Service{}, services.ErrNotFound
}
func (f *fakeServices) List(context.Context) ([]services.Service, error) { return nil, nil }

func startReq(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	op := operators.Operator{ID: uuid.New(), Email: "op@knull.local"}
	req := httptest.NewRequest(http.MethodPost, "/api/incidents", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), operatorContextKey, op))
	rr := httptest.NewRecorder()
	srv.handleStartInvestigation(rr, req)
	return rr
}

func TestStartInvestigationCreatesIncident(t *testing.T) {
	svcID := uuid.New()
	svc := services.Service{ID: svcID, Key: "checkout-api", Environment: "production"}
	inc := &fakeIncidents{active: map[string]incidents.Incident{}}
	srv := New(Options{Incidents: inc, Services: &fakeServices{svc: &svc}})

	rr := startReq(t, srv, `{"serviceId":"`+svcID.String()+`","summary":"pods crashing","symptoms":{"errorRate":"38%"}}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if len(inc.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(inc.created))
	}
	got := inc.created[0]
	if got.Actor != "op@knull.local" {
		t.Fatalf("actor = %q, want operator email", got.Actor)
	}
	if got.Summary != "pods crashing" || got.Symptoms["errorRate"] != "38%" {
		t.Fatalf("summary/symptoms not carried: %+v", got)
	}
	if got.AlertIdentity != manualAlertIdentity {
		t.Fatalf("alert identity = %q, want %q", got.AlertIdentity, manualAlertIdentity)
	}
}

func TestStartInvestigationRequiresSummary(t *testing.T) {
	svcID := uuid.New()
	svc := services.Service{ID: svcID, Key: "x", Environment: "production"}
	srv := New(Options{Incidents: &fakeIncidents{active: map[string]incidents.Incident{}}, Services: &fakeServices{svc: &svc}})
	rr := startReq(t, srv, `{"serviceId":"`+svcID.String()+`","summary":"  "}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestStartInvestigationRejectsUnknownService(t *testing.T) {
	srv := New(Options{Incidents: &fakeIncidents{active: map[string]incidents.Incident{}}, Services: &fakeServices{}})
	rr := startReq(t, srv, `{"serviceId":"`+uuid.New().String()+`","summary":"x"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown service", rr.Code)
	}
}

func TestStartInvestigationDuplicateGuarded(t *testing.T) {
	svcID := uuid.New()
	svc := services.Service{ID: svcID, Key: "checkout-api", Environment: "production"}
	existing := incidents.Incident{ID: uuid.New(), State: incidents.StateInvestigating}
	inc := &fakeIncidents{active: map[string]incidents.Incident{
		"checkout-api|production|" + manualAlertIdentity: existing,
	}}
	srv := New(Options{Incidents: inc, Services: &fakeServices{svc: &svc}})

	// Without force -> 409.
	rr := startReq(t, srv, `{"serviceId":"`+svcID.String()+`","summary":"again"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	// With force -> 201 (explicit new incident allowed).
	rr2 := startReq(t, srv, `{"serviceId":"`+svcID.String()+`","summary":"again","force":true}`)
	if rr2.Code != http.StatusCreated {
		t.Fatalf("forced status = %d, want 201", rr2.Code)
	}
}
