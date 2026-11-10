package handler

import (
	"encoding/json"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func iformDocumentNeedsFinish(document map[string]any) bool {
	status, _ := document["status"].(string)
	if status != "signed" && status != "completed" {
		return false
	}
	plan, _ := document["workflow_plan"].(map[string]any)
	if plan["phase"] != "signing" {
		return false
	}
	stages, _ := plan["stages"].([]any)
	if len(stages) == 0 {
		return false
	}
	if raw, exists := plan["current_stage_index"]; exists {
		index, valid := raw.(float64)
		if !valid || index != float64(len(stages)-1) {
			return false
		}
	} else if len(stages) != 1 {
		return false
	}
	final, _ := stages[len(stages)-1].(map[string]any)
	return final["kind"] == "signing" && final["completion_status"] == "completed"
}

// A successful card creation is insufficient when iForm requested a file/route.
// Older ODO versions must fail closed instead of silently dropping these fields.
func iformCreatedDocumentResult(inputs map[string]json.RawMessage, result map[string]any, id string) (json.RawMessage, error) {
	route, _ := result["route_confirmed"].(bool)
	file, _ := result["main_file_confirmed"].(bool)
	checksum, _ := result["main_file_sha256"].(string)
	if _, requested := inputs["workflow_plan"]; requested && !route {
		return nil, apperrors.New("IFORM_DOCUMENT_NOT_PREPARED", "ODO route not confirmed", 503)
	}
	if raw, requested := inputs["main_file"]; requested {
		var source struct {
			SHA256 string `json:"sha256"`
		}
		if json.Unmarshal(raw, &source) != nil || len(source.SHA256) != 64 || !file || checksum != source.SHA256 {
			return nil, apperrors.New("IFORM_DOCUMENT_NOT_PREPARED", "ODO main file not confirmed", 503)
		}
	}
	return json.Marshal(map[string]any{"state": "SUCCEEDED", "document_id": id, "path": "/odo/documents/" + id, "route_confirmed": route, "main_file_confirmed": file, "main_file_sha256": checksum})
}
