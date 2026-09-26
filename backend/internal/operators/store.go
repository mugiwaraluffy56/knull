// Package operators persists the human identities that sign in and later
// approve or deny production actions.
package operators

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no operator matches the lookup.
var ErrNotFound = errors.New("operator not found")

// Operator is a signed-in human identity, keyed by OIDC issuer+subject.
type Operator struct {
	ID        uuid.UUID `json:"id"`
	Issuer    string    `json:"-"`
	Subject   string    `json:"-"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	LastLogin time.Time `json:"lastLogin"`
}

// Store persists operators in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds an operator store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Upsert records an operator on sign-in, creating the row on first login and
// refreshing email, name, and last_login on subsequent logins. Identity is
// keyed by (issuer, subject) so the same person is stable across logins.
func (s *Store) Upsert(ctx context.Context, issuer, subject, email, name string) (Operator, error) {
	if issuer == "" || subject == "" {
		return Operator{}, errors.New("issuer and subject are required")
	}
	var op Operator
	err := s.pool.QueryRow(ctx, `
		INSERT INTO operators (issuer, subject, email, name, last_login)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (issuer, subject) DO UPDATE
		  SET email = EXCLUDED.email,
		      name = EXCLUDED.name,
		      last_login = now()
		RETURNING id, issuer, subject, email, name, created_at, last_login`,
		issuer, subject, email, name).
		Scan(&op.ID, &op.Issuer, &op.Subject, &op.Email, &op.Name, &op.CreatedAt, &op.LastLogin)
	if err != nil {
		return Operator{}, fmt.Errorf("upsert operator: %w", err)
	}
	return op, nil
}

// Get returns an operator by id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Operator, error) {
	var op Operator
	err := s.pool.QueryRow(ctx, `
		SELECT id, issuer, subject, email, name, created_at, last_login
		FROM operators WHERE id = $1`, id).
		Scan(&op.ID, &op.Issuer, &op.Subject, &op.Email, &op.Name, &op.CreatedAt, &op.LastLogin)
	if errors.Is(err, pgx.ErrNoRows) {
		return Operator{}, ErrNotFound
	}
	if err != nil {
		return Operator{}, fmt.Errorf("get operator: %w", err)
	}
	return op, nil
}
