package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
)

type fakeCluster struct {
	uid       string
	applied   []any
	deleted   bool
	exists    bool
	applyErr  error
	deleteErr error
	verifyErr error
}

func (f *fakeCluster) ClusterUID(context.Context) (string, error) { return f.uid, nil }
func (f *fakeCluster) Apply(_ context.Context, m any) error {
	f.applied = append(f.applied, m)
	return f.applyErr
}
func (f *fakeCluster) VerifyControls(context.Context, string, Workload) error { return f.verifyErr }
func (f *fakeCluster) DeleteNamespace(context.Context, string) error {
	f.deleted = true
	return f.deleteErr
}
func (f *fakeCluster) NamespaceExists(context.Context, string) (bool, error) { return f.exists, nil }

func candidate() (actions.Contract, Workload) {
	action, _ := actions.Seal(actions.Draft{Type: actions.Memory, Environment: "production", Target: actions.Target{Cluster: "prod", Namespace: "checkout", Kind: "Deployment", Name: "checkout-api", Container: "api"}, Field: "resources.limits.memory", CurrentValue: "256Mi", DesiredValue: "1Gi", Preconditions: actions.Preconditions{ResourceUID: "71c18cb6-35c9-4624-9dba-43d1773284d7", ResourceVersion: "1", CurrentValue: "256Mi"}, ExpectedImpact: "restart pods", Risk: "MEDIUM", EvidenceIDs: []string{"ev-1"}})
	w := Workload{ImageDigest: "registry.example/checkout@sha256:" + strings.Repeat("a", 64), Container: "api", Replicas: 2, CPU: "500m", Memory: "1Gi"}
	return action, w
}

func TestSandboxAppliesControlsAndVerifiesCleanup(t *testing.T) {
	action, workload := candidate()
	cluster := &fakeCluster{uid: "sandbox-uid"}
	run, err := NewRunner(cluster, Config{ClusterUID: "sandbox-uid", ProductionClusterUIDs: []string{"prod-uid"}, AllowedImageRegistry: "registry.example"}).Run(context.Background(), action, workload)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "prepared" || !run.CleanupVerified || !cluster.deleted || len(cluster.applied) != 5 || run.ActionDigest != action.Digest {
		t.Fatalf("invalid sandbox run: %+v", run)
	}
	for _, manifest := range cluster.applied {
		data, _ := MarshalManifest(manifest)
		if strings.Contains(string(data), "Secret") || strings.Contains(string(data), "prod-uid") || strings.Contains(string(data), "kubeconfig") {
			t.Fatalf("unsafe manifest: %s", data)
		}
	}
	deployment, _ := MarshalManifest(cluster.applied[4])
	if !strings.Contains(string(deployment), `"automountServiceAccountToken":false`) || !strings.Contains(string(deployment), `"readOnlyRootFilesystem":true`) {
		t.Fatalf("pod security missing: %s", deployment)
	}
}

func TestSandboxFailsClosed(t *testing.T) {
	action, workload := candidate()
	for name, cluster := range map[string]*fakeCluster{"production identity": {uid: "prod-uid"}, "mismatched identity": {uid: "other"}} {
		t.Run(name, func(t *testing.T) {
			_, err := NewRunner(cluster, Config{ClusterUID: "sandbox-uid", ProductionClusterUIDs: []string{"prod-uid"}, AllowedImageRegistry: "registry.example"}).Run(context.Background(), action, workload)
			if !errors.Is(err, ErrIsolation) || len(cluster.applied) != 0 {
				t.Fatalf("unsafe cluster accepted: %v", err)
			}
		})
	}
	cluster := &fakeCluster{uid: "sandbox-uid", exists: true}
	run, err := NewRunner(cluster, Config{ClusterUID: "sandbox-uid", ProductionClusterUIDs: []string{"prod-uid"}, AllowedImageRegistry: "registry.example"}).Run(context.Background(), action, workload)
	if !errors.Is(err, ErrCleanup) || run.Status == "prepared" {
		t.Fatalf("failed cleanup passed: %+v %v", run, err)
	}
	workload.Memory = "256Mi"
	_, err = NewRunner(&fakeCluster{uid: "sandbox-uid"}, Config{ClusterUID: "sandbox-uid", ProductionClusterUIDs: []string{"prod-uid"}, AllowedImageRegistry: "registry.example"}).Run(context.Background(), action, workload)
	if !errors.Is(err, ErrIsolation) {
		t.Fatalf("wrong candidate accepted: %v", err)
	}
	_, err = NewRunner(&fakeCluster{uid: "sandbox-uid", verifyErr: errors.New("network policy removed")}, Config{ClusterUID: "sandbox-uid", ProductionClusterUIDs: []string{"prod-uid"}, AllowedImageRegistry: "registry.example"}).Run(context.Background(), action, func() Workload { _, w := candidate(); return w }())
	if !errors.Is(err, ErrIsolation) {
		t.Fatalf("unenforced controls accepted: %v", err)
	}
}
