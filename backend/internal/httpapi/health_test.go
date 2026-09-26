package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(pgErr, redisErr error) *Server {
	return New(staticChecker{postgresErr: pgErr, redisErr: redisErr}, "http://localhost:3000", time.Second)
}

func TestHandleLiveAlwaysOK(t *testing.T) {
	// Liveness must not depend on backing services.
	srv := newTestServer(errors.New("db down"), errors.New("redis down"))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", rr.Code, http.StatusOK)
	}
	var body healthStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q, want %q", body.Status, "ok")
	}
}

func TestHandleReadyHealthy(t *testing.T) {
	srv := newTestServer(nil, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want %d", rr.Code, http.StatusOK)
	}
	var body healthStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Dependencies["postgres"] != "ok" || body.Dependencies["redis"] != "ok" {
		t.Fatalf("dependencies = %v, want all ok", body.Dependencies)
	}
}

func TestHandleReadyDegraded(t *testing.T) {
	srv := newTestServer(errors.New("db down"), nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	var body healthStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" {
		t.Fatalf("status = %q, want %q", body.Status, "degraded")
	}
	if body.Dependencies["postgres"] != "unavailable" {
		t.Fatalf("postgres = %q, want unavailable", body.Dependencies["postgres"])
	}
	if body.Dependencies["redis"] != "ok" {
		t.Fatalf("redis = %q, want ok", body.Dependencies["redis"])
	}
}

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	srv := newTestServer(nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/readyz", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin = %q, want configured origin", got)
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	srv := newTestServer(nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	req.Header.Set("Origin", "http://evil.example")
	srv.Handler().ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin = %q, want empty for unknown origin", got)
	}
}
