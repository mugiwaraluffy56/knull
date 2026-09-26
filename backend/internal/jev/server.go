package jev

import (
	"context"
	"errors"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewServer(service *Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "jev-mcp", Version: decisionVersion}, nil)
	classificationSchema, _ := jsonschema.For[ClassificationResult](nil)
	nextActionSchema, _ := jsonschema.For[NextActionResult](nil)
	riskSchema, _ := jsonschema.For[RiskResult](nil)
	recoverySchema, _ := jsonschema.For[RecoveryResult](nil)
	mcp.AddTool(server, &mcp.Tool{Name: "classify_incident", Description: "Rank incident cause hypotheses using supplied evidence", OutputSchema: classificationSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		out, err := service.Classify(ctx, in)
		if err != nil {
			return toolResult(err), nil, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "select_next_action", Description: "Suggest the next incident investigation or response action", OutputSchema: nextActionSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		out, err := service.SelectNextAction(ctx, in)
		if err != nil {
			return toolResult(err), nil, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "classify_remediation_risk", Description: "Classify a proposed remediation risk without executing it", OutputSchema: riskSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		out, err := service.ClassifyRisk(ctx, in)
		if err != nil {
			return toolResult(err), nil, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "verify_recovery", Description: "Assess recovery using supplied evidence and signals", OutputSchema: recoverySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		out, err := service.VerifyRecovery(ctx, in)
		if err != nil {
			return toolResult(err), nil, nil
		}
		return nil, out, nil
	})
	return server
}

func toolResult(err error) *mcp.CallToolResult {
	if err == nil {
		return nil
	}
	code := "PROVIDER_FAILURE"
	switch {
	case errors.Is(err, ErrInvalidInput):
		code = "INVALID_INPUT"
	case errors.Is(err, ErrInvalidOutput):
		code = "INVALID_OUTPUT"
	case errors.Is(err, ErrTimeout):
		code = "TIMEOUT"
	case errors.Is(err, ErrBudget):
		code = "BUDGET_EXHAUSTED"
	}
	return &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": code, "message": err.Error()}, Content: []mcp.Content{&mcp.TextContent{Text: code + ": " + err.Error()}}}
}
