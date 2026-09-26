// Package approval records human decisions about exact, validated actions and
// checks the approval and live target again at the production execution seam.
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
)

const Lifetime = 15 * time.Minute

var (
	ErrInvalid    = errors.New("approval request is invalid")
	ErrConflict   = errors.New("action already has a decision")
	ErrIneligible = errors.New("action is not eligible for approval")
	ErrExpired    = errors.New("approval has expired")
	ErrStale      = errors.New("production target changed")
)

type Decision string

const (
	Approved Decision = "APPROVED"
	Denied   Decision = "DENIED"
)

type Record struct {
	ID            uuid.UUID  `json:"id"`
	IncidentID    uuid.UUID  `json:"incidentId"`
	ActionEventID uuid.UUID  `json:"actionEventId"`
	ActionDigest  string     `json:"actionDigest"`
	Decision      Decision   `json:"decision"`
	OperatorID    uuid.UUID  `json:"operatorId"`
	OperatorEmail string     `json:"operatorEmail"`
	Reason        string     `json:"reason,omitempty"`
	DecidedAt     time.Time  `json:"decidedAt"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	InvalidatedAt *time.Time `json:"invalidatedAt,omitempty"`
}

// TargetReader uses production read credentials only. A caller must use the
// same exact UID, resource version, and value as write preconditions when it
// later mutates Kubernetes, since a separate read cannot prevent a race.
type TargetReader interface {
	ReadPreconditions(context.Context, actions.Target, string) (actions.Preconditions, error)
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Decide records a single operator decision and its audit event atomically.
// A successful approval keeps the incident paused at AWAITING_APPROVAL.
func (s *Service) Decide(ctx context.Context, incidentID, actionEventID uuid.UUID, digest string, operator operators.Operator, decision Decision, reason string) (Record, error) {
	if s == nil || s.pool == nil || incidentID == uuid.Nil || actionEventID == uuid.Nil || operator.ID == uuid.Nil || operator.Email == "" || (decision != Approved && decision != Denied) || len(reason) > 500 || strings.TrimSpace(digest) == "" {
		return Record{}, ErrInvalid
	}
	var record Record
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		state, version, correlationID, err := lockIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if state != incidents.StateAwaitingApproval {
			return ErrIneligible
		}
		action, err := eligibleAction(ctx, tx, incidentID, actionEventID)
		if err != nil {
			return err
		}
		if action.Digest != digest {
			return ErrIneligible
		}
		now := time.Now().UTC()
		var expires *time.Time
		if decision == Approved {
			end := now.Add(Lifetime)
			expires = &end
		}
		record = Record{ID: uuid.New(), IncidentID: incidentID, ActionEventID: actionEventID, ActionDigest: digest, Decision: decision, OperatorID: operator.ID, OperatorEmail: operator.Email, Reason: reason, DecidedAt: now, ExpiresAt: expires}
		_, err = tx.Exec(ctx, `INSERT INTO action_approvals (id, incident_id, action_event_id, action_digest, decision, operator_id, operator_email, reason, decided_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, record.ID, incidentID, actionEventID, digest, string(decision), operator.ID, operator.Email, reason, now, expires)
		if err != nil {
			// Unique action_event_id prevents a second decision from replacing the
			// first, including under concurrent approval/denial requests.
			var dbErr *pgconn.PgError
			if errors.As(err, &dbErr) && dbErr.Code == "23505" {
				return ErrConflict
			}
			return fmt.Errorf("insert decision: %w", err)
		}
		if decision == Denied {
			result, err := tx.Exec(ctx, `UPDATE incidents SET state=$2, version=version+1, updated_at=$3 WHERE id=$1 AND version=$4`, incidentID, string(incidents.StateDenied), now, version)
			if err != nil {
				return err
			}
			if result.RowsAffected() != 1 {
				return incidents.ErrConflict
			}
		} else {
			_, err = tx.Exec(ctx, `UPDATE incidents SET updated_at=$2 WHERE id=$1`, incidentID, now)
			if err != nil {
				return err
			}
		}
		data, err := json.Marshal(map[string]any{"decisionId": record.ID, "actionEventId": actionEventID, "actionDigest": digest, "decision": decision, "operatorId": operator.ID, "expiresAt": expires})
		if err != nil {
			return err
		}
		var seq int64
		if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM incident_events WHERE incident_id=$1`, incidentID).Scan(&seq); err != nil {
			return err
		}
		eventType := incidents.EventNote
		if decision == Denied {
			eventType = incidents.EventStateChange
		}
		_, err = tx.Exec(ctx, `INSERT INTO incident_events (id,incident_id,seq,type,category,source,target,from_state,to_state,actor,reason,correlation_id,data,observed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, uuid.New(), incidentID, seq, string(eventType), string(incidents.CategoryDecision), "approval", action.Target.Cluster+"/"+action.Target.Namespace+"/"+action.Target.Name, stateForAudit(state, decision), toStateForAudit(decision), operator.Email, reason, correlationID, data, now)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func stateForAudit(state incidents.State, decision Decision) string {
	if decision == Denied {
		return string(state)
	}
	return ""
}
func toStateForAudit(decision Decision) string {
	if decision == Denied {
		return string(incidents.StateDenied)
	}
	return ""
}

// Check is the server-side gate to call immediately before a production write.
// It returns the sealed action only after an unexpired approval, matching
// validation, current service mapping, and a live production read all agree.
func (s *Service) Check(ctx context.Context, incidentID, actionEventID uuid.UUID, digest string, reader TargetReader) (actions.Contract, error) {
	if s == nil || s.pool == nil || reader == nil || incidentID == uuid.Nil || actionEventID == uuid.Nil || digest == "" {
		return actions.Contract{}, ErrIneligible
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return actions.Contract{}, err
	}
	defer tx.Rollback(ctx)
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM incidents WHERE id=$1`, incidentID).Scan(&state); err != nil {
		return actions.Contract{}, err
	}
	if incidents.State(state) != incidents.StateAwaitingApproval {
		return actions.Contract{}, ErrIneligible
	}
	action, err := eligibleAction(ctx, tx, incidentID, actionEventID)
	if err != nil {
		if errors.Is(err, ErrIneligible) {
			_ = tx.Rollback(ctx)
			// A changed action or service mapping makes the old approval unusable.
			// An undecided action has no row to invalidate and stays pending.
			_ = s.invalidate(ctx, incidentID, actionEventID, digest, "action or service mapping changed; new validation required")
		}
		return actions.Contract{}, err
	}
	if action.Digest != digest {
		return actions.Contract{}, ErrIneligible
	}
	if err := checkApproval(ctx, tx, incidentID, actionEventID, action.Digest); err != nil {
		if errors.Is(err, ErrExpired) {
			_ = tx.Rollback(ctx)
			if invalidateErr := s.invalidate(ctx, incidentID, actionEventID, digest, "approval expired; new planning and validation required"); invalidateErr != nil {
				return actions.Contract{}, invalidateErr
			}
		}
		return actions.Contract{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return actions.Contract{}, err
	}
	live, err := reader.ReadPreconditions(ctx, action.Target, action.Field)
	if err != nil {
		return actions.Contract{}, err
	}
	if live != action.Preconditions {
		if err := s.invalidate(ctx, incidentID, actionEventID, digest, "production target no longer matches the reviewed precondition"); err != nil {
			return actions.Contract{}, err
		}
		return actions.Contract{}, ErrStale
	}
	return action, nil
}

func (s *Service) invalidate(ctx context.Context, incidentID, actionEventID uuid.UUID, digest, reason string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		state, version, correlationID, err := lockIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if state != incidents.StateAwaitingApproval {
			return ErrIneligible
		}
		now := time.Now().UTC()
		result, err := tx.Exec(ctx, `UPDATE action_approvals SET invalidated_at=$4,invalidation_reason=$5 WHERE incident_id=$1 AND action_event_id=$2 AND action_digest=$3 AND decision='APPROVED' AND invalidated_at IS NULL`, incidentID, actionEventID, digest, now, reason)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return ErrIneligible
		}
		result, err = tx.Exec(ctx, `UPDATE incidents SET state=$2,version=version+1,updated_at=$3 WHERE id=$1 AND version=$4`, incidentID, string(incidents.StatePlanning), now, version)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return incidents.ErrConflict
		}
		var seq int64
		if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM incident_events WHERE incident_id=$1`, incidentID).Scan(&seq); err != nil {
			return err
		}
		data, err := json.Marshal(map[string]any{"actionEventId": actionEventID, "actionDigest": digest, "approval": "invalidated"})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO incident_events (id,incident_id,seq,type,category,source,target,from_state,to_state,actor,reason,correlation_id,data,observed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, uuid.New(), incidentID, seq, string(incidents.EventStateChange), string(incidents.CategoryDecision), "approval", "production target", string(state), string(incidents.StatePlanning), "approval-gate", reason, correlationID, data, now)
		return err
	})
}

