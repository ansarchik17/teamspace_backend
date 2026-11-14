package agent

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type allowlistPermissionChecker struct{}

func NewPermissionChecker() PermissionChecker {
	return &allowlistPermissionChecker{}
}

func (c *allowlistPermissionChecker) Check(_ context.Context, user domain.AgentUserContext, intent *domain.AgentIntentResult, _ map[string]interface{}) *apperrors.AppError {
	if user.UserID <= 0 {
		return apperrors.Unauthorized("user context is required")
	}
	switch intent.Name {
	case domain.AgentIntentGetTasks, domain.AgentIntentGetTaskBoards, domain.AgentIntentGetTaskSummary,
		domain.AgentIntentGetCalendar, domain.AgentIntentFindSlots, domain.AgentIntentGetNotes,
		domain.AgentIntentGetNoteFolders, domain.AgentIntentGetDriveFiles, domain.AgentIntentGetDriveAccess,
		domain.AgentIntentCreateReminder, domain.AgentIntentCreateEvent, domain.AgentIntentSetReminder,
		domain.AgentIntentCreateMeeting, domain.AgentIntentCreateTask, domain.AgentIntentUpdateTask,
		domain.AgentIntentCreateNote, domain.AgentIntentUpdateNote, domain.AgentIntentDeleteNote,
		domain.AgentIntentShareDrive, domain.AgentIntentCreateDriveLink,
		domain.AgentIntentGetNotify, domain.AgentIntentOpenScreen, domain.AgentIntentSearch, domain.AgentIntentWebSearch,
		domain.AgentIntentStartCall, domain.AgentIntentSummarize, domain.AgentIntentFillForm,
		domain.AgentIntentGetStatus, domain.AgentIntentHelp, domain.AgentIntentGeneralChat:
		return nil
	default:
		return apperrors.Forbidden("intent is not allowed")
	}
}
