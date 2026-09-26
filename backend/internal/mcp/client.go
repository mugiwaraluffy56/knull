// Package mcp is a minimal client for Model Context Protocol servers. Knull
// reuses external MCP servers for Kubernetes, Prometheus, and GitHub rather than
// building those integrations from scratch; this client speaks JSON-RPC 2.0
// tools/call to them over a pluggable transport.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Transport carries a single JSON-RPC request/response. Implementations may use
// HTTP, stdio, or an in-memory fake for tests.
type Transport interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// Content is one item in a tool result. MCP tools usually return text content
// carrying JSON.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolResult is the result of a tools/call.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

// Text concatenates all text content, which is where MCP servers place their
// (usually JSON) payload.
func (r ToolResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Client calls tools on an MCP server.
type Client struct {
	transport Transport
}

// NewClient builds a Client over a transport.
func NewClient(t Transport) *Client {
	return &Client{transport: t}
}

// CallTool invokes a named tool with arguments and decodes the tool result. A
// tool that reports IsError is returned as an error so callers never treat a
// failed tool call as a successful finding.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	raw, err := c.transport.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return ToolResult{}, fmt.Errorf("call tool %s: %w", name, err)
	}
	var res ToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return ToolResult{}, fmt.Errorf("decode tool result for %s: %w", name, err)
	}
	if res.IsError {
		return res, fmt.Errorf("tool %s reported an error: %s", name, res.Text())
	}
	return res, nil
}

// CallToolJSON invokes a tool and unmarshals its text content into out.
func (c *Client) CallToolJSON(ctx context.Context, name string, args map[string]any, out any) error {
	res, err := c.CallTool(ctx, name, args)
	if err != nil {
		return err
	}
	text := res.Text()
	if text == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("decode %s payload: %w", name, err)
	}
	return nil
}
