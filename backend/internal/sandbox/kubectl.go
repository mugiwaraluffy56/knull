package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Kubectl uses an explicit sandbox kubeconfig and context for every call. The
// process must run under a sandbox-only identity with no production kubeconfig.
type Kubectl struct {
	Binary     string
	Kubeconfig string
	Context    string
}

type PodStatus struct {
	Desired    int
	Healthy    int
	OOMKills   int
	ObservedAt time.Time
}

type terminatedStatus struct {
	Reason string `json:"reason"`
}

var runNamespace = regexp.MustCompile(`^knull-run-[a-f0-9]{20}$`)

// ReadPodStatus reads only the isolated run namespace through the sandbox
// kubeconfig. It does not infer health from a generated validation script.
func (k Kubectl) ReadPodStatus(ctx context.Context, namespace string) (PodStatus, error) {
	if !runNamespace.MatchString(namespace) {
		return PodStatus{}, fmt.Errorf("%w: invalid run namespace", ErrIsolation)
	}
	deployment, err := k.getObject(ctx, "deployment", "candidate", namespace)
	if err != nil {
		return PodStatus{}, err
	}
	replicas, ok := nested(deployment, "spec", "replicas").(float64)
	if !ok || replicas < 1 || replicas > 6 || replicas != float64(int(replicas)) {
		return PodStatus{}, fmt.Errorf("%w: invalid desired replicas", ErrIsolation)
	}
	data, err := k.command(ctx, nil, "get", "pods", "-n", namespace, "-l", "app.kubernetes.io/managed-by=knull", "-o", "json")
	if err != nil {
		return PodStatus{}, err
	}
	return parsePodStatus(int(replicas), data)
}

