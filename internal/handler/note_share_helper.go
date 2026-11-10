package handler

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

type noteShareMetadata struct {
	Kind   string `json:"kind"`
	Type   string `json:"type"`
	NoteID string `json:"note_id"`
	NoteId string `json:"noteId"`
}

func extractSharedNoteID(metadata *string) (uuid.UUID, bool) {
	if metadata == nil || strings.TrimSpace(*metadata) == "" {
		return uuid.Nil, false
	}

	var payload noteShareMetadata
	if err := json.Unmarshal([]byte(*metadata), &payload); err != nil {
		return uuid.Nil, false
	}

	if !strings.EqualFold(payload.Kind, "note") && !strings.EqualFold(payload.Type, "note") {
		return uuid.Nil, false
	}

	noteID := strings.TrimSpace(payload.NoteID)
	if noteID == "" {
		noteID = strings.TrimSpace(payload.NoteId)
	}
	if noteID == "" {
		return uuid.Nil, false
	}

	parsedID, err := uuid.Parse(noteID)
	if err != nil {
		return uuid.Nil, false
	}

	return parsedID, true
}

func (h *ChatHandler) maybeGrantSharedNoteAccessForConversation(ctx context.Context, metadata *string, conversationID uuid.UUID, userID int64) error {
	noteID, ok := extractSharedNoteID(metadata)
	if !ok {
		return nil
	}
	if h.noteService == nil {
		return apperrors.Internal("note service is unavailable")
	}

	participantIDs, err := h.chatService.GetConversationParticipantIDs(ctx, conversationID, userID)
	if err != nil {
		return err
	}

	if _, err := h.noteService.ShareNoteWithUsers(ctx, userID, noteID, participantIDs); err != nil {
		return err
	}

	return nil
}

func (h *GroupHandler) maybeGrantSharedNoteAccessForGroup(ctx context.Context, metadata *string, groupID uuid.UUID, userID int64) error {
	noteID, ok := extractSharedNoteID(metadata)
	if !ok {
		return nil
	}
	if h.noteService == nil {
		return apperrors.Internal("note service is unavailable")
	}

	memberIDs, err := h.groupService.GetGroupMemberIDs(ctx, groupID, userID)
	if err != nil {
		return err
	}

	if _, err := h.noteService.ShareNoteWithUsers(ctx, userID, noteID, memberIDs); err != nil {
		return err
	}

	return nil
}
