package sandbox

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
)

// TestLiveMemoryBaselineAndCandidate runs only when explicitly supplied a
// dedicated-cluster runner kubeconfig and pinned checkout fixture image.
// Port-forward reaches a sandbox pod through the Kubernetes API; it may bypass
// ingress NetworkPolicy and must never be used as proof of network isolation.
func TestLiveMemoryBaselineAndCandidate(t *testing.T) {
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
		Preconditions:  actions.Preconditions{ResourceUID: uuid.NewString(), ResourceVersion: "1", CurrentValue: "256Mi"},
		ExpectedImpact: "replace checkout pods", Risk: "MEDIUM", EvidenceIDs: []string{"live-memory-fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := Kubectl{Kubeconfig: kubeconfig, Context: contextName}
	runner := NewRunner(client, Config{ClusterUID: clusterUID, ProductionClusterUIDs: []string{productionUID}, AllowedImageRegistry: registry, MaxDuration: 4 * time.Minute})
	workload := Workload{ImageDigest: image, Container: "api", Replicas: 2, CPU: "500m", Memory: "1Gi"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	baseline := workload
	baseline.Memory = "256Mi"
	baselineRun, err := runner.RunBaselineWithCheck(ctx, action, baseline, func(checkCtx context.Context, namespace string) error {
		if err := waitForHealthy(checkCtx, client, namespace, 2); err != nil {
			return err
		}
		baseURL, stop, err := forwardCandidate(checkCtx, client, namespace)
		if err != nil {
			return err
		}
		defer stop()
		requestCtx, requestCancel := context.WithTimeout(checkCtx, 30*time.Second)
		defer requestCancel()
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+"/checkout", nil)
		if err != nil {
			return err
		}
		response, requestErr := (&http.Client{Timeout: 30 * time.Second}).Do(request)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		deadline := time.NewTimer(90 * time.Second)
		defer deadline.Stop()
		for {
			status, err := client.ReadPodStatus(checkCtx, namespace)
			if err != nil {
				return err
			}
			if status.OOMKills > 0 {
				t.Logf("baseline observed: namespace=%s healthy=%d/%d oomKills=%d checkoutError=%v", namespace, status.Healthy, status.Desired, status.OOMKills, requestErr)
				return nil
			}
			select {
			case <-checkCtx.Done():
				return checkCtx.Err()
			case <-deadline.C:
				return fmt.Errorf("256Mi checkout did not produce an observed OOM kill")
			case <-time.After(3 * time.Second):
			}
		}
	})
	if err != nil {
		t.Fatalf("baseline run failed: %+v: %v", baselineRun, err)
	}
	if baselineRun.Status != "checked" || !baselineRun.CleanupVerified {
		t.Fatalf("baseline run not checked and cleaned: %+v", baselineRun)
	}
	t.Logf("baseline cleaned: run=%s namespace=%s", baselineRun.ID, baselineRun.Namespace)

	candidateRun, err := runner.RunWithCheck(ctx, action, workload, func(checkCtx context.Context, namespace string) error {
		if err := waitForHealthy(checkCtx, client, namespace, 2); err != nil {
			return err
		}
		baseURL, stop, err := forwardCandidate(checkCtx, client, namespace)
		if err != nil {
			return err
		}
		defer stop()
		httpClient := &http.Client{Timeout: 30 * time.Second}
		for i := 0; i < 20; i++ {
			request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, baseURL+"/checkout", nil)
			if err != nil {
				return err
			}
			response, err := httpClient.Do(request)
			if err != nil {
				return err
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("candidate checkout status %d", response.StatusCode)
			}
		}
		status, err := client.ReadPodStatus(checkCtx, namespace)
		if err != nil {
			return err
		}
		if status.Healthy != 2 || status.Desired != 2 || status.OOMKills != 0 {
			return fmt.Errorf("candidate health mismatch: %+v", status)
		}
		t.Logf("candidate observed: namespace=%s healthy=%d/%d oomKills=%d successfulRequests=20", namespace, status.Healthy, status.Desired, status.OOMKills)
		return nil
	})
	if err != nil {
		t.Fatalf("candidate run failed: %+v: %v", candidateRun, err)
	}
	if candidateRun.Status != "checked" || !candidateRun.CleanupVerified || candidateRun.Namespace == baselineRun.Namespace {
		t.Fatalf("candidate run not checked and independently cleaned: %+v", candidateRun)
	}
	t.Logf("candidate cleaned: run=%s namespace=%s", candidateRun.ID, candidateRun.Namespace)
}

func waitForHealthy(ctx context.Context, client Kubectl, namespace string, desired int) error {
	for {
		status, err := client.ReadPodStatus(ctx, namespace)
		if err != nil {
			return err
		}
		if status.Desired == desired && status.Healthy == desired {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func forwardCandidate(ctx context.Context, client Kubectl, namespace string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	command := exec.CommandContext(ctx, "kubectl", "--kubeconfig", client.Kubeconfig, "--context", client.Context, "--request-timeout=20s", "port-forward", "-n", namespace, "deployment/candidate", fmt.Sprintf("%d:8080", port), "--address", "127.0.0.1")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "KUBECONFIG=" + client.Kubeconfig, "AWS_EC2_METADATA_DISABLED=true"}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stop := func() {
		_ = command.Process.Kill()
		<-done
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	startupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		request, err := http.NewRequestWithContext(startupCtx, http.MethodGet, baseURL+"/healthz", nil)
		if err == nil {
			response, requestErr := (&http.Client{Timeout: time.Second}).Do(request)
			if requestErr == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusNoContent {
					return baseURL, stop, nil
				}
			}
		}
		select {
		case err := <-done:
			return "", nil, fmt.Errorf("sandbox port-forward exited: %v", err)
		case <-startupCtx.Done():
			stop()
			return "", nil, fmt.Errorf("sandbox port-forward unavailable: %w", startupCtx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}
