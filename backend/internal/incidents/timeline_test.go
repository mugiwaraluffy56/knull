package incidents

import (
	"context"
	"testing"
	"time"
)

func TestTimelineOrdersByObservedTime(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()
	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Append events out of order: later observed time first, earlier second.
	if err := store.AppendEvent(ctx, inc.ID, EventInput{
		Category: CategoryObservation, Source: "kubernetes", Reason: "later fact",
		ObservedAt: base.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, inc.ID, EventInput{
		Category: CategoryObservation, Source: "prometheus", Reason: "earlier fact",
		ObservedAt: base.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	events, err := store.Events(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	// events[0] is the opening event (observed ~now-ish at create). Filter the
	// two observation events and check their relative order by observed time.
	var obs []Event
	for _, e := range events {
		if e.Category == CategoryObservation {
			obs = append(obs, e)
		}
	}
	if len(obs) != 2 {
		t.Fatalf("expected 2 observation events, got %d", len(obs))
	}
	if obs[0].Reason != "earlier fact" || obs[1].Reason != "later fact" {
		t.Fatalf("events not ordered by observed time: %s then %s", obs[0].Reason, obs[1].Reason)
	}
	// Ingestion order (seq) is the reverse of observed order, proving reordering.
	if obs[0].Seq <= obs[1].Seq {
		t.Fatalf("expected earlier-observed event to have the later seq (ingested second)")
	}
}

func TestAppendEventRedactsSensitiveData(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()
	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, inc.ID, EventInput{
		Category: CategoryObservation,
		Source:   "github",
		Reason:   "found config",
		Data:     map[string]any{"repo": "acme/checkout", "token": "ghp_leak"},
	}); err != nil {
		t.Fatal(err)
	}
	events, _ := store.Events(ctx, inc.ID)
	last := events[len(events)-1]
	if last.Data["repo"] != "acme/checkout" {
		t.Fatalf("non-sensitive data lost: %v", last.Data)
	}
	if last.Data["token"] != redactedPlaceholder {
		t.Fatalf("token not redacted in stored event: %v", last.Data["token"])
	}
}

func TestAppendEventCategoriesDistinct(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()
	inc, err := store.Create(ctx, newIncident())
	if err != nil {
		t.Fatal(err)
	}
	cats := []EventCategory{CategoryObservation, CategoryHypothesis, CategoryDecision, CategoryAction}
	for _, c := range cats {
		if err := store.AppendEvent(ctx, inc.ID, EventInput{Category: c, Reason: string(c)}); err != nil {
			t.Fatalf("append %s: %v", c, err)
		}
	}
	if err := store.AppendEvent(ctx, inc.ID, EventInput{Category: "bogus"}); err == nil {
		t.Fatal("invalid category should be rejected")
	}
	events, _ := store.Events(ctx, inc.ID)
	seen := map[EventCategory]bool{}
	for _, e := range events {
		seen[e.Category] = true
	}
	for _, c := range cats {
		if !seen[c] {
			t.Errorf("category %s missing from timeline", c)
		}
	}
}
