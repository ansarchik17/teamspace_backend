package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIFormDocumentRequiresBothFileAndRouteConfirmation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	inputs := map[string]json.RawMessage{"workflow_plan": json.RawMessage(`{"stages":[{"kind":"signing"}]}`), "main_file": json.RawMessage(`{"sha256":"` + hash + `"}`)}
	for _, result := range []map[string]any{{}, {"route_confirmed": true}, {"main_file_confirmed": true, "main_file_sha256": hash}, {"route_confirmed": true, "main_file_confirmed": true, "main_file_sha256": strings.Repeat("b", 64)}} {
		if _, err := iformCreatedDocumentResult(inputs, result, "doc-test"); err == nil {
			t.Fatal("unconfirmed preparation accepted")
		}
	}
	if _, err := iformCreatedDocumentResult(inputs, map[string]any{"route_confirmed": true, "main_file_confirmed": true, "main_file_sha256": hash}, "doc-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := iformCreatedDocumentResult(nil, map[string]any{}, "doc-legacy"); err != nil {
		t.Fatal(err)
	}
}

func TestIFormDocumentFinishesOnlyExplicitFinalSigningStage(t *testing.T) {
	for _, tc := range []struct {
		status, phase, kind, policy string
		want                        bool
	}{
		{"signed", "signing", "signing", "completed", true},
		{"completed", "signing", "signing", "completed", true},
		{"waiting_for_signature", "signing", "signing", "completed", false},
		{"signed", "completed", "signing", "completed", false},
		{"signed", "signing", "signing", "", false},
		{"completed", "execution", "execution", "completed", false},
		{"completed", "execution", "signing", "completed", false},
	} {
		doc := map[string]any{"status": tc.status, "workflow_plan": map[string]any{"phase": tc.phase, "stages": []any{map[string]any{"kind": tc.kind, "completion_status": tc.policy}}}}
		if iformDocumentNeedsFinish(doc) != tc.want {
			t.Fatalf("unexpected finish decision: %+v", tc)
		}
	}
}