// ClaimForExecution is the only path to REMEDIATING. The executor calls it
// immediately before a compare-and-swap production write using the returned
// contract's exact Kubernetes preconditions.
func (s *Service) ClaimForExecution(ctx context.Context, incidentID, actionEventID uuid.UUID, digest string, reader TargetReader) (actions.Contract, error) {
	action, err := s.Check(ctx, incidentID, actionEventID, digest, reader)
	if err != nil {
		return actions.Contract{}, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		state, version, correlationID, err := lockIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if state != incidents.StateAwaitingApproval {
			return ErrIneligible
		}
		current, err := eligibleAction(ctx, tx, incidentID, actionEventID)
		if err != nil || current.Digest != action.Digest {
			return ErrIneligible
		}
		if err := checkApproval(ctx, tx, incidentID, actionEventID, digest); err != nil {
			return err
		}
		now := time.Now().UTC()
		result, err := tx.Exec(ctx, `UPDATE incidents SET state=$2,version=version+1,updated_at=$3 WHERE id=$1 AND version=$4`, incidentID, string(incidents.StateRemediating), now, version)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return incidents.ErrConflict
		}
		var seq int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM incident_events WHERE incident_id=$1`, incidentID).Scan(&seq); err != nil {
			return err
		}
		data, err := json.Marshal(map[string]any{"actionEventId": actionEventID, "actionDigest": digest})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO incident_events (id,incident_id,seq,type,category,source,target,from_state,to_state,actor,reason,correlation_id,data,observed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, uuid.New(), incidentID, seq, string(incidents.EventStateChange), string(incidents.CategoryAction), "approval-gate", action.Target.Cluster+"/"+action.Target.Namespace+"/"+action.Target.Name, string(state), string(incidents.StateRemediating), "executor", "approved action claimed for execution", correlationID, data, now)
		return err
	})
	if err != nil {
		return actions.Contract{}, err
	}
	return action, nil
}

func checkApproval(ctx context.Context, tx pgx.Tx, incidentID, actionEventID uuid.UUID, digest string) error {
	var decision, storedDigest string
	var expires *time.Time
	var invalidated *time.Time
	err := tx.QueryRow(ctx, `SELECT decision, action_digest, expires_at, invalidated_at FROM action_approvals WHERE incident_id=$1 AND action_event_id=$2`, incidentID, actionEventID).Scan(&decision, &storedDigest, &expires, &invalidated)
	if err != nil || decision != string(Approved) || storedDigest != digest || expires == nil || invalidated != nil {
		return ErrIneligible
	}
	if !time.Now().UTC().Before(*expires) {
		return ErrExpired
	}
	return nil
}

func lockIncident(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) (incidents.State, int64, string, error) {
	var state string
	var version int64
	var correlationID string
	err := tx.QueryRow(ctx, `SELECT state,version,correlation_id FROM incidents WHERE id=$1 FOR UPDATE`, incidentID).Scan(&state, &version, &correlationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, "", incidents.ErrNotFound
	}
	return incidents.State(state), version, correlationID, err
}

// eligibleAction checks the latest proposal, its exact successful validation,
// and the service mapping in one database snapshot.
func eligibleAction(ctx context.Context, tx pgx.Tx, incidentID, actionEventID uuid.UUID) (actions.Contract, error) {
	var latest uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM incident_events WHERE incident_id=$1 AND category='action' AND source='action-plan' ORDER BY seq DESC LIMIT 1`, incidentID).Scan(&latest)
	if err != nil || latest != actionEventID {
		return actions.Contract{}, ErrIneligible
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT data FROM incident_events WHERE incident_id=$1 AND id=$2 AND category='action' AND source='action-plan'`, incidentID, actionEventID).Scan(&raw)
	if err != nil {
		return actions.Contract{}, ErrIneligible
	}
	var body struct {
		Contract actions.Contract `json:"contract"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Contract.Validate() != nil {
		return actions.Contract{}, ErrIneligible
	}
	action := body.Contract
	var validation []byte
	err = tx.QueryRow(ctx, `SELECT data FROM incident_events WHERE incident_id=$1 AND category='observation' AND source='sandbox-validation' ORDER BY seq DESC LIMIT 1`, incidentID).Scan(&validation)
	if err != nil {
		return actions.Contract{}, ErrIneligible
	}
	var checked struct {
		Available bool `json:"available"`
		Result    struct {
			ActionEventID uuid.UUID `json:"actionEventId"`
			ActionDigest  string    `json:"actionDigest"`
			Passed        bool      `json:"passed"`
		} `json:"result"`
	}
	if json.Unmarshal(validation, &checked) != nil || !checked.Available || !checked.Result.Passed || checked.Result.ActionEventID != actionEventID || checked.Result.ActionDigest != action.Digest {
		return actions.Contract{}, ErrIneligible
	}
	var serviceID uuid.UUID
	var serviceKey, configuredKey, environment, configuredEnvironment, cluster, namespace, workload string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT i.service_id,i.service_key,s.key,i.environment,s.environment,s.k8s_cluster,s.k8s_namespace,s.k8s_workload,s.enabled FROM incidents i JOIN services s ON s.id=i.service_id WHERE i.id=$1`, incidentID).Scan(&serviceID, &serviceKey, &configuredKey, &environment, &configuredEnvironment, &cluster, &namespace, &workload, &enabled)
	if err != nil || !enabled || serviceKey != configuredKey || environment != configuredEnvironment || action.Environment != environment || action.Target.Cluster != cluster || action.Target.Namespace != namespace || action.Target.Name != workload {
		return actions.Contract{}, ErrIneligible
	}
	return action, nil
}
