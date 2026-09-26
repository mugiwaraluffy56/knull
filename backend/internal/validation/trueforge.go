package validation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// TrueForgeConfig names a dedicated validation connector. Its server-side
// credentials must grant access only to the disposable sandbox cluster.
type TrueForgeConfig struct {
	BaseURL        string
	Token          string
	Model          string
	SandboxMCPName string
	AllowedTools   []string
}

// TrueForgeScriptEngine uses the published TrueForge sessions/turns HTTP API.
// It trusts neither the model's final answer nor its claimed exit code: a
// sandbox.created event and a matching exec tool.response are required.
type TrueForgeScriptEngine struct {
	config TrueForgeConfig
	client *http.Client
}

func NewTrueForgeScriptEngine(config TrueForgeConfig) *TrueForgeScriptEngine {
	return &TrueForgeScriptEngine{config: config, client: &http.Client{Timeout: 0}}
}

var safeConnector = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$")
var safeTool = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$")
var safeNamespace = regexp.MustCompile("^knull-run-[a-f0-9]{20}$")

func (e *TrueForgeScriptEngine) GenerateAndExecute(ctx context.Context, req ScriptRequest) (ScriptResult, error) {
	if e == nil || e.client == nil {
		return ScriptResult{}, fmt.Errorf("%w: TrueForge client unavailable", ErrFailed)
	}
	base, err := url.Parse(e.config.BaseURL)
	if err != nil || base == nil || (base.Scheme != "https" && !(base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1"))) || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return ScriptResult{}, fmt.Errorf("%w: invalid TrueForge URL", ErrFailed)
	}
	if e.config.Model == "" || !safeConnector.MatchString(e.config.SandboxMCPName) || len(e.config.AllowedTools) == 0 || len(e.config.AllowedTools) > 8 {
		return ScriptResult{}, fmt.Errorf("%w: dedicated validation model and connector required", ErrFailed)
	}
	for _, tool := range e.config.AllowedTools {
		if !safeTool.MatchString(tool) {
			return ScriptResult{}, fmt.Errorf("%w: invalid validation tool allowlist", ErrFailed)
		}
	}
	if !safeNamespace.MatchString(req.Namespace) || !strings.HasPrefix(req.ImageDigest, "sha256:") && !strings.Contains(req.ImageDigest, "@sha256:") || len(req.ActionDigest) == 0 || len(req.ActionDigest) > 128 || (req.MemoryLimit != "256Mi" && req.MemoryLimit != "1Gi") || req.DurationSeconds < 1 || req.DurationSeconds > 120 || req.MaxRequests < 1 || req.MaxRequests > 1000 {
		return ScriptResult{}, fmt.Errorf("%w: invalid bounded validation request", ErrFailed)
	}
	deadline := time.Duration(req.DurationSeconds+60) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	baseURL := strings.TrimRight(base.String(), "/")
	spec := map[string]any{
		"model":        map[string]string{"name": e.config.Model},
		"instructions": "You validate only the explicitly named disposable knull sandbox namespace. Write and execute one bounded Python validation script in the TrueForge sandbox. Use only the attached sandbox-only MCP connector to exercise the candidate workload. Generate no more than the requested number of requests and stop within the requested duration. Report pod health, error rate, and p95 latency. Do not request credentials, use other connectors, or access production. If the sandbox connector cannot exercise the workload, stop and report the failure.",
		"mcp_servers":  []any{map[string]any{"name": e.config.SandboxMCPName, "enable_tools": e.config.AllowedTools, "require_approval_for_tools": []string{"@write", "@destructive"}}},
		"config":       map[string]any{"sandbox": map[string]any{"enabled": true, "file_downloads": false}, "dynamic_sub_agents": map[string]bool{"enabled": false}, "web_search": map[string]bool{"enabled": false}, "generative_ui": map[string]bool{"enabled": false}, "ask_user_questions": map[string]bool{"enabled": false}, "iteration_limit": 8},
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := e.postJSON(callCtx, baseURL+"/api/v1/sessions", map[string]any{"agent": map[string]any{"spec": spec}, "metadata": map[string]string{"purpose": "knull-memory-validation", "namespace": req.Namespace}}, &created); err != nil {
		return ScriptResult{}, err
	}
	if !safeConnector.MatchString(created.Data.ID) {
		return ScriptResult{}, fmt.Errorf("%w: invalid TrueForge session identity", ErrFailed)
	}
	prompt := fmt.Sprintf("Run the bounded validation script for sandbox namespace %s, image %s, memory limit %s, action digest %s. Limit runtime to %d seconds and load to %d requests. Print only one compact JSON object with namespace, actionDigest, podHealthy (boolean), errorRate (fraction), p95Milliseconds, and samples. Use only the attached sandbox-only MCP tools; if the workload is unreachable or observations are missing, report failure.", req.Namespace, req.ImageDigest, req.MemoryLimit, req.ActionDigest, req.DurationSeconds, req.MaxRequests)
	body := map[string]any{"input": []any{map[string]string{"type": "user.message", "content": prompt}}, "stream": true}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ScriptResult{}, err
	}
	streamURL := baseURL + "/api/v1/sessions/" + url.PathEscape(created.Data.ID) + "/turns"
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, streamURL, bytes.NewReader(encoded))
	if err != nil {
		return ScriptResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if e.config.Token != "" {
		request.Header.Set("Authorization", "Bearer "+e.config.Token)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return ScriptResult{}, fmt.Errorf("%w: TrueForge turn unavailable: %v", ErrFailed, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return ScriptResult{}, fmt.Errorf("%w: TrueForge turn returned status %d", ErrFailed, response.StatusCode)
	}
	return readValidationTurn(callCtx, response.Body, created.Data.ID, req)
}

func (e *TrueForgeScriptEngine) postJSON(ctx context.Context, endpoint string, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if e.config.Token != "" {
		request.Header.Set("Authorization", "Bearer "+e.config.Token)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: TrueForge unavailable: %v", ErrFailed, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("%w: TrueForge session returned status %d", ErrFailed, response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(out); err != nil {
		return fmt.Errorf("%w: malformed TrueForge session: %v", ErrFailed, err)
	}
	return nil
}

type trueForgeEvent struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	TurnID     string `json:"turn_id"`
	SandboxID  string `json:"sandbox_id"`
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	State      struct {
		Status          string `json:"status"`
		RequiredActions []any  `json:"required_actions"`
	} `json:"state"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
		ToolInfo struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tool_info"`
	} `json:"tool_calls"`
}

