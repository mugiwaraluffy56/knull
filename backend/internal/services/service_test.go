package services

import (
	"errors"
	"testing"
)

func validInput() Input {
	return Input{
		Key:          "checkout-api",
		DisplayName:  "Checkout API",
		Environment:  "production",
		K8sCluster:   "prod-eks",
		K8sNamespace: "shop",
		K8sWorkload:  "checkout-api",
		GitHubRepo:   "acme/checkout",
		GitHubRef:    "main",
	}
}

func fieldErrors(t *testing.T, in Input) map[string]string {
	t.Helper()
	_, err := normalizeAndValidate(in)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	return ve.Fields
}

func TestValidateAcceptsGoodInput(t *testing.T) {
	out, err := normalizeAndValidate(validInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.PrometheusLabels == nil {
		t.Fatal("prometheus labels should default to empty map")
	}
}

func TestValidateRequiresExplicitEnvironment(t *testing.T) {
	in := validInput()
	in.Environment = ""
	if _, ok := fieldErrors(t, in)["environment"]; !ok {
		t.Fatal("empty environment must be rejected")
	}

	in2 := validInput()
	in2.Environment = "Production Cluster!" // spaces + punctuation
	if _, ok := fieldErrors(t, in2)["environment"]; !ok {
		t.Fatal("malformed environment must be rejected")
	}
}

func TestValidateRequiresK8sTarget(t *testing.T) {
	in := validInput()
	in.K8sNamespace = ""
	in.K8sWorkload = ""
	in.K8sCluster = ""
	fields := fieldErrors(t, in)
	for _, f := range []string{"k8sCluster", "k8sNamespace", "k8sWorkload"} {
		if _, ok := fields[f]; !ok {
			t.Errorf("missing %s must be rejected", f)
		}
	}
}

func TestValidateRejectsBadRepo(t *testing.T) {
	in := validInput()
	in.GitHubRepo = "not-a-repo"
	if _, ok := fieldErrors(t, in)["githubRepo"]; !ok {
		t.Fatal("bad repo must be rejected")
	}
}

func TestValidateAllowsEmptyOptionalRepo(t *testing.T) {
	in := validInput()
	in.GitHubRepo = ""
	in.GitHubRef = ""
	if _, err := normalizeAndValidate(in); err != nil {
		t.Fatalf("empty optional repo should be allowed: %v", err)
	}
}

func TestValidateRejectsEmptyPrometheusLabel(t *testing.T) {
	in := validInput()
	in.PrometheusLabels = map[string]string{"app": ""}
	if _, ok := fieldErrors(t, in)["prometheusLabels"]; !ok {
		t.Fatal("empty label value must be rejected")
	}
}
