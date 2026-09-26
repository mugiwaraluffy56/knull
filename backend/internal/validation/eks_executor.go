package validation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EKSExecutorConfig identifies only the dedicated sandbox cluster. The
// kubeconfig must contain a sandbox-only service-account token, never AWS or
// production credentials. ImageDigest is a pinned Python executor image.
type EKSExecutorConfig struct {
	Kubeconfig           string
	Context              string
	ClusterUID           string
	ImageDigest          string
	AllowedImageRegistry string
}

type KubernetesCodeExecutor struct {
	config EKSExecutorConfig
}

func NewKubernetesCodeExecutor(config EKSExecutorConfig) *KubernetesCodeExecutor {
	return &KubernetesCodeExecutor{config: config}
}

var pinnedExecutorImage = regexp.MustCompile(`^[a-z0-9][a-z0-9./:_-]+@sha256:[a-f0-9]{64}$`)

func (e *KubernetesCodeExecutor) Execute(ctx context.Context, request ScriptRequest, code, artifact string) (CodeExecution, error) {
	if e == nil || e.config.Kubeconfig == "" || e.config.Context == "" || e.config.ClusterUID == "" ||
		e.config.AllowedImageRegistry == "" || !pinnedExecutorImage.MatchString(e.config.ImageDigest) ||
		!strings.HasPrefix(e.config.ImageDigest, e.config.AllowedImageRegistry+"/") ||
		!validationNamespace.MatchString(request.Namespace) || !validationDigest.MatchString(artifact) ||
		len(code) == 0 || len(code) > 12*1024 {
		return CodeExecution{}, fmt.Errorf("%w: EKS executor isolation configuration incomplete", ErrFailed)
	}
	clusterUID, err := e.getClusterUID(ctx)
	if err != nil || clusterUID != e.config.ClusterUID {
		return CodeExecution{}, fmt.Errorf("%w: EKS sandbox cluster identity mismatch", ErrFailed)
	}
	target, err := e.candidatePodIP(ctx, request.Namespace)
	if err != nil {
		return CodeExecution{}, err
	}
	jobName := "validation-" + strings.TrimPrefix(artifact, "sha256:")[:20]
	if err := e.apply(ctx, validationNetworkPolicy(request.Namespace, jobName)); err != nil {
		return CodeExecution{}, fmt.Errorf("%w: allow only candidate traffic: %v", ErrFailed, err)
	}
	if err := e.apply(ctx, validationCandidateIngress(request.Namespace, jobName)); err != nil {
		return CodeExecution{}, fmt.Errorf("%w: allow candidate ingress from validator: %v", ErrFailed, err)
	}
	job := validationJob(request, jobName, e.config.ImageDigest, target, code, artifact)
	if err := e.apply(ctx, job); err != nil {
		return CodeExecution{}, fmt.Errorf("%w: create EKS validation Job: %v", ErrFailed, err)
	}
	deadline := time.Duration(request.DurationSeconds+30) * time.Second
	if deadline > 150*time.Second {
		deadline = 150 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	uid, err := e.waitForJob(waitCtx, request.Namespace, jobName, artifact)
	if err != nil {
		return CodeExecution{}, err
	}
	podName, err := e.jobPodName(waitCtx, request.Namespace, jobName)
	if err != nil {
		return CodeExecution{}, err
	}
	output, err := e.kubectl(waitCtx, nil, "logs", podName, "-n", request.Namespace, "-c", "validator", "--limit-bytes=8193")
	if err != nil {
		return CodeExecution{}, fmt.Errorf("%w: read EKS validation output: %v", ErrFailed, err)
	}
	if len(output) > 8192 {
		return CodeExecution{}, fmt.Errorf("%w: EKS validation output too large", ErrFailed)
	}
	return CodeExecution{ID: uid, ExitCode: 0, Output: strings.TrimSpace(string(output))}, nil
}

func (e *KubernetesCodeExecutor) kubectl(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "kubectl", append([]string{
		"--kubeconfig", e.config.Kubeconfig, "--context", e.config.Context, "--request-timeout=20s",
	}, args...)...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent",
		"KUBECONFIG=" + e.config.Kubeconfig, "AWS_EC2_METADATA_DISABLED=true"}
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (e *KubernetesCodeExecutor) apply(ctx context.Context, manifest any) error {
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	_, err = e.kubectl(ctx, body, "apply", "--server-side", "--field-manager=knull-validation", "-f", "-")
	return err
}

func (e *KubernetesCodeExecutor) getClusterUID(ctx context.Context) (string, error) {
	body, err := e.kubectl(ctx, nil, "get", "namespace", "kube-system", "-o", "json")
	if err != nil {
		return "", err
	}
	var resource struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &resource) != nil || resource.Metadata.UID == "" {
		return "", fmt.Errorf("EKS cluster UID unavailable")
	}
	return resource.Metadata.UID, nil
}

