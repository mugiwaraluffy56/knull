package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckoutRetainsMemoryAndReportsCompletedRequests(t *testing.T) {
	f := &fixture{}
	recorder := httptest.NewRecorder()
	f.checkout(1)(recorder, httptest.NewRequest(http.MethodGet, "/checkout", nil))
	if recorder.Code != http.StatusOK || len(f.held) != 1024*1024 {
		t.Fatalf("fixture did not retain allocation: status=%d bytes=%d", recorder.Code, len(f.held))
	}
	metrics := httptest.NewRecorder()
	f.metrics(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `checkout_requests_total{status="200"} 1`) || !strings.Contains(metrics.Body.String(), "checkout_request_duration_seconds_count 1") {
		t.Fatalf("completed request not reported: %s", metrics.Body.String())
	}
}
