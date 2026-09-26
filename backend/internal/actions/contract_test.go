package actions

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func baseDraft() Draft {
	return Draft{Type: Memory, Environment: "production", Target: Target{Cluster: "prod-east", Namespace: "checkout", Kind: "Deployment", Name: "checkout-api", Container: "api"}, Field: "resources.limits.memory", CurrentValue: "256Mi", DesiredValue: "1Gi", Preconditions: Preconditions{ResourceUID: "71c18cb6-35c9-4624-9dba-43d1773284d7", ResourceVersion: "9123", CurrentValue: "256Mi"}, ExpectedImpact: "rolling restart of 6 pods", Risk: "MEDIUM", EvidenceIDs: []string{"ev-1"}}
}

func TestAllowedActionContracts(t *testing.T) {
	for name, update := range map[string]func(*Draft){
		"memory": func(*Draft) {},
		"cpu": func(d *Draft) {
			d.Type = CPU
			d.Field = "resources.limits.cpu"
			d.CurrentValue = "500m"
			d.DesiredValue = "1000m"
			d.Preconditions.CurrentValue = d.CurrentValue
		},
		"scale": func(d *Draft) {
			d.Type = Scale
			d.Target.Container = ""
			d.Field = "spec.replicas"
			d.CurrentValue = "2"
			d.DesiredValue = "4"
			d.Preconditions.CurrentValue = d.CurrentValue
		},
		"restart": func(d *Draft) {
			d.Type = Restart
			d.Target.Container = ""
			d.Field = "spec.template.metadata.annotations.knull.dev/restartedAt"
			d.CurrentValue = "absent"
			d.DesiredValue = time.Now().UTC().Format(time.RFC3339Nano)
			d.Preconditions.CurrentValue = d.CurrentValue
		},
		"rollback": func(d *Draft) {
			d.Type = Rollback
			d.Field = "imageDigest"
			d.CurrentValue = "sha256:" + strings.Repeat("a", 64)
			d.DesiredValue = "sha256:" + strings.Repeat("b", 64)
			d.Preconditions.CurrentValue = d.CurrentValue
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := baseDraft()
			update(&d)
			contract, err := Seal(d)
			if err != nil {
				t.Fatal(err)
			}
			if contract.Version != Version || contract.Digest == "" || contract.Validate() != nil {
				t.Fatalf("bad contract: %+v", contract)
			}
			summary, err := contract.Summary()
			if err != nil || !strings.Contains(summary, d.CurrentValue) || !strings.Contains(summary, d.DesiredValue) {
				t.Fatalf("bad summary %q: %v", summary, err)
			}
			contract.DesiredValue = "tampered"
			if !errors.Is(contract.Validate(), ErrInvalid) {
				t.Fatal("tampered contract accepted")
			}
		})
	}
}

func TestActionContractRejectsAmbiguity(t *testing.T) {
	for name, update := range map[string]func(*Draft){
		"wildcard target":      func(d *Draft) { d.Target.Name = "*" },
		"other environment":    func(d *Draft) { d.Environment = "*" },
		"unsupported patch":    func(d *Draft) { d.Field = "spec.unknown" },
		"unsupported action":   func(d *Draft) { d.Type = "DELETE_DEPLOYMENT" },
		"missing precondition": func(d *Draft) { d.Preconditions.ResourceVersion = "" },
		"stale current value":  func(d *Draft) { d.Preconditions.CurrentValue = "1Gi" },
		"unbounded memory":     func(d *Draft) { d.DesiredValue = "999999999Gi" },
		"no evidence":          func(d *Draft) { d.EvidenceIDs = nil },
	} {
		t.Run(name, func(t *testing.T) {
			d := baseDraft()
			update(&d)
			if _, err := Seal(d); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unsafe action accepted: %v", err)
			}
		})
	}
}
