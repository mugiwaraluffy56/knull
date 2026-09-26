package alerts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

// IncidentStore is the incident behavior alert intake needs.
type IncidentStore interface {
	Create(ctx context.Context, in incidents.NewIncident) (incidents.Incident, error)
	FindActiveByAlert(ctx context.Context, serviceKey, environment, alertIdentity string) (incidents.Incident, error)
	AppendNote(ctx context.Context, id uuid.UUID, actor, reason string, data map[string]any) error
}

// ServiceResolver resolves a configured service id from key+environment.
type ServiceResolver interface {
	ResolveID(ctx context.Context, key, environment string) (uuid.UUID, bool)
}

// FailureRecorder records rejected or unroutable deliveries for visibility.
type FailureRecorder interface {
	Record(ctx context.Context, reason, remoteAddr, detail string) error
}

// Disposition is the outcome of processing a single alert.
type Disposition string

const (
	DispositionCreated  Disposition = "created"  // a new incident was opened
	DispositionDeduped  Disposition = "deduped"  // attached to an existing incident
	DispositionResolved Disposition = "resolved" // resolved note attached
	DispositionIgnored  Disposition = "ignored"  // resolved with no active incident
	DispositionRejected Disposition = "rejected" // unroutable; recorded as a failure
)

// Outcome reports what happened to one alert.
type Outcome struct {
	AlertIdentity string      `json:"alertIdentity"`
	Disposition   Disposition `json:"disposition"`
	IncidentID    *uuid.UUID  `json:"incidentId,omitempty"`
}

// Intake turns Alertmanager payloads into incidents with deduplication.
type Intake struct {
	incidents IncidentStore
	services  ServiceResolver
	failures  FailureRecorder
	mapping   LabelMapping
}

// NewIntake builds an Intake. services and failures may be nil.
func NewIntake(inc IncidentStore, svc ServiceResolver, fail FailureRecorder, mapping LabelMapping) *Intake {
	if mapping.ServiceLabel == "" {
		mapping = DefaultLabelMapping
	}
	return &Intake{incidents: inc, services: svc, failures: fail, mapping: mapping}
}

// Process handles every alert in a payload, deduplicating repeats and resolved
// notifications onto the matching active incident. It returns per-alert outcomes.
func (i *Intake) Process(ctx context.Context, p AlertmanagerPayload, remoteAddr string) ([]Outcome, error) {
	if len(p.Alerts) == 0 {
		i.recordFailure(ctx, "empty payload", remoteAddr, "no alerts in delivery")
		return nil, errors.New("payload contains no alerts")
	}

	outcomes := make([]Outcome, 0, len(p.Alerts))
	for _, a := range p.Alerts {
		na, ok := Normalize(p, a, i.mapping)
		if !ok {
			i.recordFailure(ctx, "unroutable alert", remoteAddr,
				"alert missing service or environment label")
			outcomes = append(outcomes, Outcome{Disposition: DispositionRejected})
			continue
		}
		outcome, err := i.processOne(ctx, na)
		if err != nil {
			return outcomes, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

func (i *Intake) processOne(ctx context.Context, na NormalizedAlert) (Outcome, error) {
	existing, err := i.incidents.FindActiveByAlert(ctx, na.ServiceKey, na.Environment, na.AlertIdentity)
	switch {
	case err == nil:
		// An active incident already exists for this alert: attach the
		// notification rather than starting a duplicate investigation.
		reason := "repeat alert notification"
		disp := DispositionDeduped
		if na.Resolved {
			reason = "alert resolved notification"
			disp = DispositionResolved
		}
		if err := i.incidents.AppendNote(ctx, existing.ID, "alertmanager", reason, map[string]any{
			"alertIdentity": na.AlertIdentity,
			"resolved":      na.Resolved,
			"eventTime":     na.EventTime,
		}); err != nil {
			return Outcome{}, fmt.Errorf("append notification: %w", err)
		}
		id := existing.ID
		return Outcome{AlertIdentity: na.AlertIdentity, Disposition: disp, IncidentID: &id}, nil

	case errors.Is(err, incidents.ErrNotFound):
		if na.Resolved {
			// Nothing active to resolve.
			return Outcome{AlertIdentity: na.AlertIdentity, Disposition: DispositionIgnored}, nil
		}
		return i.createIncident(ctx, na)

	default:
		return Outcome{}, fmt.Errorf("dedup lookup: %w", err)
	}
}

func (i *Intake) createIncident(ctx context.Context, na NormalizedAlert) (Outcome, error) {
	var serviceID *uuid.UUID
	if i.services != nil {
		if id, ok := i.services.ResolveID(ctx, na.ServiceKey, na.Environment); ok {
			serviceID = &id
		}
	}
	inc, err := i.incidents.Create(ctx, incidents.NewIncident{
		ServiceID:     serviceID,
		ServiceKey:    na.ServiceKey,
		Environment:   na.Environment,
		AlertIdentity: na.AlertIdentity,
		Summary:       na.Summary,
		Symptoms:      na.Symptoms,
		Actor:         "alertmanager",
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("create incident: %w", err)
	}
	return Outcome{AlertIdentity: na.AlertIdentity, Disposition: DispositionCreated, IncidentID: &inc.ID}, nil
}

func (i *Intake) recordFailure(ctx context.Context, reason, remoteAddr, detail string) {
	if i.failures == nil {
		return
	}
	_ = i.failures.Record(ctx, reason, remoteAddr, detail)
}
