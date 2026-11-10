package domain

import "testing"

func TestIFormDocumentObservationSeparatesBusinessOutcomes(t *testing.T) {
	for _, tc := range []struct{ status, phase, state, outcome string }{
		{"draft", "planned", "WAITING", "WAITING"}, {"registered", "planned", "WAITING", "WAITING"},
		{"revision_required", "revision_required", "WAITING", "RETURNED"}, {"completed", "revision_required", "WAITING", "RETURNED"},
		{"completed", "execution", "WAITING", "WAITING"}, {"completed", "completed", "SUCCEEDED", "COMPLETED"},
		{"rejected", "approval", "FAILED", "REJECTED"}, {"signed", "rejected", "FAILED", "REJECTED"},
		{"cancelled", "signing", "FAILED", "CANCELLED"}, {"withdrawn", "planned", "FAILED", "CANCELLED"},
		{"closed", "completed", "FAILED", "BLOCKED"}, {"archived", "completed", "FAILED", "BLOCKED"},
	} {
		state, outcome, message, valid := IFormDocumentObservation(tc.status, "completed", tc.phase, true)
		if !valid || state != tc.state || outcome != tc.outcome || message == "" {
			t.Fatalf("%+v: %s %s %t", tc, state, outcome, valid)
		}
	}
	if _, _, _, valid := IFormDocumentObservation("unknown", "completed", "completed", true); valid {
		t.Fatal("unknown status accepted")
	}
}
