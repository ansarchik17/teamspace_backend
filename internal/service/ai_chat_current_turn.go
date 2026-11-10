package service

import (
	"regexp"
	"strings"

	"github.com/west6ide/workspace-backend/internal/alem"
)

// Explicit information requests must not resume a mutation from earlier turns.
// Requests combining lookup and a new action remain eligible for action tools.
var chatReadRequest = regexp.MustCompile(`^(?:какие|какой|какая|какое|сколько|покажи|покажите|перечисли|найди|найдите|список|what|which|how many|show|list|find)(?:\s|[?!.:,]|$)`)
var chatWriteRequest = regexp.MustCompile(`отмен|созда|создай|отправ|напиши|напиш|постав|назнач|запланир|закрой|закры|измени|перенес|перенёс|удали|напомни|\b(?:cancel|create|send|write|schedule|close|delete|update|reschedule|remind)\b`)

func chatCurrentTurnReadOnly(question string) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	return chatReadRequest.MatchString(text) && !chatWriteRequest.MatchString(text)
}

func chatToolsForCurrentTurn(tools []alem.FunctionTool, readOnly bool) []alem.FunctionTool {
	if !readOnly {
		return tools
	}
	filtered := make([]alem.FunctionTool, 0, len(tools))
	for _, tool := range tools {
		if _, action := mainChatActionContracts[tool.Function.Name]; !action {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func chatTargetResolutionTool(name string) bool {
	return name == "cancel_calendar_event" || name == "set_event_reminder" || name == "close_kanban_task"
}

// Only target lookup failures can be repaired internally. Missing message text,
// reminder timing and other user choices still require the user's input.
func chatTargetOnlyClarification(name string, result AIDataResult) bool {
	if result.Status != "clarification_required" || !chatTargetResolutionTool(name) {
		return false
	}
	return result.Message == (&chatActionDetailsRequired{[]string{"какую встречу отменить"}}).Error() ||
		result.Message == (&chatActionDetailsRequired{[]string{"конкретную встречу"}}).Error() ||
		result.Message == (&chatActionDetailsRequired{[]string{"к какой встрече относится напоминание"}}).Error() ||
		result.Message == (&chatActionDetailsRequired{[]string{"какую задачу закрыть"}}).Error()
}
