package nvoken

import "testing"

// The classification is exhaustive over the contract's enum, so a status added
// later fails here until someone says which side it belongs on. Exporting the
// predicate means hosts stopped keeping their own copy, which makes this the
// copy that has to be right.
var classification = map[InvocationStatus]bool{
	InvocationQueued:     false,
	InvocationRunning:    false,
	InvocationWaiting:    false,
	InvocationBudgetHold: false,
	InvocationCompleted:  true,
	InvocationIncomplete: true,
	InvocationFailed:     true,
	InvocationCancelled:  true,
}

func TestIsTerminalStatus(t *testing.T) {
	for status, want := range classification {
		if got := IsTerminalStatus(status); got != want {
			t.Errorf("IsTerminalStatus(%q) = %t, want %t", status, got, want)
		}
	}
	if len(TerminalInvocationStatuses) != 4 {
		t.Fatalf("TerminalInvocationStatuses has %d entries, want 4", len(TerminalInvocationStatuses))
	}
}

// A budget-held turn stopped on spending capacity with its deadlines on hold. It
// still owns the Session and resumes on its own once the account is funded, so
// a caller that reads it as over abandons a turn that is still going.
func TestBudgetHoldIsNotTerminal(t *testing.T) {
	if IsTerminalStatus(InvocationBudgetHold) {
		t.Fatal("budget-held reported as terminal")
	}
}

func TestUnrecognizedStatusIsNotTerminal(t *testing.T) {
	if IsTerminalStatus(InvocationStatus("some_status_added_later")) {
		t.Fatal("unrecognized status reported as terminal")
	}
}

// Both witnesses are present and agreeing in normal operation. The status alone
// covers a server too old to send the field; the field alone covers a status
// this build has never heard of.
func TestIsTurnOverAcceptsEitherWitness(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		change InvocationChange
		want   bool
	}{
		{"both", InvocationChange{Status: InvocationCompleted, Terminal: true}, true},
		{"status only", InvocationChange{Status: InvocationCompleted}, true},
		{"field only", InvocationChange{Status: "something_new", Terminal: true}, true},
		{"replayed running", InvocationChange{Status: InvocationRunning}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsTurnOver(testCase.change); got != testCase.want {
				t.Errorf("IsTurnOver = %t, want %t", got, testCase.want)
			}
		})
	}
}
