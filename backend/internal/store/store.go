// Package store owns the PostgreSQL and Redis connections and the schema
// migrations for the incident API.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Store bundles the application's data dependencies.
type Store struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
}

// Open connects to PostgreSQL and Redis and verifies both are reachable. It
// returns a fully usable Store or an error naming the dependency that failed.
func Open(ctx context.Context, databaseURL, redisURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		pool.Close()
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Store{Pool: pool, Redis: rdb}, nil
}

// Close releases both connections. It is safe to call on a partially
// initialized Store.
func (s *Store) Close() {
	if s == nil {
		return
	}
	if s.Pool != nil {
		s.Pool.Close()
	}
	if s.Redis != nil {
		_ = s.Redis.Close()
	}
}

// PingPostgres reports whether PostgreSQL currently answers within ctx.
func (s *Store) PingPostgres(ctx context.Context) error {
	if err := s.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return nil
}

// PingRedis reports whether Redis currently answers within ctx.
func (s *Store) PingRedis(ctx context.Context) error {
	if err := s.Redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}
