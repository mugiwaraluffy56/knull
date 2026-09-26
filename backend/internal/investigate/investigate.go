// Package investigate turns read-only MCP tool calls (Kubernetes, Prometheus,
// GitHub) into normalized evidence recorded on an incident's timeline. It never
// mutates infrastructure and never fabricates a finding: a failed tool call is
// recorded as an unavailable-evidence event, not a success.
package investigate

import (
	"context"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

// EventRecorder records evidence onto an incident.
type EventRecorder interface {
	AppendEvent(ctx context.Context, id uuid.UUID, in incidents.EventInput) error
}

// Target is the environment-scoped mapping an investigation is bound to. It is
// derived from a configured service, so a call can never address another
// environment.
type Target struct {
	ServiceKey       string
	Environment      string
	Cluster          string
	Namespace        string
	Workload         string
	PrometheusLabels map[string]string
	GitHubRepo       string
	GitHubRef        string
}

// TargetFromService builds a Target from a configured service mapping.
func TargetFromService(s services.Service) Target {
	return Target{
		ServiceKey:       s.Key,
		Environment:      s.Environment,
		Cluster:          s.K8sCluster,
		Namespace:        s.K8sNamespace,
		Workload:         s.K8sWorkload,
		PrometheusLabels: s.PrometheusLabels,
		GitHubRepo:       s.GitHubRepo,
		GitHubRef:        s.GitHubRef,
	}
}

// recordObservation records a successful evidence finding.
func recordObservation(ctx context.Context, rec EventRecorder, id uuid.UUID, source, target, summary string, data map[string]any) error {
	return rec.AppendEvent(ctx, id, incidents.EventInput{
		Category: incidents.CategoryObservation,
		Source:   source,
		Target:   target,
		Actor:    source,
		Reason:   summary,
		Data:     data,
	})
}

// recordUnavailable records that a piece of evidence could not be gathered, so a
// provider failure is visible and never mistaken for a healthy result.
func recordUnavailable(ctx context.Context, rec EventRecorder, id uuid.UUID, source, target string, cause error) error {
	return rec.AppendEvent(ctx, id, incidents.EventInput{
		Category: incidents.CategoryObservation,
		Source:   source,
		Target:   target,
		Actor:    source,
		Reason:   "evidence unavailable: " + cause.Error(),
		Data:     map[string]any{"available": false},
	})
}
