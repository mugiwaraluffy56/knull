package classify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

// MCPClient calls only Jev's classification tool over a protocol-initialized
// streamable HTTP session. No infrastructure tools are exposed to this client.
type MCPClient struct {
	Endpoint   string
	HTTPClient *http.Client
}

func (c *MCPClient) Classify(ctx context.Context, input jev.Input) (jev.ClassificationResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "knull-backend", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.Endpoint, HTTPClient: c.HTTPClient, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		return jev.ClassificationResult{}, fmt.Errorf("connect Jev MCP: %w", err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "classify_incident", Arguments: input})
	if err != nil {
		return jev.ClassificationResult{}, fmt.Errorf("call Jev: %w", err)
	}
	if result.IsError {
		return jev.ClassificationResult{}, fmt.Errorf("Jev classification failed: %v", result.StructuredContent)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return jev.ClassificationResult{}, fmt.Errorf("encode Jev response: %w", err)
	}
	var decision jev.ClassificationResult
	if err := json.Unmarshal(encoded, &decision); err != nil {
		return jev.ClassificationResult{}, fmt.Errorf("decode Jev response: %w", err)
	}
	return decision, nil
}
