package workflow

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HTTPRuntimeConfig limits the inline agent to one explicitly allowlisted MCP
// connector. An empty MCPName attaches no connector. The model must already be
// configured in TrueForge; the backend never sends its provider credential.
type HTTPRuntimeConfig struct {
	Model    string
	MCPName  string
	MCPTools []string
}

type httpRuntime struct {
	base   string
	token  string
	config HTTPRuntimeConfig
	client *http.Client
}

// NewHTTPRuntime reads the inline investigator configuration from environment.
// Missing model configuration makes StartSession fail closed.
func NewHTTPRuntime(baseURL, token string) Runtime {
	config := HTTPRuntimeConfig{
		Model:   os.Getenv("KNULL_TRUEFORGE_INVESTIGATOR_MODEL"),
		MCPName: os.Getenv("KNULL_TRUEFORGE_INVESTIGATOR_MCP"),
	}
	if raw := os.Getenv("KNULL_TRUEFORGE_INVESTIGATOR_TOOLS"); raw != "" {
		for _, name := range strings.Split(raw, ",") {
			config.MCPTools = append(config.MCPTools, strings.TrimSpace(name))
		}
	}
	return NewHTTPRuntimeWithConfig(baseURL, token, config)
}

func NewHTTPRuntimeWithConfig(baseURL, token string, config HTTPRuntimeConfig) Runtime {
	return &httpRuntime{base: strings.TrimRight(baseURL, "/"), token: token, config: config, client: &http.Client{}}
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var safeToolName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (r *httpRuntime) validate() error {
	u, err := url.Parse(r.base)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return fmt.Errorf("invalid TrueForge URL")
	}
	if r.config.Model == "" || len(r.config.Model) > 128 || strings.Count(r.config.Model, "/") != 1 {
		return fmt.Errorf("KNULL_TRUEFORGE_INVESTIGATOR_MODEL must name a configured provider/model")
	}
	if r.config.MCPName == "" {
		if len(r.config.MCPTools) != 0 {
			return fmt.Errorf("investigator tools require a named MCP connector")
		}
		return nil
	}
	if !safeToolName.MatchString(r.config.MCPName) || len(r.config.MCPTools) == 0 || len(r.config.MCPTools) > 16 {
		return fmt.Errorf("investigator MCP requires an explicit tool allowlist")
	}
	for _, tool := range r.config.MCPTools {
		if !safeToolName.MatchString(tool) {
			return fmt.Errorf("invalid investigator tool name")
		}
	}
	return nil
}

func (r *httpRuntime) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, r.base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionUnavailable, err)
	}
	return response, nil
}

