package alerts

import (
	"testing"
	"time"
)

func firing() (AlertmanagerPayload, Alert) {
	p := AlertmanagerPayload{
		Version:  "4",
		Status:   "firing",
		GroupKey: "g1",
		CommonLabels: map[string]string{
			"service":     "checkout-api",
			"environment": "production",
			"namespace":   "shop",
			"alertname":   "HighErrorRate",
			"severity":    "critical",
		},
		CommonAnnotations: map[string]string{"summary": "error rate high"},
	}
	a := Alert{
		Status:      "firing",
		Fingerprint: "abc123",
		StartsAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Labels:      map[string]string{"pod": "checkout-7"},
	}
	return p, a
}

func TestNormalizeRoutableAlert(t *testing.T) {
	p, a := firing()
	na, ok := Normalize(p, a, DefaultLabelMapping)
	if !ok {
		t.Fatal("expected routable alert")
	}
	if na.ServiceKey != "checkout-api" || na.Environment != "production" {
		t.Fatalf("bad routing: %+v", na)
	}
	if na.AlertIdentity != "abc123" {
		t.Fatalf("identity = %q, want fingerprint", na.AlertIdentity)
	}
	if na.Summary != "error rate high" {
		t.Fatalf("summary = %q", na.Summary)
	}
	if na.ResourceHints["namespace"] != "shop" || na.ResourceHints["pod"] != "checkout-7" {
		t.Fatalf("resource hints missing: %+v", na.ResourceHints)
	}
	if na.Resolved {
		t.Fatal("firing alert marked resolved")
	}
}

func TestNormalizeUnroutableWithoutServiceOrEnv(t *testing.T) {
	p, a := firing()
	delete(p.CommonLabels, "service")
	delete(a.Labels, "service")
	delete(p.CommonLabels, "app")
	if _, ok := Normalize(p, a, DefaultLabelMapping); ok {
		t.Fatal("alert without service label should be unroutable")
	}
}

func TestNormalizeResolved(t *testing.T) {
	p, a := firing()
	p.Status = "resolved"
	a.Status = "resolved"
	a.EndsAt = time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	na, ok := Normalize(p, a, DefaultLabelMapping)
	if !ok || !na.Resolved {
		t.Fatalf("expected resolved routable alert: ok=%v resolved=%v", ok, na.Resolved)
	}
	if !na.EventTime.Equal(a.EndsAt) {
		t.Fatalf("resolved event time = %v, want EndsAt", na.EventTime)
	}
}

func TestNormalizeCustomLabelMapping(t *testing.T) {
	p, a := firing()
	p.CommonLabels = map[string]string{"svc": "auth", "stage": "staging", "alertname": "X"}
	a.Labels = nil
	na, ok := Normalize(p, a, LabelMapping{ServiceLabel: "svc", EnvironmentLabel: "stage"})
	if !ok || na.ServiceKey != "auth" || na.Environment != "staging" {
		t.Fatalf("custom mapping failed: ok=%v %+v", ok, na)
	}
}
