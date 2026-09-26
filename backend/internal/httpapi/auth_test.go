package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/auth"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
	"github.com/mugiwaraluffy56/knull/backend/internal/secrets"
)

// fakeOperators is an in-memory operatorStore for tests.
type fakeOperators struct {
	byID map[uuid.UUID]operators.Operator
}

func (f *fakeOperators) Get(_ context.Context, id uuid.UUID) (operators.Operator, error) {
	op, ok := f.byID[id]
	if !ok {
		return operators.Operator{}, operators.ErrNotFound
	}
	return op, nil
}

func (f *fakeOperators) Upsert(_ context.Context, issuer, subject, email, name string) (operators.Operator, error) {
	op := operators.Operator{ID: uuid.New(), Email: email, Name: name}
	if f.byID == nil {
		f.byID = map[uuid.UUID]operators.Operator{}
	}
	f.byID[op.ID] = op
	return op, nil
}

// fakeSecrets records what it was asked to store and returns metadata only.
type fakeSecrets struct {
	lastValue string
	metas     []secrets.Metadata
}

func (f *fakeSecrets) Put(_ context.Context, kind secrets.Kind, scope secrets.Scope, plaintext []byte, createdBy uuid.UUID) (secrets.Metadata, error) {
	f.lastValue = string(plaintext)
	return secrets.Metadata{
		ID:          uuid.New(),
		Kind:        kind,
		Scope:       scope,
		Fingerprint: secrets.Fingerprint(plaintext),
		CreatedBy:   createdBy,
	}, nil
}

func (f *fakeSecrets) List(context.Context) ([]secrets.Metadata, error) {
	return f.metas, nil
}

func authTestServer() *Server {
	return New(Options{
		Deps:          staticChecker{},
		AllowedOrigin: "http://localhost:3000",
		HealthTimeout: time.Second,
		// Non-nil sessions with a nil redis client: the no-cookie path returns
		// before Redis is touched, which is exactly what these tests exercise.
		Sessions:  auth.NewSessionManager(nil, time.Hour, false),
		Operators: &fakeOperators{},
		Secrets:   &fakeSecrets{},
	})
}

func TestProtectedEndpointsRejectUnauthenticated(t *testing.T) {
	srv := authTestServer().Handler()
	for _, path := range []string{"/api/me", "/api/integrations/credentials"} {
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", path, rr.Code)
		}
	}
}

func TestPutCredentialStoresValueButNeverReturnsIt(t *testing.T) {
	fs := &fakeSecrets{}
	srv := New(Options{
		Deps:          staticChecker{},
		AllowedOrigin: "http://localhost:3000",
		Operators:     &fakeOperators{},
		Secrets:       fs,
	})

	op := operators.Operator{ID: uuid.New(), Email: "op@knull.local"}
	body := `{"kind":"github","scope":"read","value":"ghp_supersecrettoken"}`
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/credentials", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), operatorContextKey, op))

	rr := httptest.NewRecorder()
	srv.handlePutCredential(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fs.lastValue != "ghp_supersecrettoken" {
		t.Fatalf("store received %q, want the plaintext", fs.lastValue)
	}
	if strings.Contains(rr.Body.String(), "ghp_supersecrettoken") {
		t.Fatalf("response leaked the secret value: %s", rr.Body.String())
	}
	var meta secrets.Metadata
	if err := json.Unmarshal(rr.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta.Fingerprint == "" || meta.Kind != secrets.KindGitHub || meta.Scope != secrets.ScopeRead {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}

func TestPutCredentialRejectsInvalidScope(t *testing.T) {
	srv := New(Options{Operators: &fakeOperators{}, Secrets: &fakeSecrets{}})
	op := operators.Operator{ID: uuid.New()}
	body := `{"kind":"github","scope":"root","value":"x"}`
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/credentials", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), operatorContextKey, op))

	rr := httptest.NewRecorder()
	srv.handlePutCredential(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}
