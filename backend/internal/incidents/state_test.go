package incidents

import "testing"

func TestValidStates(t *testing.T) {
	for _, s := range []State{
		StateReceived, StateInvestigating, StatePlanning, StateValidating,
		StateAwaitingApproval, StateRemediating, StateVerifying, StateRecovered,
		StateEscalated, StateDenied, StateFailed, StateClosed,
	} {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
	if State("BOGUS").Valid() {
		t.Error("unknown state should be invalid")
	}
}

func TestHappyPathTransitions(t *testing.T) {
	path := []State{
		StateReceived, StateInvestigating, StatePlanning, StateValidating,
		StateAwaitingApproval, StateRemediating, StateVerifying, StateRecovered,
		StateClosed,
	}
	for i := 0; i < len(path)-1; i++ {
		if !path[i].CanTransition(path[i+1]) {
			t.Errorf("%s -> %s should be allowed", path[i], path[i+1])
		}
	}
}

func TestIllegalTransitionsRejected(t *testing.T) {
	illegal := [][2]State{
		{StateReceived, StateRemediating},       // cannot skip to remediation
		{StateAwaitingApproval, StateVerifying}, // cannot verify without remediating
		{StateClosed, StateInvestigating},       // terminal
		{StateRecovered, StateRemediating},      // no re-remediate after recovery
		{StateValidating, StateRemediating},     // must pass through approval
	}
	for _, tr := range illegal {
		if tr[0].CanTransition(tr[1]) {
			t.Errorf("%s -> %s should be rejected", tr[0], tr[1])
		}
	}
}

func TestTerminal(t *testing.T) {
	if !StateClosed.IsTerminal() {
		t.Error("CLOSED must be terminal")
	}
	if StateRecovered.IsTerminal() {
		t.Error("RECOVERED is not terminal (can close)")
	}
}
