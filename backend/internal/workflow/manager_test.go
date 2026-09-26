package workflow

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

// fakeRuntime is an in-memory Runtime for orchestration tests.
type fakeRuntime struct {
	startErr  error
	session   Session
	events    []RuntimeEvent
	streamErr error
}

func (f *fakeRuntime) StartSession(context.Context, StartRequest) (Session, error) {
	if f.startErr != nil {
		return Session{}, f.startErr
	}
	return f.session, nil
}
func (f *fakeRuntime) StreamEvents(context.Context, string, string) (<-chan RuntimeEvent, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	ch := make(chan RuntimeEvent)
	go func() {
		defer close(ch)
		for _, e := range f.events {
			ch <- e
		}
	}()
	return ch, nil
}
func (f *fakeRuntime) Pause(context.Context, string) error  { return nil }
func (f *fakeRuntime) Resume(context.Context, string) error { return nil }
func (f *fakeRuntime) Cancel(context.Context, string) error { return nil }
func (f *fakeRuntime) GetSession(context.Context, string) (Session, error) {
	return f.session, nil
}

func testStore(t *testing.T) *incidents.Store {
	t.Helper()
	url := os.Getenv("KNULL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KNULL_TEST_DATABASE_URL not set; skipping database-backed test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return incidents.NewStore(pool)
}

func newInc(t *testing.T, store *incidents.Store) incidents.Incident {
	t.Helper()
	inc, err := store.Create(context.Background(), incidents.NewIncident{
		ServiceKey: "wf-test", Environment: "production", Summary: "x", Actor: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return inc
}

func TestStartForIncidentPersistsSessionAndInvestigates(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	inc := newInc(t, store)

	rt := &fakeRuntime{session: Session{ID: "s1", RunID: "r1", Status: "running"}}
	mgr := NewManager(rt, store, nil)
	if err := mgr.StartForIncident(ctx, inc); err != nil {
		t.Fatalf("start: %v", err)
	}

	got, _ := store.Get(ctx, inc.ID)
	if got.WorkflowSessionID != "s1" || got.WorkflowRunID != "r1" {
		t.Fatalf("session not persisted: %+v", got)
	}
	if got.State != incidents.StateInvestigating {
		t.Fatalf("state = %s, want INVESTIGATING", got.State)
	}
}

func TestStartForIncidentRuntimeErrorFailsIncident(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	inc := newInc(t, store)

	rt := &fakeRuntime{startErr: errors.New("boom")}
	mgr := NewManager(rt, store, nil)
	if err := mgr.StartForIncident(ctx, inc); err == nil {
		t.Fatal("expected error")
	}
	got, _ := store.Get(ctx, inc.ID)
	if got.State != incidents.StateFailed {
		t.Fatalf("state = %s, want FAILED", got.State)
	}
}

func TestApplyEventTransitionAndProgress(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	inc := newInc(t, store)
	if _, err := store.Transition(ctx, inc.ID, incidents.Transition{To: incidents.StateInvestigating, Actor: "t"}); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&fakeRuntime{}, store, nil)

	if err := mgr.applyEvent(ctx, inc.ID, RuntimeEvent{Kind: KindProgress, Summary: "queried prom"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.applyEvent(ctx, inc.ID, RuntimeEvent{Kind: KindTransition, State: "PLANNING", Summary: "rca"}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, inc.ID)
	if got.State != incidents.StatePlanning {
		t.Fatalf("state = %s, want PLANNING", got.State)
	}
	events, _ := store.Events(ctx, inc.ID)
	// opened + investigating + progress note + planning transition = 4
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
}

func TestApplyEventErrorEscalates(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	inc := newInc(t, store)
	if _, err := store.Transition(ctx, inc.ID, incidents.Transition{To: incidents.StateInvestigating, Actor: "t"}); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&fakeRuntime{}, store, nil)
	if err := mgr.applyEvent(ctx, inc.ID, RuntimeEvent{Kind: KindError, Summary: "agent crashed"}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, inc.ID)
	if got.State != incidents.StateEscalated {
		t.Fatalf("state = %s, want ESCALATED", got.State)
	}
}

func TestApplyEventIllegalTransitionRecordedNotForced(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	inc := newInc(t, store) // RECEIVED
	mgr := NewManager(&fakeRuntime{}, store, nil)

	// REMEDIATING is illegal from RECEIVED: it must be recorded, not forced.
	if err := mgr.applyEvent(ctx, inc.ID, RuntimeEvent{Kind: KindTransition, State: "REMEDIATING"}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, inc.ID)
	if got.State != incidents.StateReceived {
		t.Fatalf("state = %s, want unchanged RECEIVED", got.State)
	}
}

func TestDrainAppliesAllEvents(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	inc := newInc(t, store)
	mgr := NewManager(&fakeRuntime{}, store, nil)

	ch := make(chan RuntimeEvent, 3)
	ch <- RuntimeEvent{Kind: KindTransition, State: "INVESTIGATING"}
	ch <- RuntimeEvent{Kind: KindProgress, Summary: "step"}
	ch <- RuntimeEvent{Kind: KindTransition, State: "PLANNING"}
	close(ch)
	mgr.drain(ctx, inc.ID, ch)

	got, _ := store.Get(ctx, inc.ID)
	if got.State != incidents.StatePlanning {
		t.Fatalf("state = %s, want PLANNING", got.State)
	}
}

func TestResumeSelectsOnlyActiveWithSession(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	withSession := newInc(t, store)
	if err := store.SaveWorkflow(ctx, withSession.ID, "sess-x", "run-x"); err != nil {
		t.Fatal(err)
	}
	// A closed incident with a session must not be resumable.
	closed := newInc(t, store)
	_ = store.SaveWorkflow(ctx, closed.ID, "sess-y", "run-y")
	for _, to := range []incidents.State{incidents.StateInvestigating, incidents.StatePlanning, incidents.StateValidating, incidents.StateAwaitingApproval, incidents.StateDenied, incidents.StateClosed} {
		if _, err := store.Transition(ctx, closed.ID, incidents.Transition{To: to, Actor: "t"}); err != nil {
			t.Fatalf("advance to %s: %v", to, err)
		}
	}

	resumable, err := store.ListResumable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundActive, foundClosed := false, false
	for _, r := range resumable {
		if r.ID == withSession.ID {
			foundActive = true
		}
		if r.ID == closed.ID {
			foundClosed = true
		}
	}
	if !foundActive {
		t.Fatal("active incident with session should be resumable")
	}
	if foundClosed {
		t.Fatal("closed incident must not be resumable")
	}
}
