package alerts

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FailureStore records rejected alert deliveries so they are visible rather than
// silently dropped.
type FailureStore struct {
	pool *pgxpool.Pool
}

// NewFailureStore builds a FailureStore.
func NewFailureStore(pool *pgxpool.Pool) *FailureStore {
	return &FailureStore{pool: pool}
}

// Record persists one intake failure.
func (s *FailureStore) Record(ctx context.Context, reason, remoteAddr, detail string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO alert_intake_failures (reason, remote_addr, detail) VALUES ($1,$2,$3)`,
		reason, remoteAddr, detail)
	if err != nil {
		return fmt.Errorf("record intake failure: %w", err)
	}
	return nil
}

// Count returns the number of recorded intake failures (used by tests and ops).
func (s *FailureStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM alert_intake_failures`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count intake failures: %w", err)
	}
	return n, nil
}
