// Package sandbox runs candidate workloads only on a dedicated, customer-owned
// validation cluster. Namespace controls provide cleanup; cluster identity is
// the production isolation boundary.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
)

var ErrIsolation = errors.New("sandbox isolation check failed")
var ErrCleanup = errors.New("sandbox cleanup failed")

type Client interface {
	ClusterUID(context.Context) (string, error)
	Apply(context.Context, any) error
	VerifyControls(context.Context, string, Workload) error
	DeleteNamespace(context.Context, string) error
	NamespaceExists(context.Context, string) (bool, error)
}

type Config struct {
	ClusterUID            string
	ProductionClusterUIDs []string
	AllowedImageRegistry  string
	PullSecretName        string
	MaxDuration           time.Duration
}

type Workload struct {
	ImageDigest string `json:"imageDigest"`
	Container   string `json:"container"`
	Replicas    int    `json:"replicas"`
	CPU         string `json:"cpu"`
	Memory      string `json:"memory"`
}

type Run struct {
	ID                     uuid.UUID `json:"id"`
	Namespace              string    `json:"namespace"`
	ClusterUID             string    `json:"clusterUid"`
	ActionDigest           string    `json:"actionDigest"`
	ActionVersion          string    `json:"actionVersion"`
	Workload               Workload  `json:"workload"`
	Limitations            []string  `json:"limitations"`
	EnvironmentDifferences []string  `json:"environmentDifferences"`
	StartedAt              time.Time `json:"startedAt"`
	FinishedAt             time.Time `json:"finishedAt"`
	CleanupVerified        bool      `json:"cleanupVerified"`
	Status                 string    `json:"status"`
	Error                  string    `json:"error,omitempty"`
}

type Runner struct {
	client Client
	config Config
}

func NewRunner(client Client, config Config) *Runner { return &Runner{client: client, config: config} }

var imageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9./:_-]+@sha256:[a-f0-9]{64}$`)

// Run applies a sanitized workload with only a pinned image and bounded
// resource fields. It always attempts deletion and verifies that cleanup ended.
// It does not claim validation passed; Task 18 supplies independent checks.
func (r *Runner) Run(ctx context.Context, action actions.Contract, workload Workload) (result Run, runErr error) {
	result = Run{ID: uuid.New(), ActionDigest: action.Digest, ActionVersion: action.Version, Workload: workload, StartedAt: time.Now().UTC(), Status: "failed"}
	defer func() {
		result.FinishedAt = time.Now().UTC()
		if runErr != nil {
			result.Error = runErr.Error()
		}
	}()
	if err := action.Validate(); err != nil {
		return result, err
	}
	if err := r.validateConfig(ctx, action, workload); err != nil {
		return result, err
	}
	result.ClusterUID = r.config.ClusterUID
	result.Namespace = "knull-run-" + strings.ReplaceAll(result.ID.String(), "-", "")[:20]
	duration := r.config.MaxDuration
	if duration <= 0 || duration > 30*time.Minute {
		duration = 15 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if err := r.client.DeleteNamespace(cleanupCtx, result.Namespace); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("%w: %v", ErrCleanup, err))
		}
		if exists, err := r.client.NamespaceExists(cleanupCtx, result.Namespace); err != nil || exists {
			runErr = errors.Join(runErr, fmt.Errorf("%w: namespace still exists or cannot be checked: %v", ErrCleanup, err))
		} else {
			result.CleanupVerified = true
		}
		if runErr == nil {
			result.Status = "prepared"
		}
	}()
	for _, manifest := range manifests(result.Namespace, result.ID, action, workload, r.config.PullSecretName) {
		if err := r.client.Apply(runCtx, manifest); err != nil {
			return result, fmt.Errorf("apply sandbox controls or workload: %w", err)
		}
	}
	if err := r.client.VerifyControls(runCtx, result.Namespace, workload); err != nil {
		return result, fmt.Errorf("%w: controls not enforced: %v", ErrIsolation, err)
	}
	result.Limitations = []string{"candidate workload uses sanitized configuration only", "application dependencies and production data are absent unless independently staged"}
	result.EnvironmentDifferences = []string{"dedicated sandbox cluster", "new run namespace", "no production Secrets or data", "network ingress and egress denied by default"}
	return result, nil
}

func (r *Runner) validateConfig(ctx context.Context, action actions.Contract, workload Workload) error {
	if r.client == nil || r.config.ClusterUID == "" || len(r.config.ProductionClusterUIDs) == 0 || r.config.ProductionClusterUIDs[0] == "" || r.config.AllowedImageRegistry == "" {
		return fmt.Errorf("%w: cluster identity and image policy must be configured", ErrIsolation)
	}
	actual, err := r.client.ClusterUID(ctx)
	if err != nil || actual != r.config.ClusterUID {
		return fmt.Errorf("%w: cluster UID mismatch: %v", ErrIsolation, err)
	}
	for _, uid := range r.config.ProductionClusterUIDs {
		if uid == actual {
			return fmt.Errorf("%w: sandbox cluster is production", ErrIsolation)
		}
	}
	if !imageRef.MatchString(workload.ImageDigest) || !strings.HasPrefix(workload.ImageDigest, r.config.AllowedImageRegistry+"/") || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`).MatchString(workload.Container) || workload.Replicas < 1 || workload.Replicas > 6 || !regexp.MustCompile(`^(?:[1-9][0-9]{0,3}m|[1-2])$`).MatchString(workload.CPU) || !regexp.MustCompile(`^[1-9][0-9]{0,3}(?:Mi|Gi)$`).MatchString(workload.Memory) || (r.config.PullSecretName != "" && !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`).MatchString(r.config.PullSecretName)) {
		return fmt.Errorf("%w: invalid sanitized workload", ErrIsolation)
	}
	if action.Target.Container != "" && action.Target.Container != workload.Container {
		return fmt.Errorf("%w: container mismatch", ErrIsolation)
	}
	switch action.Type {
	case actions.Memory:
		if workload.Memory != action.DesiredValue {
			return fmt.Errorf("%w: candidate memory differs from action", ErrIsolation)
		}
	case actions.CPU:
		if workload.CPU != action.DesiredValue {
			return fmt.Errorf("%w: candidate CPU differs from action", ErrIsolation)
		}
	case actions.Scale:
		if fmt.Sprint(workload.Replicas) != action.DesiredValue {
			return fmt.Errorf("%w: candidate replicas differ from action", ErrIsolation)
		}
	case actions.Rollback:
		if !strings.HasSuffix(workload.ImageDigest, "@"+action.DesiredValue) {
			return fmt.Errorf("%w: candidate image differs from rollback action", ErrIsolation)
		}
	case actions.Restart:
		if action.DesiredValue == "" {
			return fmt.Errorf("%w: restart timestamp missing", ErrIsolation)
		}
	}
	return nil
}

func manifests(namespace string, id uuid.UUID, action actions.Contract, w Workload, pullSecret string) []any {
	labels := map[string]string{"app.kubernetes.io/managed-by": "knull", "knull.dev/run-id": id.String()}
	ns := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "labels": map[string]string{"pod-security.kubernetes.io/enforce": "restricted", "pod-security.kubernetes.io/audit": "restricted", "pod-security.kubernetes.io/warn": "restricted", "app.kubernetes.io/managed-by": "knull", "knull.dev/run-id": id.String()}}}
	quota := map[string]any{"apiVersion": "v1", "kind": "ResourceQuota", "metadata": map[string]any{"name": "knull-quota", "namespace": namespace}, "spec": map[string]any{"hard": map[string]string{"pods": "8", "requests.cpu": "4", "requests.memory": "8Gi", "limits.cpu": "8", "limits.memory": "16Gi", "services": "2", "persistentvolumeclaims": "0"}}}
	limits := map[string]any{"apiVersion": "v1", "kind": "LimitRange", "metadata": map[string]any{"name": "knull-limits", "namespace": namespace}, "spec": map[string]any{"limits": []any{map[string]any{"type": "Container", "defaultRequest": map[string]string{"cpu": "100m", "memory": "128Mi"}, "default": map[string]string{"cpu": "1", "memory": "1Gi"}, "max": map[string]string{"cpu": "2", "memory": "4Gi"}}}}}
	deny := map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]any{"name": "default-deny", "namespace": namespace}, "spec": map[string]any{"podSelector": map[string]any{}, "policyTypes": []string{"Ingress", "Egress"}}}
	container := map[string]any{"name": w.Container, "image": w.ImageDigest, "imagePullPolicy": "IfNotPresent", "resources": map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]string{"cpu": w.CPU, "memory": w.Memory}}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "runAsNonRoot": true, "capabilities": map[string]any{"drop": []string{"ALL"}}, "seccompProfile": map[string]string{"type": "RuntimeDefault"}}}
	podSpec := map[string]any{"automountServiceAccountToken": false, "containers": []any{container}, "securityContext": map[string]any{"runAsNonRoot": true}}
	if pullSecret != "" {
		podSpec["imagePullSecrets"] = []any{map[string]string{"name": pullSecret}}
	}
	templateMetadata := map[string]any{"labels": labels}
	if action.Type == actions.Restart {
		templateMetadata["annotations"] = map[string]string{"knull.dev/restartedAt": action.DesiredValue}
	}
	deployment := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "candidate", "namespace": namespace, "labels": labels}, "spec": map[string]any{"replicas": w.Replicas, "selector": map[string]any{"matchLabels": labels}, "template": map[string]any{"metadata": templateMetadata, "spec": podSpec}}}
	return []any{ns, quota, limits, deny, deployment}
}

// MarshalManifest is used by the restricted Kubernetes adapter.
func MarshalManifest(manifest any) ([]byte, error) { return json.Marshal(manifest) }
