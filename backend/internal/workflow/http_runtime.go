package workflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpRuntime talks to TrueForge over its HTTP + SSE API.
//
// ASSUMED CONTRACT (TrueForge has no in-repo docs; verify against the real
// service before production):
//
//	POST   {base}/v1/sessions              -> 201 {id, runId, status}
//	GET    {base}/v1/sessions/{id}/events  -> text/event-stream of RuntimeEvent
//	POST   {base}/v1/sessions/{id}/pause   -> 204
//	POST   {base}/v1/sessions/{id}/resume  -> 204
//	POST   {base}/v1/sessions/{id}/cancel  -> 204
//	GET    {base}/v1/sessions/{id}         -> 200 {id, runId, status}
//
// A bearer token authenticates every request.
type httpRuntime struct {
	base   string
	token  string
	client *http.Client
}

// NewHTTPRuntime builds a TrueForge HTTP client.
func NewHTTPRuntime(baseURL, token string) Runtime {
	return &httpRuntime{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		client: &http.Client{Timeout: 0}, // streaming; per-call ctx bounds reads
	}
}

func (r *httpRuntime) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionUnavailable, err)
	}
	return resp, nil
}

func (r *httpRuntime) StartSession(ctx context.Context, sr StartRequest) (Session, error) {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := r.do(callCtx, http.MethodPost, "/v1/sessions", sr)
	if err != nil {
		return Session{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("start session: unexpected status %d", resp.StatusCode)
	}
	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return Session{}, fmt.Errorf("decode session: %w", err)
	}
	return s, nil
}

func (r *httpRuntime) StreamEvents(ctx context.Context, sessionID string) (<-chan RuntimeEvent, error) {
	resp, err := r.do(ctx, http.MethodGet, "/v1/sessions/"+sessionID+"/events", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrSessionUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("stream events: unexpected status %d", resp.StatusCode)
	}

	out := make(chan RuntimeEvent)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		parseSSE(ctx, resp.Body, out)
	}()
	return out, nil
}

// parseSSE reads a text/event-stream, decoding each `data:` line as a
// RuntimeEvent and forwarding it until ctx is done or the stream ends.
func parseSSE(ctx context.Context, body io.Reader, out chan<- RuntimeEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var ev RuntimeEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue // skip malformed frames rather than aborting the stream
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return
		}
	}
	_ = scanner.Err() // stream end or read error both terminate the stream
}

func (r *httpRuntime) control(ctx context.Context, sessionID, action string) error {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := r.do(callCtx, http.MethodPost, "/v1/sessions/"+sessionID+"/"+action, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrSessionUnavailable
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s session: unexpected status %d", action, resp.StatusCode)
	}
	return nil
}

func (r *httpRuntime) Pause(ctx context.Context, id string) error { return r.control(ctx, id, "pause") }
func (r *httpRuntime) Resume(ctx context.Context, id string) error {
	return r.control(ctx, id, "resume")
}
func (r *httpRuntime) Cancel(ctx context.Context, id string) error {
	return r.control(ctx, id, "cancel")
}

func (r *httpRuntime) GetSession(ctx context.Context, sessionID string) (Session, error) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := r.do(callCtx, http.MethodGet, "/v1/sessions/"+sessionID, nil)
	if err != nil {
		return Session{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Session{}, ErrSessionUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("get session: unexpected status %d", resp.StatusCode)
	}
	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return Session{}, fmt.Errorf("decode session: %w", err)
	}
	return s, nil
}
