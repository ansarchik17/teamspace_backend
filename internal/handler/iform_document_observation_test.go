package handler

import (
	"encoding/json"
	"testing"
)

func observedDocument(status, phase string) map[string]any {
	return map[string]any{"id": "doc-test", "status": status, "registration_number": "synthetic-number", "workflow_plan": map[string]any{"document_id": "doc-test", "phase": phase, "stages": []any{map[string]any{"kind": "signing"}}, "current_stage_index": float64(1)}}
}
func TestIFormDocumentRequiresConfirmedRouteAndFinalStatus(t *testing.T) {
	for _, tc := range []struct{ status, phase, state string }{{"draft", "planned", "WAITING"}, {"registered", "planned", "WAITING"}, {"signed", "signing", "WAITING"}, {"completed", "signing", "WAITING"}, {"completed", "completed", "SUCCEEDED"}} {
		raw, err := iformDocumentObservation(observedDocument(tc.status, tc.phase), "doc-test", "completed", true)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		if result["state"] != tc.state {
			t.Fatalf("registration number or intermediate status completed the wait: %+v", result)
		}
	}
	for _, mutate := range []func(map[string]any){
		func(doc map[string]any) { delete(doc, "workflow_plan") }, func(doc map[string]any) { doc["id"] = "foreign-doc" }, func(doc map[string]any) { doc["status"] = "unknown" },
		func(doc map[string]any) { doc["workflow_plan"].(map[string]any)["stages"] = "invalid" },
		func(doc map[string]any) { doc["workflow_plan"].(map[string]any)["current_stage_index"] = float64(0) },
		func(doc map[string]any) { doc["workflow_plan"].(map[string]any)["document_id"] = "other-doc" },
	} {
		doc := observedDocument("completed", "completed")
		mutate(doc)
		if _, err := iformDocumentObservation(doc, "doc-test", "completed", true); err == nil {
			t.Fatal("unconfirmed final observation accepted")
		}
	}
}
