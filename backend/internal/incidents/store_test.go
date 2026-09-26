package incidents

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

func newIncident() NewIncident {
	return NewIncident{
		ServiceKey:    "checkout-api",
		Environment:   "production",
		AlertIdentity: "HighErrorRate",
		Summary:       "error rate elevated",
		Symptoms:      map[string]string{"errorRate": "38%"},
		Actor:         "system",
	}
}

func TestCreateOpensIncidentWithEvent(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()

	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if inc.State != StateReceived || inc.Version != 1 {
		t.Fatalf("new incident state=%s version=%d, want RECEIVED/1", inc.State, inc.Version)
	}

	events, err := store.Events(ctx, inc.ID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].ToState != StateReceived || events[0].Seq != 1 {
		t.Fatalf("expected one opening event, got %+v", events)
	}
}

func TestValidTransitionUpdatesStateAndAppendsOneEvent(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()

	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.Transition(ctx, inc.ID, Transition{
		To: StateInvestigating, Actor: "system", Reason: "begin",
	})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if updated.State != StateInvestigating {
		t.Fatalf("state = %s, want INVESTIGATING", updated.State)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}
	events, _ := store.Events(ctx, inc.ID)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].FromState != StateReceived || events[1].ToState != StateInvestigating {
		t.Fatalf("transition event wrong: %+v", events[1])
	}
}

func TestInvalidTransitionRejectedNoEventAppended(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()

	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Transition(ctx, inc.ID, Transition{To: StateRemediating, Actor: "system"})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
	// State unchanged and no extra event.
	got, _ := store.Get(ctx, inc.ID)
	if got.State != StateReceived || got.Version != 1 {
		t.Fatalf("state changed on rejected transition: %s v%d", got.State, got.Version)
	}
	events, _ := store.Events(ctx, inc.ID)
	if len(events) != 1 {
		t.Fatalf("rejected transition appended an event: %d events", len(events))
	}
}

func TestOptimisticConcurrencyConflict(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()

	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}
	// First transition succeeds and bumps version to 2.
	if _, err := store.Transition(ctx, inc.ID, Transition{
		To: StateInvestigating, ExpectedVersion: 1, Actor: "a",
	}); err != nil {
		t.Fatalf("first transition: %v", err)
	}
	// A caller still holding version 1 must be rejected, not silently applied.
	_, err = store.Transition(ctx, inc.ID, Transition{
		To: StatePlanning, ExpectedVersion: 1, Actor: "b",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale transition err = %v, want ErrConflict", err)
	}
}

func TestConcurrentTransitionsSerialized(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()

	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(ctx, inc.ID, Transition{To: StateInvestigating, Actor: "sys"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(ctx, inc.ID, Transition{To: StatePlanning, Actor: "sys"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(ctx, inc.ID, Transition{To: StateValidating, Actor: "sys"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(ctx, inc.ID, Transition{To: StateAwaitingApproval, Actor: "sys"}); err != nil {
		t.Fatal(err)
	}

	// A generic transition may never claim approval or production execution.
	if _, err := store.Transition(ctx, inc.ID, Transition{To: StateRemediating, Actor: "workflow"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("generic remediation transition = %v, want ErrInvalidTransition", err)
	}

	// Two racing allowed transitions from AWAITING_APPROVAL: return to planning
	// or deny. The row lock serializes them; exactly one must win.
	var wg sync.WaitGroup
	results := make([]error, 2)
	targets := []State{StatePlanning, StateDenied}
	wg.Add(2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			_, results[i] = store.Transition(ctx, inc.ID, Transition{To: targets[i], Actor: "op"})
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, e := range results {
		if e == nil {
			wins++
		} else if !errors.Is(e, ErrInvalidTransition) {
			t.Fatalf("unexpected error: %v", e)
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly one winner, got %d", wins)
	}
}

func TestPersistenceAcrossReconnect(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)

	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(ctx, inc.ID, Transition{To: StateInvestigating, Actor: "sys"}); err != nil {
		t.Fatal(err)
	}

	// Simulate a restart: open a fresh pool and confirm state and history
	// survived (PostgreSQL is the durable record).
	fresh, err := pgxpool.New(ctx, os.Getenv("KNULL_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	reopened := NewStore(fresh)

	got, err := reopened.Get(ctx, inc.ID)
	if err != nil {
		t.Fatalf("get after reconnect: %v", err)
	}
	if got.State != StateInvestigating {
		t.Fatalf("state after reconnect = %s, want INVESTIGATING", got.State)
	}
	events, _ := reopened.Events(ctx, inc.ID)
	if len(events) != 2 {
		t.Fatalf("events after reconnect = %d, want 2", len(events))
	}
}