func (r *httpRuntime) StartSession(ctx context.Context, sr StartRequest) (Session, error) {
	if err := r.validate(); err != nil {
		return Session{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	spec := map[string]any{
		"model":        map[string]string{"name": r.config.Model},
		"instructions": "Investigate only the supplied incident. Use attached read-only tools for evidence. Report observations separately from hypotheses. Never request credentials, mutate infrastructure, or claim an action was executed. Do not return private reasoning.",
		"config": map[string]any{
			"sandbox":            map[string]bool{"enabled": false},
			"dynamic_sub_agents": map[string]bool{"enabled": false},
			"web_search":         map[string]bool{"enabled": false},
			"generative_ui":      map[string]bool{"enabled": false},
			"ask_user_questions": map[string]bool{"enabled": false},
			"iteration_limit":    30,
		},
	}
	if r.config.MCPName != "" {
		spec["mcp_servers"] = []any{map[string]any{
			"name": r.config.MCPName, "enable_tools": r.config.MCPTools,
			"require_approval_for_tools": []string{"@write", "@destructive"},
		}}
	}
	metadata := map[string]string{"purpose": "knull-investigation"}
	if len(sr.IncidentID) <= 128 {
		metadata["incident_id"] = sr.IncidentID
	}
	response, err := r.do(callCtx, http.MethodPost, "/api/v1/sessions", map[string]any{"agent": map[string]any{"spec": spec}, "metadata": metadata})
	if err != nil {
		return Session{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return Session{}, fmt.Errorf("create TrueForge session: status %d", response.StatusCode)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&created); err != nil || !safeID.MatchString(created.Data.ID) {
		return Session{}, fmt.Errorf("invalid TrueForge session response")
	}
	input, err := json.Marshal(sr)
	if err != nil {
		return Session{}, err
	}
	if len(input) > 16*1024 {
		return Session{}, fmt.Errorf("incident context too large for TrueForge")
	}
	turnBody := map[string]any{"input": []any{map[string]string{"type": "user.message", "content": "Investigate this incident and report bounded evidence: " + string(input)}}, "stream": false}
	turnResponse, err := r.do(callCtx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(created.Data.ID)+"/turns", turnBody)
	if err != nil {
		return Session{}, err
	}
	defer turnResponse.Body.Close()
	if turnResponse.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("start TrueForge turn: status %d", turnResponse.StatusCode)
	}
	var started struct {
		Data struct {
			ID    string `json:"id"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(turnResponse.Body, 64*1024)).Decode(&started); err != nil || !safeID.MatchString(started.Data.ID) || started.Data.State.Status != "running" {
		return Session{}, fmt.Errorf("invalid TrueForge turn response")
	}
	return Session{ID: created.Data.ID, RunID: started.Data.ID, Status: "running"}, nil
}

func (r *httpRuntime) StreamEvents(ctx context.Context, sessionID, runID string) (<-chan RuntimeEvent, error) {
	if !safeID.MatchString(sessionID) || !safeID.MatchString(runID) {
		return nil, ErrSessionUnavailable
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	turn, err := r.do(callCtx, http.MethodGet, "/api/v1/sessions/"+url.PathEscape(sessionID)+"/turns/"+url.PathEscape(runID), nil)
	if err != nil {
		return nil, err
	}
	defer turn.Body.Close()
	if turn.StatusCode == http.StatusNotFound {
		return nil, ErrSessionUnavailable
	}
	if turn.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get TrueForge turn: status %d", turn.StatusCode)
	}
	out := make(chan RuntimeEvent)
	go func() {
		defer close(out)
		if err := r.replayAndSubscribe(ctx, sessionID, runID, out); err != nil {
			sendRuntimeEvent(ctx, out, RuntimeEvent{Kind: KindError, Summary: "TrueForge event stream failed", Data: map[string]any{"error": err.Error()}})
		}
	}()
	return out, nil
}

func (r *httpRuntime) replayAndSubscribe(ctx context.Context, sessionID, runID string, out chan<- RuntimeEvent) error {
	base := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/turns/" + url.PathEscape(runID)
	var cursor int64
	pageToken := ""
	for page := 0; page < 100; page++ {
		path := base + "/events?limit=100&order=asc"
		if pageToken != "" {
			path += "&page_token=" + url.QueryEscape(pageToken)
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		response, err := r.do(callCtx, http.MethodGet, path, nil)
		if err != nil {
			cancel()
			return err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			cancel()
			return fmt.Errorf("list TrueForge events: status %d", response.StatusCode)
		}
		var batch struct {
			Data       []json.RawMessage `json:"data"`
			Pagination struct {
				NextPageToken string `json:"next_page_token"`
			} `json:"pagination"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&batch)
		response.Body.Close()
		cancel()
		if err != nil {
			return fmt.Errorf("decode TrueForge events: %w", err)
		}
		for _, raw := range batch.Data {
			cursor++
			ev := mapTrueForgeEvent(raw, cursor, sessionID, runID)
			if ev.Kind == "" {
				continue
			}
			if !sendRuntimeEvent(ctx, out, ev) {
				return ctx.Err()
			}
			if ev.Kind == KindDone || ev.Kind == KindError {
				return nil
			}
		}
		pageToken = batch.Pagination.NextPageToken
		if pageToken == "" {
			break
		}
		if page == 99 {
			return fmt.Errorf("TrueForge event replay exceeded page limit")
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		path := base + "/subscribe?after_sequence_number=" + strconv.FormatInt(cursor, 10)
		response, err := r.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			if !waitRetry(ctx, attempt) {
				return err
			}
			continue
		}
		if response.StatusCode == http.StatusPreconditionFailed {
			response.Body.Close()
			return fmt.Errorf("TrueForge live stream expired before turn completion")
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return fmt.Errorf("subscribe TrueForge turn: status %d", response.StatusCode)
		}
		done, next, err := parseTrueForgeSSE(ctx, response.Body, cursor, sessionID, runID, out)
		response.Body.Close()
		cursor = next
		if done {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !waitRetry(ctx, attempt) {
			return err
		}
		if err == nil && !waitRetry(ctx, attempt) {
			return fmt.Errorf("TrueForge stream closed before turn completion")
		}
	}
	return fmt.Errorf("TrueForge stream reconnect limit exceeded")
}

func waitRetry(ctx context.Context, attempt int) bool {
	delay := time.Duration(1<<attempt) * 250 * time.Millisecond
	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}

func parseTrueForgeSSE(ctx context.Context, body io.Reader, cursor int64, sessionID, runID string, out chan<- RuntimeEvent) (bool, int64, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	var payload strings.Builder
	var sequence int64
	emit := func() (bool, error) {
		if payload.Len() == 0 {
			return false, nil
		}
		if sequence < 1 {
			return false, fmt.Errorf("TrueForge event missing sequence")
		}
		if sequence <= cursor {
			payload.Reset()
			return false, nil
		}
		if sequence != cursor+1 {
			return false, fmt.Errorf("TrueForge event sequence gap")
		}
		cursor = sequence
		ev := mapTrueForgeEvent([]byte(payload.String()), cursor, sessionID, runID)
		payload.Reset()
		if ev.Kind != "" && !sendRuntimeEvent(ctx, out, ev) {
			return false, ctx.Err()
		}
		return ev.Kind == KindDone || ev.Kind == KindError, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := emit()
			if done || err != nil {
				return done, cursor, err
			}
			sequence = 0
			continue
		}
		if strings.HasPrefix(line, "id:") {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64)
			if err != nil || n < 1 {
				return false, cursor, fmt.Errorf("invalid TrueForge event sequence")
			}
			sequence = n
		}
		if strings.HasPrefix(line, "data:") {
			if payload.Len() > 0 {
				payload.WriteByte('\n')
			}
			payload.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return false, cursor, err
	}
	if payload.Len() > 0 {
		done, err := emit()
		return done, cursor, err
	}
	return false, cursor, nil
}

func mapTrueForgeEvent(raw []byte, seq int64, sessionID, runID string) RuntimeEvent {
	var event struct {
		Type       string          `json:"type"`
		SandboxID  string          `json:"sandbox_id"`
		ToolCallID string          `json:"tool_call_id"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Function struct {
				Arguments string `json:"arguments"`
			} `json:"function"`
			ToolInfo struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tool_info"`
		} `json:"tool_calls"`
		State struct {
			Status          string `json:"status"`
			RequiredActions []any  `json:"required_actions"`
		} `json:"state"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Type == "" {
		return RuntimeEvent{Kind: KindError, Seq: seq, Summary: "malformed TrueForge event"}
	}
	data := map[string]any{"sessionId": sessionID, "runId": runID, "trueforgeType": event.Type}
	switch event.Type {
	case "turn.created":
		return RuntimeEvent{Kind: KindProgress, Seq: seq, Summary: "TrueForge turn started", Data: data}
	case "sandbox.created":
		data["sandboxId"] = event.SandboxID
		return RuntimeEvent{Kind: KindProgress, Seq: seq, Summary: "TrueForge sandbox started", Data: data}
	case "model.message":
		for _, call := range event.ToolCalls {
			if call.ToolInfo.Type == "truefoundry-system" && call.ToolInfo.Name == "exec" && len(call.Function.Arguments) <= 64*1024 {
				sum := sha256.Sum256([]byte(call.Function.Arguments))
				data["artifactRef"] = fmt.Sprintf("sha256:%x", sum)
				data["executionId"] = call.ID
				return RuntimeEvent{Kind: KindProgress, Seq: seq, Summary: "agent-authored code started", Data: data}
			}
		}
		return RuntimeEvent{Kind: KindProgress, Seq: seq, Summary: "TrueForge agent message", Data: data}
	case "tool.response":
		data["executionId"] = event.ToolCallID
		var content string
		if json.Unmarshal(event.Content, &content) == nil {
			var result struct {
				ExitCode *int `json:"exitCode"`
			}
			if json.Unmarshal([]byte(content), &result) == nil && result.ExitCode != nil {
				data["exitCode"] = *result.ExitCode
			}
		}
		return RuntimeEvent{Kind: KindProgress, Seq: seq, Summary: "TrueForge tool completed", Data: data}
	case "turn.done":
		if event.State.Status == "done" && len(event.State.RequiredActions) == 0 {
			return RuntimeEvent{Kind: KindDone, Seq: seq, Summary: "TrueForge turn completed", Data: data}
		}
		if event.State.Status == "done" {
			return RuntimeEvent{Kind: KindProgress, Seq: seq, Summary: "TrueForge turn awaits input", Data: data}
		}
		return RuntimeEvent{Kind: KindError, Seq: seq, Summary: "TrueForge turn ended without completion", Data: data}
	case "turn.update", "mcp.initialize", "tool.approval_required", "mcp.auth_required":
		return RuntimeEvent{Kind: KindProgress, Seq: seq, Summary: "TrueForge " + event.Type, Data: data}
	default:
		return RuntimeEvent{}
	}
}

func sendRuntimeEvent(ctx context.Context, out chan<- RuntimeEvent, event RuntimeEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *httpRuntime) Pause(context.Context, string) error {
	return ErrUnsupportedControl
}

func (r *httpRuntime) Resume(context.Context, string) error {
	return ErrUnsupportedControl
}

func (r *httpRuntime) Cancel(ctx context.Context, sessionID string) error {
	if !safeID.MatchString(sessionID) {
		return ErrSessionUnavailable
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := r.do(callCtx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(sessionID)+"/cancel", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return ErrSessionUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("cancel TrueForge session: status %d", response.StatusCode)
	}
	return nil
}

func (r *httpRuntime) GetSession(ctx context.Context, sessionID string) (Session, error) {
	if !safeID.MatchString(sessionID) {
		return Session{}, ErrSessionUnavailable
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := r.do(callCtx, http.MethodGet, "/api/v1/sessions/"+url.PathEscape(sessionID), nil)
	if err != nil {
		return Session{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Session{}, ErrSessionUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("get TrueForge session: status %d", response.StatusCode)
	}
	var payload struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&payload); err != nil || payload.Data.ID != sessionID {
		return Session{}, fmt.Errorf("invalid TrueForge session response")
	}
	return Session{ID: payload.Data.ID}, nil
}

var _ Runtime = (*httpRuntime)(nil)
