// Package recoverypolicy validates service-specific, versioned recovery
// criteria. Live assessments must use one immutable snapshot and its window.
package recoverypolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid recovery policy")

type MissingData string

const (
	MissingUncertain    MissingData = "UNCERTAIN"
	MissingNotRecovered MissingData = "NOT_RECOVERED"
)

type WorkloadSignal struct {
	MinReadyFraction float64 `json:"minReadyFraction"`
}

type ErrorRateSignal struct {
	MaxRatio   float64 `json:"maxRatio"`
	MinSamples int     `json:"minSamples"`
}

type LatencySignal struct {
	MaxP95Milliseconds float64 `json:"maxP95Milliseconds"`
	MinSamples         int     `json:"minSamples"`
}

// Policy requires workload readiness plus at least one service indicator.
// Missing observations can never count as recovery.
type Policy struct {
	WindowSeconds int              `json:"windowSeconds"`
	MissingData   MissingData      `json:"missingData"`
	Workload      WorkloadSignal   `json:"workload"`
	ErrorRate     *ErrorRateSignal `json:"errorRate,omitempty"`
	LatencyP95    *LatencySignal   `json:"latencyP95,omitempty"`
}

type Snapshot struct {
	ServiceID    uuid.UUID `json:"serviceId"`
	Version      int64     `json:"version"`
	Digest       string    `json:"digest"`
	Policy       Policy    `json:"policy"`
	ConfiguredBy uuid.UUID `json:"configuredBy"`
	ConfiguredAt time.Time `json:"configuredAt"`
}

func (p Policy) Validate() error {
	if p.WindowSeconds < 60 || p.WindowSeconds > 3600 {
		return fmt.Errorf("%w: windowSeconds must be 60–3600", ErrInvalid)
	}
	if p.MissingData != MissingUncertain && p.MissingData != MissingNotRecovered {
		return fmt.Errorf("%w: missingData must be UNCERTAIN or NOT_RECOVERED", ErrInvalid)
	}
	if !finite(p.Workload.MinReadyFraction) || p.Workload.MinReadyFraction <= 0 || p.Workload.MinReadyFraction > 1 {
		return fmt.Errorf("%w: workload.minReadyFraction must be greater than 0 and at most 1", ErrInvalid)
	}
	if p.ErrorRate == nil && p.LatencyP95 == nil {
		return fmt.Errorf("%w: require errorRate or latencyP95 in addition to workload health", ErrInvalid)
	}
	if p.ErrorRate != nil && (!finite(p.ErrorRate.MaxRatio) || p.ErrorRate.MaxRatio < 0 || p.ErrorRate.MaxRatio >= 1 || p.ErrorRate.MinSamples < 10) {
		return fmt.Errorf("%w: errorRate needs maxRatio in [0,1) and minSamples >= 10", ErrInvalid)
	}
	if p.LatencyP95 != nil && (!finite(p.LatencyP95.MaxP95Milliseconds) || p.LatencyP95.MaxP95Milliseconds <= 0 || p.LatencyP95.MinSamples < 10) {
		return fmt.Errorf("%w: latencyP95 needs positive maxP95Milliseconds and minSamples >= 10", ErrInvalid)
	}
	return nil
}

func (p Policy) Digest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	bytes, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// Observation covers the exact policy window. Missing signal pointers mean
// observations were unavailable, never that their thresholds passed.
type Observation struct {
	WindowStart            time.Time `json:"windowStart"`
	WindowEnd              time.Time `json:"windowEnd"`
	ReadyReplicas          *int      `json:"readyReplicas,omitempty"`
	DesiredReplicas        *int      `json:"desiredReplicas,omitempty"`
	ErrorRateRatio         *float64  `json:"errorRateRatio,omitempty"`
	ErrorRateSamples       *int      `json:"errorRateSamples,omitempty"`
	LatencyP95Milliseconds *float64  `json:"latencyP95Milliseconds,omitempty"`
	LatencySamples         *int      `json:"latencySamples,omitempty"`
}

type Outcome string

const (
	Recovered    Outcome = "RECOVERED"
	NotRecovered Outcome = "NOT_RECOVERED"
	Uncertain    Outcome = "UNCERTAIN"
)

type Assessment struct {
	PolicyVersion int64     `json:"policyVersion"`
	PolicyDigest  string    `json:"policyDigest"`
	WindowStart   time.Time `json:"windowStart"`
	WindowEnd     time.Time `json:"windowEnd"`
	Outcome       Outcome   `json:"outcome"`
	Missing       []string  `json:"missing,omitempty"`
	Failed        []string  `json:"failed,omitempty"`
}

// Assess applies deterministic thresholds to live observations. Task 24 can
// persist this result alongside the actual measurements and Jev decision.
func Assess(snapshot Snapshot, obs Observation) Assessment {
	result := Assessment{PolicyVersion: snapshot.Version, PolicyDigest: snapshot.Digest, WindowStart: obs.WindowStart, WindowEnd: obs.WindowEnd}
	if snapshot.Version < 1 || snapshot.Digest == "" || snapshot.Policy.Validate() != nil || obs.WindowStart.IsZero() || !obs.WindowEnd.After(obs.WindowStart) || obs.WindowEnd.Sub(obs.WindowStart) < time.Duration(snapshot.Policy.WindowSeconds)*time.Second {
		result.Outcome = Uncertain
		result.Missing = []string{"valid policy and complete observation window"}
		return result
	}
	if obs.ReadyReplicas == nil || obs.DesiredReplicas == nil || *obs.DesiredReplicas < 1 || *obs.ReadyReplicas < 0 || *obs.ReadyReplicas > *obs.DesiredReplicas {
		result.Missing = append(result.Missing, "workload readiness")
	} else if float64(*obs.ReadyReplicas)/float64(*obs.DesiredReplicas) < snapshot.Policy.Workload.MinReadyFraction {
		result.Failed = append(result.Failed, "workload readiness")
	}
	if signal := snapshot.Policy.ErrorRate; signal != nil {
		if obs.ErrorRateRatio == nil || obs.ErrorRateSamples == nil || !finite(*obs.ErrorRateRatio) || *obs.ErrorRateRatio < 0 || *obs.ErrorRateRatio > 1 || *obs.ErrorRateSamples < signal.MinSamples {
			result.Missing = append(result.Missing, "error rate")
		} else if *obs.ErrorRateRatio > signal.MaxRatio {
			result.Failed = append(result.Failed, "error rate")
		}
	}
	if signal := snapshot.Policy.LatencyP95; signal != nil {
		if obs.LatencyP95Milliseconds == nil || obs.LatencySamples == nil || !finite(*obs.LatencyP95Milliseconds) || *obs.LatencyP95Milliseconds < 0 || *obs.LatencySamples < signal.MinSamples {
			result.Missing = append(result.Missing, "p95 latency")
		} else if *obs.LatencyP95Milliseconds > signal.MaxP95Milliseconds {
			result.Failed = append(result.Failed, "p95 latency")
		}
	}
	if len(result.Missing) > 0 {
		result.Outcome = Uncertain
		if snapshot.Policy.MissingData == MissingNotRecovered {
			result.Outcome = NotRecovered
		}
	} else if len(result.Failed) > 0 {
		result.Outcome = NotRecovered
	} else {
		result.Outcome = Recovered
	}
	return result
}
