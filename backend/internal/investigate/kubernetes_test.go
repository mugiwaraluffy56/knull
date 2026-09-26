package investigate

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/mcp"
)

// fakeRecorder captures recorded evidence.
type fakeRecorder struct{ events []incidents.EventInput }

func (f *fakeRecorder) AppendEvent(_ context.Context, _ uuid.UUID, in incidents.EventInput) error {
	f.events = append(f.events, in)
	return nil
}

// fakeCaller returns canned results and records the args it was called with.
type fakeCaller struct {
	results  map[string]string // tool -> JSON text
	failTool string
	lastArgs map[string]any
}

func (c *fakeCaller) CallTool(_ context.Context, name string, args map[string]any) (mcp.ToolResult, error) {
	c.lastArgs = args
	if name == c.failTool {
		return mcp.ToolResult{}, errors.New("tool unavailable")
	}
	text := c.results[name]
	if text == "" {
		text = "{}"
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: text}}}, nil
}

func target() Target {
	return Target{
		ServiceKey: "checkout-api", Environment: "production",
		Cluster: "prod-eks", Namespace: "shop", Workload: "checkout-api",
	}
}

func TestKubernetesInspectRecordsScopedEvidence(t *testing.T) {
	caller := &fakeCaller{results: map[string]string{
		"list_pods":          `{"pods":[{"name":"checkout-1","phase":"CrashLoopBackOff"}]}`,
		"get_events":         `{"events":[{"reason":"OOMKilled"}]}`,
		"get_logs":           `{"lines":["OOMKilled"]}`,
		"get_deployment":     `{"replicas":6,"memory":"256Mi"}`,
		"get_resource_usage": `{"memory":"250Mi"}`,
	}}
	rec := &fakeRecorder{}
	inv := NewKubernetesInvestigator(caller)

	if err := inv.Inspect(context.Background(), rec, uuid.New(), target()); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(rec.events) != len(k8sReads) {
		t.Fatalf("recorded %d events, want %d", len(rec.events), len(k8sReads))
	}
	// Every call was scoped to the service's cluster/namespace/workload.
	if caller.lastArgs["cluster"] != "prod-eks" || caller.lastArgs["namespace"] != "shop" || caller.lastArgs["workload"] != "checkout-api" {
		t.Fatalf("calls not scoped: %v", caller.lastArgs)
	}
	for _, e := range rec.events {
		if e.Source != sourceKubernetes || e.Category != incidents.CategoryObservation {
			t.Fatalf("wrong evidence shape: %+v", e)
		}
		if e.Target != "prod-eks/shop/checkout-api" {
			t.Fatalf("wrong target: %s", e.Target)
		}
	}
}

func TestKubernetesFailedToolRecordedAsUnavailableNotSuccess(t *testing.T) {
	caller := &fakeCaller{results: map[string]string{}, failTool: "get_logs"}
	rec := &fakeRecorder{}
	inv := NewKubernetesInvestigator(caller)

	if err := inv.Inspect(context.Background(), rec, uuid.New(), target()); err != nil {
		t.Fatalf("inspect should not fail when only one read fails: %v", err)
	}
	var unavailable int
	for _, e := range rec.events {
		if av, ok := e.Data["available"]; ok && av == false {
			unavailable++
		}
	}
	if unavailable != 1 {
		t.Fatalf("expected 1 unavailable evidence, got %d", unavailable)
	}
}

func TestKubernetesAllFailsReturnsError(t *testing.T) {
	// A caller that fails every tool.
	caller := &failingCaller{}
	rec := &fakeRecorder{}
	inv := NewKubernetesInvestigator(caller)
	if err := inv.Inspect(context.Background(), rec, uuid.New(), target()); err == nil {
		t.Fatal("expected error when all reads fail")
	}
}

type failingCaller struct{}

func (failingCaller) CallTool(context.Context, string, map[string]any) (mcp.ToolResult, error) {
	return mcp.ToolResult{}, errors.New("down")
}
