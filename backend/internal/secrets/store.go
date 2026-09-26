package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no credential matches the requested kind+scope.
var ErrNotFound = errors.New("credential not found")

// Metadata describes a stored credential without ever exposing its value.
type Metadata struct {
	ID          uuid.UUID `json:"id"`
	Kind        Kind      `json:"kind"`
	Scope       Scope     `json:"scope"`
	Fingerprint string    `json:"fingerprint"`
	CreatedBy   uuid.UUID `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Store persists integration credentials encrypted at rest.
type Store struct {
	pool   *pgxpool.Pool
	cipher *Cipher
}

// NewStore builds a credential store. A nil cipher disables writes but still
// allows metadata listing, so the API stays usable when no key is configured.
func NewStore(pool *pgxpool.Pool, cipher *Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

// Put stores or replaces the credential for a kind+scope. The plaintext is
// encrypted before it reaches the database and is never logged.
func (s *Store) Put(ctx context.Context, kind Kind, scope Scope, plaintext []byte, createdBy uuid.UUID) (Metadata, error) {
	if !kind.Valid() {
		return Metadata{}, fmt.Errorf("invalid kind %q", kind)
	}
	if !scope.Valid() {
		return Metadata{}, fmt.Errorf("invalid scope %q", scope)
	}
	if s.cipher == nil {
		return Metadata{}, errors.New("cannot store credential: encryption key not configured")
	}
	if len(plaintext) == 0 {
		return Metadata{}, errors.New("credential value must not be empty")
	}

	ciphertext, nonce, err := s.cipher.Encrypt(plaintext)
	if err != nil {
		return Metadata{}, err
	}
	fp := Fingerprint(plaintext)

	var m Metadata
	err = s.pool.QueryRow(ctx, `
		INSERT INTO integration_credentials (kind, scope, ciphertext, nonce, fingerprint, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (kind, scope) DO UPDATE
		  SET ciphertext = EXCLUDED.ciphertext,
		      nonce = EXCLUDED.nonce,
		      fingerprint = EXCLUDED.fingerprint,
		      created_by = EXCLUDED.created_by,
		      updated_at = now()
		RETURNING id, kind, scope, fingerprint, created_by, created_at, updated_at`,
		string(kind), string(scope), ciphertext, nonce, fp, createdBy).
		Scan(&m.ID, &m.Kind, &m.Scope, &m.Fingerprint, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return Metadata{}, fmt.Errorf("store credential: %w", err)
	}
	return m, nil
}

// List returns metadata for every stored credential. It never returns secret
// values.
func (s *Store) List(ctx context.Context) ([]Metadata, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, scope, fingerprint, created_by, created_at, updated_at
		FROM integration_credentials
		ORDER BY kind, scope`)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()

	var out []Metadata
	for rows.Next() {
		var m Metadata
		if err := rows.Scan(&m.ID, &m.Kind, &m.Scope, &m.Fingerprint, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Reveal decrypts and returns the credential for an EXACT kind+scope. The scope
// must match precisely: requesting a production credential never returns a read
// or sandbox one. This is the single choke point for credential retrieval, kept
// internal to the trusted runtime and never exposed over the API.
func (s *Store) Reveal(ctx context.Context, kind Kind, scope Scope) ([]byte, error) {
	if !kind.Valid() {
		return nil, fmt.Errorf("invalid kind %q", kind)
	}
	if !scope.Valid() {
		return nil, fmt.Errorf("invalid scope %q", scope)
	}
	if s.cipher == nil {
		return nil, errors.New("cannot reveal credential: encryption key not configured")
	}

	var ciphertext, nonce []byte
	err := s.pool.QueryRow(ctx, `
		SELECT ciphertext, nonce FROM integration_credentials
		WHERE kind = $1 AND scope = $2`, string(kind), string(scope)).
		Scan(&ciphertext, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load credential: %w", err)
	}
	return s.cipher.Decrypt(ciphertext, nonce)
}
