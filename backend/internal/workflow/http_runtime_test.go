package workflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A stub TrueForge server implementing the assumed contract, used to verify the
// HTTP/SSE client end to end.
func stubServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"sess-1","runId":"run-1","status":"running"}`))
	})
	mux.HandleFunc("GET /v1/sessions/sess-1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		frames := []string{
			`{"kind":"progress","seq":1,"summary":"queried prometheus"}`,
			`{"kind":"transition","seq":2,"state":"PLANNING","summary":"root cause found"}`,
			`{"kind":"done","seq":3,"summary":"complete"}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("GET /v1/sessions/missing/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("POST /v1/sessions/sess-1/pause", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPRuntimeStartAndStream(t *testing.T) {
	srv := stubServer(t)
	rt := NewHTTPRuntime(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, err := rt.StartSession(ctx, StartRequest{IncidentID: "i1", ServiceKey: "checkout"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if s.ID != "sess-1" || s.RunID != "run-1" {
		t.Fatalf("session = %+v", s)
	}

	ch, err := rt.StreamEvents(ctx, "sess-1")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var kinds []EventKind
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
	}
	if len(kinds) != 3 || kinds[0] != KindProgress || kinds[1] != KindTransition || kinds[2] != KindDone {
		t.Fatalf("events = %v", kinds)
	}
}

func TestHTTPRuntimeMissingSessionIsUnavailable(t *testing.T) {
	srv := stubServer(t)
	rt := NewHTTPRuntime(srv.URL, "tok")
	_, err := rt.StreamEvents(context.Background(), "missing")
	if err != ErrSessionUnavailable {
		t.Fatalf("err = %v, want ErrSessionUnavailable", err)
	}
}

func TestHTTPRuntimeControl(t *testing.T) {
	srv := stubServer(t)
	rt := NewHTTPRuntime(srv.URL, "tok")
	if err := rt.Pause(context.Background(), "sess-1"); err != nil {
		t.Fatalf("pause: %v", err)
	}
}

func TestHTTPRuntimeUnreachable(t *testing.T) {
	rt := NewHTTPRuntime("http://127.0.0.1:0", "tok")
	_, err := rt.StartSession(context.Background(), StartRequest{})
	if err == nil {
		t.Fatal("expected error for unreachable runtime")
	}
}
