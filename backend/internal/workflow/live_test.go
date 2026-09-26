package workflow

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run explicitly against a locally configured TrueForge server. This creates
// one model turn and uses no infrastructure connector or incident evidence.
func TestLiveTrueForgeSessionTurn(t *testing.T) {
	endpoint := os.Getenv("KNULL_TRUEFORGE_LIVE_URL")
	model := os.Getenv("KNULL_TRUEFORGE_INVESTIGATOR_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("KNULL_TRUEFORGE_LIVE_URL and KNULL_TRUEFORGE_INVESTIGATOR_MODEL required")
	}
	runtime := NewHTTPRuntimeWithConfig(endpoint, "", HTTPRuntimeConfig{Model: model})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := runtime.StartSession(ctx, StartRequest{
		IncidentID: "local-smoke", ServiceKey: "smoke", Environment: "local",
		Summary: "Reply with exactly OK. Do not call tools.",
	})
	if err != nil {
		t.Fatalf("start live turn: %v", err)
	}
	if session.ID == "" || session.RunID == "" {
		t.Fatalf("missing session or turn id: %+v", session)
	}
	events, err := runtime.StreamEvents(ctx, session.ID, session.RunID)
	if err != nil {
		t.Fatalf("stream live turn: %v", err)
	}
	var done bool
	for event := range events {
		if event.Kind == KindError {
			t.Fatalf("live turn failed: %s", event.Summary)
		}
		if event.Kind == KindDone {
			done = true
		}
	}
	if !done {
		t.Fatal("live turn ended without turn.done")
	}
	t.Logf("TrueForge session %s turn %s completed", session.ID, session.RunID)
}
