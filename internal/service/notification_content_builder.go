package service

import (
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

type notificationContent struct {
	Title             string
	MessageText       string
	ShortText         string
	SourceSystemName  string
	NotificationType  string
	Status            string
	ApplicationNumber string
	ActionURL         string
	Language          string
}

func normalizeNotificationLocale(value string) i18n.Locale {
	// Preserve the existing external notification API's English and kz aliases.
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en":
		return i18n.Locale("en")
	case "kz":
		return i18n.Kazakh
	default:
		return i18n.ResolveRecipient(value)
	}
}

func defaultSourceSystemName(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "E_OTINISH", "E-OTINISH", "EOTINISH":
		return "E-Otinish"
	case "SHEP", "ШЭП":
		return "ШЭП"
	case "ALEM":
		return "Team"
	default:
		return strings.TrimSpace(code)
	}
}

func buildNotificationContent(req domain.InternalNotificationRequest) notificationContent {
	locale := normalizeNotificationLocale(req.Language)
	translate := func(key string, params map[string]any) string {
		return i18n.Text(locale, "notification."+key, params)
	}
	notificationType := strings.ToUpper(strings.TrimSpace(req.NotificationType))
	status := strings.ToUpper(strings.TrimSpace(req.Status))
	sourceSystemName := strings.TrimSpace(req.SourceSystemName)
	if sourceSystemName == "" {
		sourceSystemName = defaultSourceSystemName(req.SourceSystemCode)
	}

	applicationNumber := strings.TrimSpace(req.ApplicationNumber)
	referenceID := applicationNumber
	if referenceID == "" {
		referenceID = strings.TrimSpace(req.ExternalMessageID)
	}

	if notificationType == "" {
		if applicationNumber != "" || req.ExternalMessageID != "" {
			notificationType = "APPLICATION_STATUS"
		} else {
			notificationType = "SYSTEM"
		}
	}
	if status == "" {
		if notificationType == "APPLICATION_STATUS" {
			status = "PROCESSED"
		} else {
			status = "INFO"
		}
	}

	title := strings.TrimSpace(req.Title)
	messageText := strings.TrimSpace(req.MessageText)
	shortText := strings.TrimSpace(req.ShortText)

	switch {
	case notificationType == "APPLICATION_STATUS" && (status == domain.NotificationStatusProcessed || status == "SUCCESS"):
		if title == "" {
			title = translate("ApplicationProcessed", nil)
		}
		if referenceID != "" {
			messageText = translate("ProcessedMessageWithID", map[string]any{"ID": referenceID})
		} else {
			messageText = translate("ProcessedMessageFallback", nil)
		}
	case notificationType == "APPLICATION_STATUS" && status == "PENDING":
		if title == "" {
			title = translate("ApplicationStatusChanged", nil)
		}
		if referenceID != "" {
			messageText = translate("PendingMessageWithID", map[string]any{"ID": referenceID})
		} else {
			messageText = translate("PendingMessageFallback", nil)
		}
	case notificationType == "RESPONSE" || notificationType == "APPEAL_RESPONSE":
		if title == "" {
			title = translate("ResponseReceived", nil)
		}
		if messageText == "" {
			messageText = translate("ResponseReceivedMessage", nil)
		}
	default:
		if title == "" {
			if sourceSystemName != "" {
				title = translate("SourceNotificationPattern", map[string]any{"Source": sourceSystemName})
			} else {
				title = translate("SystemNotification", nil)
			}
		}
		if messageText == "" {
			messageText = translate("ResponseReceivedMessage", nil)
		}
	}

	if shortText == "" {
		shortText = messageText
	}

	return notificationContent{
		Title:             title,
		MessageText:       messageText,
		ShortText:         shortText,
		SourceSystemName:  sourceSystemName,
		NotificationType:  notificationType,
		Status:            status,
		ApplicationNumber: applicationNumber,
		ActionURL:         strings.TrimSpace(req.ActionURL),
		Language:          string(locale),
	}
}

// Recipient templates must not inherit the request or sender's language.
func buildRecipientNotificationContent(req domain.InternalNotificationRequest, recipientLanguage string) notificationContent {
	req.Language = string(i18n.ResolveRecipient(recipientLanguage))
	return buildNotificationContent(req)
}