func (e *KubernetesCodeExecutor) candidatePodIP(ctx context.Context, namespace string) (string, error) {
	for {
		body, err := e.kubectl(ctx, nil, "get", "pods", "-n", namespace, "-l", "app.kubernetes.io/managed-by=knull", "-o", "json")
		if err != nil {
			return "", err
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase             string `json:"phase"`
					PodIP             string `json:"podIP"`
					ContainerStatuses []struct {
						Ready bool `json:"ready"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return "", err
		}
		for _, pod := range list.Items {
			if pod.Status.Phase == "Running" && len(pod.Status.ContainerStatuses) == 1 &&
				pod.Status.ContainerStatuses[0].Ready && net.ParseIP(pod.Status.PodIP) != nil {
				return pod.Status.PodIP, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: candidate pod never became ready", ErrFailed)
		case <-time.After(3 * time.Second):
		}
	}
}

func (e *KubernetesCodeExecutor) waitForJob(ctx context.Context, namespace, jobName, artifact string) (string, error) {
	for {
		body, err := e.kubectl(ctx, nil, "get", "job", jobName, "-n", namespace, "-o", "json")
		if err != nil {
			return "", err
		}
		var job struct {
			Metadata struct {
				UID         string            `json:"uid"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						AutomountServiceAccountToken *bool `json:"automountServiceAccountToken"`
						Containers                   []struct {
							Name  string `json:"name"`
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"status"`
		}
		if err := json.Unmarshal(body, &job); err != nil {
			return "", err
		}
		if job.Metadata.UID == "" || job.Metadata.Annotations["knull.dev/code-digest"] != artifact ||
			job.Spec.Template.Spec.AutomountServiceAccountToken == nil ||
			*job.Spec.Template.Spec.AutomountServiceAccountToken ||
			len(job.Spec.Template.Spec.Containers) != 1 ||
			job.Spec.Template.Spec.Containers[0].Name != "validator" ||
			job.Spec.Template.Spec.Containers[0].Image != e.config.ImageDigest {
			return "", fmt.Errorf("%w: EKS Job provenance or isolation changed", ErrFailed)
		}
		if job.Status.Succeeded == 1 {
			return job.Metadata.UID, nil
		}
		if job.Status.Failed > 0 {
			return "", fmt.Errorf("%w: EKS generated code exited unsuccessfully", ErrFailed)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: EKS generated code timed out", ErrFailed)
		case <-time.After(2 * time.Second):
		}
	}
}

func (e *KubernetesCodeExecutor) jobPodName(ctx context.Context, namespace, jobName string) (string, error) {
	body, err := e.kubectl(ctx, nil, "get", "pods", "-n", namespace, "-l", "job-name="+jobName, "-o", "json")
	if err != nil {
		return "", err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list.Items) != 1 || list.Items[0].Metadata.Name == "" {
		return "", fmt.Errorf("%w: EKS Job pod identity missing", ErrFailed)
	}
	return list.Items[0].Metadata.Name, nil
}

func validationNetworkPolicy(namespace, jobName string) map[string]any {
	jobLabels := map[string]string{"knull.dev/code-job": jobName}
	candidateLabels := map[string]string{"app.kubernetes.io/managed-by": "knull"}
	port := map[string]any{"protocol": "TCP", "port": 8080}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": jobName + "-candidate", "namespace": namespace},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": jobLabels},
			"policyTypes": []string{"Egress"},
			"egress": []any{map[string]any{
				"to":    []any{map[string]any{"podSelector": map[string]any{"matchLabels": candidateLabels}}},
				"ports": []any{port},
			}},
		},
	}
}

func validationCandidateIngress(namespace, jobName string) map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": jobName + "-candidate-ingress", "namespace": namespace},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/managed-by": "knull"}},
			"policyTypes": []string{"Ingress"},
			"ingress": []any{map[string]any{
				"from":  []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{"knull.dev/code-job": jobName}}}},
				"ports": []any{map[string]any{"protocol": "TCP", "port": 8080}},
			}},
		},
	}
}

func validationJob(request ScriptRequest, jobName, image, target, code, artifact string) map[string]any {
	labels := map[string]string{"knull.dev/code-job": jobName}
	environment := []any{
		map[string]string{"name": "TARGET_URL", "value": "http://" + net.JoinHostPort(target, "8080") + "/checkout"},
		map[string]string{"name": "NAMESPACE", "value": request.Namespace},
		map[string]string{"name": "ACTION_DIGEST", "value": request.ActionDigest},
		map[string]string{"name": "MAX_REQUESTS", "value": strconv.Itoa(request.MaxRequests)},
		map[string]string{"name": "DURATION_SECONDS", "value": strconv.Itoa(request.DurationSeconds)},
		map[string]string{"name": "PYTHONDONTWRITEBYTECODE", "value": "1"},
	}
	return map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": jobName, "namespace": request.Namespace,
			"annotations": map[string]string{"knull.dev/code-digest": artifact}},
		"spec": map[string]any{
			"backoffLimit": 0, "activeDeadlineSeconds": request.DurationSeconds + 15,
			"ttlSecondsAfterFinished": 300,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"restartPolicy": "Never", "automountServiceAccountToken": false,
					"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
					"containers": []any{map[string]any{
						"name": "validator", "image": image, "imagePullPolicy": "IfNotPresent",
						"command": []string{"python3", "-c", code}, "env": environment,
						"resources": map[string]any{"requests": map[string]string{"cpu": "50m", "memory": "64Mi"},
							"limits": map[string]string{"cpu": "500m", "memory": "256Mi"}},
						"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true,
							"capabilities": map[string]any{"drop": []string{"ALL"}}},
					}},
				},
			},
		},
	}
}

var _ CodeExecutor = (*KubernetesCodeExecutor)(nil)
var _ io.Reader
