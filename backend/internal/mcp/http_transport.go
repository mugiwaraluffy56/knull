package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// HTTPTransport speaks JSON-RPC 2.0 to an MCP server over HTTP (the streamable
// HTTP transport). A bearer token authenticates requests when configured.
type HTTPTransport struct {
	url    string
	token  string
	client *http.Client
	nextID atomic.Int64
}

// NewHTTPTransport builds an HTTP JSON-RPC transport.
func NewHTTPTransport(url, token string) *HTTPTransport {
	return &HTTPTransport{
		url:    url,
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Call performs one JSON-RPC request and returns the raw result.
func (t *HTTPTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      t.nextID.Add(1),
		Method:  method,
		Params:  params,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if t.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp server returned status %d", resp.StatusCode)
	}
	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("jsonrpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}
