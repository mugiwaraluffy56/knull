package recoverypolicy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("recovery policy not found")
var ErrServiceNotFound = errors.New("service not found")

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Put creates a new immutable version only when the validated policy changes.
// Locking the service row serializes concurrent configuration writes.
func (s *Store) Put(ctx context.Context, serviceID uuid.UUID, policy Policy, operatorID uuid.UUID) (Snapshot, error) {
	if serviceID == uuid.Nil || operatorID == uuid.Nil {
		return Snapshot{}, ErrInvalid
	}
	digest, err := policy.Digest()
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var exists uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM services WHERE id=$1 FOR UPDATE`, serviceID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrServiceNotFound
		}
		if err != nil {
			return fmt.Errorf("lock service: %w", err)
		}
		var current Snapshot
		err = tx.QueryRow(ctx, `SELECT service_id,version,digest,policy,configured_by,configured_at FROM recovery_policies WHERE service_id=$1 ORDER BY version DESC LIMIT 1`, serviceID).Scan(&current.ServiceID, &current.Version, &current.Digest, &current.Policy, &current.ConfiguredBy, &current.ConfiguredAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read current recovery policy: %w", err)
		}
		if err == nil && current.Digest == digest {
			snapshot = current
			return nil
		}
		version := current.Version + 1
		err = tx.QueryRow(ctx, `INSERT INTO recovery_policies (service_id,version,digest,policy,configured_by) VALUES ($1,$2,$3,$4,$5) RETURNING service_id,version,digest,policy,configured_by,configured_at`, serviceID, version, digest, policy, operatorID).Scan(&snapshot.ServiceID, &snapshot.Version, &snapshot.Digest, &snapshot.Policy, &snapshot.ConfiguredBy, &snapshot.ConfiguredAt)
		if err != nil {
			return fmt.Errorf("insert recovery policy: %w", err)
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Current returns the latest immutable policy snapshot for a service.
func (s *Store) Current(ctx context.Context, serviceID uuid.UUID) (Snapshot, error) {
	var snapshot Snapshot
	err := s.pool.QueryRow(ctx, `SELECT service_id,version,digest,policy,configured_by,configured_at FROM recovery_policies WHERE service_id=$1 ORDER BY version DESC LIMIT 1`, serviceID).Scan(&snapshot.ServiceID, &snapshot.Version, &snapshot.Digest, &snapshot.Policy, &snapshot.ConfiguredBy, &snapshot.ConfiguredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("get recovery policy: %w", err)
	}
	return snapshot, nil
}

// Version loads the exact criteria used by a historic assessment.
func (s *Store) Version(ctx context.Context, serviceID uuid.UUID, version int64) (Snapshot, error) {
	var snapshot Snapshot
	err := s.pool.QueryRow(ctx, `SELECT service_id,version,digest,policy,configured_by,configured_at FROM recovery_policies WHERE service_id=$1 AND version=$2`, serviceID, version).Scan(&snapshot.ServiceID, &snapshot.Version, &snapshot.Digest, &snapshot.Policy, &snapshot.ConfiguredBy, &snapshot.ConfiguredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("get recovery policy version: %w", err)
	}
	return snapshot, nil
}

// RecordAssessment stores the exact policy version, window, measurements and
// deterministic threshold result. Task 24 adds the live readers and Jev's
// typed recovery decision before transitioning the incident.
func (s *Store) RecordAssessment(ctx context.Context, incidentID uuid.UUID, snapshot Snapshot, observation Observation) (uuid.UUID, Assessment, error) {
	if incidentID == uuid.Nil || snapshot.ServiceID == uuid.Nil || snapshot.Version < 1 || snapshot.Digest == "" {
		return uuid.Nil, Assessment{}, ErrInvalid
	}
	computedDigest, err := snapshot.Policy.Digest()
	if err != nil || computedDigest != snapshot.Digest {
		return uuid.Nil, Assessment{}, ErrInvalid
	}
	assessment := Assess(snapshot, observation)
	if observation.WindowStart.IsZero() || !observation.WindowEnd.After(observation.WindowStart) {
		return uuid.Nil, Assessment{}, ErrInvalid
	}
	if assessment.Missing == nil {
		assessment.Missing = []string{}
	}
	if assessment.Failed == nil {
		assessment.Failed = []string{}
	}
	var recorded uuid.UUID
	err = s.pool.QueryRow(ctx, `INSERT INTO recovery_assessments (incident_id,service_id,policy_version,policy_digest,window_start,window_end,observation,outcome,missing,failed) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10 WHERE EXISTS (SELECT 1 FROM incidents WHERE id=$1 AND service_id=$2) AND EXISTS (SELECT 1 FROM recovery_policies WHERE service_id=$2 AND version=$3 AND digest=$4) RETURNING id`, incidentID, snapshot.ServiceID, snapshot.Version, snapshot.Digest, observation.WindowStart, observation.WindowEnd, observation, string(assessment.Outcome), assessment.Missing, assessment.Failed).Scan(&recorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, Assessment{}, ErrServiceNotFound
	}
	if err != nil {
		return uuid.Nil, Assessment{}, fmt.Errorf("record recovery assessment: %w", err)
	}
	return recorded, assessment, nil
}
