package recoverypolicy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mugiwaraluffy56/knull/backend/internal/store"
)

func TestVersionedPolicyAndAssessmentSnapshot(t *testing.T) {
	url := os.Getenv("KNULL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KNULL_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := (&store.Store{Pool: pool}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var operatorID, serviceID, incidentID uuid.UUID
	err = pool.QueryRow(ctx, `INSERT INTO operators (issuer,subject,email,name) VALUES ('test',$1,'policy@example.test','Policy Operator') RETURNING id`, uuid.NewString()).Scan(&operatorID)
	if err != nil {
		t.Fatal(err)
	}
	key := "policy-" + uuid.NewString()[:12]
	err = pool.QueryRow(ctx, `INSERT INTO services (key,display_name,environment,k8s_cluster,k8s_namespace,k8s_workload) VALUES ($1,'Policy test','production','prod','checkout','checkout-api') RETURNING id`, key).Scan(&serviceID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO incidents (service_id,service_key,environment,state,correlation_id) VALUES ($1,$2,'production','VERIFYING',$3) RETURNING id`, serviceID, key, uuid.NewString()).Scan(&incidentID)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(pool)
	policy := validPolicy()
	first, err := s.Put(ctx, serviceID, policy, operatorID)
	if err != nil || first.Version != 1 {
		t.Fatalf("first version: %+v, %v", first, err)
	}
	again, err := s.Put(ctx, serviceID, policy, operatorID)
	if err != nil || again.Version != 1 {
		t.Fatalf("idempotent version: %+v, %v", again, err)
	}
	policy.ErrorRate.MaxRatio = .02
	second, err := s.Put(ctx, serviceID, policy, operatorID)
	if err != nil || second.Version != 2 || second.Digest == first.Digest {
		t.Fatalf("second version: %+v, %v", second, err)
	}
	current, err := s.Current(ctx, serviceID)
	if err != nil || current.Version != 2 {
		t.Fatalf("current version: %+v, %v", current, err)
	}
	historic, err := s.Version(ctx, serviceID, 1)
	if err != nil || historic.Digest != first.Digest {
		t.Fatalf("historic version: %+v, %v", historic, err)
	}
	start := time.Now().Add(-6 * time.Minute).UTC().Truncate(time.Microsecond)
	missing := Observation{WindowStart: start, WindowEnd: start.Add(5 * time.Minute)}
	id, assessment, err := s.RecordAssessment(ctx, incidentID, historic, missing)
	if err != nil || id == uuid.Nil || assessment.Outcome == Recovered {
		t.Fatalf("missing assessment: %+v, %v", assessment, err)
	}
	var version int64
	var digest string
	var from, to time.Time
	err = pool.QueryRow(ctx, `SELECT policy_version,policy_digest,window_start,window_end FROM recovery_assessments WHERE id=$1`, id).Scan(&version, &digest, &from, &to)
	if err != nil || version != 1 || digest != first.Digest || !from.Equal(start) || !to.Equal(start.Add(5*time.Minute)) {
		t.Fatalf("stored snapshot: version=%d digest=%q from=%s to=%s err=%v", version, digest, from, to, err)
	}
}
