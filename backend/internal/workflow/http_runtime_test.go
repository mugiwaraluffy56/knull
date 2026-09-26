package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func trueForgeStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Agent struct {
				Spec struct {
					Model struct {
						Name string `json:"name"`
					} `json:"model"`
					MCPServers []any `json:"mcp_servers"`
				} `json:"spec"`
			} `json:"agent"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Agent.Spec.Model.Name != "openai/test-model" || len(body.Agent.Spec.MCPServers) != 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"sess-1"}}`))
	})
	mux.HandleFunc("POST /api/v1/sessions/sess-1/turns", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
			Input  []struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			} `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Stream || len(body.Input) != 1 || body.Input[0].Type != "user.message" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"turn-1","state":{"status":"running"}}}`))
	})
	mux.HandleFunc("GET /api/v1/sessions/sess-1/turns/turn-1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"turn-1","state":{"status":"running"}}}`))
	})
	mux.HandleFunc("GET /api/v1/sessions/missing/turns/turn-1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /api/v1/sessions/sess-1/turns/turn-1/events", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"type":"turn.created","turn_id":"turn-1"}],"pagination":{"limit":100}}`))
	})
	mux.HandleFunc("GET /api/v1/sessions/sess-1/turns/turn-1/subscribe", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after_sequence_number") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "id: 2\ndata: {\"type\":\"model.message\",\"content\":\"found root cause\"}\n\nid: 3\ndata: {\"type\":\"turn.done\",\"state\":{\"status\":\"done\",\"required_actions\":[]}}\n\n")
	})
	mux.HandleFunc("POST /api/v1/sessions/sess-1/cancel", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("GET /api/v1/sessions/sess-1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"sess-1"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func configuredRuntime(endpoint string) Runtime {
	return NewHTTPRuntimeWithConfig(endpoint, "tok", HTTPRuntimeConfig{Model: "openai/test-model"})
}

func TestHTTPRuntimeStartAndStream(t *testing.T) {
	srv := trueForgeStub(t)
	rt := configuredRuntime(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := rt.StartSession(ctx, StartRequest{IncidentID: "i1", ServiceKey: "checkout"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if session.ID != "sess-1" || session.RunID != "turn-1" || session.Status != "running" {
		t.Fatalf("session = %+v", session)
	}
	events, err := rt.StreamEvents(ctx, session.ID, session.RunID)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var kinds []EventKind
	var sequences []int64
	for event := range events {
		kinds = append(kinds, event.Kind)
		sequences = append(sequences, event.Seq)
	}
	if len(kinds) != 3 || kinds[0] != KindProgress || kinds[1] != KindProgress || kinds[2] != KindDone ||
		sequences[0] != 1 || sequences[1] != 2 || sequences[2] != 3 {
		t.Fatalf("events = %v, sequences = %v", kinds, sequences)
	}
}

func TestHTTPRuntimeMissingSessionIsUnavailable(t *testing.T) {
	srv := trueForgeStub(t)
	_, err := configuredRuntime(srv.URL).StreamEvents(context.Background(), "missing", "turn-1")
	if !errors.Is(err, ErrSessionUnavailable) {
		t.Fatalf("err = %v, want ErrSessionUnavailable", err)
	}
}

func TestHTTPRuntimeControls(t *testing.T) {
	srv := trueForgeStub(t)
	rt := configuredRuntime(srv.URL)
	if err := rt.Pause(context.Background(), "sess-1"); !errors.Is(err, ErrUnsupportedControl) {
		t.Fatalf("pause: %v", err)
	}
	if err := rt.Resume(context.Background(), "sess-1"); !errors.Is(err, ErrUnsupportedControl) {
		t.Fatalf("resume: %v", err)
	}
	if err := rt.Cancel(context.Background(), "sess-1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	session, err := rt.GetSession(context.Background(), "sess-1")
	if err != nil || session.ID != "sess-1" {
		t.Fatalf("get: %+v, %v", session, err)
	}
}

func TestHTTPRuntimeUnreachable(t *testing.T) {
	_, err := configuredRuntime("http://127.0.0.1:0").StartSession(context.Background(), StartRequest{})
	if !errors.Is(err, ErrSessionUnavailable) {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestHTTPRuntimeMissingModelFailsClosed(t *testing.T) {
	srv := trueForgeStub(t)
	rt := NewHTTPRuntimeWithConfig(srv.URL, "tok", HTTPRuntimeConfig{})
	if _, err := rt.StartSession(context.Background(), StartRequest{}); err == nil {
		t.Fatal("missing model accepted")
	}
}

func TestHTTPRuntimeReconnectUsesSequenceCursor(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/s1/turns/t1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"t1"}}`))
	})
	mux.HandleFunc("GET /api/v1/sessions/s1/turns/t1/events", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"limit":100}}`))
	})
	mux.HandleFunc("GET /api/v1/sessions/s1/turns/t1/subscribe", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			if r.URL.Query().Get("after_sequence_number") != "0" {
				t.Errorf("first cursor = %s", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, "id: 1\ndata: {\"type\":\"turn.created\"}\n\n")
		case 2:
			if r.URL.Query().Get("after_sequence_number") != "1" {
				t.Errorf("second cursor = %s", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, "id: 2\ndata: {\"type\":\"turn.done\",\"state\":{\"status\":\"done\",\"required_actions\":[]}}\n\n")
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := configuredRuntime(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := rt.StreamEvents(ctx, "s1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for event := range ch {
		count++
		if event.Kind == KindError {
			t.Fatalf("stream error: %+v", event)
		}
	}
	if count != 2 || calls.Load() != 2 {
		t.Fatalf("events=%d calls=%d", count, calls.Load())
	}
}
