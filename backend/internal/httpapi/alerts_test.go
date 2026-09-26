package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mugiwaraluffy56/knull/backend/internal/alerts"
)

type fakeIntake struct{ called atomic.Int32 }

func (f *fakeIntake) Process(context.Context, alerts.AlertmanagerPayload, string) ([]alerts.Outcome, error) {
	f.called.Add(1)
	return []alerts.Outcome{{Disposition: alerts.DispositionCreated}}, nil
}

type fakeFailures struct{ count atomic.Int32 }

func (f *fakeFailures) Record(context.Context, string, string, string) error {
	f.count.Add(1)
	return nil
}

func alertServer(secret string, intake alertIntake, fail failureRecorder) http.Handler {
	return New(Options{
		Deps:          staticChecker{},
		AllowedOrigin: "http://localhost:3000",
		AlertIntake:   intake,
		AlertFailures: fail,
		AlertSecret:   secret,
	}).Handler()
}

func TestWebhookRejectsMissingSecret(t *testing.T) {
	intake := &fakeIntake{}
	fail := &fakeFailures{}
	srv := alertServer("s3cr3t", intake, fail)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/webhook", strings.NewReader(`{"alerts":[]}`))
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if intake.called.Load() != 0 {
		t.Fatal("intake ran despite bad auth")
	}
	if fail.count.Load() != 1 {
		t.Fatal("unauthorized delivery not recorded")
	}
}

func TestWebhookRejectsWrongSecret(t *testing.T) {
	srv := alertServer("s3cr3t", &fakeIntake{}, &fakeFailures{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/webhook", strings.NewReader(`{"alerts":[]}`))
	req.Header.Set("Authorization", "Bearer wrong")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestWebhookMalformedBodyRejected(t *testing.T) {
	fail := &fakeFailures{}
	srv := alertServer("s3cr3t", &fakeIntake{}, fail)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/webhook", strings.NewReader(`{not json`))
	req.Header.Set("Authorization", "Bearer s3cr3t")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if fail.count.Load() != 1 {
		t.Fatal("malformed delivery not recorded")
	}
}

func TestWebhookAcceptsValidDelivery(t *testing.T) {
	intake := &fakeIntake{}
	srv := alertServer("s3cr3t", intake, &fakeFailures{})
	rr := httptest.NewRecorder()
	body := `{"version":"4","status":"firing","alerts":[{"status":"firing","fingerprint":"a","labels":{"service":"x","environment":"production"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/webhook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer s3cr3t")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
	}
	if intake.called.Load() != 1 {
		t.Fatal("intake not invoked for valid delivery")
	}
}

func TestWebhookTokenViaQueryParam(t *testing.T) {
	intake := &fakeIntake{}
	srv := alertServer("s3cr3t", intake, &fakeFailures{})
	rr := httptest.NewRecorder()
	body := `{"version":"4","status":"firing","alerts":[{"status":"firing","fingerprint":"a","labels":{"service":"x","environment":"production"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/webhook?token=s3cr3t", strings.NewReader(body))
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
}
