package services

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func mustUUID() uuid.UUID { return uuid.New() }

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("KNULL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KNULL_TEST_DATABASE_URL not set; skipping database-backed test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	// Isolate from other runs.
	_, _ = pool.Exec(context.Background(), `DELETE FROM services WHERE key LIKE 'test-%'`)
	return NewStore(pool)
}

func testInput(key, env string) Input {
	return Input{
		Key:          key,
		DisplayName:  "Test " + key,
		Environment:  env,
		K8sCluster:   env + "-eks",
		K8sNamespace: "shop",
		K8sWorkload:  key,
		PrometheusLabels: map[string]string{
			"app":         key,
			"environment": env,
		},
		GitHubRepo: "acme/" + key,
		GitHubRef:  "main",
	}
}

func TestCreateAndGetRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	created, err := store.Create(ctx, testInput("test-checkout", "production"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID.String() == "" || !created.Enabled {
		t.Fatal("created service should have an id and be enabled")
	}
	if created.PrometheusLabels["environment"] != "production" {
		t.Fatalf("labels not persisted: %v", created.PrometheusLabels)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Key != "test-checkout" || got.Environment != "production" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestSameKeyAllowedAcrossEnvironmentsButNotWithin(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, testInput("test-orders", "production")); err != nil {
		t.Fatalf("create prod: %v", err)
	}
	// Same key, different environment: allowed. Each mapping is explicitly
	// scoped to its own environment.
	if _, err := store.Create(ctx, testInput("test-orders", "staging")); err != nil {
		t.Fatalf("create staging should be allowed: %v", err)
	}
	// Same key, same environment: rejected as duplicate.
	if _, err := store.Create(ctx, testInput("test-orders", "production")); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate create = %v, want ErrDuplicate", err)
	}
}

func TestUpdateAndDisable(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	created, err := store.Create(ctx, testInput("test-auth", "production"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	upd := testInput("test-auth", "production")
	upd.DisplayName = "Auth Service (renamed)"
	updated, err := store.Update(ctx, created.ID, upd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DisplayName != "Auth Service (renamed)" {
		t.Fatalf("update not applied: %q", updated.DisplayName)
	}

	disabled, err := store.SetEnabled(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("service should be disabled")
	}
}

func TestUpdateMissingReturnsNotFound(t *testing.T) {
	store := testStore(t)
	_, err := store.Update(context.Background(), mustUUID(), testInput("test-ghost", "production"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v, want ErrNotFound", err)
	}
}

func TestListReturnsCreated(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, testInput("test-fleet", "production")); err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, s := range list {
		if s.Key == "test-fleet" {
			found = true
		}
	}
	if !found {
		t.Fatal("created service not in list")
	}
}
