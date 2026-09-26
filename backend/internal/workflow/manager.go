package workflow

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

// IncidentOps is the incident behavior the manager needs.
type IncidentOps interface {
	Get(ctx context.Context, id uuid.UUID) (incidents.Incident, error)
	SaveWorkflow(ctx context.Context, id uuid.UUID, sessionID, runID string) error
	Transition(ctx context.Context, id uuid.UUID, t incidents.Transition) (incidents.Incident, error)
	AppendNote(ctx context.Context, id uuid.UUID, actor, reason string, data map[string]any) error
	ListResumable(ctx context.Context) ([]incidents.Incident, error)
}

// Manager starts durable workflows for incidents and maps their streamed events
// onto the incident lifecycle and audit history.
type Manager struct {
	runtime Runtime
	store   IncidentOps
	logger  *slog.Logger
}

// NewManager builds a Manager.
func NewManager(rt Runtime, store IncidentOps, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{runtime: rt, store: store, logger: logger}
}

// StartForIncident begins a durable workflow for an incident, persists its
// session identifiers, moves it into INVESTIGATING, and starts consuming its
// event stream in the background. A runtime failure is surfaced by escalating
// the incident rather than leaving it silently stuck.
func (m *Manager) StartForIncident(ctx context.Context, inc incidents.Incident) error {
	session, err := m.runtime.StartSession(ctx, StartRequest{
		IncidentID:  inc.ID.String(),
		ServiceKey:  inc.ServiceKey,
		Environment: inc.Environment,
		Summary:     inc.Summary,
		Symptoms:    inc.Symptoms,
	})
	if err != nil {
		m.logger.Error("start workflow session", "incident", inc.ID, "error", err)
		m.failIncident(ctx, inc.ID, "workflow runtime unavailable")
		return err
	}
	if err := m.store.SaveWorkflow(ctx, inc.ID, session.ID, session.RunID); err != nil {
		m.logger.Error("save workflow ids", "incident", inc.ID, "error", err)
		return err
	}
	m.tryTransition(ctx, inc.ID, incidents.StateInvestigating, "investigation started")

	go m.consume(context.Background(), inc.ID, session.ID)
	return nil
}

// ResumeAll re-attaches to every resumable incident's workflow stream, e.g. on
// backend startup. Re-attaching is idempotent: already-applied transitions are
// rejected harmlessly and no production action is repeated (those are gated by
// human approval elsewhere).
func (m *Manager) ResumeAll(ctx context.Context) error {
	resumable, err := m.store.ListResumable(ctx)
	if err != nil {
		return err
	}
	for _, inc := range resumable {
		m.logger.Info("resuming workflow", "incident", inc.ID, "session", inc.WorkflowSessionID)
		go m.consume(context.Background(), inc.ID, inc.WorkflowSessionID)
	}
	return nil
}

// consume streams a session's events and applies each, with bounded retries for
// transient stream drops. An unavailable session escalates the incident.
func (m *Manager) consume(ctx context.Context, incidentID uuid.UUID, sessionID string) {
	const maxRetries = 5
	backoff := 500 * time.Millisecond

	for attempt := 0; ; attempt++ {
		ch, err := m.runtime.StreamEvents(ctx, sessionID)
		if err != nil {
			if errors.Is(err, ErrSessionUnavailable) {
				m.failIncident(ctx, incidentID, "workflow session unavailable")
				return
			}
			if attempt >= maxRetries {
				m.failIncident(ctx, incidentID, "workflow stream failed")
				return
			}
			select {
			case <-time.After(backoff):
				backoff *= 2
				continue
			case <-ctx.Done():
				return
			}
		}
		m.drain(ctx, incidentID, ch)
		return
	}
}

// drain applies every event from a channel. Exposed for testing.
func (m *Manager) drain(ctx context.Context, incidentID uuid.UUID, ch <-chan RuntimeEvent) {
	for ev := range ch {
		if err := m.applyEvent(ctx, incidentID, ev); err != nil {
			m.logger.Error("apply workflow event", "incident", incidentID, "error", err)
		}
	}
}

// applyEvent maps one runtime event onto the incident model.
func (m *Manager) applyEvent(ctx context.Context, incidentID uuid.UUID, ev RuntimeEvent) error {
	switch ev.Kind {
	case KindProgress:
		return m.store.AppendNote(ctx, incidentID, "trueforge", summaryOr(ev, "investigation step"), ev.Data)

	case KindTransition:
		target := incidents.State(ev.State)
		if !target.Valid() {
			return m.store.AppendNote(ctx, incidentID, "trueforge", "ignored unknown target state: "+ev.State, ev.Data)
		}
		inc, err := m.store.Get(ctx, incidentID)
		if err != nil {
			return err
		}
		if !inc.State.CanTransition(target) {
			// Not valid from the current state (e.g. a duplicate on resume);
			// record it rather than forcing an illegal transition.
			return m.store.AppendNote(ctx, incidentID, "trueforge",
				"skipped transition "+string(inc.State)+" -> "+ev.State, ev.Data)
		}
		_, err = m.store.Transition(ctx, incidentID, incidents.Transition{
			To:     target,
			Actor:  "trueforge",
			Reason: summaryOr(ev, "workflow transition"),
			Data:   ev.Data,
		})
		return err

	case KindError:
		m.failIncident(ctx, incidentID, summaryOr(ev, "workflow error"))
		return nil

	case KindDone:
		return m.store.AppendNote(ctx, incidentID, "trueforge", summaryOr(ev, "workflow complete"), ev.Data)

	default:
		return m.store.AppendNote(ctx, incidentID, "trueforge", "unknown event", ev.Data)
	}
}

// failIncident moves an incident to ESCALATED (or FAILED if escalation is not
// legal from its current state) with a reason, so a runtime problem is visible.
func (m *Manager) failIncident(ctx context.Context, incidentID uuid.UUID, reason string) {
	inc, err := m.store.Get(ctx, incidentID)
	if err != nil {
		m.logger.Error("failIncident get", "incident", incidentID, "error", err)
		return
	}
	target := incidents.StateEscalated
	if !inc.State.CanTransition(target) {
		if inc.State.CanTransition(incidents.StateFailed) {
			target = incidents.StateFailed
		} else {
			_ = m.store.AppendNote(ctx, incidentID, "trueforge", "workflow problem: "+reason, nil)
			return
		}
	}
	if _, err := m.store.Transition(ctx, incidentID, incidents.Transition{
		To: target, Actor: "trueforge", Reason: reason,
	}); err != nil {
		m.logger.Error("failIncident transition", "incident", incidentID, "error", err)
	}
}

func (m *Manager) tryTransition(ctx context.Context, incidentID uuid.UUID, to incidents.State, reason string) {
	inc, err := m.store.Get(ctx, incidentID)
	if err != nil {
		return
	}
	if !inc.State.CanTransition(to) {
		return
	}
	if _, err := m.store.Transition(ctx, incidentID, incidents.Transition{To: to, Actor: "trueforge", Reason: reason}); err != nil {
		m.logger.Error("transition", "incident", incidentID, "error", err)
	}
}

func summaryOr(ev RuntimeEvent, fallback string) string {
	if ev.Summary != "" {
		return ev.Summary
	}
	return fallback
}
