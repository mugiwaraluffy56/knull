// Package services stores the operator-configured services and their
// environment-scoped mappings to Kubernetes, Prometheus, and GitHub.
package services

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// slugPattern constrains identifiers to a safe, predictable shape.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// repoPattern matches an owner/repo GitHub reference.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Service is a configured product identity in one environment.
type Service struct {
	ID                uuid.UUID         `json:"id"`
	Key               string            `json:"key"`
	DisplayName       string            `json:"displayName"`
	Environment       string            `json:"environment"`
	K8sCluster        string            `json:"k8sCluster"`
	K8sNamespace      string            `json:"k8sNamespace"`
	K8sWorkload       string            `json:"k8sWorkload"`
	PrometheusLabels  map[string]string `json:"prometheusLabels"`
	GitHubRepo        string            `json:"githubRepo"`
	GitHubRef         string            `json:"githubRef"`
	RecoveryPolicyRef string            `json:"recoveryPolicyRef"`
	Enabled           bool              `json:"enabled"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

// Input carries the mutable fields for creating or updating a service.
type Input struct {
	Key               string            `json:"key"`
	DisplayName       string            `json:"displayName"`
	Environment       string            `json:"environment"`
	K8sCluster        string            `json:"k8sCluster"`
	K8sNamespace      string            `json:"k8sNamespace"`
	K8sWorkload       string            `json:"k8sWorkload"`
	PrometheusLabels  map[string]string `json:"prometheusLabels"`
	GitHubRepo        string            `json:"githubRepo"`
	GitHubRef         string            `json:"githubRef"`
	RecoveryPolicyRef string            `json:"recoveryPolicyRef"`
}

// ValidationError describes one or more rejected fields with actionable
// messages, so the UI can tell the operator exactly what to fix.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for f, msg := range e.Fields {
		parts = append(parts, f+": "+msg)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// normalizeAndValidate trims input, applies rules, and returns a cleaned copy or
// a ValidationError naming every offending field.
func normalizeAndValidate(in Input) (Input, error) {
	out := Input{
		Key:               strings.TrimSpace(in.Key),
		DisplayName:       strings.TrimSpace(in.DisplayName),
		Environment:       strings.TrimSpace(in.Environment),
		K8sCluster:        strings.TrimSpace(in.K8sCluster),
		K8sNamespace:      strings.TrimSpace(in.K8sNamespace),
		K8sWorkload:       strings.TrimSpace(in.K8sWorkload),
		GitHubRepo:        strings.TrimSpace(in.GitHubRepo),
		GitHubRef:         strings.TrimSpace(in.GitHubRef),
		RecoveryPolicyRef: strings.TrimSpace(in.RecoveryPolicyRef),
		PrometheusLabels:  in.PrometheusLabels,
	}
	if out.PrometheusLabels == nil {
		out.PrometheusLabels = map[string]string{}
	}

	fields := map[string]string{}

	if !slugPattern.MatchString(out.Key) {
		fields["key"] = "required; lowercase letters, digits, and hyphens (e.g. checkout-api)"
	}
	if out.DisplayName == "" {
		fields["displayName"] = "required"
	}
	// Environment must be explicit and well-formed; an empty or malformed
	// environment is what would let a mapping silently target the wrong place.
	if !slugPattern.MatchString(out.Environment) {
		fields["environment"] = "required; explicit environment slug (e.g. production, staging)"
	}
	if out.K8sCluster == "" {
		fields["k8sCluster"] = "required; the cluster this service runs in"
	}
	if out.K8sNamespace == "" {
		fields["k8sNamespace"] = "required"
	}
	if out.K8sWorkload == "" {
		fields["k8sWorkload"] = "required"
	}
	if out.GitHubRepo != "" && !repoPattern.MatchString(out.GitHubRepo) {
		fields["githubRepo"] = "must be in owner/repo form"
	}
	if out.GitHubRepo != "" && !refPattern.MatchString(out.GitHubRef) {
		fields["githubRef"] = "required with a GitHub repository; use an explicit branch or tag"
	}
	for k, v := range out.PrometheusLabels {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			fields["prometheusLabels"] = "label names and values must be non-empty"
			break
		}
	}

	if len(fields) > 0 {
		return Input{}, &ValidationError{Fields: fields}
	}
	return out, nil
}
