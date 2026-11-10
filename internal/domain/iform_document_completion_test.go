package domain

import "testing"

func TestIFormDocumentCompletionUsesExplicitMilestone(t *testing.T) {
	for _, status := range []string{"draft", "registered", "on_approval", "approved", "in_execution", "waiting_for_signature", "signed", "completed", "closed", "archived", "rejected", "cancelled", "withdrawn", "unknown"} {
		want := "WAITING"
		switch status {
		case "completed":
			want = "SUCCEEDED"
		case "closed", "archived", "rejected", "cancelled", "withdrawn":
			want = "FAILED"
		}
		got, valid := IFormDocumentCompletion(status, "completed")
		if !valid || got != want {
			t.Fatalf("status=%s: %s valid=%t want=%s", status, got, valid, want)
		}
	}
	for _, status := range []string{"completed", "closed", "archived"} {
		if state, valid := IFormDocumentCompletion(status, ""); !valid || state != "SUCCEEDED" {
			t.Fatal("legacy process completion changed")
		}
	}
	if _, valid := IFormDocumentCompletion("completed", "any"); valid {
		t.Fatal("unknown milestone accepted")
	}
	if state, valid := IFormDocumentCompletion("closed", "archived"); !valid || state != "WAITING" {
		t.Fatal("closed prevented an explicit wait for archive")
	}
}

func TestIFormDocumentCompletionDoesNotSkipRemainingRoute(t *testing.T) {
	for _, phase := range []string{"", "planned", "registration", "execution", "approval", "review", "signing", "completed", "rejected", "cancelled"} {
		want := "WAITING"
		if phase == "completed" {
			want = "SUCCEEDED"
		} else if phase == "rejected" || phase == "cancelled" {
			want = "FAILED"
		}
		if state, valid := IFormDocumentRouteCompletion("completed", "completed", phase, true); !valid || state != want {
			t.Fatalf("phase=%s state=%s valid=%t want=%s", phase, state, valid, want)
		}
	}
	if state, _ := IFormDocumentRouteCompletion("completed", "completed", "", false); state != "SUCCEEDED" {
		t.Fatal("plain completed document unnecessarily required a workflow plan")
	}
	if state, _ := IFormDocumentRouteCompletion("completed", "", "signing", true); state != "SUCCEEDED" {
		t.Fatal("legacy process contract changed")
	}
}
