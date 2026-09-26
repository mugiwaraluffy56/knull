package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeProvider struct {
	text   string
	err    error
	byName map[string]string
}

func (f fakeProvider) Decide(_ context.Context, _, name string, _ map[string]any, _ Input) (ModelResponse, error) {
	output := f.text
	if f.byName != nil {
		output = f.byName[name]
	}
	return ModelResponse{Text: []byte(output), Model: "test-model", ResponseID: "resp-1"}, f.err
}

func TestServiceRejectsInvalidOutput(t *testing.T) {
	in := Input{Evidence: []Evidence{{ID: "ev-1", Source: "kubernetes", Summary: "OOMKilled"}}}
	for name, output := range map[string]string{
		"unknown reference": `{"classes":[{"class":"RESOURCE_EXHAUSTION","confidence":0.8,"evidence_ids":["invented"]}],"rationale":"OOM"}`,
		"missing field":     `{"classes":[{"class":"RESOURCE_EXHAUSTION","confidence":0.8}],"rationale":"OOM"}`,
		"missing support":   `{"classes":[{"class":"RESOURCE_EXHAUSTION","confidence":0.8,"evidence_ids":[]}],"rationale":"OOM"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewService(fakeProvider{text: output}, "test-model").Classify(context.Background(), in)
			if !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("want invalid output, got %v", err)
			}
		})
	}
}

func TestMCPToolDiscoveryAndCalls(t *testing.T) {
	ctx := context.Background()
	server := NewServer(NewService(fakeProvider{byName: map[string]string{
		"classify_incident":         `{"classes":[{"class":"RESOURCE_EXHAUSTION","confidence":0.8,"evidence_ids":["ev-1"]}],"rationale":"OOM"}`,
		"select_next_action":        `{"action":"INVESTIGATE_MORE","rationale":"check logs","confidence":0.7,"evidence_ids":["ev-1"],"required_evidence":["logs"]}`,
		"classify_remediation_risk": `{"risk":"HIGH","action_ref":"action-1","risk_factors":["restart"],"evidence_ids":["ev-1"]}`,
		"verify_recovery":           `{"outcome":"UNCERTAIN","confidence":0.5,"evidence_ids":["ev-1"],"observed_signals":["errors falling"]}`,
	}}, "test-model"))
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"classify_incident": true, "select_next_action": true, "classify_remediation_risk": true, "verify_recovery": true}
	if len(listed.Tools) != len(want) {
		t.Fatalf("tools: %d", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
	}
	for name := range want {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"evidence": []map[string]string{{"id": "ev-1", "source": "kubernetes", "summary": "OOMKilled"}}, "action_ref": "action-1", "action": "restart", "signals": []string{"errors falling"}}})
		if err != nil || result.IsError {
			t.Fatalf("%s call: %v, %+v", name, err, result)
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		if !strings.Contains(string(encoded), `"decision_version":"jev-v1"`) {
			t.Fatalf("%s missing version: %s", name, encoded)
		}
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "classify_incident", Arguments: map[string]any{"evidence": []any{}}})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid call: %v, %+v", err, invalid)
	}
	encoded, _ := json.Marshal(invalid.StructuredContent)
	if !strings.Contains(string(encoded), `INVALID_INPUT`) {
		t.Fatalf("missing typed error: %s", encoded)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAIProviderContract(t *testing.T) {
	var calls int
	p := &OpenAIProvider{APIKey: "customer-key", Endpoint: "https://example.invalid/v1/responses", Timeout: time.Second, MaxRequests: 1, MaxOutputTokens: 200, MaxRetries: 0}
	p.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if got := req.Header.Get("Authorization"); got != "Bearer customer-key" {
			t.Fatalf("auth %q", got)
		}
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		format := payload["text"].(map[string]any)["format"].(map[string]any)
		if format["strict"] != true || format["type"] != "json_schema" || payload["store"] != false {
			t.Fatalf("provider request: %s", body)
		}
		if strings.Contains(string(body), "customer-key") {
			t.Fatal("credential in request body")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"resp-1","model":"test-model","status":"completed","output":[{"content":[{"type":"output_text","text":"{\"classes\":[],\"rationale\":\"unknown\"}"}]}]}`)), Header: make(http.Header)}, nil
	})}
	if _, err := p.Decide(context.Background(), "test-model", "classify_incident", schemaFor("classify_incident"), Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Decide(context.Background(), "test-model", "classify_incident", schemaFor("classify_incident"), Input{}); !errors.Is(err, ErrBudget) {
		t.Fatalf("budget error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestProviderFailuresAreTyped(t *testing.T) {
	for name, response := range map[string]string{
		"refusal":      `{"status":"completed","output":[{"content":[{"type":"refusal","refusal":"cannot answer"}]}]}`,
		"incomplete":   `{"status":"incomplete","output":[]}`,
		"missing text": `{"status":"completed","output":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := &OpenAIProvider{APIKey: "key", Timeout: time.Second, MaxRequests: 1, MaxOutputTokens: 100, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}}
			_, err := p.Decide(context.Background(), "model", "classify_incident", schemaFor("classify_incident"), Input{})
			if !errors.Is(err, ErrProvider) {
				t.Fatalf("provider error: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &OpenAIProvider{APIKey: "key", Timeout: time.Second, MaxRequests: 1, MaxOutputTokens: 100}
	_, err := p.Decide(ctx, "model", "classify_incident", schemaFor("classify_incident"), Input{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout error: %v", err)
	}
}
