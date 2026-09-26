package recoverypolicy

import (
	"errors"
	"testing"
	"time"
)

func validPolicy() Policy {
	return Policy{WindowSeconds: 300, MissingData: MissingUncertain, Workload: WorkloadSignal{MinReadyFraction: 1}, ErrorRate: &ErrorRateSignal{MaxRatio: .01, MinSamples: 10}, LatencyP95: &LatencySignal{MaxP95Milliseconds: 500, MinSamples: 10}}
}

func TestPolicyRequiresWorkloadAndServiceSignal(t *testing.T) {
	valid := validPolicy()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.ErrorRate = nil
	invalid.LatencyP95 = nil
	if !errors.Is(invalid.Validate(), ErrInvalid) {
		t.Fatal("policy without service signal accepted")
	}
	invalid = valid
	invalid.Workload.MinReadyFraction = 0
	if !errors.Is(invalid.Validate(), ErrInvalid) {
		t.Fatal("policy without workload threshold accepted")
	}
	invalid = valid
	invalid.ErrorRate = &ErrorRateSignal{MaxRatio: 1.1, MinSamples: 10}
	if !errors.Is(invalid.Validate(), ErrInvalid) {
		t.Fatal("invalid error-rate threshold accepted")
	}
	invalid = valid
	invalid.WindowSeconds = 0
	if !errors.Is(invalid.Validate(), ErrInvalid) {
		t.Fatal("missing observation window accepted")
	}
}

func TestMissingOrOutOfThresholdSignalsNeverRecover(t *testing.T) {
	policy := validPolicy()
	digest, err := policy.Digest()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Version: 3, Digest: digest, Policy: policy}
	start := time.Now().Add(-6 * time.Minute)
	end := start.Add(5 * time.Minute)
	ready, desired, samples := 3, 3, 20
	errorRate, latency := .005, 300.0
	obs := Observation{WindowStart: start, WindowEnd: end, ReadyReplicas: &ready, DesiredReplicas: &desired, ErrorRateRatio: &errorRate, ErrorRateSamples: &samples, LatencyP95Milliseconds: &latency, LatencySamples: &samples}
	got := Assess(snapshot, obs)
	if got.Outcome != Recovered || got.PolicyVersion != 3 || got.PolicyDigest != digest || !got.WindowStart.Equal(start) || !got.WindowEnd.Equal(end) {
		t.Fatalf("healthy assessment: %+v", got)
	}
	obs.ErrorRateRatio = nil
	if got = Assess(snapshot, obs); got.Outcome != Uncertain {
		t.Fatalf("missing metric: %+v", got)
	}
	policy.MissingData = MissingNotRecovered
	digest, _ = policy.Digest()
	snapshot.Policy = policy
	snapshot.Digest = digest
	if got = Assess(snapshot, obs); got.Outcome != NotRecovered {
		t.Fatalf("configured missing-data outcome: %+v", got)
	}
	obs.ErrorRateRatio = &errorRate
	ready = 2
	if got = Assess(snapshot, obs); got.Outcome != NotRecovered {
		t.Fatalf("degraded workload: %+v", got)
	}
	ready = 3
	obs.WindowEnd = obs.WindowStart.Add(30 * time.Second)
	if got = Assess(snapshot, obs); got.Outcome != Uncertain {
		t.Fatalf("incomplete window: %+v", got)
	}
}
