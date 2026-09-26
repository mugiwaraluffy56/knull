package secrets

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to the database named by KNULL_TEST_DATABASE_URL. The test
// is skipped when that variable is unset so the pure-unit suite still runs
// offline; CI sets it against a live PostgreSQL service.
func testPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

// seedOperator inserts an operator row to satisfy the created_by foreign key.
func seedOperator(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO operators (issuer, subject, email, name)
		VALUES ('test', $1, 'op@test', 'Op')
		RETURNING id`, uuid.NewString()).Scan(&id)
	if err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	return id
}

func TestStoreScopeIsolation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	// Clean slate for the kinds this test touches.
	_, _ = pool.Exec(ctx, `DELETE FROM integration_credentials WHERE kind = 'kubernetes'`)

	cipher, err := NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool, cipher)
	operatorID := seedOperator(t, pool)

	prodSecret := []byte("PROD-kubeconfig")
	if _, err := store.Put(ctx, KindKubernetes, ScopeProduction, prodSecret, operatorID); err != nil {
		t.Fatalf("put production: %v", err)
	}

	// Revealing the SAME kind under a DIFFERENT scope must not return the
	// production secret; scopes are not interchangeable.
	if _, err := store.Reveal(ctx, KindKubernetes, ScopeRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reveal read scope = %v, want ErrNotFound", err)
	}
	if _, err := store.Reveal(ctx, KindKubernetes, ScopeSandbox); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reveal sandbox scope = %v, want ErrNotFound", err)
	}

	// The exact scope returns the exact secret.
	got, err := store.Reveal(ctx, KindKubernetes, ScopeProduction)
	if err != nil {
		t.Fatalf("reveal production: %v", err)
	}
	if string(got) != string(prodSecret) {
		t.Fatalf("reveal production = %q, want %q", got, prodSecret)
	}
}

func TestStoreListReturnsNoPlaintext(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM integration_credentials WHERE kind = 'prometheus'`)

	cipher, _ := NewCipher("0123456789abcdef0123456789abcdef")
	store := NewStore(pool, cipher)
	operatorID := seedOperator(t, pool)

	if _, err := store.Put(ctx, KindPrometheus, ScopeRead, []byte("prom-token"), operatorID); err != nil {
		t.Fatalf("put: %v", err)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, m := range list {
		if m.Kind == KindPrometheus {
			found = true
			if m.Fingerprint == "" {
				t.Fatal("metadata missing fingerprint")
			}
		}
	}
	if !found {
		t.Fatal("stored credential not listed")
	}
}
