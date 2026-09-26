package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/mugiwaraluffy56/knull/backend/internal/secrets"
)

// putCredentialRequest is the body for storing an integration credential. The
// value is write-only: it is accepted here, encrypted at rest, and never
// returned by any endpoint.
type putCredentialRequest struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	Value string `json:"value"`
}

// handlePutCredential stores or replaces an integration credential. Operator
// only. The plaintext value is encrypted before persistence and never logged.
func (s *Server) handlePutCredential(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "credential storage is not configured")
		return
	}
	op, ok := operatorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req putCredentialRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	kind := secrets.Kind(req.Kind)
	scope := secrets.Scope(req.Scope)
	if !kind.Valid() {
		writeError(w, http.StatusBadRequest, "invalid kind; want one of kubernetes, prometheus, github, openai")
		return
	}
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "invalid scope; want one of read, sandbox, production")
		return
	}
	if req.Value == "" {
		writeError(w, http.StatusBadRequest, "value must not be empty")
		return
	}

	meta, err := s.secrets.Put(r.Context(), kind, scope, []byte(req.Value), op.ID)
	if err != nil {
		s.internalError(w, "store credential", err)
		return
	}
	// meta carries no plaintext, only a fingerprint and metadata.
	writeJSON(w, http.StatusOK, meta)
}

// handleListCredentials returns metadata for all stored credentials. Operator
// only. No plaintext value is ever included.
func (s *Server) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "credential storage is not configured")
		return
	}
	list, err := s.secrets.List(r.Context())
	if err != nil {
		s.internalError(w, "list credentials", err)
		return
	}
	if list == nil {
		list = []secrets.Metadata{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": list})
}
