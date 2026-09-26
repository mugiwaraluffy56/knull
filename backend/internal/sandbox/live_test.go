package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
)

// Run only with a newly provisioned dedicated sandbox and short-lived runner
// kubeconfig. CI skips it because it intentionally creates and deletes a live
// namespace in the configured cluster.
func TestLiveSandboxRunAndCleanup(t *testing.T) {
	kubeconfig := mustLiveEnv(t, "KNULL_SANDBOX_KUBECONFIG")
	contextName := mustLiveEnv(t, "KNULL_SANDBOX_CONTEXT")
	clusterUID := mustLiveEnv(t, "KNULL_SANDBOX_CLUSTER_UID")
	image := mustLiveEnv(t, "KNULL_SANDBOX_LIVE_IMAGE_DIGEST")
	registry := mustLiveEnv(t, "KNULL_SANDBOX_IMAGE_REGISTRY")
	productionUID := mustLiveEnv(t, "KNULL_PRODUCTION_CLUSTER_UIDS")
	if strings.Contains(productionUID, ",") {
		productionUID = strings.Split(productionUID, ",")[0]
	}

	action, err := actions.Seal(actions.Draft{
		Type: actions.Memory, Environment: "production",
		Target: actions.Target{Cluster: "production", Namespace: "checkout", Kind: "Deployment", Name: "checkout-api", Container: "api"},
		Field:  "resources.limits.memory", CurrentValue: "256Mi", DesiredValue: "1Gi",
		Preconditions:  actions.Preconditions{ResourceUID: "71c18cb6-35c9-4624-9dba-43d1773284d7", ResourceVersion: "1", CurrentValue: "256Mi"},
		ExpectedImpact: "replace checkout pods", Risk: "MEDIUM", EvidenceIDs: []string{"live-sandbox-fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := Kubectl{Kubeconfig: kubeconfig, Context: contextName}
	runner := NewRunner(client, Config{ClusterUID: clusterUID, ProductionClusterUIDs: []string{productionUID}, AllowedImageRegistry: registry, MaxDuration: 4 * time.Minute})
	workload := Workload{ImageDigest: image, Container: "api", Replicas: 2, CPU: "500m", Memory: "1Gi"}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	run, err := runner.RunWithCheck(ctx, action, workload, func(ctx context.Context, namespace string) error {
		for {
			status, readErr := client.ReadPodStatus(ctx, namespace)
			if readErr != nil {
				return readErr
			}
			if status.Healthy == status.Desired && status.Desired == workload.Replicas {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	})
	if err != nil {
		t.Fatalf("live sandbox run failed: %+v: %v", run, err)
	}
	if run.Status != "checked" || !run.CleanupVerified || run.ClusterUID != clusterUID {
		t.Fatalf("run was not checked and cleaned: %+v", run)
	}
	t.Logf("sandbox run %s checked and cleaned namespace %s", run.ID, run.Namespace)
}

func mustLiveEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Skipf("%s not set for live sandbox verification", name)
	}
	return value
}
