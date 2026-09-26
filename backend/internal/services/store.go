package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no service matches the lookup.
var ErrNotFound = errors.New("service not found")

// ErrDuplicate is returned when a (key, environment) pair already exists.
var ErrDuplicate = errors.New("service already exists in this environment")

// Store persists services in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a service store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const serviceColumns = `id, key, display_name, environment,
	k8s_cluster, k8s_namespace, k8s_workload,
	prometheus_labels, github_repo, github_ref,
	recovery_policy_ref, enabled, created_at, updated_at`

// Create validates and inserts a new service. It rejects a duplicate
// (key, environment) with ErrDuplicate and invalid input with *ValidationError.
func (s *Store) Create(ctx context.Context, in Input) (Service, error) {
	clean, err := normalizeAndValidate(in)
	if err != nil {
		return Service{}, err
	}

	var svc Service
	err = s.pool.QueryRow(ctx, `
		INSERT INTO services (key, display_name, environment,
			k8s_cluster, k8s_namespace, k8s_workload,
			prometheus_labels, github_repo, github_ref, recovery_policy_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING `+serviceColumns,
		clean.Key, clean.DisplayName, clean.Environment,
		clean.K8sCluster, clean.K8sNamespace, clean.K8sWorkload,
		clean.PrometheusLabels, clean.GitHubRepo, clean.GitHubRef, clean.RecoveryPolicyRef).
		Scan(scanTargets(&svc)...)
	if err != nil {
		if isUniqueViolation(err) {
			return Service{}, ErrDuplicate
		}
		return Service{}, fmt.Errorf("insert service: %w", err)
	}
	return svc, nil
}

// Update validates and replaces the mutable fields of an existing service. The
// key and environment may change but must remain unique together.
func (s *Store) Update(ctx context.Context, id uuid.UUID, in Input) (Service, error) {
	clean, err := normalizeAndValidate(in)
	if err != nil {
		return Service{}, err
	}

	var svc Service
	err = s.pool.QueryRow(ctx, `
		UPDATE services SET
			key = $2, display_name = $3, environment = $4,
			k8s_cluster = $5, k8s_namespace = $6, k8s_workload = $7,
			prometheus_labels = $8, github_repo = $9, github_ref = $10,
			recovery_policy_ref = $11, updated_at = now()
		WHERE id = $1
		RETURNING `+serviceColumns,
		id, clean.Key, clean.DisplayName, clean.Environment,
		clean.K8sCluster, clean.K8sNamespace, clean.K8sWorkload,
		clean.PrometheusLabels, clean.GitHubRepo, clean.GitHubRef, clean.RecoveryPolicyRef).
		Scan(scanTargets(&svc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return Service{}, ErrDuplicate
		}
		return Service{}, fmt.Errorf("update service: %w", err)
	}
	return svc, nil
}

// SetEnabled enables or disables a service. Disabling is preferred over deletion
// so history and mappings are preserved.
func (s *Store) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (Service, error) {
	var svc Service
	err := s.pool.QueryRow(ctx, `
		UPDATE services SET enabled = $2, updated_at = now()
		WHERE id = $1 RETURNING `+serviceColumns, id, enabled).
		Scan(scanTargets(&svc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	if err != nil {
		return Service{}, fmt.Errorf("set enabled: %w", err)
	}
	return svc, nil
}

// Get returns a service by id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Service, error) {
	var svc Service
	err := s.pool.QueryRow(ctx, `SELECT `+serviceColumns+` FROM services WHERE id = $1`, id).
		Scan(scanTargets(&svc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	if err != nil {
		return Service{}, fmt.Errorf("get service: %w", err)
	}
	return svc, nil
}

// ResolveID returns the id of an enabled service matching key+environment. The
// boolean is false when no such service is configured, so alert intake can still
// record an incident for an unmapped service without failing.
func (s *Store) ResolveID(ctx context.Context, key, environment string) (uuid.UUID, bool) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM services WHERE key = $1 AND environment = $2`, key, environment).Scan(&id)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// List returns all services ordered by environment then key.
func (s *Store) List(ctx context.Context) ([]Service, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+serviceColumns+` FROM services ORDER BY environment, key`)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()

	var out []Service
	for rows.Next() {
		var svc Service
		if err := rows.Scan(scanTargets(&svc)...); err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

func scanTargets(svc *Service) []any {
	return []any{
		&svc.ID, &svc.Key, &svc.DisplayName, &svc.Environment,
		&svc.K8sCluster, &svc.K8sNamespace, &svc.K8sWorkload,
		&svc.PrometheusLabels, &svc.GitHubRepo, &svc.GitHubRef,
		&svc.RecoveryPolicyRef, &svc.Enabled, &svc.CreatedAt, &svc.UpdatedAt,
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
