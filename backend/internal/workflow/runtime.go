// Package workflow drives durable incident investigations through the TrueForge
// agent runtime and streams their progress into the incident event model.
//
// TrueForge is reached over a language-neutral HTTP + Server-Sent Events API, so
// the backend needs no TypeScript SDK. The concrete HTTP client uses the
// published session and turn API; the Runtime interface remains testable.
package workflow

import (
	"context"
	"errors"
)

// ErrSessionUnavailable indicates the runtime could not be reached or the
// session does not exist.
var ErrSessionUnavailable = errors.New("workflow session unavailable")
var ErrUnsupportedControl = errors.New("workflow control unsupported by TrueForge")

// EventKind classifies a runtime event.
type EventKind string

const (
	// KindProgress is an informational investigation step (tool call, finding).
	KindProgress EventKind = "progress"
	// KindTransition requests an incident lifecycle state change.
	KindTransition EventKind = "transition"
	// KindError signals the workflow failed.
	KindError EventKind = "error"
	// KindDone signals the workflow reached its end for now.
	KindDone EventKind = "done"
)

// RuntimeEvent is one item streamed from a workflow session.
type RuntimeEvent struct {
	Kind    EventKind      `json:"kind"`
	Seq     int64          `json:"seq"`
	State   string         `json:"state,omitempty"` // target state for KindTransition
	Summary string         `json:"summary,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// Session identifies a running workflow.
type Session struct {
	ID     string `json:"id"`
	RunID  string `json:"runId"`
	Status string `json:"status"`
}

// StartRequest carries the incident context needed to start a workflow.
type StartRequest struct {
	IncidentID  string            `json:"incidentId"`
	ServiceKey  string            `json:"serviceKey"`
	Environment string            `json:"environment"`
	Summary     string            `json:"summary"`
	Symptoms    map[string]string `json:"symptoms"`
}

// Runtime is the durable agent runtime (TrueForge) the manager orchestrates.
type Runtime interface {
	// StartSession begins a durable workflow for an incident.
	StartSession(ctx context.Context, req StartRequest) (Session, error)
	// StreamEvents returns a channel of events for a persisted turn. The channel closes
	// when the stream ends; implementations should reconnect internally for
	// transient drops and return ErrSessionUnavailable when the session is gone.
	StreamEvents(ctx context.Context, sessionID, runID string) (<-chan RuntimeEvent, error)
	// Pause and Resume fail closed when unsupported. Cancel stops the last turn.
	Pause(ctx context.Context, sessionID string) error
	Resume(ctx context.Context, sessionID string) error
	Cancel(ctx context.Context, sessionID string) error
	// GetSession returns current session status.
	GetSession(ctx context.Context, sessionID string) (Session, error)
}
