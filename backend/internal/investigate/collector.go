package investigate

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

// IncidentStore is the incident behavior the collector needs.
type IncidentStore interface {
	Get(ctx context.Context, id uuid.UUID) (incidents.Incident, error)
	AppendEvent(ctx context.Context, id uuid.UUID, in incidents.EventInput) error
}

// ServiceStore resolves a service mapping for an incident.
type ServiceStore interface {
	Get(ctx context.Context, id uuid.UUID) (services.Service, error)
}

// Collector runs the configured read-only investigators for an incident and
// records their evidence. Each investigator is optional; only connected ones run.
type Collector struct {
	incidents  IncidentStore
	services   ServiceStore
	k8s        *KubernetesInvestigator
	prometheus *PrometheusInvestigator
	github     *GitHubInvestigator
}

// WithGitHub connects read-only source control investigation.
func (c *Collector) WithGitHub(g *GitHubInvestigator) *Collector {
	c.github = g
	return c
}

// NewCollector builds a Collector with the connected investigators. The
// variadic Prometheus argument keeps existing Kubernetes-only wiring valid.
func NewCollector(inc IncidentStore, svc ServiceStore, k8s *KubernetesInvestigator, prometheus ...*PrometheusInvestigator) *Collector {
	c := &Collector{incidents: inc, services: svc, k8s: k8s}
	if len(prometheus) > 0 {
		c.prometheus = prometheus[0]
	}
	return c
}

// Enabled reports whether at least one investigator is connected.
func (c *Collector) Enabled() bool {
	return c.k8s != nil || c.prometheus != nil || c.github != nil
}

// Collect resolves the incident's service mapping and runs every connected
// investigator, recording evidence on the incident. It returns an error only
// when the incident or its service mapping cannot be resolved; individual
// investigator failures are recorded as unavailable evidence, not fatal.
func (c *Collector) Collect(ctx context.Context, incidentID uuid.UUID) error {
	inc, err := c.incidents.Get(ctx, incidentID)
	if err != nil {
		return err
	}
	if inc.ServiceID == nil {
		return errors.New("incident has no configured service mapping to investigate")
	}
	svc, err := c.services.Get(ctx, *inc.ServiceID)
	if err != nil {
		return fmt.Errorf("resolve service mapping: %w", err)
	}
	t := TargetFromService(svc)

	if c.k8s != nil {
		_ = c.k8s.Inspect(ctx, c.incidents, incidentID, t)
	}
	if c.prometheus != nil {
		_ = c.prometheus.Inspect(ctx, c.incidents, incidentID, t)
	}
	if c.github != nil {
		_ = c.github.Inspect(ctx, c.incidents, incidentID, t)
	}
	return nil
}
