// Package incidents persists incidents, their current lifecycle state, and an
// append-only history of every state transition and audit event.
package incidents

import "slices"

// State is a point in the incident lifecycle. The set and the legal transitions
// between them follow docs/SPEC.md section 12.
type State string

const (
	StateReceived         State = "RECEIVED"
	StateInvestigating    State = "INVESTIGATING"
	StatePlanning         State = "PLANNING"
	StateValidating       State = "VALIDATING"
	StateAwaitingApproval State = "AWAITING_APPROVAL"
	StateRemediating      State = "REMEDIATING"
	StateVerifying        State = "VERIFYING"
	StateRecovered        State = "RECOVERED"
	StateEscalated        State = "ESCALATED"
	StateDenied           State = "DENIED"
	StateFailed           State = "FAILED"
	StateClosed           State = "CLOSED"
)

// allowedTransitions maps each state to the states it may move to. A transition
// not listed here is rejected by the store.
var allowedTransitions = map[State][]State{
	StateReceived:         {StateInvestigating, StateFailed, StateClosed},
	StateInvestigating:    {StatePlanning, StateEscalated, StateFailed},
	StatePlanning:         {StateValidating, StateEscalated, StateFailed},
	StateValidating:       {StateAwaitingApproval, StateEscalated, StateFailed},
	StateAwaitingApproval: {StateRemediating, StateDenied, StatePlanning, StateEscalated},
	StateRemediating:      {StateVerifying, StateEscalated, StateFailed},
	StateVerifying:        {StateRecovered, StateEscalated, StateFailed, StateClosed},
	StateRecovered:        {StateClosed},
	StateDenied:           {StateEscalated, StateClosed},
	StateEscalated:        {StateClosed},
	StateFailed:           {StateEscalated, StateClosed},
	StateClosed:           {},
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	_, ok := allowedTransitions[s]
	return ok
}

// IsTerminal reports whether no further transition is possible.
func (s State) IsTerminal() bool {
	return s == StateClosed
}

// CanTransition reports whether moving from s to next is allowed.
func (s State) CanTransition(next State) bool {
	return slices.Contains(allowedTransitions[s], next)
}

// EventType classifies an entry in the incident history. Transitions use
// EventStateChange; non-transition audit records (tool calls, findings,
// decisions added by later tasks) use their own types.
type EventType string

const (
	EventStateChange EventType = "STATE_CHANGE"
	EventNote        EventType = "NOTE"
)

// EventCategory separates the kinds of timeline entries so the UI can present
// observed facts, hypotheses, decisions, and actions distinctly.
type EventCategory string

const (
	CategorySystem      EventCategory = "system"      // lifecycle/process events
	CategoryObservation EventCategory = "observation" // an observed fact / evidence
	CategoryHypothesis  EventCategory = "hypothesis"  // a proposed explanation
	CategoryDecision    EventCategory = "decision"    // a typed decision (e.g. Jev)
	CategoryAction      EventCategory = "action"      // an action taken or proposed
)

// Valid reports whether c is a known category.
func (c EventCategory) Valid() bool {
	switch c {
	case CategorySystem, CategoryObservation, CategoryHypothesis, CategoryDecision, CategoryAction:
		return true
	default:
		return false
	}
}
