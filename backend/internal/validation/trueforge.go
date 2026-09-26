package validation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// CodeExecutor runs agent-authored code within the current EKS run namespace.
// The implementation must supply its own isolation and a bounded output.
type CodeExecutor interface {
	Execute(context.Context, ScriptRequest, string, string) (CodeExecution, error)
}

type CodeExecution struct {
	ID       string
	ExitCode int
	Output   string
}

type TrueForgeConfig struct {
	BaseURL  string
	Token    string
	Model    string
	Executor CodeExecutor
}

// TrueForgeScriptEngine obtains code from an inline agent, then delegates
// execution to the dedicated EKS sandbox. TrueForge itself gets no Kubernetes
// credentials, production connectors, or code-execution capability.
type TrueForgeScriptEngine struct {
	config TrueForgeConfig
	client *http.Client
}

func NewTrueForgeScriptEngine(config TrueForgeConfig) *TrueForgeScriptEngine {
	return &TrueForgeScriptEngine{config: config, client: &http.Client{}}
}

var validationNamespace = regexp.MustCompile(`^knull-run-[a-f0-9]{20}$`)
var validationDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (e *TrueForgeScriptEngine) GenerateAndExecute(ctx context.Context, req ScriptRequest) (ScriptResult, error) {
	if e == nil || e.config.Executor == nil || e.client == nil {
		return ScriptResult{}, fmt.Errorf("%w: EKS code executor unavailable", ErrFailed)
	}
	base, err := url.Parse(e.config.BaseURL)
	if err != nil || base == nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && !(base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1"))) ||
		e.config.Model == "" || strings.Count(e.config.Model, "/") != 1 {
		return ScriptResult{}, fmt.Errorf("%w: invalid TrueForge configuration", ErrFailed)
	}
	if !validationNamespace.MatchString(req.Namespace) || !validationDigest.MatchString(req.ActionDigest) ||
		!strings.Contains(req.ImageDigest, "@sha256:") || len(req.ImageDigest) > 300 ||
		(req.MemoryLimit != "256Mi" && req.MemoryLimit != "1Gi") ||
		req.DurationSeconds < 1 || req.DurationSeconds > 120 || req.MaxRequests < 1 || req.MaxRequests > 1000 {
		return ScriptResult{}, fmt.Errorf("%w: invalid bounded validation request", ErrFailed)
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(req.DurationSeconds+90)*time.Second)
	defer cancel()
	spec := map[string]any{
		"model":        map[string]string{"name": e.config.Model},
		"instructions": "Generate one Python 3.13 standard-library script for a disposable EKS validation Job. Do not execute it. Return only a JSON object with a code string. The backend will execute it in a restricted EKS namespace. The script must read TARGET_URL, NAMESPACE, ACTION_DIGEST, MAX_REQUESTS and DURATION_SECONDS from environment, send bounded GET requests to TARGET_URL (already ending in /checkout), and print only a JSON report containing namespace, actionDigest, podHealthy, errorRate, p95Milliseconds, and samples. Count failed HTTP attempts as errors; a failed checkout at 256Mi is expected and must still produce a JSON report with podHealthy false and exit 0. Exit nonzero only when no attempt or report is possible. Never request or print credentials. Do not access Kubernetes API, metadata endpoints, or other hosts.",
		"config": map[string]any{
			"sandbox":            map[string]bool{"enabled": false},
			"dynamic_sub_agents": map[string]bool{"enabled": false},
			"web_search":         map[string]bool{"enabled": false},
			"generative_ui":      map[string]bool{"enabled": false},
			"ask_user_questions": map[string]bool{"enabled": false},
			"iteration_limit":    4,
		},
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	baseURL := strings.TrimRight(base.String(), "/")
	if err := e.postJSON(callCtx, baseURL+"/api/v1/sessions", map[string]any{
		"agent":    map[string]any{"spec": spec},
		"metadata": map[string]string{"purpose": "knull-memory-validation", "namespace": req.Namespace},
	}, &created); err != nil {
		return ScriptResult{}, err
	}
	if created.Data.ID == "" || len(created.Data.ID) > 128 {
		return ScriptResult{}, fmt.Errorf("%w: missing TrueForge session identity", ErrFailed)
	}
	prompt := fmt.Sprintf("Author the bounded EKS validation script for namespace %s, image %s, memory %s, action digest %s. Limit requests to %d and elapsed seconds to %d. TARGET_URL points only to the candidate pod. Print the exact JSON report fields. Return only JSON with code, no Markdown.", req.Namespace, req.ImageDigest, req.MemoryLimit, req.ActionDigest, req.MaxRequests, req.DurationSeconds)
	turnBody := map[string]any{"input": []any{map[string]string{"type": "user.message", "content": prompt}}, "stream": true}
	encoded, err := json.Marshal(turnBody)
	if err != nil {
		return ScriptResult{}, err
	}
	turnURL := baseURL + "/api/v1/sessions/" + url.PathEscape(created.Data.ID) + "/turns"
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, turnURL, bytes.NewReader(encoded))
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
		return ScriptResult{}, fmt.Errorf("%w: TrueForge code generation unavailable: %v", ErrFailed, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return ScriptResult{}, fmt.Errorf("%w: TrueForge code generation status %d", ErrFailed, response.StatusCode)
	}
	turnID, code, err := readGeneratedCode(callCtx, response.Body)
	if err != nil {
		return ScriptResult{}, err
	}
	sum := sha256.Sum256([]byte(code))
	artifact := fmt.Sprintf("sha256:%x", sum)
	execution, err := e.config.Executor.Execute(callCtx, req, code, artifact)
	if err != nil {
		return ScriptResult{}, err
	}
	if execution.ID == "" || execution.ExitCode != 0 || len(execution.Output) > 8192 || !validScriptReport(execution.Output, req) {
		return ScriptResult{}, fmt.Errorf("%w: EKS validation execution or report failed", ErrFailed)
	}
	return ScriptResult{
		ArtifactRef: artifact, Code: code, SessionID: created.Data.ID, TurnID: turnID,
		SandboxID: req.Namespace, ExecutionID: execution.ID, ExitCode: execution.ExitCode,
		Output: execution.Output,
	}, nil
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
		return fmt.Errorf("%w: TrueForge session status %d", ErrFailed, response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(out); err != nil {
		return fmt.Errorf("%w: malformed TrueForge session: %v", ErrFailed, err)
	}
	return nil
}

func readGeneratedCode(ctx context.Context, body io.Reader) (string, string, error) {
	scanner := bufio.NewScanner(io.LimitReader(body, 256*1024))
	scanner.Buffer(make([]byte, 32*1024), 64*1024)
	var frame strings.Builder
	var turnID, content string
	done := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("%w: code generation timed out", ErrFailed)
		}
		line := scanner.Text()
		if line == "" {
			if frame.Len() == 0 {
				continue
			}
			var event struct {
				Type   string `json:"type"`
				TurnID string `json:"turn_id"`
				State  struct {
					Status          string `json:"status"`
					RequiredActions []any  `json:"required_actions"`
					Output          struct {
						Content any `json:"content"`
					} `json:"output"`
				} `json:"state"`
			}
			if err := json.Unmarshal([]byte(frame.String()), &event); err != nil {
				return "", "", fmt.Errorf("%w: malformed TrueForge event", ErrFailed)
			}
			frame.Reset()
			switch event.Type {
			case "turn.created":
				turnID = event.TurnID
			case "turn.done":
				if event.State.Status != "done" || len(event.State.RequiredActions) != 0 {
					return "", "", fmt.Errorf("%w: code generation did not complete", ErrFailed)
				}
				var ok bool
				content, ok = event.State.Output.Content.(string)
				if !ok {
					return "", "", fmt.Errorf("%w: missing generated code", ErrFailed)
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
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("%w: TrueForge stream failed: %v", ErrFailed, err)
	}
	if !done || turnID == "" || len(content) > 16*1024 {
		return "", "", fmt.Errorf("%w: incomplete generated code", ErrFailed)
	}
	var value struct {
		Code string `json:"code"`
	}
	if json.Unmarshal([]byte(content), &value) != nil || len(value.Code) == 0 || len(value.Code) > 12*1024 || !utf8.ValidString(value.Code) || strings.ContainsRune(value.Code, 0) {
		return "", "", fmt.Errorf("%w: generated code is not bounded JSON", ErrFailed)
	}
	return turnID, value.Code, nil
}

func validScriptReport(output string, request ScriptRequest) bool {
	var report struct {
		Namespace       string   `json:"namespace"`
		ActionDigest    string   `json:"actionDigest"`
		PodHealthy      *bool    `json:"podHealthy"`
		ErrorRate       *float64 `json:"errorRate"`
		P95Milliseconds *float64 `json:"p95Milliseconds"`
		Samples         *int     `json:"samples"`
	}
	if json.Unmarshal([]byte(output), &report) != nil || report.Namespace != request.Namespace ||
		report.ActionDigest != request.ActionDigest || report.PodHealthy == nil || report.ErrorRate == nil ||
		report.P95Milliseconds == nil || report.Samples == nil {
		return false
	}
	return !math.IsNaN(*report.ErrorRate) && !math.IsInf(*report.ErrorRate, 0) &&
		*report.ErrorRate >= 0 && *report.ErrorRate <= 1 &&
		!math.IsNaN(*report.P95Milliseconds) && !math.IsInf(*report.P95Milliseconds, 0) &&
		*report.P95Milliseconds >= 0 && *report.Samples > 0 && *report.Samples <= request.MaxRequests
}

var _ ScriptEngine = (*TrueForgeScriptEngine)(nil)
