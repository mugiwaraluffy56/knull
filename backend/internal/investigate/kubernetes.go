package investigate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/mcp"
)

// toolCaller is the MCP behavior the investigators need (satisfied by
// *mcp.Client and by a fake in tests).
type toolCaller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (mcp.ToolResult, error)
}

// KubernetesInvestigator gathers read-only Kubernetes evidence through a pinned
// read-only kubernetes-mcp-server. It holds no mutation capability.
type KubernetesInvestigator struct {
	client toolCaller
}

// NewKubernetesInvestigator builds a KubernetesInvestigator over an MCP client.
func NewKubernetesInvestigator(client toolCaller) *KubernetesInvestigator {
	return &KubernetesInvestigator{client: client}
}

const sourceKubernetes = "kubernetes"

// k8sReads is the standard read-only tool set, mapped to a human summary.
var k8sReads = []struct {
	tool    string
	summary string
}{
	{"list_pods", "listed pods"},
	{"get_deployment", "inspected deployment"},
	{"get_events", "inspected events"},
	{"get_logs", "collected logs"},
	{"get_resource_usage", "read resource usage"},
}

// Inspect runs the read-only Kubernetes tool set scoped to the target and
// records each result as evidence. Every call is scoped to the target's cluster,
// namespace, and workload, so it can never read another environment. It returns
// an error only when every read failed.
func (k *KubernetesInvestigator) Inspect(ctx context.Context, rec EventRecorder, incidentID uuid.UUID, t Target) error {
	args := k.scopedArgs(t)
	target := t.Cluster + "/" + t.Namespace + "/" + t.Workload

	var failures int
	for _, r := range k8sReads {
		res, err := k.client.CallTool(ctx, r.tool, args)
		if err != nil {
			failures++
			if recErr := recordUnavailable(ctx, rec, incidentID, sourceKubernetes, target, fmt.Errorf("%s: %w", r.tool, err)); recErr != nil {
				return recErr
			}
			continue
		}
		data := parseToolData(res.Text())
		data["tool"] = r.tool
		if err := recordObservation(ctx, rec, incidentID, sourceKubernetes, target, r.summary, data); err != nil {
			return err
		}
	}
	if failures == len(k8sReads) {
		return fmt.Errorf("all kubernetes reads failed for %s", target)
	}
	return nil
}

// scopedArgs binds every tool call to the target's cluster/namespace/workload.
func (k *KubernetesInvestigator) scopedArgs(t Target) map[string]any {
	return map[string]any{
		"cluster":   t.Cluster,
		"namespace": t.Namespace,
		"workload":  t.Workload,
	}
}

// parseToolData decodes a tool's JSON text into a map, or wraps raw text when it
// is not a JSON object, so evidence always has a structured payload.
func parseToolData(text string) map[string]any {
	if text == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err == nil {
		return m
	}
	return map[string]any{"raw": text}
}
