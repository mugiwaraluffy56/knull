package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mugiwaraluffy56/knull/backend/internal/alerts"
)

// alertIntake is the intake behavior the webhook needs.
type alertIntake interface {
	Process(ctx context.Context, p alerts.AlertmanagerPayload, remoteAddr string) ([]alerts.Outcome, error)
}

// failureRecorder records rejected deliveries (unauthorized, malformed).
type failureRecorder interface {
	Record(ctx context.Context, reason, remoteAddr, detail string) error
}

// handleAlertWebhook receives Prometheus Alertmanager notifications. It is not
// operator-authenticated; instead it verifies a shared secret in constant time.
// Unauthorized or malformed deliveries are recorded as intake failures and
// rejected without starting an investigation.
func (s *Server) handleAlertWebhook(w http.ResponseWriter, r *http.Request) {
	if s.alertIntake == nil {
		writeError(w, http.StatusServiceUnavailable, "alert intake is not configured")
		return
	}
	remote := clientAddr(r)

	if !s.alertSecretOK(r) {
		if s.alertFailures != nil {
			_ = s.alertFailures.Record(r.Context(), "unauthorized", remote, "invalid or missing webhook secret")
		}
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var payload alerts.AlertmanagerPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20)).Decode(&payload); err != nil {
		if s.alertFailures != nil {
			_ = s.alertFailures.Record(r.Context(), "malformed", remote, "invalid JSON body")
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	outcomes, err := s.alertIntake.Process(r.Context(), payload, remote)
	if err != nil {
		// A payload-level rejection (e.g. no alerts) is a client error and is
		// already recorded by the intake as a failure where relevant.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"outcomes": outcomes})
}

// alertSecretOK checks the configured shared secret against the Authorization
// bearer token or the `token` query parameter, in constant time.
func (s *Server) alertSecretOK(r *http.Request) bool {
	if s.alertSecret == "" {
		return false // fail closed: no secret configured means no intake
	}
	provided := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		provided = strings.TrimPrefix(h, "Bearer ")
	} else if t := r.URL.Query().Get("token"); t != "" {
		provided = t
	}
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.alertSecret)) == 1
}

func clientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	return r.RemoteAddr
}
