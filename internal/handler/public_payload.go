package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// Only server diagnostic fields in known response DTOs are replaced. User text,
// documents, chat messages, and machine statuses are not inspected or translated.
func publicPayload(c *gin.Context, body any) any {
	friendly := apperrors.LocalizedMessage(apperrors.ErrCodeInternal, 500, middleware.RequestLocale(c))
	safeReason := func(reason *string) *string {
		if reason == nil || *reason == "" {
			return reason
		}
		value := friendly
		return &value
	}
	switch value := body.(type) {
	case StartPersonalRecordingResponse:
		value.FailureReason = safeReason(value.FailureReason)
		value.LiveKitErrorMessage = nil
		return value
	case StopPersonalRecordingResponse:
		value.FailureReason = safeReason(value.FailureReason)
		value.LiveKitErrorMessage = nil
		return value
	case PersonalRecordingResponse:
		value.FailureReason = safeReason(value.FailureReason)
		value.FailedReason = safeReason(value.FailedReason)
		value.LiveKitErrorMessage = nil
		return value
	case []PersonalRecordingResponse:
		if value == nil {
			return value
		}
		result := append(make([]PersonalRecordingResponse, 0, len(value)), value...)
		for index := range result {
			result[index] = publicPayload(c, result[index]).(PersonalRecordingResponse)
		}
		return result
	case []domain.Notification:
		if value == nil {
			return value
		}
		result := append(make([]domain.Notification, 0, len(value)), value...)
		for index := range result {
			result[index].ErrorMessage = safeReason(result[index].ErrorMessage)
		}
		return result
	case *domain.OrganizationIdentityProvider:
		if value == nil {
			return value
		}
		result := *value
		if result.LastError != "" {
			result.LastError = friendly
		}
		return &result
	case []domain.OrganizationIdentityProvider:
		if value == nil {
			return value
		}
		result := append(make([]domain.OrganizationIdentityProvider, 0, len(value)), value...)
		for index := range result {
			if result[index].LastError != "" {
				result[index].LastError = friendly
			}
		}
		return result
	case *domain.PublicationBroadcastStatsResponse:
		if value == nil {
			return value
		}
		result := *value
		if value.Broadcasts != nil {
			result.Broadcasts = append(make([]domain.BroadcastRecord, 0, len(value.Broadcasts)), value.Broadcasts...)
		}
		for index := range result.Broadcasts {
			result.Broadcasts[index].ErrorMessage = safeReason(result.Broadcasts[index].ErrorMessage)
		}
		return &result
	default:
		return body
	}
}
