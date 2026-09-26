package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
)

func memoryAction(t *testing.T) actions.Contract {
	t.Helper()
	action, err := actions.Seal(actions.Draft{
		Type: actions.Memory, Environment: "production",
		Target: actions.Target{Cluster: "prod-east", Namespace: "checkout", Kind: "Deployment", Name: "checkout-api", Container: "api"},
		Field:  "resources.limits.memory", CurrentValue: "256Mi", DesiredValue: "1Gi",
		Preconditions:  actions.Preconditions{ResourceUID: "71c18cb6-35c9-4624-9dba-43d1773284d7", ResourceVersion: "9123", CurrentValue: "256Mi"},
		ExpectedImpact: "rolling restart", Risk: "MEDIUM", EvidenceIDs: []string{"ev-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func deployment(action actions.Contract) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: action.Target.Name, Namespace: action.Target.Namespace, UID: "71c18cb6-35c9-4624-9dba-43d1773284d7", ResourceVersion: "9123"},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar"}, {Name: "api", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")}}}}}}},
	}
}

func TestPatchMemoryUsesAtomicTestsAndExactField(t *testing.T) {
	action := memoryAction(t)
	client := fake.NewSimpleClientset(deployment(action))
	client.PrependReactor("patch", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		patchAction := a.(ktesting.PatchAction)
		var ops []struct {
			Op    string `json:"op"`
			Path  string `json:"path"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(patchAction.GetPatch(), &ops); err != nil {
			t.Fatal(err)
		}
		want := []string{"/metadata/uid", "/metadata/resourceVersion", "/spec/template/spec/containers/1/name", "/spec/template/spec/containers/1/resources/limits/memory", "/spec/template/spec/containers/1/resources/limits/memory"}
		if len(ops) != len(want) {
			t.Fatalf("operations: %+v", ops)
		}
		for i, op := range ops {
			if op.Path != want[i] {
				t.Fatalf("operation %d: %+v", i, op)
			}
		}
		if ops[0].Value != action.Preconditions.ResourceUID || ops[1].Value != action.Preconditions.ResourceVersion || ops[2].Value != "api" || ops[3].Value != "256Mi" || ops[4].Op != "replace" || ops[4].Value != "1Gi" {
			t.Fatalf("unsealed patch: %+v", ops)
		}
		updated := deployment(action)
		updated.ResourceVersion = "9124"
		updated.Spec.Template.Spec.Containers[1].Resources.Limits[corev1.ResourceMemory] = resource.MustParse("1Gi")
		return true, updated, nil
	})
	k := Kubernetes{Client: client, Cluster: "prod-east", Namespace: "checkout", Workload: "checkout-api"}
	updated, _, err := k.PatchMemory(context.Background(), action)
	if err != nil || updated.ResourceVersion != "9124" {
		t.Fatalf("patch: %v %+v", err, updated)
	}
}

func TestPatchMemoryRejectsWrongTargetAndStaleValue(t *testing.T) {
	action := memoryAction(t)
	client := fake.NewSimpleClientset(deployment(action))
	k := Kubernetes{Client: client, Cluster: "different", Namespace: "checkout", Workload: "checkout-api"}
	if _, _, err := k.PatchMemory(context.Background(), action); !errors.Is(err, ErrTarget) {
		t.Fatalf("wrong cluster: %v", err)
	}
	k.Cluster = "prod-east"
	stale := action
	stale.Preconditions.ResourceVersion = "9122"
	stale, _ = actions.Seal(stale.Draft)
	if _, _, err := k.PatchMemory(context.Background(), stale); !errors.Is(err, ErrTarget) {
		t.Fatalf("stale version: %v", err)
	}
	if len(client.Actions()) != 1 || client.Actions()[0].GetVerb() != "get" {
		t.Fatalf("unexpected mutation: %+v", client.Actions())
	}
}
