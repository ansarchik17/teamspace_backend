package handler

import (
	"encoding/json"
	"testing"
)

func TestIFormODOWaitsForWholeRoute(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"intermediate execution", `{"status":"completed","workflow_plan":{"phase":"review","stages":[{},{}]}}`, "WAITING"},
		{"waiting for signing", `{"status":"completed","workflow_plan":{"phase":"signing","stages":[{},{}]}}`, "WAITING"},
		{"fully signed route", `{"status":"signed","workflow_plan":{"phase":"completed","stages":[{},{}]}}`, "SUCCEEDED"},
		{"rejected route", `{"status":"rejected","workflow_plan":{"phase":"approval","stages":[{}]}}`, "FAILED"},
		{"premature manual close", `{"status":"closed","workflow_plan":{"phase":"execution","stages":[{}]}}`, "WAITING"},
		{"legacy completed document", `{"status":"completed"}`, "SUCCEEDED"},
		{"no configured stages", `{"status":"completed","workflow_plan":{"phase":"planned","stages":[]}}`, "SUCCEEDED"},
		{"unknown route state", `{"status":"completed","workflow_plan":{"stages":[{}]}}`, "WAITING"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var d map[string]interface{}
			if err := json.Unmarshal([]byte(tt.body), &d); err != nil {
				t.Fatal(err)
			}
			if got := iformODOWaitState(d); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
