// Package actions defines the sealed, versioned contract shared by incident
// planning, sandbox validation, approval, and the production executor.
package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const Version = "action-v1"

var ErrInvalid = errors.New("invalid remediation action")

type Type string

const (
	Restart  Type = "RESTART_WORKLOAD"
	Scale    Type = "SCALE_REPLICAS"
	CPU      Type = "ADJUST_CPU"
	Memory   Type = "ADJUST_MEMORY"
	Rollback Type = "ROLLBACK_DEPLOYMENT"
)

type Target struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
}
type Preconditions struct {
	ResourceUID     string `json:"resourceUid"`
	ResourceVersion string `json:"resourceVersion"`
	CurrentValue    string `json:"currentValue"`
}
type Draft struct {
	Type           Type          `json:"type"`
	Environment    string        `json:"environment"`
	Target         Target        `json:"target"`
	Field          string        `json:"field"`
	CurrentValue   string        `json:"currentValue"`
	DesiredValue   string        `json:"desiredValue"`
	Preconditions  Preconditions `json:"preconditions"`
	ExpectedImpact string        `json:"expectedImpact"`
	Risk           string        `json:"risk"`
	EvidenceIDs    []string      `json:"evidenceIds"`
}
type Contract struct {
	Version string `json:"version"`
	Draft
	Digest string `json:"digest"`
}

var slug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$`)
var imageDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var cpuQuantity = regexp.MustCompile(`^(?:[1-9][0-9]{0,4}m|[1-9][0-9]?)$`)
var memoryQuantity = regexp.MustCompile(`^[1-9][0-9]{0,5}(?:Mi|Gi)$`)

func Seal(d Draft) (Contract, error) {
	if err := validateDraft(d); err != nil {
		return Contract{}, err
	}
	c := Contract{Version: Version, Draft: d}
	c.Digest = digest(c.Version, c.Draft)
	return c, nil
}

func (c Contract) Validate() error {
	if c.Version != Version || c.Digest == "" || c.Digest != digest(c.Version, c.Draft) {
		return fmt.Errorf("%w: contract digest or version mismatch", ErrInvalid)
	}
	return validateDraft(c.Draft)
}

func digest(version string, d Draft) string {
	data, _ := json.Marshal(struct {
		Version string `json:"version"`
		Draft   Draft  `json:"draft"`
	}{version, d})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateDraft(d Draft) error {
	if !slug.MatchString(d.Environment) || !slug.MatchString(d.Target.Cluster) || !slug.MatchString(d.Target.Namespace) || !slug.MatchString(d.Target.Name) || d.Target.Kind != "Deployment" {
		return fmt.Errorf("%w: exact deployment target and environment required", ErrInvalid)
	}
	if _, err := uuid.Parse(d.Preconditions.ResourceUID); err != nil {
		return fmt.Errorf("%w: resource UID must be exact", ErrInvalid)
	}
	if _, err := strconv.ParseUint(d.Preconditions.ResourceVersion, 10, 64); err != nil {
		return fmt.Errorf("%w: resource version must be exact", ErrInvalid)
	}
	if d.Preconditions.CurrentValue != d.CurrentValue {
		return fmt.Errorf("%w: exact resource UID, version, and current value preconditions required", ErrInvalid)
	}
	if d.CurrentValue == "" || d.DesiredValue == "" || d.CurrentValue == d.DesiredValue || len(d.CurrentValue) > 200 || len(d.DesiredValue) > 200 {
		return fmt.Errorf("%w: bounded before/after values required", ErrInvalid)
	}
	if d.ExpectedImpact == "" || len(d.ExpectedImpact) > 500 || !oneOf(d.Risk, "LOW", "MEDIUM", "HIGH") {
		return fmt.Errorf("%w: impact and risk required", ErrInvalid)
	}
	if len(d.EvidenceIDs) == 0 || len(d.EvidenceIDs) > 20 {
		return fmt.Errorf("%w: 1-20 evidence references required", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, id := range d.EvidenceIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("%w: unique evidence references required", ErrInvalid)
		}
		seen[id] = true
	}
	switch d.Type {
	case Restart:
		if d.Field != "spec.template.metadata.annotations.knull.dev/restartedAt" || d.Target.Container != "" {
			return fmt.Errorf("%w: restart field", ErrInvalid)
		}
		if _, err := time.Parse(time.RFC3339Nano, d.DesiredValue); err != nil {
			return fmt.Errorf("%w: restart timestamp", ErrInvalid)
		}
	case Scale:
		if d.Field != "spec.replicas" || d.Target.Container != "" {
			return fmt.Errorf("%w: scale field", ErrInvalid)
		}
		for _, value := range []string{d.CurrentValue, d.DesiredValue} {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 100 {
				return fmt.Errorf("%w: replicas must be 1-100", ErrInvalid)
			}
		}
	case CPU:
		if !slug.MatchString(d.Target.Container) || d.Field != "resources.limits.cpu" || !validCPU(d.CurrentValue) || !validCPU(d.DesiredValue) {
			return fmt.Errorf("%w: CPU field or quantity", ErrInvalid)
		}
	case Memory:
		if !slug.MatchString(d.Target.Container) || d.Field != "resources.limits.memory" || !validMemory(d.CurrentValue) || !validMemory(d.DesiredValue) {
			return fmt.Errorf("%w: memory field or quantity", ErrInvalid)
		}
	case Rollback:
		if !slug.MatchString(d.Target.Container) || d.Field != "imageDigest" || !imageDigest.MatchString(d.CurrentValue) || !imageDigest.MatchString(d.DesiredValue) {
			return fmt.Errorf("%w: exact image digests required", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported action type", ErrInvalid)
	}
	return nil
}

func oneOf(value string, choices ...string) bool {
	for _, c := range choices {
		if c == value {
			return true
		}
	}
	return false
}

func validCPU(value string) bool {
	if !cpuQuantity.MatchString(value) {
		return false
	}
	if strings.HasSuffix(value, "m") {
		n, _ := strconv.Atoi(strings.TrimSuffix(value, "m"))
		return n >= 10 && n <= 64000
	}
	n, _ := strconv.Atoi(value)
	return n <= 64
}

func validMemory(value string) bool {
	if !memoryQuantity.MatchString(value) {
		return false
	}
	if strings.HasSuffix(value, "Gi") {
		n, _ := strconv.Atoi(strings.TrimSuffix(value, "Gi"))
		return n <= 256
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(value, "Mi"))
	return n >= 16 && n <= 262144
}

// Summary uses the sealed contract, so the text an operator reviews is tied to
// the same digest the executor will later verify.
func (c Contract) Summary() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s/%s/%s in %s: %s %q → %q; risk %s; expected impact: %s", c.Type, c.Target.Cluster, c.Target.Namespace, c.Target.Name, c.Environment, c.Field, c.CurrentValue, c.DesiredValue, c.Risk, c.ExpectedImpact), nil
}
