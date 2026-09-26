package incidents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when no incident matches the lookup.
	ErrNotFound = errors.New("incident not found")
	// ErrInvalidTransition is returned when a transition is not permitted from
	// the incident's current state.
	ErrInvalidTransition = errors.New("invalid state transition")
	// ErrConflict is returned when the incident changed under a caller that
	// supplied a stale expected version.
	ErrConflict = errors.New("incident was modified concurrently")
)

// Incident is the current-state projection of an incident.
type Incident struct {
	ID                uuid.UUID         `json:"id"`
	ServiceID         *uuid.UUID        `json:"serviceId,omitempty"`
	ServiceKey        string            `json:"serviceKey"`
	Environment       string            `json:"environment"`
	AlertIdentity     string            `json:"alertIdentity"`
	Summary           string            `json:"summary"`
	Symptoms          map[string]string `json:"symptoms"`
	State             State             `json:"state"`
	Version           int64             `json:"version"`
	CorrelationID     string            `json:"correlationId"`
	WorkflowSessionID string            `json:"workflowSessionId,omitempty"`
	WorkflowRunID     string            `json:"workflowRunId,omitempty"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	ClosedAt          *time.Time        `json:"closedAt,omitempty"`
}

// Event is one immutable entry in an incident's history.
type Event struct {
	ID            uuid.UUID      `json:"id"`
	IncidentID    uuid.UUID      `json:"incidentId"`
	Seq           int64          `json:"seq"`
	Type          EventType      `json:"type"`
	FromState     State          `json:"fromState,omitempty"`
	ToState       State          `json:"toState,omitempty"`
	Actor         string         `json:"actor"`
	Reason        string         `json:"reason"`
	CorrelationID string         `json:"correlationId"`
	Data          map[string]any `json:"data"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// NewIncident carries the fields needed to open an incident.
type NewIncident struct {
	ServiceID     *uuid.UUID
	ServiceKey    string
	Environment   string
	AlertIdentity string
	Summary       string
	Symptoms      map[string]string
	CorrelationID string
	Actor         string
}

// Transition carries a requested state change and its audit metadata.
type Transition struct {
	To     State
	Actor  string
	Reason string
	// ExpectedVersion, when > 0, requires the incident to still be at that
	// version; otherwise ErrConflict is returned. Zero disables the check.
	ExpectedVersion int64
	Data            map[string]any
}

// Store persists incidents and their events in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds an incident store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create opens a new incident in the RECEIVED state and records the opening
// event atomically.
func (s *Store) Create(ctx context.Context, in NewIncident) (Incident, error) {
	if in.ServiceKey == "" || in.Environment == "" {
		return Incident{}, errors.New("service key and environment are required")
	}
	if in.Symptoms == nil {
		in.Symptoms = map[string]string{}
	}
	if in.CorrelationID == "" {
		in.CorrelationID = uuid.NewString()
	}

	var inc Incident
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO incidents (service_id, service_key, environment,
				alert_identity, summary, symptoms, state, version, correlation_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8)
			RETURNING `+incidentColumns,
			in.ServiceID, in.ServiceKey, in.Environment, in.AlertIdentity,
			in.Summary, in.Symptoms, string(StateReceived), in.CorrelationID)
		if err := row.Scan(incidentTargets(&inc)...); err != nil {
			return fmt.Errorf("insert incident: %w", err)
		}
		return appendEvent(ctx, tx, inc.ID, 1, Event{
			Type:          EventStateChange,
			ToState:       StateReceived,
			Actor:         in.Actor,
			Reason:        "incident opened",
			CorrelationID: in.CorrelationID,
		})
	})
	if err != nil {
		return Incident{}, err
	}
	return inc, nil
}

// Transition validates and applies a state change, appending exactly one event.
// It locks the incident row for the duration so concurrent transitions are
// serialized: the loser sees the updated state and is rejected rather than
// silently overwriting an approval or execution transition.
func (s *Store) Transition(ctx context.Context, id uuid.UUID, t Transition) (Incident, error) {
	if !t.To.Valid() {
		return Incident{}, fmt.Errorf("%w: unknown target state %q", ErrInvalidTransition, t.To)
	}

	var inc Incident
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			current State
			version int64
		)
		err := tx.QueryRow(ctx,
			`SELECT state, version FROM incidents WHERE id = $1 FOR UPDATE`, id).
			Scan(&current, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock incident: %w", err)
		}

		if t.ExpectedVersion > 0 && t.ExpectedVersion != version {
			return ErrConflict
		}
		if !current.CanTransition(t.To) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, t.To)
		}

		var seq int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(seq),0)+1 FROM incident_events WHERE incident_id = $1`, id).
			Scan(&seq); err != nil {
			return fmt.Errorf("next seq: %w", err)
		}

		closedClause := ""
		if t.To.IsTerminal() {
			closedClause = ", closed_at = now()"
		}
		if err := tx.QueryRow(ctx, `
			UPDATE incidents
			SET state = $2, version = version + 1, updated_at = now()`+closedClause+`
			WHERE id = $1
			RETURNING `+incidentColumns, id, string(t.To)).
			Scan(incidentTargets(&inc)...); err != nil {
			return fmt.Errorf("update incident: %w", err)
		}

		return appendEvent(ctx, tx, id, seq, Event{
			Type:          EventStateChange,
			FromState:     current,
			ToState:       t.To,
			Actor:         t.Actor,
			Reason:        t.Reason,
			CorrelationID: inc.CorrelationID,
			Data:          t.Data,
		})
	})
	if err != nil {
		return Incident{}, err
	}
	return inc, nil
}

