package service

import (
	"strings"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func normalizeMessageAttachment(messageType string, attachment *MessageAttachmentInput) (*string, *string, *int64, error) {
	if attachment == nil || attachment.URL == nil || strings.TrimSpace(*attachment.URL) == "" {
		return nil, nil, nil, nil
	}

	url := strings.TrimSpace(*attachment.URL)

	var attachmentType *string
	if attachment.Type != nil && strings.TrimSpace(*attachment.Type) != "" {
		value := strings.TrimSpace(*attachment.Type)
		attachmentType = &value
	} else if strings.TrimSpace(messageType) != "" {
		value := strings.TrimSpace(messageType)
		attachmentType = &value
	}

	if attachment.Size != nil && *attachment.Size < 0 {
		return nil, nil, nil, apperrors.BadRequest("attachment_size cannot be negative")
	}

	return &url, attachmentType, attachment.Size, nil
}

func attachmentName(a *MessageAttachmentInput) *string {
	if a == nil || a.Name == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*a.Name)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func inferMessageType(attachmentType *string) string {
	if attachmentType == nil {
		return "text"
	}

	value := strings.ToLower(strings.TrimSpace(*attachmentType))
	switch {
	case strings.HasPrefix(value, "image"):
		return "image"
	case strings.HasPrefix(value, "video"):
		return "video"
	case strings.HasPrefix(value, "audio"):
		return "audio"
	case value == "voice":
		return "voice"
	case value == "video_message":
		return "video_message"
	default:
		return "file"
	}
}
