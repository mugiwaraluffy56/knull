package validation

import (
	"context"
	"fmt"

	"github.com/mugiwaraluffy56/knull/backend/internal/sandbox"
)

// KubernetesPodObserver obtains independent workload state from the sandbox
// cluster identity, never from the generated script's self-reported output.
type KubernetesPodObserver struct {
	Reader interface {
		ReadPodStatus(context.Context, string) (sandbox.PodStatus, error)
	}
}

func (o KubernetesPodObserver) ObservePods(ctx context.Context, namespace string) (PodObservation, error) {
	if o.Reader == nil {
		return PodObservation{}, fmt.Errorf("%w: sandbox pod reader unavailable", ErrFailed)
	}
	status, err := o.Reader.ReadPodStatus(ctx, namespace)
	if err != nil {
		return PodObservation{}, err
	}
	return PodObservation{Desired: status.Desired, Healthy: status.Healthy, OOMKills: status.OOMKills, ObservedAt: status.ObservedAt}, nil
}

var _ PodObserver = KubernetesPodObserver{}
