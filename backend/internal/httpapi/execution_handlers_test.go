package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/executor"
)

type stubExecutor struct{ calls int }

func (s *stubExecutor) Execute(context.Context, uuid.UUID, uuid.UUID, string) (executor.Receipt, error) {
	s.calls++
	return executor.Receipt{RequestID: "request-1", Status: executor.Completed}, nil
}

func TestExecuteMemoryDisabledByDefault(t *testing.T) {
	s := New(Options{AllowedOrigin: "http://localhost:3000"})
	r := httptest.NewRequest(http.MethodPost, "/api/incidents/"+uuid.NewString()+"/executions/memory", nil)
	r.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	s.handleExecuteMemory(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestExecuteMemoryRequiresTrustedOrigin(t *testing.T) {
	exec := &stubExecutor{}
	s := New(Options{AllowedOrigin: "http://localhost:3000", Executor: exec})
	r := httptest.NewRequest(http.MethodPost, "/api/incidents/"+uuid.NewString()+"/executions/memory", strings.NewReader(`{"actionEventId":"`+uuid.NewString()+`","actionDigest":"digest"}`))
	w := httptest.NewRecorder()
	s.handleExecuteMemory(w, r)
	if w.Code != http.StatusForbidden || exec.calls != 0 {
		t.Fatalf("status=%d calls=%d", w.Code, exec.calls)
	}
}
