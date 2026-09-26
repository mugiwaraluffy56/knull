// Package jev is the side-effect-free decision layer for incident evidence.
// It cannot collect infrastructure data, change incident state, or authorize
// production actions. Every returned reference is checked against the input.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

type Evidence struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Summary string `json:"summary"`
}

type Input struct {
	Evidence       []Evidence      `json:"evidence"`
	Classification *Classification `json:"classification,omitempty"`
	ActionRef      string          `json:"action_ref,omitempty"`
	Action         string          `json:"action,omitempty"`
	Signals        []string        `json:"signals,omitempty"`
}

type Metadata struct {
	Model           string `json:"model"`
	ResponseID      string `json:"response_id"`
	DecisionVersion string `json:"decision_version"`
	Calibrated      bool   `json:"calibrated"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
}

type Hypothesis struct {
	Class       string   `json:"class"`
	Confidence  float64  `json:"confidence"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Classification struct {
	Classes   []Hypothesis `json:"classes"`
	Rationale string       `json:"rationale"`
}

type NextAction struct {
	Action           string   `json:"action"`
	Rationale        string   `json:"rationale"`
	Confidence       float64  `json:"confidence"`
	EvidenceIDs      []string `json:"evidence_ids"`
	RequiredEvidence []string `json:"required_evidence"`
}

type Risk struct {
	Risk        string   `json:"risk"`
	ActionRef   string   `json:"action_ref"`
	RiskFactors []string `json:"risk_factors"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Recovery struct {
	Outcome         string   `json:"outcome"`
	Confidence      float64  `json:"confidence"`
	EvidenceIDs     []string `json:"evidence_ids"`
	ObservedSignals []string `json:"observed_signals"`
}

type ClassificationResult struct {
	Decision Classification `json:"decision"`
	Metadata Metadata       `json:"metadata"`
}
type NextActionResult struct {
	Decision NextAction `json:"decision"`
	Metadata Metadata   `json:"metadata"`
}
type RiskResult struct {
	Decision Risk     `json:"decision"`
	Metadata Metadata `json:"metadata"`
}
type RecoveryResult struct {
	Decision Recovery `json:"decision"`
	Metadata Metadata `json:"metadata"`
}

type ModelResponse struct {
	Text         []byte
	Model        string
	ResponseID   string
	InputTokens  int
	OutputTokens int
}

type Provider interface {
	Decide(ctx context.Context, model, name string, schema map[string]any, input Input) (ModelResponse, error)
}

type Service struct {
	provider Provider
	model    string
}

func NewService(provider Provider, model string) *Service {
	return &Service{provider: provider, model: model}
}

const decisionVersion = "jev-v1"

var (
	ErrInvalidInput  = errors.New("invalid decision input")
	ErrInvalidOutput = errors.New("invalid model output")
)

func (s *Service) Classify(ctx context.Context, in Input) (ClassificationResult, error) {
	var out Classification
	meta, err := s.decide(ctx, "classify_incident", in, &out)
	if err != nil {
		return ClassificationResult{}, err
	}
	if len(out.Classes) == 0 || len(out.Classes) > 6 {
		return ClassificationResult{}, fmt.Errorf("%w: expected 1-6 ranked classes", ErrInvalidOutput)
	}
	for _, h := range out.Classes {
		if !oneOf(h.Class, "RESOURCE_EXHAUSTION", "BAD_DEPLOYMENT", "DEPENDENCY_FAILURE", "TRAFFIC_SPIKE", "CONFIGURATION_ERROR", "UNKNOWN") || !validConfidence(h.Confidence) {
			return ClassificationResult{}, fmt.Errorf("%w: invalid class or confidence", ErrInvalidOutput)
		}
		if err := validateReferences(in, h.EvidenceIDs); err != nil {
			return ClassificationResult{}, err
		}
		if h.Class != "UNKNOWN" && len(h.EvidenceIDs) == 0 {
			return ClassificationResult{}, fmt.Errorf("%w: class lacks supporting evidence", ErrInvalidOutput)
		}
	}
	return ClassificationResult{Decision: out, Metadata: meta}, nil
}

func (s *Service) SelectNextAction(ctx context.Context, in Input) (NextActionResult, error) {
	var out NextAction
	meta, err := s.decide(ctx, "select_next_action", in, &out)
	if err != nil {
		return NextActionResult{}, err
	}
	if !oneOf(out.Action, "INVESTIGATE_MORE", "TEST_REMEDIATION", "REMEDIATE", "ESCALATE") || !validConfidence(out.Confidence) {
		return NextActionResult{}, fmt.Errorf("%w: invalid next action or confidence", ErrInvalidOutput)
	}
	if err := validateReferences(in, out.EvidenceIDs); err != nil {
		return NextActionResult{}, err
	}
	return NextActionResult{Decision: out, Metadata: meta}, nil
}

func (s *Service) ClassifyRisk(ctx context.Context, in Input) (RiskResult, error) {
	if in.ActionRef == "" || strings.TrimSpace(in.Action) == "" {
		return RiskResult{}, fmt.Errorf("%w: action_ref and action required", ErrInvalidInput)
	}
	var out Risk
	meta, err := s.decide(ctx, "classify_remediation_risk", in, &out)
	if err != nil {
		return RiskResult{}, err
	}
	if !oneOf(out.Risk, "LOW", "MEDIUM", "HIGH") || out.ActionRef != in.ActionRef {
		return RiskResult{}, fmt.Errorf("%w: invalid risk or action reference", ErrInvalidOutput)
	}
	if err := validateReferences(in, out.EvidenceIDs); err != nil {
		return RiskResult{}, err
	}
	return RiskResult{Decision: out, Metadata: meta}, nil
}

func (s *Service) VerifyRecovery(ctx context.Context, in Input) (RecoveryResult, error) {
	if len(in.Signals) == 0 {
		return RecoveryResult{}, fmt.Errorf("%w: observed signals required", ErrInvalidInput)
	}
	var out Recovery
	meta, err := s.decide(ctx, "verify_recovery", in, &out)
	if err != nil {
		return RecoveryResult{}, err
	}
	if !oneOf(out.Outcome, "RECOVERED", "PARTIALLY_RECOVERED", "NOT_RECOVERED", "UNCERTAIN") || !validConfidence(out.Confidence) {
		return RecoveryResult{}, fmt.Errorf("%w: invalid recovery outcome or confidence", ErrInvalidOutput)
	}
	if err := validateReferences(in, out.EvidenceIDs); err != nil {
		return RecoveryResult{}, err
	}
	knownSignals := map[string]bool{}
	for _, signal := range in.Signals {
		knownSignals[signal] = true
	}
	for _, signal := range out.ObservedSignals {
		if !knownSignals[signal] {
			return RecoveryResult{}, fmt.Errorf("%w: unknown signal %q", ErrInvalidOutput, signal)
		}
	}
	return RecoveryResult{Decision: out, Metadata: meta}, nil
}

func (s *Service) decide(ctx context.Context, name string, in Input, out any) (Metadata, error) {
	if err := validateInput(in); err != nil {
		return Metadata{}, err
	}
	response, err := s.provider.Decide(ctx, s.model, name, schemaFor(name), in)
	if err != nil {
		return Metadata{}, err
	}
	var schema jsonschema.Schema
	schemaBytes, _ := json.Marshal(schemaFor(name))
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return Metadata{}, fmt.Errorf("%w: local schema: %v", ErrInvalidOutput, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return Metadata{}, fmt.Errorf("%w: local schema: %v", ErrInvalidOutput, err)
	}
	var raw any
	if err := json.Unmarshal(response.Text, &raw); err != nil {
		return Metadata{}, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if err := resolved.Validate(raw); err != nil {
		return Metadata{}, fmt.Errorf("%w: schema: %v", ErrInvalidOutput, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return Metadata{}, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Metadata{}, fmt.Errorf("%w: trailing response content", ErrInvalidOutput)
	}
	return Metadata{
		Model: response.Model, ResponseID: response.ResponseID,
		DecisionVersion: decisionVersion, Calibrated: false,
		InputTokens: response.InputTokens, OutputTokens: response.OutputTokens,
	}, nil
}

func validateInput(in Input) error {
	if len(in.Evidence) == 0 || len(in.Evidence) > 40 {
		return fmt.Errorf("%w: provide 1-40 evidence records", ErrInvalidInput)
	}
	seen := map[string]bool{}
	for _, e := range in.Evidence {
		if e.ID == "" || e.Source == "" || e.Summary == "" || len(e.Summary) > 2000 || seen[e.ID] {
			return fmt.Errorf("%w: evidence needs a unique id, source, and bounded summary", ErrInvalidInput)
		}
		seen[e.ID] = true
	}
	if len(in.Action) > 2000 || len(in.Signals) > 30 {
		return fmt.Errorf("%w: action or signal budget exceeded", ErrInvalidInput)
	}
	if in.Classification != nil {
		for _, h := range in.Classification.Classes {
			if err := validateReferences(in, h.EvidenceIDs); err != nil {
				return fmt.Errorf("%w: classification references unavailable evidence", ErrInvalidInput)
			}
		}
	}
	return nil
}

func validateReferences(in Input, ids []string) error {
	known := map[string]bool{}
	for _, evidence := range in.Evidence {
		known[evidence.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return fmt.Errorf("%w: unknown evidence reference %q", ErrInvalidOutput, id)
		}
	}
	return nil
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func validConfidence(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
