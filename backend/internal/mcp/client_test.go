package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeTransport struct {
	result json.RawMessage
	err    error
	method string
	params any
}

func (f *fakeTransport) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.method = method
	f.params = params
	return f.result, f.err
}

func TestCallToolSuccess(t *testing.T) {
	ft := &fakeTransport{result: json.RawMessage(`{"content":[{"type":"text","text":"{\"pods\":3}"}]}`)}
	c := NewClient(ft)
	res, err := c.CallTool(context.Background(), "list_pods", map[string]any{"namespace": "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if ft.method != "tools/call" {
		t.Fatalf("method = %s, want tools/call", ft.method)
	}
	if res.Text() != `{"pods":3}` {
		t.Fatalf("text = %q", res.Text())
	}
}

func TestCallToolReportsToolError(t *testing.T) {
	ft := &fakeTransport{result: json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"boom"}]}`)}
	c := NewClient(ft)
	if _, err := c.CallTool(context.Background(), "x", nil); err == nil {
		t.Fatal("expected error for isError result")
	}
}

func TestCallToolTransportError(t *testing.T) {
	ft := &fakeTransport{err: errors.New("network")}
	c := NewClient(ft)
	if _, err := c.CallTool(context.Background(), "x", nil); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestCallToolJSON(t *testing.T) {
	ft := &fakeTransport{result: json.RawMessage(`{"content":[{"type":"text","text":"{\"replicas\":6}"}]}`)}
	c := NewClient(ft)
	var out struct {
		Replicas int `json:"replicas"`
	}
	if err := c.CallToolJSON(context.Background(), "get_deployment", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Replicas != 6 {
		t.Fatalf("replicas = %d", out.Replicas)
	}
}
