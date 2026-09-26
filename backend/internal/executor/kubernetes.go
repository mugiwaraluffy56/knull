// Package executor applies only a sealed, approved memory-limit patch through
// a dedicated production Kubernetes identity.
package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
)

var ErrTarget = errors.New("production target does not match executor scope")

// Kubernetes uses only the in-cluster service account token. Its Deployment
// permission must be limited by a namespace Role to get and patch one workload.
type Kubernetes struct {
	Client    kubernetes.Interface
	Cluster   string
	Namespace string
	Workload  string
}

// InCluster constructs the production adapter. The CA pin binds the configured
// cluster name to the expected API server certificate; no user kubeconfig or
// investigation MCP connection can provide mutation credentials here.
func InCluster(cluster, namespace, workload, caSHA256 string) (Kubernetes, error) {
	if cluster == "" || namespace == "" || workload == "" || len(caSHA256) != 64 {
		return Kubernetes{}, ErrTarget
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return Kubernetes{}, err
	}
	ca, err := os.ReadFile(config.TLSClientConfig.CAFile)
	if err != nil {
		return Kubernetes{}, err
	}
	sum := sha256.Sum256(ca)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), caSHA256) {
		return Kubernetes{}, ErrTarget
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return Kubernetes{}, err
	}
	return Kubernetes{Client: client, Cluster: cluster, Namespace: namespace, Workload: workload}, nil
}

func (k Kubernetes) checkTarget(target actions.Target, field string) error {
	if k.Client == nil || target.Cluster != k.Cluster || target.Namespace != k.Namespace || target.Name != k.Workload || target.Kind != "Deployment" || field != "resources.limits.memory" || target.Container == "" {
		return ErrTarget
	}
	return nil
}

func (k Kubernetes) deployment(ctx context.Context, target actions.Target) (*appsv1.Deployment, error) {
	return k.Client.AppsV1().Deployments(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
}

func memory(deployment *appsv1.Deployment, name string) (string, int, error) {
	for i, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name != name {
			continue
		}
		quantity, ok := container.Resources.Limits[corev1.ResourceMemory]
		if !ok {
			return "", -1, ErrTarget
		}
		return quantity.String(), i, nil
	}
	return "", -1, ErrTarget
}

func (k Kubernetes) ReadPreconditions(ctx context.Context, target actions.Target, field string) (actions.Preconditions, error) {
	if err := k.checkTarget(target, field); err != nil {
		return actions.Preconditions{}, err
	}
	deployment, err := k.deployment(ctx, target)
	if err != nil {
		return actions.Preconditions{}, err
	}
	value, _, err := memory(deployment, target.Container)
	if err != nil {
		return actions.Preconditions{}, err
	}
	return actions.Preconditions{ResourceUID: string(deployment.UID), ResourceVersion: deployment.ResourceVersion, CurrentValue: value}, nil
}

// PatchMemory uses JSON Patch test operations as API-server-side compare and
// swap. An intervening change to UID, version, container, or value rejects the
// mutation atomically. The returned patch is safe to persist as an audit item.
func (k Kubernetes) PatchMemory(ctx context.Context, action actions.Contract) (*appsv1.Deployment, json.RawMessage, error) {
	if err := action.Validate(); err != nil {
		return nil, nil, err
	}
	if action.Type != actions.Memory || action.CurrentValue != "256Mi" || action.DesiredValue != "1Gi" {
		return nil, nil, ErrTarget
	}
	if err := k.checkTarget(action.Target, action.Field); err != nil {
		return nil, nil, err
	}
	current, err := k.deployment(ctx, action.Target)
	if err != nil {
		return nil, nil, err
	}
	value, index, err := memory(current, action.Target.Container)
	if err != nil {
		return nil, nil, err
	}
	if string(current.UID) != action.Preconditions.ResourceUID || current.ResourceVersion != action.Preconditions.ResourceVersion || value != action.CurrentValue {
		return nil, nil, ErrTarget
	}
	base := fmt.Sprintf("/spec/template/spec/containers/%d", index)
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": action.Preconditions.ResourceUID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": action.Preconditions.ResourceVersion},
		{"op": "test", "path": base + "/name", "value": action.Target.Container},
		{"op": "test", "path": base + "/resources/limits/memory", "value": action.CurrentValue},
		{"op": "replace", "path": base + "/resources/limits/memory", "value": action.DesiredValue},
	})
	if err != nil {
		return nil, nil, err
	}
	updated, err := k.Client.AppsV1().Deployments(action.Target.Namespace).Patch(ctx, action.Target.Name, types.JSONPatchType, patch, metav1.PatchOptions{FieldManager: "knull-production-executor"})
	return updated, patch, err
}
