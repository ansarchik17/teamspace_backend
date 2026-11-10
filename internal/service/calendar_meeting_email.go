package service

import (
	"context"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func meetingDescription(event domain.Event) string {
	description := stringPtrValue(event.Description)
	if event.HasMeetingLink() {
		if description != "" {
			description += "\n\n"
		}
		description += stringPtrValue(event.MeetingURL)
	}
	return description
}
func (s *NotificationService) SendCalendarInviteWithMeeting(ctx context.Context, event domain.Event, userID int64, title string, isUpdate, isCancel bool, sequence int) {
	meetingURL := ""
	if event.HasMeetingLink() {
		meetingURL = strings.TrimSpace(stringPtrValue(event.MeetingURL))
	}
	s.sendCalendarInviteEmail(ctx, event.ID, userID, &event.UserID, title, meetingDescription(event), event.MeetingLocation(), event.StartTime, event.EndTime, isUpdate, isCancel, sequence, meetingURL)
}
func (s *CalendarService) sendMeetingEmail(ctx context.Context, event domain.Event, userID int64, title string, isUpdate, isCancel bool) {
	if s.notifService == nil {
		return
	}
	if sender, ok := s.notifService.(interface {
		SendCalendarInviteWithMeeting(context.Context, domain.Event, int64, string, bool, bool, int)
	}); ok {
		sender.SendCalendarInviteWithMeeting(ctx, event, userID, title, isUpdate, isCancel, 0)
		return
	}
	s.notifService.SendCalendarInviteEmail(ctx, event.ID, userID, &event.UserID, title, meetingDescription(event), event.MeetingLocation(), event.StartTime, event.EndTime, isUpdate, isCancel, 0)
}
