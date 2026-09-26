package approval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
	"github.com/mugiwaraluffy56/knull/backend/internal/store"
)

type fixedReader struct{ value actions.Preconditions }

func (r fixedReader) ReadPreconditions(context.Context, actions.Target, string) (actions.Preconditions, error) {
	return r.value, nil
}

type fixture struct {
	pool       *pgxpool.Pool
	incidentID uuid.UUID
	actionID   uuid.UUID
	action     actions.Contract
	operator   operators.Operator
}

func newFixture(t *testing.T) fixture {
	t.Helper()
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
	var serviceID uuid.UUID
	key := "approval-" + uuid.NewString()[:12]
	err = pool.QueryRow(ctx, `INSERT INTO services (key,display_name,environment,k8s_cluster,k8s_namespace,k8s_workload) VALUES ($1,'Approval fixture','production','prod-east','checkout','checkout-api') RETURNING id`, key).Scan(&serviceID)
	if err != nil {
		t.Fatal(err)
	}
	var incidentID uuid.UUID
	err = pool.QueryRow(ctx, `INSERT INTO incidents (service_id,service_key,environment,state,correlation_id) VALUES ($1,$2,'production','AWAITING_APPROVAL',$3) RETURNING id`, serviceID, key, uuid.NewString()).Scan(&incidentID)
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.NewString()
	draft := actions.Draft{Type: actions.Memory, Environment: "production", Target: actions.Target{Cluster: "prod-east", Namespace: "checkout", Kind: "Deployment", Name: "checkout-api", Container: "api"}, Field: "resources.limits.memory", CurrentValue: "256Mi", DesiredValue: "1Gi", Preconditions: actions.Preconditions{ResourceUID: uid, ResourceVersion: "123", CurrentValue: "256Mi"}, ExpectedImpact: "rolling restart", Risk: "MEDIUM", EvidenceIDs: []string{uuid.NewString()}}
	action, err := actions.Seal(draft)
	if err != nil {
		t.Fatal(err)
	}
	actionID := uuid.New()
	plan, _ := json.Marshal(map[string]any{"contract": action, "status": "proposed"})
	_, err = pool.Exec(ctx, `INSERT INTO incident_events (id,incident_id,seq,type,category,source,data) VALUES ($1,$2,1,'NOTE','action','action-plan',$3)`, actionID, incidentID, plan)
	if err != nil {
		t.Fatal(err)
	}
	validation, _ := json.Marshal(map[string]any{"available": true, "result": map[string]any{"actionEventId": actionID, "actionDigest": action.Digest, "passed": true}})
	_, err = pool.Exec(ctx, `INSERT INTO incident_events (incident_id,seq,type,category,source,data) VALUES ($1,2,'NOTE','observation','sandbox-validation',$2)`, incidentID, validation)
	if err != nil {
		t.Fatal(err)
	}
	op := operators.Operator{Email: "approver@example.test"}
	err = pool.QueryRow(ctx, `INSERT INTO operators (issuer,subject,email,name) VALUES ('test',$1,$2,'Approver') RETURNING id`, uuid.NewString(), op.Email).Scan(&op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{pool, incidentID, actionID, action, op}
}

func TestApprovalGateIsActionSpecificAndStaleTargetInvalidates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := NewService(f.pool)
	if _, err := s.Check(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{f.action.Preconditions}); !errors.Is(err, ErrIneligible) {
		t.Fatalf("pending approval: %v", err)
	}
	decision, err := s.Decide(ctx, f.incidentID, f.actionID, f.action.Digest, f.operator, Approved, "")
	if err != nil {
		t.Fatal(err)
	}
	if decision.ExpiresAt == nil || decision.OperatorID != f.operator.ID {
		t.Fatalf("incomplete approval: %+v", decision)
	}
	if _, err := s.Decide(ctx, f.incidentID, f.actionID, f.action.Digest, f.operator, Denied, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("second decision: %v", err)
	}
	if _, err := s.Check(ctx, f.incidentID, f.actionID, "other-digest", fixedReader{f.action.Preconditions}); !errors.Is(err, ErrIneligible) {
		t.Fatalf("altered action: %v", err)
	}
	if _, err := s.Check(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{f.action.Preconditions}); err != nil {
		t.Fatalf("valid gate: %v", err)
	}
	changed := f.action.Preconditions
	changed.CurrentValue = "512Mi"
	if _, err := s.Check(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{changed}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale target: %v", err)
	}
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM incidents WHERE id=$1`, f.incidentID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(incidents.StatePlanning) {
		t.Fatalf("state after drift: %s", state)
	}
	var invalidated bool
	if err := f.pool.QueryRow(ctx, `SELECT invalidated_at IS NOT NULL FROM action_approvals WHERE action_event_id=$1`, f.actionID).Scan(&invalidated); err != nil {
		t.Fatal(err)
	}
	if !invalidated {
		t.Fatal("approval not invalidated")
	}
}

func TestDeniedActionNeverPassesGate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := NewService(f.pool)
	decision, err := s.Decide(ctx, f.incidentID, f.actionID, f.action.Digest, f.operator, Denied, "risk too high")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Reason != "risk too high" || decision.ExpiresAt != nil {
		t.Fatalf("denial: %+v", decision)
	}
	if _, err := s.ClaimForExecution(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{f.action.Preconditions}); !errors.Is(err, ErrIneligible) {
		t.Fatalf("denied claim: %v", err)
	}
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM incidents WHERE id=$1`, f.incidentID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(incidents.StateDenied) {
		t.Fatalf("denied state: %s", state)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM incident_events WHERE incident_id=$1 AND source='approval' AND actor=$2 AND data->>'actionDigest'=$3`, f.incidentID, f.operator.Email, f.action.Digest).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("denial audit count: %d", count)
	}
}

func TestApprovedActionCanBeClaimedOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := NewService(f.pool)
	if _, err := s.Decide(ctx, f.incidentID, f.actionID, f.action.Digest, f.operator, Approved, ""); err != nil {
		t.Fatal(err)
	}
	action, err := s.ClaimForExecution(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{f.action.Preconditions})
	if err != nil || action.Digest != f.action.Digest {
		t.Fatalf("claim: %v, %+v", err, action)
	}
	if _, err := s.ClaimForExecution(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{f.action.Preconditions}); !errors.Is(err, ErrIneligible) {
		t.Fatalf("second claim: %v", err)
	}
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM incidents WHERE id=$1`, f.incidentID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(incidents.StateRemediating) {
		t.Fatalf("claimed state: %s", state)
	}
}

func TestExpiredApprovalRequiresNewPlan(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := NewService(f.pool)
	if _, err := s.Decide(ctx, f.incidentID, f.actionID, f.action.Digest, f.operator, Approved, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE action_approvals SET expires_at=now()-interval '1 second' WHERE action_event_id=$1`, f.actionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{f.action.Preconditions}); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired approval: %v", err)
	}
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM incidents WHERE id=$1`, f.incidentID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(incidents.StatePlanning) {
		t.Fatalf("expired state: %s", state)
	}
}

func TestChangedServiceMappingInvalidatesApproval(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := NewService(f.pool)
	if _, err := s.Decide(ctx, f.incidentID, f.actionID, f.action.Digest, f.operator, Approved, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE services SET k8s_workload='replacement' WHERE id=(SELECT service_id FROM incidents WHERE id=$1)`, f.incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(ctx, f.incidentID, f.actionID, f.action.Digest, fixedReader{f.action.Preconditions}); !errors.Is(err, ErrIneligible) {
		t.Fatalf("changed mapping: %v", err)
	}
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM incidents WHERE id=$1`, f.incidentID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(incidents.StatePlanning) {
		t.Fatalf("changed mapping state: %s", state)
	}
}
