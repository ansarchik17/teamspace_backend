package service

import (
	"context"
	"strconv"

	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func (s *CalendarService) dispatchMeetingChanges(ctx context.Context) {
	if s.repo == nil || s.notifService == nil {
		return
	}
	changes, err := s.repo.PendingMeetingChanges(ctx)
	if err != nil {
		s.logger.Warn("calendar change queue unavailable", zap.Error(err))
		return
	}
	for _, item := range changes {
		title, content := "Место встречи изменено", "Изменилось место или онлайн-подключение встречи: %s"
		value := item.Metadata["event_title"]
		if item.Kind == "room.disabled" {
			title = "Переговорная отключена"
			content = "Переговорная %s отключена. Существующая бронь сохранена."
			value = item.Metadata["room_name"]
		}
		item.Metadata["change_id"] = strconv.FormatInt(item.ID, 10)
		req := domain.CreateNotificationRequest{UserID: item.UserID, Type: item.Kind, Title: title, TitleTemplate: &domain.NotificationTemplate{Source: title}, ContentTemplate: &domain.NotificationTemplate{Source: content, Args: []any{value}}, Metadata: item.Metadata}
		if err = s.notifService.CreateAndSendNotification(ctx, req); err != nil {
			s.logger.Warn("calendar change delivery failed", zap.Error(err))
			continue
		}
		if item.EventID != nil && item.Kind == "event.location_changed" {
			if event, loadErr := s.repo.GetByID(ctx, *item.EventID); loadErr == nil && event != nil {
				s.sendMeetingEmail(ctx, *event, item.UserID, event.Title, true, false)
			}
		}
		if err = s.repo.MarkMeetingChangeDelivered(ctx, item.ID); err != nil {
			s.logger.Warn("calendar change acknowledgement failed", zap.Error(err))
		}
	}
}
