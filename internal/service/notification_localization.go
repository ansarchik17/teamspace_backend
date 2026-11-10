package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

func (s *NotificationService) recipientLocale(ctx context.Context, userID int64) i18n.Locale {
	if s.userRepo != nil {
		user, err := s.userRepo.GetByID(ctx, userID)
		if err == nil && user != nil {
			return i18n.ResolveRecipient(user.Language)
		}
	}
	return i18n.Russian
}

func renderNotificationTemplate(locale i18n.Locale, text *domain.NotificationTemplate, fallback string) string {
	if text == nil {
		return fallback
	}
	args := append([]any(nil), text.Args...)
	for i, arg := range args {
		if label, ok := arg.(domain.NotificationLabel); ok {
			args[i] = i18n.SystemNotification(locale, string(label))
		}
		if minutes, ok := arg.(domain.NotificationCountdown); ok {
			args[i] = localizedCountdown(locale, int(minutes))
		}
	}
	return i18n.SystemNotification(locale, text.Source, args...)
}

func localizedCountdown(locale i18n.Locale, minutes int) string {
	if locale == i18n.Russian {
		return FormatCountdownRU(minutes)
	}
	if minutes <= 0 {
		return i18n.SystemNotification(locale, "сейчас")
	}
	if minutes < 60 {
		return i18n.SystemNotification(locale, "через %s минут", strconv.Itoa(minutes))
	}
	if minutes%60 == 0 {
		return i18n.SystemNotification(locale, "через %s часов", strconv.Itoa(minutes/60))
	}
	return i18n.SystemNotification(locale, "через %s часов %s минут", strconv.Itoa(minutes/60), strconv.Itoa(minutes%60))
}

func cloneNotificationMetadata(meta map[string]string) map[string]string {
	copy := make(map[string]string, len(meta)+1)
	for key, value := range meta {
		copy[key] = value
	}
	return copy
}

func (s *NotificationService) localizeNotification(ctx context.Context, req domain.CreateNotificationRequest) (string, string, i18n.Locale) {
	locale := s.recipientLocale(ctx, req.UserID)
	title, content := localizeNotificationForLocale(req, locale)
	return title, content, locale
}

func localizeNotificationForLocale(req domain.CreateNotificationRequest, locale i18n.Locale) (string, string) {
	title, content := normalizeNotificationText(req.Type, req.Title, req.Content, req.Metadata)
	if req.TitleTemplate != nil {
		title = renderNotificationTemplate(locale, req.TitleTemplate, title)
	} else if req.Title == "" || title != strings.TrimSpace(req.Title) {
		switch req.Type {
		case "kanban_task_created":
			title = joinTitle(i18n.SystemNotification(locale, "Новая задача"), firstNonEmpty(req.Metadata["task_key"], req.Metadata["task_id"]))
		case "kanban_task_assigned":
			title = joinTitle(i18n.SystemNotification(locale, "Вам назначили задачу"), firstNonEmpty(req.Metadata["task_key"], req.Metadata["task_id"]))
		case "calendar_reminder":
			if eventTitle := firstNonEmpty(req.Metadata["event_title"], req.Metadata["title"]); eventTitle != "" {
				title = i18n.SystemNotification(locale, "Напоминание: «%s»", eventTitle)
			} else {
				title = i18n.SystemNotification(locale, "Напоминание о встрече")
			}
		default:
			title = i18n.SystemNotification(locale, title)
		}
	}
	if req.ContentTemplate != nil {
		content = renderNotificationTemplate(locale, req.ContentTemplate, content)
	} else if req.Content == "" {
		content = localizedNotificationDefault(locale, req.Type, req.Metadata, content)
	}
	return title, content
}

func localizedNotificationDefault(locale i18n.Locale, kind string, meta map[string]string, fallback string) string {
	switch kind {
	case "kanban_task_created":
		return i18n.SystemNotification(locale, "Создана задача %s на доске «%s»: %s", firstNonEmpty(meta["task_key"], meta["task_id"]), firstNonEmpty(meta["board_name"], "AlemBoard"), firstNonEmpty(meta["task_title"], i18n.SystemNotification(locale, "задача")))
	case "kanban_task_assigned":
		return i18n.SystemNotification(locale, "Проверьте задачу %s на доске «%s»: %s", firstNonEmpty(meta["task_key"], meta["task_id"]), firstNonEmpty(meta["board_name"], "AlemBoard"), firstNonEmpty(meta["task_title"], i18n.SystemNotification(locale, "задача")))
	case "calendar_reminder":
		return i18n.SystemNotification(locale, "Встреча «%s» начнется %s", firstNonEmpty(meta["event_title"], meta["title"], i18n.SystemNotification(locale, "Встреча")), i18n.SystemNotification(locale, "скоро"))
	}
	return i18n.SystemNotification(locale, fallback)
}

func (s *NotificationService) localizeCallPayload(ctx context.Context, userID int64, payload *callNotificationPayload) {
	locale := s.recipientLocale(ctx, userID)
	payload.Title = i18n.SystemNotification(locale, "Входящий звонок")
	payload.Body = i18n.SystemNotification(locale, "%s звонит вам", payload.FromName)
}
