package service

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/west6ide/workspace-backend/internal/alem"
)

// Recover a single party only from successful, permission-scoped directory
// evidence in this turn and an alias literally present in user-authored input.
// CRM contacts, model answers and message bodies cannot supply this binding.
func chatBindDirectoryParty(call alem.ToolCall, evidence []aiReadEvidence, userText string) alem.ToolCall {
	field, ok := chatActionPartyField(call.Function.Name)
	if !ok {
		return call
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || args == nil {
		return call
	}
	if raw, exists := args[field.key]; exists {
		ids, ok := raw.([]any)
		if !ok || len(ids) > 1 || (field.key == "attendee_ids" && len(ids) == 0) {
			return call
		}
	}
	type match struct {
		id    float64
		alias string
	}
	matches := map[float64]match{}
	incomplete := false
	for _, source := range evidence {
		if source.Source.Dataset != "colleague_search" && source.Source.Dataset != "colleagues" {
			continue
		}
		var result AIDataResult
		if json.Unmarshal(source.Result, &result) != nil || result.Status != "ok" {
			continue
		}
		if result.Truncated || result.HasMoreText || source.Source.Truncated {
			incomplete = true
		}
		data := result.Data
		if object, ok := data.(map[string]any); ok {
			data = object["users"]
		}
		records, _ := data.([]any)
		if len(records) >= 25 || aiDataHasMore(result.Data) {
			incomplete = true
		}
		for _, raw := range records {
			record, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, ok := record["id"].(float64)
			if !ok || id <= 0 || id > 9007199254740991 || id != float64(int64(id)) {
				continue
			}
			aliases := []string{}
			for _, key := range []string{"username", "email", "full_name", "name"} {
				if text, ok := record[key].(string); ok && strings.TrimSpace(text) != "" {
					aliases = append(aliases, strings.TrimSpace(text))
				}
			}
			first, _ := record["first_name"].(string)
			last, _ := record["last_name"].(string)
			if first != "" && last != "" {
				aliases = append(aliases, first+" "+last)
			}
			for _, alias := range aliases {
				boundary := regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_.@-])` + regexp.QuoteMeta(alias) + `(?:$|[^\p{L}\p{N}_.@-])`)
				if boundary.MatchString(userText) {
					matches[id] = match{id, alias}
					break
				}
			}
		}
	}
	// Never resolve an ambiguous or truncated directory result by guessing.
	if incomplete || len(matches) != 1 {
		return call
	}
	for _, candidate := range matches {
		if ids, ok := args[field.key].([]any); ok && len(ids) == 1 && ids[0] != candidate.id {
			return call
		}
		args[field.key] = []any{candidate.id}
		args["participant_query"] = candidate.alias
	}
	encoded, err := json.Marshal(args)
	if err == nil {
		call.Function.Arguments = string(encoded)
	}
	return call
}

func chatPartyOnlyClarification(name string, result AIDataResult) bool {
	field, ok := chatActionPartyField(name)
	return ok && result.Status == "clarification_required" && result.Message == (&chatActionDetailsRequired{[]string{field.label}}).Error()
}