// FindActiveByAlert returns the active (non-closed) incident matching a
// service+environment+alert identity, used to deduplicate repeated alert
// deliveries. ErrNotFound means no active incident exists for that alert.
func (s *Store) FindActiveByAlert(ctx context.Context, serviceKey, environment, alertIdentity string) (Incident, error) {
	var inc Incident
	err := s.pool.QueryRow(ctx, `SELECT `+incidentColumns+`
		FROM incidents
		WHERE service_key = $1 AND environment = $2 AND alert_identity = $3
		  AND state <> $4
		ORDER BY created_at DESC
		LIMIT 1`,
		serviceKey, environment, alertIdentity, string(StateClosed)).
		Scan(incidentTargets(&inc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("find active incident: %w", err)
	}
	return inc, nil
}

// AppendNote appends a non-transition audit event to an incident's history
// without changing its state. Used to attach repeat/resolved alert
// notifications to an existing incident.
func (s *Store) AppendNote(ctx context.Context, id uuid.UUID, actor, reason string, data map[string]any) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incidents WHERE id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("check incident: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
		var seq int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(seq),0)+1 FROM incident_events WHERE incident_id = $1`, id).Scan(&seq); err != nil {
			return fmt.Errorf("next seq: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE incidents SET updated_at = now() WHERE id = $1`, id); err != nil {
			return fmt.Errorf("touch incident: %w", err)
		}
		return appendEvent(ctx, tx, id, seq, Event{
			Type:   EventNote,
			Actor:  actor,
			Reason: reason,
			Data:   data,
		})
	})
}

// Get returns an incident by id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Incident, error) {
	var inc Incident
	err := s.pool.QueryRow(ctx, `SELECT `+incidentColumns+` FROM incidents WHERE id = $1`, id).
		Scan(incidentTargets(&inc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("get incident: %w", err)
	}
	return inc, nil
}

// List returns incidents, most recently updated first.
func (s *Store) List(ctx context.Context) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+incidentColumns+` FROM incidents ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	var out []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(incidentTargets(&inc)...); err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// ListActive returns all incidents that are not closed, most recently updated
// first. Used to compute fleet health.
func (s *Store) ListActive(ctx context.Context) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+incidentColumns+`
		FROM incidents WHERE state <> $1 ORDER BY updated_at DESC`, string(StateClosed))
	if err != nil {
		return nil, fmt.Errorf("list active incidents: %w", err)
	}
	defer rows.Close()

	var out []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(incidentTargets(&inc)...); err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// Events returns an incident's full history in order.
func (s *Store) Events(ctx context.Context, id uuid.UUID) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, incident_id, seq, type, from_state, to_state, actor, reason, correlation_id, data, created_at
		FROM incident_events WHERE incident_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.IncidentID, &e.Seq, &e.Type, &e.FromState,
			&e.ToState, &e.Actor, &e.Reason, &e.CorrelationID, &e.Data, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const incidentColumns = `id, service_id, service_key, environment, alert_identity,
	summary, symptoms, state, version, correlation_id,
	workflow_session_id, workflow_run_id, created_at, updated_at, closed_at`

func incidentTargets(inc *Incident) []any {
	return []any{
		&inc.ID, &inc.ServiceID, &inc.ServiceKey, &inc.Environment, &inc.AlertIdentity,
		&inc.Summary, &inc.Symptoms, &inc.State, &inc.Version, &inc.CorrelationID,
		&inc.WorkflowSessionID, &inc.WorkflowRunID, &inc.CreatedAt, &inc.UpdatedAt, &inc.ClosedAt,
	}
}

// SaveWorkflow records the durable workflow session/run identifiers on an
// incident so the investigation can be resumed after a restart.
func (s *Store) SaveWorkflow(ctx context.Context, id uuid.UUID, sessionID, runID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE incidents SET workflow_session_id = $2, workflow_run_id = $3, updated_at = now() WHERE id = $1`,
		id, sessionID, runID)
	if err != nil {
		return fmt.Errorf("save workflow ids: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListResumable returns incidents that have a workflow session and are not in a
// terminal or already-resolved state, so their streams can be re-attached on
// startup.
func (s *Store) ListResumable(ctx context.Context) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+incidentColumns+`
		FROM incidents
		WHERE workflow_session_id <> '' AND state NOT IN ($1,$2,$3,$4,$5)
		ORDER BY updated_at DESC`,
		string(StateClosed), string(StateRecovered), string(StateEscalated),
		string(StateDenied), string(StateFailed))
	if err != nil {
		return nil, fmt.Errorf("list resumable: %w", err)
	}
	defer rows.Close()

	var out []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(incidentTargets(&inc)...); err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// appendEvent inserts one immutable history row. The UNIQUE(incident_id, seq)
// constraint makes a duplicate append fail rather than corrupt the history.
func appendEvent(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID, seq int64, e Event) error {
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO incident_events (incident_id, seq, type, from_state, to_state, actor, reason, correlation_id, data)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		incidentID, seq, string(e.Type), string(e.FromState), string(e.ToState),
		e.Actor, e.Reason, e.CorrelationID, e.Data)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}
