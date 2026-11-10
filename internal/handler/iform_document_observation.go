package handler

import (
	"encoding/json"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func iformDocumentObservation(document map[string]any, id, milestone string, requireRoute bool) (json.RawMessage, error) {
	unavailable := func() (json.RawMessage, error) {
		return nil, apperrors.New("IFORM_DOCUMENT_STATUS_UNAVAILABLE", "ODO did not confirm document and route state", 503)
	}
	if document["id"] != id {
		return unavailable()
	}
	source, _ := document["status"].(string)
	phase, configured, routeComplete := "", false, false
	if raw, exists := document["workflow_plan"]; exists && raw != nil {
		plan, ok := raw.(map[string]any)
		if !ok {
			return unavailable()
		}
		phase, _ = plan["phase"].(string)
		stages, ok := plan["stages"].([]any)
		if !ok && plan["stages"] != nil {
			return unavailable()
		}
		configured = len(stages) > 0
		if configured {
			switch phase {
			case "planned", "registration", "approval", "review", "execution", "signing", "revision_required", "rejected", "cancelled", "completed":
			default:
				return unavailable()
			}
			if requireRoute && plan["document_id"] != id {
				return unavailable()
			}
			routeComplete = phase == "completed"
			if requireRoute && routeComplete {
				index, ok := plan["current_stage_index"].(float64)
				if !ok || index != float64(len(stages)) {
					return unavailable()
				}
			}
		}
	}
	state, outcome, message, valid := domain.IFormDocumentObservation(source, milestone, phase, configured)
	if !valid {
		return unavailable()
	}
	if requireRoute && !configured && outcome != "REJECTED" && outcome != "CANCELLED" {
		return unavailable()
	}
	return json.Marshal(map[string]any{"state": state, "outcome": outcome, "document_id": id, "source_status": source, "source_phase": phase, "source_stage": message, "completion_status": milestone, "require_route_completion": requireRoute, "workflow_configured": configured, "route_completed": routeComplete, "path": "/odo/documents/" + id})
}