func readValidationTurn(ctx context.Context, body io.Reader, sessionID string, request ScriptRequest) (ScriptResult, error) {
	result := ScriptResult{SessionID: sessionID, ExitCode: -1}
	scanner := bufio.NewScanner(io.LimitReader(body, 1024*1024))
	scanner.Buffer(make([]byte, 64*1024), 128*1024)
	var frame strings.Builder
	execCalls := map[string]string{}
	execCount := 0
	done := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return result, fmt.Errorf("%w: TrueForge validation timed out: %v", ErrFailed, ctx.Err())
		}
		line := scanner.Text()
		if line == "" {
			if frame.Len() == 0 {
				continue
			}
			var event trueForgeEvent
			if err := json.Unmarshal([]byte(frame.String()), &event); err != nil {
				return result, fmt.Errorf("%w: malformed TrueForge event: %v", ErrFailed, err)
			}
			frame.Reset()
			switch event.Type {
			case "turn.created":
				result.TurnID = event.TurnID
			case "sandbox.created":
				result.SandboxID = event.SandboxID
			case "model.message":
				for _, call := range event.ToolCalls {
					if call.ToolInfo.Type == "truefoundry-system" && call.ToolInfo.Name == "exec" && call.ID != "" && len(call.Function.Arguments) <= 64*1024 {
						execCount++
						if execCount > 8 {
							return result, fmt.Errorf("%w: too many validation executions", ErrFailed)
						}
						execCalls[call.ID] = call.Function.Arguments
					}
				}
			case "tool.response":
				arguments, ok := execCalls[event.ToolCallID]
				if !ok {
					continue
				}
				var response struct {
					Success  bool   `json:"success"`
					ExitCode int    `json:"exitCode"`
					Result   string `json:"result"`
				}
				if err := json.Unmarshal([]byte(event.Content), &response); err != nil || !response.Success || response.ExitCode != 0 || len(response.Result) > 8192 {
					return result, fmt.Errorf("%w: generated validation code failed", ErrFailed)
				}
				sum := sha256.Sum256([]byte(arguments))
				result.ArtifactRef = fmt.Sprintf("sha256:%x", sum)
				result.ExecutionID = event.ToolCallID
				result.ExitCode = response.ExitCode
				result.Output = response.Result
			case "turn.done":
				if event.State.Status != "done" || len(event.State.RequiredActions) != 0 {
					return result, fmt.Errorf("%w: TrueForge turn did not finish successfully", ErrFailed)
				}
				done = true
			}
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if frame.Len() > 0 {
				frame.WriteByte('\n')
			}
			frame.WriteString(strings.TrimPrefix(line, "data:"))
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return result, fmt.Errorf("%w: TrueForge stream failed: %v", ErrFailed, err)
	}
	if !done || result.TurnID == "" || result.SandboxID == "" || result.ExecutionID == "" || result.ArtifactRef == "" || result.Output == "" {
		return result, fmt.Errorf("%w: TrueForge sandbox execution evidence incomplete", ErrFailed)
	}
	var report struct {
		Namespace       string   `json:"namespace"`
		ActionDigest    string   `json:"actionDigest"`
		PodHealthy      *bool    `json:"podHealthy"`
		ErrorRate       *float64 `json:"errorRate"`
		P95Milliseconds *float64 `json:"p95Milliseconds"`
		Samples         *int     `json:"samples"`
	}
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil || report.Namespace != request.Namespace || report.ActionDigest != request.ActionDigest || report.PodHealthy == nil || report.ErrorRate == nil || report.P95Milliseconds == nil || report.Samples == nil || math.IsNaN(*report.ErrorRate) || math.IsInf(*report.ErrorRate, 0) || *report.ErrorRate < 0 || *report.ErrorRate > 1 || math.IsNaN(*report.P95Milliseconds) || math.IsInf(*report.P95Milliseconds, 0) || *report.P95Milliseconds < 0 || *report.Samples < 1 {
		return result, fmt.Errorf("%w: generated validation report incomplete", ErrFailed)
	}
	return result, nil
}

var _ ScriptEngine = (*TrueForgeScriptEngine)(nil)
