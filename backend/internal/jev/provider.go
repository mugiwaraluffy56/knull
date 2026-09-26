package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	ErrProvider = errors.New("model provider failure")
	ErrTimeout  = errors.New("model request timeout")
	ErrBudget   = errors.New("model request budget exhausted")
)

type OpenAIProvider struct {
	Client          *http.Client
	APIKey          string
	Endpoint        string
	Timeout         time.Duration
	MaxRequests     int64
	MaxOutputTokens int
	MaxRetries      int
	calls           atomic.Int64
}

func (p *OpenAIProvider) Decide(ctx context.Context, model, name string, schema map[string]any, input Input) (ModelResponse, error) {
	if p.APIKey == "" {
		return ModelResponse{}, fmt.Errorf("%w: API key is not configured", ErrProvider)
	}
	if p.MaxRequests <= 0 || p.calls.Add(1) > p.MaxRequests {
		return ModelResponse{}, ErrBudget
	}
	if p.Timeout <= 0 || p.MaxOutputTokens <= 0 {
		return ModelResponse{}, fmt.Errorf("%w: invalid provider limits", ErrProvider)
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("%w: encode input: %v", ErrProvider, err)
	}
	payload, err := json.Marshal(map[string]any{
		"model":             model,
		"instructions":      "You are Jev, a read-only incident decision service. Use only supplied evidence and signals. Return only the requested JSON. Never invent evidence IDs, signals, or action references. You cannot approve, execute, or mutate infrastructure.",
		"input":             "Decision: " + name + "\nEvidence and context: " + string(inputJSON),
		"text":              map[string]any{"format": map[string]any{"type": "json_schema", "name": name, "strict": true, "schema": schema}},
		"max_output_tokens": p.MaxOutputTokens,
		"store":             false,
	})
	if err != nil {
		return ModelResponse{}, fmt.Errorf("%w: encode request: %v", ErrProvider, err)
	}
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/responses"
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	retries := p.MaxRetries
	if retries < 0 {
		retries = 0
	}
	if retries > 2 {
		retries = 2
	}
	for attempt := 0; attempt <= retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return ModelResponse{}, fmt.Errorf("%w: build request: %v", ErrProvider, err)
		}
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ModelResponse{}, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
			}
			if attempt < retries {
				continue
			}
			return ModelResponse{}, fmt.Errorf("%w: transport: %v", ErrProvider, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if readErr != nil {
			return ModelResponse{}, fmt.Errorf("%w: read response: %v", ErrProvider, readErr)
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			if attempt < retries {
				select {
				case <-ctx.Done():
					return ModelResponse{}, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
				case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
				}
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			return ModelResponse{}, fmt.Errorf("%w: HTTP %d", ErrProvider, resp.StatusCode)
		}
		var result struct {
			ID     string `json:"id"`
			Model  string `json:"model"`
			Status string `json:"status"`
			Output []struct {
				Content []struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Refusal string `json:"refusal"`
				} `json:"content"`
			} `json:"output"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return ModelResponse{}, fmt.Errorf("%w: malformed response: %v", ErrProvider, err)
		}
		if result.Status != "completed" {
			return ModelResponse{}, fmt.Errorf("%w: response status %q", ErrProvider, result.Status)
		}
		for _, output := range result.Output {
			for _, content := range output.Content {
				if content.Type == "refusal" || content.Refusal != "" {
					return ModelResponse{}, fmt.Errorf("%w: refusal", ErrProvider)
				}
				if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
					return ModelResponse{Text: []byte(content.Text), Model: result.Model, ResponseID: result.ID, InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}, nil
				}
			}
		}
		return ModelResponse{}, fmt.Errorf("%w: missing output text", ErrProvider)
	}
	return ModelResponse{}, fmt.Errorf("%w: retry limit %s", ErrProvider, strconv.Itoa(retries))
}
