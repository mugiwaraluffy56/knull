package alerts

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
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

func payloadFor(identity, status string) AlertmanagerPayload {
	return AlertmanagerPayload{
		Version: "4",
		Status:  status,
		CommonLabels: map[string]string{
			"service":     "intake-test-svc",
			"environment": "production",
			"alertname":   "IntakeTest",
		},
		CommonAnnotations: map[string]string{"summary": "intake test"},
		Alerts: []Alert{{
			Status:      status,
			Fingerprint: identity,
			Labels:      map[string]string{},
		}},
	}
}

func TestIntakeCreatesThenDedups(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM incident_events WHERE incident_id IN (SELECT id FROM incidents WHERE service_key='intake-test-svc')`)
	_, _ = pool.Exec(ctx, `DELETE FROM incidents WHERE service_key='intake-test-svc'`)

	intake := NewIntake(incidents.NewStore(pool), services.NewStore(pool), NewFailureStore(pool), DefaultLabelMapping)

	// First firing -> creates one incident.
	out, err := intake.Process(ctx, payloadFor("fp-1", "firing"), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Disposition != DispositionCreated {
		t.Fatalf("first delivery = %+v, want created", out)
	}
	incidentID := *out[0].IncidentID

	// Repeat firing -> deduped onto the same incident.
	out2, err := intake.Process(ctx, payloadFor("fp-1", "firing"), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if out2[0].Disposition != DispositionDeduped || *out2[0].IncidentID != incidentID {
		t.Fatalf("repeat = %+v, want deduped onto same incident", out2)
	}

	// Resolved -> resolved note on the same incident.
	out3, err := intake.Process(ctx, payloadFor("fp-1", "resolved"), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if out3[0].Disposition != DispositionResolved {
		t.Fatalf("resolved = %+v", out3)
	}

	// Exactly one incident exists for this alert.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM incidents WHERE service_key='intake-test-svc'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("incident count = %d, want 1 (no duplicates)", count)
	}

	// History has: opened + repeat note + resolved note = 3 events.
	events, _ := incidents.NewStore(pool).Events(ctx, incidentID)
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
}

func TestIntakeRejectsUnroutableAndRecordsFailure(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	failures := NewFailureStore(pool)
	before, _ := failures.Count(ctx)

	intake := NewIntake(incidents.NewStore(pool), services.NewStore(pool), failures, DefaultLabelMapping)
	p := payloadFor("fp-x", "firing")
	delete(p.CommonLabels, "service") // now unroutable

	out, err := intake.Process(ctx, p, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Disposition != DispositionRejected {
		t.Fatalf("expected rejected, got %+v", out)
	}
	after, _ := failures.Count(ctx)
	if after != before+1 {
		t.Fatalf("failure not recorded: before=%d after=%d", before, after)
	}
}