func parsePodStatus(desired int, data []byte) (PodStatus, error) {
	var list struct {
		Items []struct {
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready     bool `json:"ready"`
					LastState struct {
						Terminated *terminatedStatus `json:"terminated"`
					} `json:"lastState"`
					State struct {
						Terminated *terminatedStatus `json:"terminated"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return PodStatus{}, err
	}
	result := PodStatus{Desired: desired, ObservedAt: time.Now().UTC()}
	for _, pod := range list.Items {
		if len(pod.Status.ContainerStatuses) != 1 {
			continue
		}
		container := pod.Status.ContainerStatuses[0]
		if pod.Status.Phase == "Running" && container.Ready {
			result.Healthy++
		}
		for _, terminated := range []*terminatedStatus{container.LastState.Terminated, container.State.Terminated} {
			if terminated != nil && terminated.Reason == "OOMKilled" {
				result.OOMKills++
			}
		}
	}
	return result, nil
}

func (k Kubectl) command(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	if k.Kubeconfig == "" || k.Context == "" {
		return nil, fmt.Errorf("%w: explicit sandbox kubeconfig and context required", ErrIsolation)
	}
	binary := k.Binary
	if binary == "" {
		binary = "kubectl"
	}
	cmd := exec.CommandContext(ctx, binary, append([]string{"--kubeconfig", k.Kubeconfig, "--context", k.Context, "--request-timeout=20s"}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "KUBECONFIG=" + k.Kubeconfig, "AWS_EC2_METADATA_DISABLED=true"}
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (k Kubectl) ClusterUID(ctx context.Context) (string, error) {
	data, err := k.command(ctx, nil, "get", "namespace", "kube-system", "-o", "json")
	if err != nil {
		return "", err
	}
	var value struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	if value.Metadata.UID == "" {
		return "", fmt.Errorf("%w: kube-system UID missing", ErrIsolation)
	}
	return value.Metadata.UID, nil
}

func (k Kubectl) Apply(ctx context.Context, manifest any) error {
	data, err := MarshalManifest(manifest)
	if err != nil {
		return err
	}
	_, err = k.command(ctx, data, "apply", "--server-side", "--field-manager=knull-sandbox", "-f", "-")
	return err
}

func (k Kubectl) getObject(ctx context.Context, kind, name, namespace string) (map[string]any, error) {
	args := []string{"get", kind, name}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "-o", "json")
	data, err := k.command(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func nested(value map[string]any, keys ...string) any {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

// VerifyControls reads server state after apply. Admission mutations or
// missing controls fail the run before it can be recorded as prepared.
func (k Kubectl) VerifyControls(ctx context.Context, namespace string, workload Workload) error {
	ns, err := k.getObject(ctx, "namespace", namespace, "")
	if err != nil {
		return err
	}
	if nested(ns, "metadata", "labels", "pod-security.kubernetes.io/enforce") != "restricted" {
		return fmt.Errorf("restricted Pod Security label missing")
	}
	quota, err := k.getObject(ctx, "resourcequota", "knull-quota", namespace)
	if err != nil {
		return err
	}
	if nested(quota, "spec", "hard", "pods") != "8" || nested(quota, "spec", "hard", "persistentvolumeclaims") != "0" {
		return fmt.Errorf("resource quota missing or changed")
	}
	limits, err := k.getObject(ctx, "limitrange", "knull-limits", namespace)
	if err != nil {
		return err
	}
	limitItems, ok := nested(limits, "spec", "limits").([]any)
	if !ok || len(limitItems) == 0 {
		return fmt.Errorf("container limit range missing")
	}
	limit, ok := limitItems[0].(map[string]any)
	if !ok || nested(limit, "max", "cpu") != "2" || nested(limit, "max", "memory") != "4Gi" {
		return fmt.Errorf("container limit range changed")
	}
	policy, err := k.getObject(ctx, "networkpolicy", "default-deny", namespace)
	if err != nil {
		return err
	}
	types, ok := nested(policy, "spec", "policyTypes").([]any)
	if !ok || len(types) != 2 || nested(policy, "spec", "ingress") != nil || nested(policy, "spec", "egress") != nil {
		return fmt.Errorf("default-deny network policy changed")
	}
	seen := map[string]bool{}
	for _, item := range types {
		if name, ok := item.(string); ok {
			seen[name] = true
		}
	}
	if !seen["Ingress"] || !seen["Egress"] {
		return fmt.Errorf("network policy does not deny both directions")
	}
	deployment, err := k.getObject(ctx, "deployment", "candidate", namespace)
	if err != nil {
		return err
	}
	spec := nested(deployment, "spec", "template", "spec")
	pod, ok := spec.(map[string]any)
	if !ok || pod["automountServiceAccountToken"] != false {
		return fmt.Errorf("service account automount not disabled")
	}
	containers, ok := pod["containers"].([]any)
	if !ok || len(containers) != 1 {
		return fmt.Errorf("unexpected container count")
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != workload.Container || container["image"] != workload.ImageDigest || nested(container, "resources", "limits", "cpu") != workload.CPU || nested(container, "resources", "limits", "memory") != workload.Memory || nested(container, "securityContext", "readOnlyRootFilesystem") != true || nested(container, "securityContext", "allowPrivilegeEscalation") != false {
		return fmt.Errorf("sanitized deployment changed")
	}
	return nil
}

func (k Kubectl) DeleteNamespace(ctx context.Context, namespace string) error {
	_, err := k.command(ctx, nil, "delete", "namespace", namespace, "--wait=false")
	if err != nil {
		return err
	}
	return WaitForDeletion(ctx, k, namespace)
}

func (k Kubectl) NamespaceExists(ctx context.Context, namespace string) (bool, error) {
	_, err := k.command(ctx, nil, "get", "namespace", namespace, "-o", "name")
	if err == nil {
		return true, nil
	}
	// Only Kubernetes NotFound proves deletion. Authentication and transport
	// errors remain failures.
	if strings.Contains(err.Error(), "(NotFound)") {
		return false, nil
	}
	return false, err
}

var _ Client = Kubectl{}

// WaitForDeletion can be used by non-kubectl adapters that delete
// asynchronously. It never treats a read error as successful cleanup.
func WaitForDeletion(ctx context.Context, client Client, namespace string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		exists, err := client.NamespaceExists(ctx, namespace)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
