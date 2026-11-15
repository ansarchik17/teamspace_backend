package service

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type NotificationSender interface {
	CreateAndSendNotification(ctx context.Context, req domain.CreateNotificationRequest) error
	SendCalendarEventEmail(toEmail string, userName string, title string, description string, start, end time.Time)
	SendCalendarEventNotification(ctx context.Context, userID int64, organizerID *int64, toEmail string, userName string, title string, description string, start, end time.Time, eventID string)
	SendCalendarInviteEmail(ctx context.Context, eventID uuid.UUID, attendeeID int64, organizerID *int64, title, description, location string, start, end time.Time, isUpdate, isCancel bool, sequence int)
}

type calendarInviteResponderRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Event, error)
	IsAttendee(ctx context.Context, eventID uuid.UUID, userID int64) (bool, error)
	RemoveAttendee(ctx context.Context, eventID uuid.UUID, userID int64) error
	DeleteReminder(ctx context.Context, eventID uuid.UUID, userID int64) error
	UpdateAttendeeStatus(ctx context.Context, eventID uuid.UUID, userID int64, status string, comment *string) (previousStatus string, changed bool, err error)
	MarkInviteNotificationsAsRead(ctx context.Context, userID int64, eventID string) error
}

type calendarInviteUserRepo interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
}

type CalendarService struct {
	repo                   *repository.CalendarRepository
	taskRepo               *repository.TaskRepository
	userRepo               *repository.UserRepository
	notifService           NotificationSender
	googleCalService       *GoogleCalendarService
	appleCalService        *AppleCalendarService
	availabilityService    *CalendarAvailabilityService
	meetingProviderService *CalendarMeetingProviderService
	gamification           *GamificationService // новое поле
	eventPublisher         EventPublisher
	logger                 *zap.Logger
}

type CalendarMeetingJoinResult struct {
	EventID            uuid.UUID
	CallID             string
	Title              string
	RoomName           string
	Token              string
	URL                string
	MeetingURL         string
	ParticipantsCount  int
	MeetingProvider    string
	OnlineMeetingState string
}

type calendarEventPayload struct {
	EventID         uuid.UUID `json:"event_id"`
	OrganizerID     int64     `json:"organizer_id"`
	Title           string    `json:"title"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	IsOnline        bool      `json:"is_online"`
	AttendeeIDs     []int64   `json:"attendee_ids,omitempty"`
	ReminderMinutes *int      `json:"reminder_minutes,omitempty"`
}

type calendarInviteResponsePayload struct {
	EventID     uuid.UUID `json:"event_id"`
	UserID      int64     `json:"user_id"`
	OrganizerID int64     `json:"organizer_id"`
	Status      string    `json:"status"`
	Comment     *string   `json:"comment,omitempty"`
	RespondedAt time.Time `json:"responded_at"`
}

type calendarReminderPayload struct {
	EventID       uuid.UUID `json:"event_id"`
	UserID        int64     `json:"user_id"`
	MinutesBefore *int      `json:"minutes_before,omitempty"`
}

func NewCalendarService(
	repo *repository.CalendarRepository,
	taskRepo *repository.TaskRepository,
	notifService NotificationSender,
	userRepo *repository.UserRepository,
	googleCalService *GoogleCalendarService,
	appleCalService *AppleCalendarService,
	availabilityService *CalendarAvailabilityService,
	meetingProviderService *CalendarMeetingProviderService,
	gamification *GamificationService,
	eventPublisher EventPublisher,
	logger *zap.Logger,
) *CalendarService {
	return &CalendarService{
		repo:                   repo,
		taskRepo:               taskRepo,
		notifService:           notifService,
		userRepo:               userRepo,
		googleCalService:       googleCalService,
		appleCalService:        appleCalService,
		availabilityService:    availabilityService,
		meetingProviderService: meetingProviderService,
		gamification:           gamification,
		eventPublisher:         eventPublisher,
		logger:                 logger.With(zap.String("service", "calendar")),
	}
}

func (s *CalendarService) publishCalendarEvent(routingKey string, event *domain.Event, reminderMinutes *int) {
	if event == nil {
		return
	}

	attendeeIDs := make([]int64, 0, len(event.Attendees))
	for _, attendee := range event.Attendees {
		attendeeIDs = append(attendeeIDs, attendee.UserID)
	}

	publishEventAsync(s.logger, s.eventPublisher, routingKey, calendarEventPayload{
		EventID:         event.ID,
		OrganizerID:     event.UserID,
		Title:           event.Title,
		StartTime:       event.StartTime,
		EndTime:         event.EndTime,
		IsOnline:        event.IsOnline,
		AttendeeIDs:     attendeeIDs,
		ReminderMinutes: reminderMinutes,
	}, zap.String("event_id", event.ID.String()))
}

func (s *CalendarService) publishInviteResponse(event *domain.Event, userID int64, req *domain.RespondToInviteRequest) {
	if event == nil || req == nil {
		return
	}

	publishEventAsync(s.logger, s.eventPublisher, "calendar.invite.responded", calendarInviteResponsePayload{
		EventID:     event.ID,
		UserID:      userID,
		OrganizerID: event.UserID,
		Status:      strings.ToLower(strings.TrimSpace(req.Status)),
		Comment:     req.Comment,
		RespondedAt: time.Now().UTC(),
	}, zap.String("event_id", event.ID.String()), zap.Int64("user_id", userID))
}

func (s *CalendarService) publishReminderEvent(routingKey string, eventID uuid.UUID, userID int64, minutesBefore *int) {
	publishEventAsync(s.logger, s.eventPublisher, routingKey, calendarReminderPayload{
		EventID:       eventID,
		UserID:        userID,
		MinutesBefore: minutesBefore,
	}, zap.String("event_id", eventID.String()), zap.Int64("user_id", userID))
}

func (s *CalendarService) CreateEvent(ctx context.Context, userID int64, req *domain.CreateEventRequest) (*domain.Event, error) {
	return s.CreateEventWithOptions(ctx, userID, req, false)
}

func (s *CalendarService) ListCalendars(ctx context.Context, userID int64) ([]domain.UserCalendar, error) {
	items, err := s.repo.ListCalendars(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось загрузить календари", err)
	}
	return items, nil
}

func (s *CalendarService) CreateCalendar(ctx context.Context, userID int64, req *domain.CreateUserCalendarRequest) (*domain.UserCalendar, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("Название календаря обязательно")
	}
	if len([]rune(name)) > 120 {
		return nil, apperrors.BadRequest("Название календаря слишком длинное")
	}
	item, err := s.repo.CreateCalendar(ctx, userID, name, req.Color)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось создать календарь", err)
	}
	return item, nil
}

func (s *CalendarService) CreateEventWithOptions(ctx context.Context, userID int64, req *domain.CreateEventRequest, force bool) (*domain.Event, error) {
	if err := ensureUsersExist(ctx, s.userRepo, append([]int64{userID}, req.AttendeeIDs...)); err != nil {
		return nil, err
	}

	if !req.EndTime.After(req.StartTime) {
		return nil, apperrors.BadRequest("Время завершения события должно быть позже времени начала")
	}
	if err := s.ensureAttendeesWithinScope(ctx, userID, req.AttendeeIDs); err != nil {
		return nil, err
	}
	calendar, err := s.repo.EnsureDefaultCalendar(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось загрузить календарь", err)
	}
	if req.CalendarID != nil {
		calendar, err = s.repo.GetOwnedCalendar(ctx, userID, *req.CalendarID)
		if err != nil {
			return nil, apperrors.InternalWithErr("Не удалось проверить календарь", err)
		}
		if calendar == nil {
			return nil, apperrors.BadRequest("Выбранный календарь не найден")
		}
	}
	if !force && s.availabilityService != nil {
		availability, err := s.availabilityService.CheckAvailability(ctx, userID, domain.CalendarAvailabilityRequest{
			StartTime:   req.StartTime,
			EndTime:     req.EndTime,
			AttendeeIDs: req.AttendeeIDs,
		})
		if err != nil {
			return nil, err
		}
		if !availability.IsAvailable {
			return nil, apperrors.Conflict("calendar_conflict").WithDetails(map[string]any{
				"conflicts":       availability.Conflicts,
				"suggested_slots": availability.SuggestedSlots,
			})
		}
	}

	rule, rruleInterval, rruleUntil, err := parseCalendarRRULE(req.RecurrenceRule)
	if err != nil {
		return nil, apperrors.BadRequest("Неверный формат повторения события")
	}

	interval := 1
	if rruleInterval != nil {
		interval = *rruleInterval
	}
	if req.RecurrenceInterval != nil {
		if *req.RecurrenceInterval < 1 || *req.RecurrenceInterval > 3650 {
			return nil, apperrors.BadRequest("Интервал повторения должен быть от 1 до 3650")
		}
		interval = *req.RecurrenceInterval
	}

	var until *time.Time
	if rule != nil {
		until = rruleUntil
		if req.RecurrenceUntil != nil {
			until = req.RecurrenceUntil
		}
		if until != nil && until.Before(req.StartTime) {
			return nil, apperrors.BadRequest("Дата окончания повторений не может быть раньше даты начала события")
		}
	}

	// Check the physical room before provisioning an external online meeting.
	if req.RoomID != nil {
		room, roomErr := s.getCompanyRoom(ctx, userID, *req.RoomID)
		if roomErr != nil {
			return nil, roomErr
		}
		if !room.IsActive {
			return nil, apperrors.RoomInactive()
		}
	}
	meetingInfo := domain.CalendarMeetingInfo{Provider: "none", Status: "none"}
	hostUser, attendees := s.loadEventParticipants(ctx, userID, req.AttendeeIDs)
	eventID := uuid.New()
	if req.IsOnline && s.meetingProviderService != nil {
		reqProvider := ""
		if req.MeetingProvider != nil {
			reqProvider = strings.TrimSpace(strings.ToLower(*req.MeetingProvider))
		}
		if reqProvider != "" && reqProvider != "qmeet" && reqProvider != "livekit" && reqProvider != "cisco" {
			return nil, apperrors.BadRequest("Некорректный провайдер видеовстречи")
		}
		if reqProvider == "cisco" && hostUser != nil {
			meetingInfo = s.meetingProviderService.DetermineMeetingInfo(CalendarMeetingCreateRequest{
				EventID:           eventID,
				Title:             req.Title,
				StartTime:         req.StartTime,
				EndTime:           req.EndTime,
				Organizer:         hostUser,
				Attendees:         attendees,
				TotalParticipants: countCalendarParticipants(userID, req.AttendeeIDs),
			})
		} else if reqProvider == "livekit" && hostUser != nil {
			info, err := s.meetingProviderService.liveKitProvider.CreateMeeting(ctx, CalendarMeetingCreateRequest{EventID: eventID, Title: req.Title})
			if err != nil {
				return nil, apperrors.InternalWithErr("Не удалось создать видеовстречу", err)
			}
			meetingInfo = *info
		} else {
			meetingInfo = s.meetingProviderService.GenerateQMeetRoomInfo(eventID, req.Title)
		}
	}

	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "Asia/Almaty"
	}
	if err := ValidateCalendarTimezone(tz); err != nil {
		return nil, err
	}

	event := &domain.Event{
		ID:                 eventID,
		UserID:             userID,
		CalendarID:         calendar.ID,
		Title:              req.Title,
		Description:        req.Description,
		StartTime:          req.StartTime,
		EndTime:            req.EndTime,
		Timezone:           tz,
		Location:           req.Location,
		DefaultAttendance:  req.DefaultAttendance,
		IsAllDay:           req.IsAllDay,
		IsOnline:           req.IsOnline,
		RecurrenceRule:     rule,
		RecurrenceInterval: interval,
		RecurrenceUntil:    until,
		Color:              req.Color,
		MeetingProvider:    meetingInfo.Provider,
		MeetingRoomID:      meetingInfo.MeetingRoomID,
		MeetingURL:         meetingInfo.MeetingURL,
		LiveKitRoomName:    meetingInfo.LiveKitRoomName,
		CiscoMeetingID:     meetingInfo.CiscoMeetingID,
		CiscoJoinURL:       meetingInfo.CiscoJoinURL,
		MeetingStatus:      meetingInfo.Status,
		URL:                req.URL,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if event.Color == nil || strings.TrimSpace(*event.Color) == "" {
		event.Color = &calendar.Color
	}

	if req.IsOnline && event.MeetingProvider == "livekit" {
		roomReq := CalendarMeetingCreateRequest{
			EventID:           event.ID,
			Title:             req.Title,
			StartTime:         req.StartTime,
			EndTime:           req.EndTime,
			Organizer:         hostUser,
			Attendees:         attendees,
			TotalParticipants: countCalendarParticipants(userID, req.AttendeeIDs),
		}
		if info, err := s.meetingProviderService.Provision(ctx, roomReq); err == nil && info != nil {
			event.MeetingProvider = info.Provider
			event.MeetingRoomID = info.MeetingRoomID
			event.MeetingURL = info.MeetingURL
			event.LiveKitRoomName = info.LiveKitRoomName
			event.CiscoMeetingID = info.CiscoMeetingID
			event.CiscoJoinURL = info.CiscoJoinURL
			event.MeetingStatus = info.Status
		}
	}
	if s.meetingProviderService != nil {
		s.meetingProviderService.NormalizeEventMeetingInfo(event)
	}

	if event.IsOnline && event.MeetingURL == nil {
		info := s.meetingProviderService.GenerateQMeetRoomInfo(event.ID, event.Title)
		event.MeetingProvider = info.Provider
		event.MeetingRoomID = info.MeetingRoomID
		event.MeetingURL = info.MeetingURL
		event.LiveKitRoomName = info.LiveKitRoomName
		event.MeetingStatus = info.Status
	}
	if req.RoomID != nil {
		room, err := s.getCompanyRoom(ctx, userID, *req.RoomID)
		if err != nil {
			return nil, calendarOperationError(err, "Не удалось проверить переговорную")
		}
		if room == nil {
			return nil, apperrors.RoomNotFound()
		}
		if !room.IsActive {
			return nil, apperrors.RoomInactive()
		}

		event.RoomID = req.RoomID
		event.Room = room

	}
	if !event.AttendanceAllowed(event.DefaultAttendance) {
		return nil, apperrors.New("ATTENDANCE_NOT_ALLOWED", "attendance not allowed", 422)
	}
	roomBookings, err := buildEventRoomBookings(*event, nil)
	if err != nil {
		return nil, err
	}

	if err := s.repo.CreateEventWithRoomBookings(ctx, event, req.AttendeeIDs, req.ReminderMinutes, roomBookings, req.ReminderMinutesSet); err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, apperrors.InternalWithErr("Не удалось сохранить событие", err)
	}

	if stored, err := s.repo.GetByIDForUser(ctx, event.ID, userID); err != nil {
		return nil, err
	} else if stored != nil {
		event = stored
	}
	// Гамификация: создание встречи
	publishEventAsync(s.logger, s.eventPublisher, "calendar.event.created", calendarEventPayload{
		EventID:         event.ID,
		OrganizerID:     event.UserID,
		Title:           event.Title,
		StartTime:       event.StartTime,
		EndTime:         event.EndTime,
		IsOnline:        event.IsOnline,
		AttendeeIDs:     append([]int64(nil), req.AttendeeIDs...),
		ReminderMinutes: req.ReminderMinutes,
	}, zap.String("event_id", event.ID.String()))

	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionMeetingCreated) }()
	}

	if req.ReminderMinutes != nil && s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionEventReminder) }()
	}

	if req.IsOnline && event.MeetingProvider == "cisco" && s.meetingProviderService != nil && hostUser != nil {
		go s.provisionDeferredCalendarMeeting(event.ID, CalendarMeetingCreateRequest{
			EventID:           event.ID,
			Title:             req.Title,
			StartTime:         req.StartTime,
			EndTime:           req.EndTime,
			Organizer:         hostUser,
			Attendees:         attendees,
			TotalParticipants: countCalendarParticipants(userID, req.AttendeeIDs),
		})
	}

	// Persist invitations before returning success. Push/email delivery may still
	// fail independently, but recipients can recover the in-app invitation.
	{
		inviteCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		creatorName := "Коллега"
		if hostUser != nil {
			creatorName = strings.TrimSpace(hostUser.FirstName + " " + hostUser.LastName)
			if creatorName == "" {
				creatorName = hostUser.Username
			}
		}

		seenInvitees := make(map[int64]bool, len(req.AttendeeIDs))
		for _, attendeeID := range req.AttendeeIDs {
			// Explicitly skip if attendee is the creator/organizer
			if attendeeID == userID || attendeeID == event.UserID || seenInvitees[attendeeID] {
				continue
			}
			seenInvitees[attendeeID] = true

			s.logger.Info("Checking attendee", zap.Int64("attendee", attendeeID), zap.Int64("creator", userID))

			notifReq := domain.CreateNotificationRequest{
				UserID:          attendeeID,
				OrganizerID:     &userID,
				Type:            "calendar_invite",
				TitleTemplate:   &domain.NotificationTemplate{Source: "Приглашение на встречу", Args: []any{}},
				Title:           "Приглашение на встречу",
				ContentTemplate: &domain.NotificationTemplate{Source: "%s пригласил вас на встречу: %s. Место: %s. Онлайн: %s", Args: []any{creatorName, event.Title, event.MeetingLocation(), stringPtrValue(event.MeetingURL)}},
				Content:         fmt.Sprintf("%s пригласил вас на встречу: %s", creatorName, event.Title),
				Metadata: map[string]string{
					"event_id":    event.ID.String(),
					"start_time":  event.StartTime.Format(time.RFC3339),
					"location":    event.MeetingLocation(),
					"meeting_url": stringPtrValue(event.MeetingURL),
				},
			}

			if s.notifService == nil {
				continue
			}
			go func(copy domain.Event, uid int64) {
				mailCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				s.sendMeetingEmail(mailCtx, copy, uid, copy.Title, false, false)
			}(*event, attendeeID)
			if err := s.notifService.CreateAndSendNotification(inviteCtx, notifReq); err != nil {
				s.logger.Error("failed to send calendar notification",
					zap.Int64("attendee_id", attendeeID),
					zap.String("event_id", event.ID.String()),
					zap.Error(err),
				)
			}
		}
	}

	s.syncCreatedEventToLinkedCalendars(userID, event)
	FormatEventLocalTimes(event, event.Timezone)

	return event, nil
}

func getSafeLocation(loc *string) string {
	if loc == nil {
		return ""
	}
	return *loc
}

func countCalendarParticipants(organizerID int64, attendeeIDs []int64) int {
	seen := map[int64]bool{organizerID: true}
	total := 1
	for _, attendeeID := range attendeeIDs {
		if attendeeID <= 0 || seen[attendeeID] {
			continue
		}
		seen[attendeeID] = true
		total++
	}
	return total
}

func (s *CalendarService) loadEventParticipants(ctx context.Context, organizerID int64, attendeeIDs []int64) (*domain.User, []*domain.User) {
	hostUser, err := s.userRepo.GetByID(ctx, organizerID)
	if err != nil || hostUser == nil {
		return nil, nil
	}
	if len(attendeeIDs) == 0 {
		return hostUser, []*domain.User{}
	}
	attendees, err := s.userRepo.GetByIDs(ctx, attendeeIDs)
	if err != nil {
		return hostUser, []*domain.User{}
	}
	return hostUser, attendees
}

func (s *CalendarService) provisionDeferredCalendarMeeting(eventID uuid.UUID, req CalendarMeetingCreateRequest) {
	asyncCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	info, err := s.meetingProviderService.Provision(asyncCtx, req)
	if err != nil {
		s.logger.Warn("failed to provision deferred calendar meeting", zap.String("event_id", eventID.String()), zap.Error(err))
		_ = s.repo.UpdateMeetingInfo(asyncCtx, eventID, domain.CalendarMeetingInfo{Provider: "cisco", Status: "failed"})
		return
	}
	if info == nil {
		return
	}
	if s.meetingProviderService != nil {
		event := &domain.Event{
			IsOnline:        true,
			Location:        nil,
			MeetingProvider: info.Provider,
			MeetingRoomID:   info.MeetingRoomID,
			MeetingURL:      info.MeetingURL,
			LiveKitRoomName: info.LiveKitRoomName,
			CiscoMeetingID:  info.CiscoMeetingID,
			CiscoJoinURL:    info.CiscoJoinURL,
			MeetingStatus:   info.Status,
		}
		s.meetingProviderService.NormalizeEventMeetingInfo(event)
		info.Provider = event.MeetingProvider
		info.MeetingRoomID = event.MeetingRoomID
		info.MeetingURL = event.MeetingURL
		info.LiveKitRoomName = event.LiveKitRoomName
		info.CiscoMeetingID = event.CiscoMeetingID
		info.CiscoJoinURL = event.CiscoJoinURL
		info.Status = event.MeetingStatus
	}
	if err := s.repo.UpdateMeetingInfo(asyncCtx, eventID, *info); err != nil {
		s.logger.Warn("failed to persist deferred meeting info", zap.String("event_id", eventID.String()), zap.Error(err))
	}
}

func (s *CalendarService) CheckAvailability(ctx context.Context, userID int64, req domain.CalendarAvailabilityRequest) (*domain.CalendarAvailabilityResponse, error) {
	if s.availabilityService == nil {
		return nil, apperrors.Internal("calendar availability service is not configured")
	}
	return s.availabilityService.CheckAvailability(ctx, userID, req)
}

func (s *CalendarService) SuggestTimes(ctx context.Context, userID int64, req domain.CalendarSuggestTimesRequest) (*domain.CalendarSuggestTimesResponse, error) {
	if s.availabilityService == nil {
		return nil, apperrors.Internal("calendar availability service is not configured")
	}
	return s.availabilityService.SuggestTimes(ctx, userID, req)
}

func (s *CalendarService) GetFreeBusy(ctx context.Context, userID int64, req domain.CalendarFreeBusyRequest) (*domain.CalendarFreeBusyResponse, error) {
	if s.availabilityService == nil {
		return nil, apperrors.Internal("calendar availability service is not configured")
	}
	return s.availabilityService.GetFreeBusy(ctx, userID, req)
}

func (s *CalendarService) syncCreatedEventToLinkedCalendars(userID int64, event *domain.Event) {
	if event == nil {
		return
	}

	if s.googleCalService != nil {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if err := s.googleCalService.SyncEventToGoogle(syncCtx, userID, event); err != nil {
				s.logger.Error("failed to sync event to Google Calendar",
					zap.Int64("user_id", userID),
					zap.String("event_id", event.ID.String()),
					zap.Error(err),
				)
			}
		}()
	}

	if s.appleCalService != nil {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if err := s.appleCalService.SyncEventToApple(syncCtx, userID, event); err != nil {
				s.logger.Error("failed to sync event to Apple Calendar",
					zap.Int64("user_id", userID),
					zap.String("event_id", event.ID.String()),
					zap.Error(err),
				)
			}
		}()
	}
}

func (s *CalendarService) SearchEvents(
	ctx context.Context,
	userID int64,
	title string,
	from, to time.Time,
	limit int,
) ([]domain.Event, error) {

	title = strings.TrimSpace(title)
	if title == "" {
		return nil, apperrors.BadRequest("title is required")
	}
	if to.Before(from) {
		return nil, apperrors.BadRequest("to must be after from")
	}

	events, err := s.repo.SearchByTitleAndTime(ctx, userID, title, from, to, limit)
	if err != nil {
		return nil, err
	}

	// Optimization: Batch fetch users
	userIDs := make([]int64, 0)
	seen := make(map[int64]bool)

	for _, e := range events {
		for _, att := range e.Attendees {
			if !seen[att.UserID] {
				userIDs = append(userIDs, att.UserID)
				seen[att.UserID] = true
			}
		}
	}

	if len(userIDs) > 0 {
		users, err := s.userRepo.GetByIDs(ctx, userIDs)
		if err != nil {
			s.logger.Error("failed to batch fetch users", zap.Error(err))
			// Continue without users rather than failing entirely? Or just log.
		}

		userMap := make(map[int64]*domain.UserResponse)
		for _, u := range users {
			userMap[u.ID] = u.ToResponse()
		}

		for ei := range events {
			for ai, att := range events[ei].Attendees {
				if u, ok := userMap[att.UserID]; ok {
					events[ei].Attendees[ai].User = u
				}
			}
		}
	}

	for i := range events {
		if s.meetingProviderService != nil {
			s.meetingProviderService.NormalizeEventMeetingInfo(&events[i])
		}
	}

	return events, nil
}

func (s *CalendarService) SearchUsers(ctx context.Context, userID int64, query string, limit, offset int) ([]*domain.UserResponse, error) {
	query = strings.TrimSpace(query)

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	if offset < 0 {
		offset = 0
	}

	users, err := s.userRepo.SearchVisibleTo(ctx, userID, query, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to search calendar users", err)
	}

	responses := make([]*domain.UserResponse, 0, len(users))
	for _, user := range users {
		responses = append(responses, user.ToResponse())
	}

	return responses, nil
}

func (s *CalendarService) UpdateEvent(ctx context.Context, userID int64, eventID uuid.UUID, req *domain.UpdateEventRequest) (*domain.Event, error) {
	force := req != nil && req.Force != nil && *req.Force
	return s.UpdateEventWithOptions(ctx, userID, eventID, req, force)
}

func (s *CalendarService) UpdateEventWithOptions(ctx context.Context, userID int64, eventID uuid.UUID, req *domain.UpdateEventRequest, force bool) (*domain.Event, error) {
	if err := s.ensureAttendeesWithinScope(ctx, userID, req.AttendeeIDs); err != nil {
		return nil, err
	}

	event, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Ошибка получения события", err)
	}
	if event == nil {
		return nil, apperrors.NotFound("Событие не найдено")
	}
	if event.UserID != userID {
		return nil, apperrors.Forbidden("Только организатор может редактировать эту встречу")
	}
	if req.CalendarID != nil {
		calendar, err := s.repo.GetOwnedCalendar(ctx, userID, *req.CalendarID)
		if err != nil {
			return nil, apperrors.InternalWithErr("Не удалось проверить календарь", err)
		}
		if calendar == nil {
			return nil, apperrors.BadRequest("Выбранный календарь не найден")
		}
		event.CalendarID = calendar.ID
		if req.Color == nil {
			event.Color = &calendar.Color
		}
	}

	targetAttendeeIDs := req.AttendeeIDs
	if targetAttendeeIDs == nil {
		targetAttendeeIDs = make([]int64, 0, len(event.Attendees))
		for _, att := range event.Attendees {
			targetAttendeeIDs = append(targetAttendeeIDs, att.UserID)
		}
	}

	if req.StartTime != nil || req.EndTime != nil || req.AttendeeIDs != nil {
		newStart := event.StartTime
		newEnd := event.EndTime
		if req.StartTime != nil {
			newStart = *req.StartTime
		}
		if req.EndTime != nil {
			newEnd = *req.EndTime
		}

		if newEnd.Before(newStart) {
			return nil, apperrors.BadRequest("Время завершения не может быть раньше времени начала")
		}

		if !force && s.availabilityService != nil {
			availability, err := s.availabilityService.CheckAvailability(ctx, userID, domain.CalendarAvailabilityRequest{
				StartTime:      newStart,
				EndTime:        newEnd,
				AttendeeIDs:    targetAttendeeIDs,
				ExcludeEventID: &eventID,
			})
			if err != nil {
				return nil, err
			}
			if !availability.IsAvailable {
				return nil, apperrors.Conflict("calendar_conflict").WithDetails(map[string]any{
					"conflicts":       availability.Conflicts,
					"alerts":          availability.Alerts,
					"suggested_slots": availability.SuggestedSlots,
				})
			}
		}
	}

	if req.Title != nil {
		event.Title = *req.Title
	}
	if req.Description != nil {
		event.Description = req.Description
	}
	if req.StartTime != nil {
		event.StartTime = *req.StartTime
	}
	if req.EndTime != nil {
		event.EndTime = *req.EndTime
	}
	if req.Location != nil || req.LocationSet {
		event.Location = req.Location
	}
	if req.Timezone != nil {
		if err := ValidateCalendarTimezone(*req.Timezone); err != nil {
			return nil, err
		}
		event.Timezone = strings.TrimSpace(*req.Timezone)
	}

	var deferredMeetingReq *CalendarMeetingCreateRequest
	if req.MeetingProvider != nil && strings.TrimSpace(*req.MeetingProvider) != "" && s.meetingProviderService != nil {
		provider := strings.TrimSpace(strings.ToLower(*req.MeetingProvider))
		if provider != "qmeet" && provider != "livekit" && provider != "cisco" && provider != "none" {
			return nil, apperrors.BadRequest("Некорректный провайдер видеовстречи")
		}
		if provider == "qmeet" {
			event.IsOnline = true
			qmeetInfo := s.meetingProviderService.GenerateQMeetRoomInfo(event.ID, event.Title)
			event.MeetingProvider = qmeetInfo.Provider
			event.MeetingRoomID = qmeetInfo.MeetingRoomID
			event.MeetingURL = qmeetInfo.MeetingURL
			event.LiveKitRoomName = qmeetInfo.LiveKitRoomName
			event.MeetingStatus = qmeetInfo.Status
		}
	}
	if req.IsAllDay != nil {
		event.IsAllDay = *req.IsAllDay
	}
	if req.IsOnline != nil {
		event.IsOnline = *req.IsOnline
		if !event.IsOnline {
			event.MeetingProvider = ""
			event.MeetingRoomID, event.MeetingURL, event.LiveKitRoomName = nil, nil, nil
			event.CiscoMeetingID, event.CiscoJoinURL = nil, nil
			event.MeetingStatus = ""
		}

		if event.IsOnline && s.meetingProviderService != nil && event.MeetingProvider != "qmeet" {
			hostUser, attendees := s.loadEventParticipants(ctx, userID, targetAttendeeIDs)
			if hostUser != nil {
				meetingReq := CalendarMeetingCreateRequest{
					EventID:           event.ID,
					Title:             event.Title,
					StartTime:         event.StartTime,
					EndTime:           event.EndTime,
					Organizer:         hostUser,
					Attendees:         attendees,
					TotalParticipants: countCalendarParticipants(userID, targetAttendeeIDs),
				}
				meetingInfo := s.meetingProviderService.DetermineMeetingInfo(meetingReq)
				event.MeetingProvider = meetingInfo.Provider
				event.MeetingRoomID = meetingInfo.MeetingRoomID
				event.MeetingURL = meetingInfo.MeetingURL
				event.LiveKitRoomName = meetingInfo.LiveKitRoomName
				event.CiscoMeetingID = meetingInfo.CiscoMeetingID
				event.CiscoJoinURL = meetingInfo.CiscoJoinURL
				event.MeetingStatus = meetingInfo.Status

				if meetingInfo.Provider == "livekit" {
					if info, err := s.meetingProviderService.Provision(ctx, meetingReq); err == nil && info != nil {
						event.MeetingProvider = info.Provider
						event.MeetingRoomID = info.MeetingRoomID
						event.MeetingURL = info.MeetingURL
						event.LiveKitRoomName = info.LiveKitRoomName
						event.CiscoMeetingID = info.CiscoMeetingID
						event.CiscoJoinURL = info.CiscoJoinURL
						event.MeetingStatus = info.Status
					}
				} else if meetingInfo.Provider == "cisco" {
					reqCopy := meetingReq
					deferredMeetingReq = &reqCopy
				}
			}
		}
	}
	if s.meetingProviderService != nil {
		s.meetingProviderService.NormalizeEventMeetingInfo(event)
	}
	if req.Color != nil {
		event.Color = req.Color
	}
	if req.URL != nil {
		event.URL = req.URL
	}

	if req.RecurrenceRule != nil {
		parsedRule, parsedInterval, parsedUntil, parseErr := parseCalendarRRULE(req.RecurrenceRule)
		if parseErr != nil {
			return nil, apperrors.BadRequest("Неверный формат повторения")
		}
		event.RecurrenceRule = parsedRule
		if parsedInterval != nil {
			event.RecurrenceInterval = *parsedInterval
		}
		if parsedUntil != nil {
			event.RecurrenceUntil = parsedUntil
		}
		if event.RecurrenceRule == nil {
			event.RecurrenceUntil = nil
			event.RecurrenceInterval = 1
		}
	}

	if req.RecurrenceInterval != nil {
		if *req.RecurrenceInterval < 1 || *req.RecurrenceInterval > 3650 {
			return nil, apperrors.BadRequest("Интервал повторения должен быть от 1 до 3650")
		}
		event.RecurrenceInterval = *req.RecurrenceInterval
	}

	if req.RecurrenceUntil != nil {
		if event.RecurrenceRule == nil {
			return nil, apperrors.BadRequest("Нельзя установить дату окончания повтора без правила повторения")
		}
		event.RecurrenceUntil = req.RecurrenceUntil
	}

	if !event.EndTime.After(event.StartTime) {
		return nil, apperrors.BadRequest("Время завершения должно быть позже времени начала")
	}
	if event.RecurrenceRule != nil && event.RecurrenceUntil != nil && event.RecurrenceUntil.Before(event.StartTime) {
		return nil, apperrors.BadRequest("Дата окончания повтора не может быть раньше начала события")
	}

	expectedUpdatedAt := event.UpdatedAt
	event.UpdatedAt = time.Now()

	if !event.IsOnline {
		event.MeetingProvider = "none"
		event.MeetingURL = nil
		event.MeetingRoomID = nil
		event.LiveKitRoomName = nil
		event.CiscoMeetingID = nil
		event.CiscoJoinURL = nil
		event.MeetingStatus = "none"
	} else if event.MeetingURL == nil {
		info := s.meetingProviderService.GenerateQMeetRoomInfo(event.ID, event.Title)
		event.MeetingProvider = info.Provider
		event.MeetingRoomID = info.MeetingRoomID
		event.MeetingURL = info.MeetingURL
		event.LiveKitRoomName = info.LiveKitRoomName
		event.MeetingStatus = info.Status
	}
	if req.RoomIDSet && req.RoomID == nil && !req.LocationSet && event.Room != nil && stringPtrValue(event.Location) == event.Room.Name {
		event.Location = nil
	}
	targetRoomID := event.RoomID
	if req.RoomID != nil || req.RoomIDSet {
		targetRoomID = req.RoomID
		if req.RoomID != nil && *req.RoomID == uuid.Nil {
			targetRoomID = nil
		}
	}
	if targetRoomID != nil {
		room, err := s.getCompanyRoom(ctx, userID, *targetRoomID)
		if err != nil {
			return nil, calendarOperationError(err, "Не удалось проверить переговорную")
		}
		if room == nil {
			return nil, apperrors.RoomNotFound()
		}
		if !room.IsActive && (event.RoomID == nil || *event.RoomID != *targetRoomID) {
			return nil, apperrors.RoomInactive()
		}
		event.RoomID = targetRoomID
		event.Room = room

	} else {
		event.RoomID = nil
		event.Room = nil
	}

	event.DefaultAttendance = req.DefaultAttendance
	if !event.AttendanceAllowed(event.DefaultAttendance) {
		return nil, apperrors.New("ATTENDANCE_NOT_ALLOWED", "attendance not allowed", 422)
	}
	plan := repository.RoomBookingPlan{ExpectedUpdatedAt: &expectedUpdatedAt, Build: func(exceptions []domain.CalendarEventException) ([]domain.RoomBooking, error) {
		return buildEventRoomBookings(*event, exceptions)
	}}
	plan.ReminderMinutesSet = req.ReminderMinutesSet || req.ReminderMinutes != nil
	plan.ReminderMinutes = req.ReminderMinutes
	if err := s.repo.UpdateEventWithRoomBookings(ctx, event, req.AttendeeIDs, nil, plan); err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, apperrors.InternalWithErr("Ошибка обновления события", err)
	}
	if event.IsOnline && s.meetingProviderService != nil {
		if err := s.repo.UpdateMeetingInfo(ctx, event.ID, domain.CalendarMeetingInfo{
			Provider:        event.MeetingProvider,
			MeetingRoomID:   event.MeetingRoomID,
			MeetingURL:      event.MeetingURL,
			LiveKitRoomName: event.LiveKitRoomName,
			CiscoMeetingID:  event.CiscoMeetingID,
			CiscoJoinURL:    event.CiscoJoinURL,
			Status:          event.MeetingStatus,
		}); err != nil {
			return nil, apperrors.InternalWithErr("Ошибка обновления онлайн-встречи", err)
		}
	}
	if deferredMeetingReq != nil {
		go s.provisionDeferredCalendarMeeting(event.ID, *deferredMeetingReq)
	}

	if req.AttendeeIDs != nil {
		// Update preserves RSVP rows; reload the response and event payload so
		// clients do not receive the pre-edit attendee list.
		if refreshed, loadErr := s.repo.GetByID(ctx, event.ID); loadErr == nil && refreshed != nil {
			event.Attendees = refreshed.Attendees
		} else if loadErr != nil {
			s.logger.Warn("failed to reload attendees after event update", zap.Error(loadErr))
		}
	}

	if stored, err := s.repo.GetByIDForUser(ctx, event.ID, userID); err != nil {
		return nil, err
	} else if stored != nil {
		event = stored
	}
	s.dispatchMeetingChanges(ctx)
	s.publishCalendarEvent("calendar.event.updated", event, req.ReminderMinutes)

	go func() {
		if s.notifService == nil {
			return
		}
		asyncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var targetUserIDs []int64

		if req.AttendeeIDs != nil {
			targetUserIDs = req.AttendeeIDs
		} else {
			for _, att := range event.Attendees {
				targetUserIDs = append(targetUserIDs, att.UserID)
			}
		}

		creatorName := "Организатор"
		creator, _ := s.userRepo.GetByID(asyncCtx, userID)
		if creator != nil {
			creatorName = strings.TrimSpace(creator.FirstName + " " + creator.LastName)
			if creatorName == "" {
				creatorName = creator.Username
			}
		}

		for _, uid := range targetUserIDs {
			if uid == userID {
				continue
			}

			notifReq := domain.CreateNotificationRequest{
				UserID:          uid,
				OrganizerID:     &userID,
				Type:            "calendar_event_updated",
				TitleTemplate:   &domain.NotificationTemplate{Source: "Встреча обновлена", Args: []any{}},
				Title:           "Встреча обновлена",
				ContentTemplate: &domain.NotificationTemplate{Source: "%s обновил встречу: %s", Args: []any{creatorName, event.Title}},
				Content:         fmt.Sprintf("%s обновил встречу: %s", creatorName, event.Title),
				Metadata: map[string]string{
					"event_id":   event.ID.String(),
					"start_time": event.StartTime.Format(time.RFC3339),
				},
			}

			if err := s.notifService.CreateAndSendNotification(asyncCtx, notifReq); err != nil {
				s.logger.Error("failed to send update notification",
					zap.Int64("user_id", uid),
					zap.String("event_id", event.ID.String()),
					zap.Error(err),
				)
			}
		}
	}()

	// Sync to Google Calendar if enabled
	if s.googleCalService != nil {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if err := s.googleCalService.SyncEventToGoogle(syncCtx, userID, event); err != nil {
				s.logger.Error("failed to sync updated event to Google Calendar",
					zap.Int64("user_id", userID),
					zap.String("event_id", event.ID.String()),
					zap.Error(err),
				)
			}
		}()
	}

	FormatEventLocalTimes(event, event.Timezone)
	return event, nil
}

func (s *CalendarService) DeleteEvent(ctx context.Context, userID int64, eventID uuid.UUID) error {
	event, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return apperrors.InternalWithErr("failed to get event", err)
	}
	if event == nil {
		return calendarEventNotFoundError()
	}

	attendeesToNotify := event.Attendees
	eventTitle := event.Title
	eventStart := event.StartTime
	ownerID := event.UserID

	ownerDeleted, err := s.repo.DeleteForUser(ctx, eventID, userID)
	if err != nil {
		if err.Error() == "event not found" {
			return calendarEventNotFoundError()
		}
		if err.Error() == "permission denied" {
			return calendarEventAccessDeniedError()
		}
		return apperrors.InternalWithErr("failed to delete event", err)
	}

	if !ownerDeleted {
		return nil
	}

	s.publishCalendarEvent("calendar.event.deleted", event, nil)

	go func() {
		if len(attendeesToNotify) == 0 {
			return
		}

		asyncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		creatorName := "Организатор"
		creator, _ := s.userRepo.GetByID(asyncCtx, userID)
		if creator != nil {
			creatorName = strings.TrimSpace(creator.FirstName + " " + creator.LastName)
			if creatorName == "" {
				creatorName = creator.Username
			}
		}

		for _, att := range attendeesToNotify {
			if att.UserID == userID {
				continue
			}

			_ = s.notifService.CreateAndSendNotification(asyncCtx, domain.CreateNotificationRequest{
				UserID:          att.UserID,
				OrganizerID:     &userID,
				Type:            "calendar_event_deleted",
				TitleTemplate:   &domain.NotificationTemplate{Source: "Встреча отменена", Args: []any{}},
				Title:           "Встреча отменена",
				ContentTemplate: &domain.NotificationTemplate{Source: "%s отменил встречу: %s", Args: []any{creatorName, eventTitle}},
				Content:         fmt.Sprintf("%s отменил встречу: %s", creatorName, eventTitle),
				Metadata: map[string]string{
					"event_id":            eventID.String(),
					"original_start_time": eventStart.Format(time.RFC3339),
					"owner_id":            fmt.Sprintf("%d", ownerID),
				},
			})
		}
	}()

	// Delete from Google Calendar if enabled
	if s.googleCalService != nil && ownerDeleted {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if err := s.googleCalService.DeleteEventFromGoogle(syncCtx, userID, eventID); err != nil {
				s.logger.Error("failed to delete event from Google Calendar",
					zap.Int64("user_id", userID),
					zap.String("event_id", eventID.String()),
					zap.Error(err),
				)
			}
		}()
	}

	// Delete from Apple Calendar if enabled
	if s.appleCalService != nil && ownerDeleted {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if err := s.appleCalService.DeleteEventFromApple(syncCtx, userID, eventID); err != nil {
				s.logger.Error("failed to delete event from Apple Calendar",
					zap.Int64("user_id", userID),
					zap.String("event_id", eventID.String()),
					zap.Error(err),
				)
			}
		}()
	}

	return nil
}

func (s *CalendarService) GetEvent(ctx context.Context, userID int64, eventID uuid.UUID) (*domain.Event, error) {
	event, err := s.repo.GetByIDForUser(ctx, eventID, userID)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, calendarEventNotFoundError()
	}

	if !userCanAccessEvent(userID, event) {
		return nil, calendarEventNotFoundError()
	}

	s.attachEventOwnerIdentity(ctx, event)

	// Batch fetch attendees
	attendeeIDs := make([]int64, len(event.Attendees))
	for i, att := range event.Attendees {
		attendeeIDs[i] = att.UserID
	}
	if len(attendeeIDs) > 0 {
		users, err := s.userRepo.GetByIDs(ctx, attendeeIDs)
		if err == nil {
			uMap := make(map[int64]*domain.UserResponse)
			for _, u := range users {
				uMap[u.ID] = u.ToResponse()
			}
			for i, att := range event.Attendees {
				if u, ok := uMap[att.UserID]; ok {
					event.Attendees[i].User = u
				}
			}
		} else {
			s.logger.Error("failed to batch users", zap.Error(err))
		}
	}

	// Set IsOrganizer flag for mobile apps
	event.IsOrganizer = (event.UserID == userID)
	redactCalendarAttendees(event.Attendees, userID, event.UserID)
	if s.meetingProviderService != nil {
		s.meetingProviderService.NormalizeEventMeetingInfo(event)
	}
	FormatEventLocalTimes(event, event.Timezone)

	return event, nil
}

func (s *CalendarService) RejoinLiveKitMeetingByRoomName(
	ctx context.Context,
	userID int64,
	userName string,
	roomName string,
	tokenCtx LiveKitTokenContext,
) (*CalendarMeetingJoinResult, error) {
	if s == nil || s.repo == nil {
		return nil, apperrors.Internal("calendar service is not configured")
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return nil, apperrors.Validation("room_name is required")
	}

	event, err := s.repo.GetByLiveKitRoomName(ctx, roomName)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return s.RejoinLiveKitMeetingByRoomNameBypass(ctx, userID, userName, roomName, tokenCtx)
	}

	if !userCanAccessEvent(userID, event) {
		return nil, calendarEventAccessDeniedError()
	}
	if !event.IsOnline {
		return nil, apperrors.Conflict("calendar event is not an online meeting")
	}

	if s.meetingProviderService == nil {
		return nil, apperrors.Internal("meeting provider service is not configured")
	}

	s.meetingProviderService.NormalizeEventMeetingInfo(event)
	if strings.TrimSpace(event.MeetingProvider) != "livekit" && strings.TrimSpace(event.MeetingProvider) != "qmeet" {
		return nil, apperrors.Conflict("calendar meeting is not backed by LiveKit / QMeet")
	}

	resolvedRoomName := strings.TrimSpace(stringPtrValue(event.LiveKitRoomName))
	if resolvedRoomName == "" {
		resolvedRoomName = strings.TrimSpace(stringPtrValue(event.MeetingRoomID))
	}
	if resolvedRoomName == "" {
		resolvedRoomName = roomName
	}

	participantsCount, participantsErr := s.meetingProviderService.CountLiveKitParticipants(ctx, resolvedRoomName)
	if participantsErr != nil {
		s.logger.Warn("failed to list livekit calendar participants",
			zap.String("room_name", resolvedRoomName),
			zap.String("event_id", event.ID.String()),
			zap.Error(participantsErr),
		)
	}

	token, url, err := s.meetingProviderService.GenerateLiveKitJoinToken(resolvedRoomName, userID, userName, tokenCtx)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to generate calendar meeting token", err)
	}

	return &CalendarMeetingJoinResult{
		EventID:            event.ID,
		CallID:             "",
		Title:              event.Title,
		RoomName:           resolvedRoomName,
		Token:              token,
		URL:                url,
		MeetingURL:         stringPtrValue(event.MeetingURL),
		ParticipantsCount:  participantsCount,
		MeetingProvider:    event.MeetingProvider,
		OnlineMeetingState: event.MeetingStatus,
	}, nil
}

func (s *CalendarService) RejoinLiveKitMeetingByRoomNameBypass(
	ctx context.Context,
	userID int64,
	userName string,
	roomName string,
	tokenCtx LiveKitTokenContext,
) (*CalendarMeetingJoinResult, error) {
	if s == nil || s.meetingProviderService == nil {
		return nil, apperrors.Internal("meeting provider service is not configured")
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return nil, apperrors.Validation("room_name is required")
	}

	participantsCount, participantsErr := s.meetingProviderService.CountLiveKitParticipants(ctx, roomName)
	if participantsErr != nil {
		s.logger.Warn("failed to list livekit calendar participants during bypass",
			zap.String("room_name", roomName),
			zap.Error(participantsErr),
		)
	}

	token, url, err := s.meetingProviderService.GenerateLiveKitJoinToken(roomName, userID, userName, tokenCtx)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to generate calendar meeting token", err)
	}

	result := &CalendarMeetingJoinResult{
		RoomName:           roomName,
		Token:              token,
		URL:                url,
		ParticipantsCount:  participantsCount,
		MeetingProvider:    "livekit",
		OnlineMeetingState: "unknown",
	}

	if s.repo != nil {
		if event, eventErr := s.repo.GetByLiveKitRoomName(ctx, roomName); eventErr == nil && event != nil {
			result.EventID = event.ID
			result.Title = event.Title
			if strings.TrimSpace(event.MeetingProvider) != "" {
				result.MeetingProvider = event.MeetingProvider
			}
			if strings.TrimSpace(event.MeetingStatus) != "" {
				result.OnlineMeetingState = event.MeetingStatus
			}
		}
	}

	return result, nil
}

func (s *CalendarService) GetEventByOwner(ctx context.Context, viewerID, ownerID int64, eventID uuid.UUID) (*domain.Event, error) {
	if err := ensureUsersExist(ctx, s.userRepo, []int64{viewerID, ownerID}); err != nil {
		return nil, err
	}

	event, err := s.repo.GetByIDForUser(ctx, eventID, ownerID)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, calendarEventNotFoundError()
	}

	if !eventVisibleInUserCalendar(ownerID, event) {
		return nil, calendarEventNotFoundError()
	}

	s.attachEventOwnerIdentity(ctx, event)

	// Batch fetch attendees
	attendeeIDs := make([]int64, len(event.Attendees))
	for i, att := range event.Attendees {
		attendeeIDs[i] = att.UserID
	}
	if len(attendeeIDs) > 0 {
		users, err := s.userRepo.GetByIDs(ctx, attendeeIDs)
		if err == nil {
			uMap := make(map[int64]*domain.UserResponse)
			for _, u := range users {
				uMap[u.ID] = u.ToResponse()
			}
			for i, att := range event.Attendees {
				if u, ok := uMap[att.UserID]; ok {
					event.Attendees[i].User = u
				}
			}
		} else {
			s.logger.Error("failed to batch users", zap.Error(err))
		}
	}

	// Set IsOrganizer flag for mobile apps (based on viewerID)
	event.IsOrganizer = (event.UserID == viewerID)
	redactCalendarAttendees(event.Attendees, viewerID, event.UserID)

	s.sanitizeEventForViewer(event, viewerID)
	if s.meetingProviderService != nil {
		s.meetingProviderService.NormalizeEventMeetingInfo(event)
	}

	return event, nil
}

func (s *CalendarService) ListEvents(ctx context.Context, viewerID, targetUserID int64, from, to time.Time) ([]domain.Event, error) {
	if err := ensureUsersExist(ctx, s.userRepo, []int64{viewerID, targetUserID}); err != nil {
		return nil, err
	}

	// Optimized List call: returns only events targetUserID is involved in (not declined),
	// and populates Attendees with ONLY the viewer's record if viewer is participating.
	base, err := s.repo.List(ctx, targetUserID, viewerID, from, to)
	if err != nil {
		return nil, err
	}

	out, err := s.expandEventsWithExceptions(ctx, base, from, to)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to expand calendar events", err)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].StartTime.Before(out[j].StartTime) })

	for i := range out {
		// Set IsOrganizer flag for mobile apps
		out[i].IsOrganizer = (out[i].UserID == viewerID)
		s.sanitizeEventForViewer(&out[i], viewerID)
		if s.meetingProviderService != nil {
			s.meetingProviderService.NormalizeEventMeetingInfo(&out[i])
		}
		FormatEventLocalTimes(&out[i], out[i].Timezone)
	}

	return out, nil
}

func (s *CalendarService) sanitizeEventForViewer(e *domain.Event, viewerID int64) {
	redactCalendarAttendees(e.Attendees, viewerID, e.UserID)
	if e.UserID == viewerID {
		return
	}

	for _, att := range e.Attendees {
		if att.UserID == viewerID {
			return
		}
	}

	e.Title = "Занят"
	e.Description = nil
	e.Location = nil
	e.Room = nil
	e.RoomID = nil
	e.MeetingURL = nil
	e.URL = nil
	e.CiscoJoinURL = nil
	e.CiscoMeetingID = nil
	e.LiveKitRoomName = nil
	e.MeetingRoomID = nil
	e.AttendanceSummary = domain.AttendanceSummary{}
	e.Warnings = nil

	e.Attendees = nil
}

func calendarEventNotFoundError() *apperrors.AppError {
	return apperrors.New(apperrors.ErrCodeNotFound, "Событие не найдено", http.StatusNotFound)
}

func calendarEventAccessDeniedError() *apperrors.AppError {
	return apperrors.New(apperrors.ErrCodeForbidden, "У вас нет доступа к этому событию", http.StatusForbidden)
}

func userCanAccessEvent(userID int64, event *domain.Event) bool {
	if event == nil {
		return false
	}
	if event.UserID == userID {
		return true
	}
	for _, attendee := range event.Attendees {
		if attendee.UserID == userID {
			return true
		}
	}
	return false
}

func eventVisibleInUserCalendar(ownerID int64, event *domain.Event) bool {
	if event == nil {
		return false
	}
	if event.UserID == ownerID {
		return true
	}
	for _, attendee := range event.Attendees {
		if attendee.UserID == ownerID {
			return true
		}
	}
	return false
}

func (s *CalendarService) attachEventOwnerIdentity(ctx context.Context, event *domain.Event) {
	if s == nil || s.userRepo == nil || event == nil || event.UserID <= 0 {
		return
	}

	user, err := s.userRepo.GetByID(ctx, event.UserID)
	if err != nil || user == nil {
		if err != nil {
			s.logger.Debug("failed to load event owner identity",
				zap.Int64("user_id", event.UserID),
				zap.Error(err),
			)
		}
		return
	}

	setEventOwnerIdentityFromUser(event, user)
}

func setEventOwnerIdentityFromUser(event *domain.Event, user *domain.User) {
	if event == nil || user == nil {
		return
	}

	username := strings.TrimSpace(user.Username)
	firstName := strings.TrimSpace(user.FirstName)
	lastName := strings.TrimSpace(user.LastName)

	if username != "" {
		event.Username = &username
	}
	if firstName != "" {
		event.FirstName = &firstName
	}
	if lastName != "" {
		event.LastName = &lastName
	}
}

type BatchConflict struct {
	Email  string    `json:"email"`
	UserID int64     `json:"user_id"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Reason string    `json:"reason"`
}

func (s *CalendarService) BatchCreateEvents(ctx context.Context, organizerID int64, req *domain.BatchCreateEventRequest) ([]*domain.Event, error) {
	if organizerID <= 0 {
		return nil, apperrors.Unauthorized("authentication required to create calendar events")
	}
	if req == nil {
		return nil, apperrors.Validation("invalid request body")
	}
	organizer, err := s.userRepo.GetByID(ctx, organizerID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check calendar owner", err)
	}
	if organizer == nil || !organizer.IsActive {
		return nil, apperrors.Forbidden("calendar owner is unavailable")
	}
	// Check the entire batch before writing anything. There is no delegated
	// calendar-write permission in this API; colleagues are invited through
	// CreateEvent with attendee_ids, never impersonated as event organizers.
	ownerEmail := strings.ToLower(strings.TrimSpace(organizer.Email))
	for _, item := range req.Items {
		if ownerEmail == "" || strings.ToLower(strings.TrimSpace(item.Mail)) != ownerEmail {
			return nil, apperrors.Forbidden("cannot create events in another user's calendar")
		}
	}

	start, end, err := s.parseBatchDates(req.DateFrom, req.DateTo)
	if err != nil {
		return nil, err
	}

	createdEvents := make([]*domain.Event, 0, len(req.Items))
	conflicts := make([]BatchConflict, 0)

	for _, item := range req.Items {
		item.Mail = strings.ToLower(strings.TrimSpace(item.Mail))

		event, c, err := s.processBatchItemWithConflict(ctx, organizerID, item, start, end)
		if err != nil {
			s.logger.Warn("skipping batch event item", zap.String("email", item.Mail), zap.Error(err))
			continue
		}
		if c != nil {
			conflicts = append(conflicts, *c)
			continue
		}
		if event != nil {
			createdEvents = append(createdEvents, event)
		}
	}

	if len(conflicts) > 0 {
		return nil, apperrors.Conflict("events overlap").WithDetails(map[string]any{
			"conflicts": conflicts,
		})
	}

	return createdEvents, nil
}

func (s *CalendarService) processBatchItemWithConflict(
	ctx context.Context,
	organizerID int64,
	item domain.BatchEventItem,
	start, end time.Time,
) (*domain.Event, *BatchConflict, error) {

	hasOverlap, err := s.repo.HasOverlap(ctx, organizerID, start, end, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("check overlap failed: %w", err)
	}
	if hasOverlap {
		return nil, &BatchConflict{
			Email:  item.Mail,
			UserID: organizerID,
			Start:  start,
			End:    end,
			Reason: "overlap",
		}, nil
	}

	// Reuse normal creation so the owner, default calendar, invitations and
	// other event permissions cannot diverge from the authenticated endpoint.
	event, err := s.CreateEvent(ctx, organizerID, &domain.CreateEventRequest{
		Title: item.MeetingName, Description: &item.MeetingDescription,
		StartTime: start, EndTime: end,
	})
	if err != nil {
		return nil, nil, err
	}
	return event, nil, nil
}

func (s *CalendarService) parseBatchDates(from, to string) (time.Time, time.Time, error) {
	const layoutNoTZ = "2006-01-02T15:04:05"
	parse := func(val string) (time.Time, error) {
		if t, err := time.Parse(layoutNoTZ, val); err == nil {
			return t, nil
		}
		if t, err := time.Parse(time.DateOnly, val); err == nil {
			return t, nil
		}
		return time.Parse(time.RFC3339, val)
	}

	dateFrom, err := parse(from)
	if err != nil {
		return time.Time{}, time.Time{}, apperrors.BadRequest("invalid date_from format")
	}
	dateTo, err := parse(to)
	if err != nil {
		return time.Time{}, time.Time{}, apperrors.BadRequest("invalid date_to format")
	}
	if dateTo.Before(dateFrom) {
		return time.Time{}, time.Time{}, fmt.Errorf("date_to must be after date_from")
	}
	return dateFrom, dateTo, nil
}

func (s *CalendarService) RespondToInvite(ctx context.Context, userID int64, eventID uuid.UUID, req *domain.RespondToInviteRequest) error {
	event, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return apperrors.InternalWithErr("РћС€РёР±РєР° РїСЂРё РїРѕРёСЃРєРµ СЃРѕР±С‹С‚РёСЏ", err)
	}

	if event != nil && event.UserID == userID && req != nil && (req.AttendanceSet || req.Attendance != nil) {
		_, changed, err := s.repo.UpdateAttendeeResponse(ctx, eventID, userID, "accepted", req.Comment, req.Attendance, req.AttendanceSet)
		if err != nil {
			return err
		}
		if changed {
			s.publishInviteResponse(event, userID, req)
		}
		return nil
	}
	changed, err := respondToInviteWithDepsChanged(ctx, userID, eventID, req, s.repo, s.userRepo, s.notifService, true)
	if err != nil {
		return err
	}
	if changed {
		s.publishInviteResponse(event, userID, req)
	}
	return nil
}

func (s *CalendarService) GetEventAttendees(ctx context.Context, userID int64, eventID uuid.UUID) ([]domain.EventAttendee, error) {
	// Check if user has access to the event (organizer or attendee)
	event, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Ошибка при поиске события", err)
	}
	if event == nil {
		return nil, apperrors.NotFound("Событие не найдено")
	}

	// Check access rights
	isOrganizer := event.UserID == userID
	isAttendee := false
	if !isOrganizer {
		isAttendee, err = s.repo.IsAttendee(ctx, eventID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("Ошибка проверки доступа", err)
		}
	}

	if !isOrganizer && !isAttendee {
		return nil, apperrors.Forbidden("У вас нет доступа к этому событию")
	}

	attendees, err := s.repo.GetAttendeesWithUserInfo(ctx, eventID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось загрузить участников", err)
	}
	redactCalendarAttendees(attendees, userID, event.UserID)

	return attendees, nil
}

func respondToInviteWithDeps(
	ctx context.Context,
	userID int64,
	eventID uuid.UUID,
	req *domain.RespondToInviteRequest,
	repo calendarInviteResponderRepo,
	userRepo calendarInviteUserRepo,
	notifService NotificationSender,
	runAsync bool,
) error {
	_, err := respondToInviteWithDepsChanged(ctx, userID, eventID, req, repo, userRepo, notifService, runAsync)
	return err
}

func respondToInviteWithDepsChanged(
	ctx context.Context,
	userID int64,
	eventID uuid.UUID,
	req *domain.RespondToInviteRequest,
	repo calendarInviteResponderRepo,
	userRepo calendarInviteUserRepo,
	notifService NotificationSender,
	runAsync bool,
) (bool, error) {
	if req == nil {
		return false, apperrors.BadRequest("Пустой запрос")
	}

	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "accepted" && status != "declined" && status != "tentative" {
		return false, apperrors.BadRequest("Неверный статус. Используйте 'accepted', 'declined' или 'tentative'")
	}
	if req.Comment != nil && len([]rune(*req.Comment)) > 1000 {
		return false, apperrors.BadRequest("Комментарий слишком длинный")
	}

	event, err := repo.GetByID(ctx, eventID)
	if err != nil {
		return false, apperrors.InternalWithErr("Ошибка при поиске события", err)
	}
	if event == nil {
		return false, apperrors.NotFound("Событие не найдено")
	}

	if event.UserID == userID {
		// Organizer is always "accepted", but purely for UI/UX we just return success
		return false, nil
	}

	isAtt, err := repo.IsAttendee(ctx, eventID, userID)
	if err != nil {
		return false, apperrors.InternalWithErr("Ошибка проверки участия", err)
	}
	if !isAtt {
		return false, apperrors.Forbidden("Вы не приглашены на эту встречу")
	}

	// Update attendee status for all three statuses
	var previousStatus string
	var changed bool
	if responder, ok := repo.(interface {
		UpdateAttendeeResponse(context.Context, uuid.UUID, int64, string, *string, *string, bool) (string, bool, error)
	}); ok {
		previousStatus, changed, err = responder.UpdateAttendeeResponse(ctx, eventID, userID, status, req.Comment, req.Attendance, req.AttendanceSet)
	} else {
		previousStatus, changed, err = repo.UpdateAttendeeStatus(ctx, eventID, userID, status, req.Comment)
	}
	if err != nil {
		if stillInvited, checkErr := repo.IsAttendee(ctx, eventID, userID); checkErr == nil && !stillInvited {
			return false, apperrors.Forbidden("Вы больше не приглашены на эту встречу")
		}
		return false, calendarOperationError(err, "Не удалось обновить статус приглашения")
	}

	// Notify organizer when invitation is declined
	if changed && status == "declined" && previousStatus != "declined" {
		notifyDecline := func() {
			if notifService == nil || userRepo == nil {
				return
			}
			asyncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			user, err := userRepo.GetByID(asyncCtx, userID)
			userName := "Участник"
			if err == nil && user != nil {
				userName = strings.TrimSpace(user.FirstName + " " + user.LastName)
				if userName == "" {
					userName = user.Username
				}
			}

			_ = notifService.CreateAndSendNotification(asyncCtx, domain.CreateNotificationRequest{
				UserID:          event.UserID,
				OrganizerID:     &userID,
				Type:            "calendar_event_declined",
				TitleTemplate:   &domain.NotificationTemplate{Source: "Приглашение отклонено", Args: []any{}},
				Title:           "Приглашение отклонено",
				ContentTemplate: &domain.NotificationTemplate{Source: "%s отклонил(-а) встречу: %s", Args: []any{userName, event.Title}},
				Content:         fmt.Sprintf("%s отклонил(-а) встречу: %s", userName, event.Title),
				Metadata: map[string]string{
					"event_id": eventID.String(),
				},
			})
		}

		if runAsync {
			go notifyDecline()
		} else {
			notifyDecline()
		}
	}

	markInviteNotificationsAsRead := func() {
		asyncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = repo.MarkInviteNotificationsAsRead(asyncCtx, userID, eventID.String())
	}

	if runAsync {
		go markInviteNotificationsAsRead()
	} else {
		markInviteNotificationsAsRead()
	}

	return changed, nil
}

func (s *CalendarService) GetTodaySummary(ctx context.Context, userID int64, timezoneOffsetMinutes int) (*domain.TodaySummaryResponse, error) {
	nowUTC := time.Now().UTC()
	userNow := nowUTC.Add(time.Duration(timezoneOffsetMinutes) * time.Minute)

	y, m, d := userNow.Date()

	start := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-time.Duration(timezoneOffsetMinutes) * time.Minute)
	end := start.Add(24 * time.Hour)

	targetColumns := []string{domain.ColTODO, domain.ColInProgress}

	tasks, err := s.taskRepo.GetTasksByUserAndColumnKeys(ctx, userID, targetColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to get active tasks: %w", err)
	}

	events, err := s.ListEvents(ctx, userID, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to get today events: %w", err)
	}

	return &domain.TodaySummaryResponse{
		TasksCount:  len(tasks),
		EventsCount: len(events),
		Tasks:       tasks,
		Events:      events,
	}, nil
}

func (s *CalendarService) SetReminder(ctx context.Context, userID int64, eventID uuid.UUID, minutesBefore int) error {
	if minutesBefore < 0 {
		minutesBefore = 0
	}

	ev, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	if ev == nil {
		return calendarEventNotFoundError()
	}

	allowed := ev.UserID == userID
	if !allowed {
		for _, a := range ev.Attendees {
			if a.UserID == userID {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return calendarEventAccessDeniedError()
	}

	if err := s.repo.UpsertReminder(ctx, eventID, userID, minutesBefore); err != nil {
		return err
	}

	s.publishReminderEvent("calendar.reminder.set", eventID, userID, &minutesBefore)
	return nil
}

func (s *CalendarService) DeleteReminder(ctx context.Context, userID int64, eventID uuid.UUID) error {
	ev, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	if ev == nil {
		return calendarEventNotFoundError()
	}

	allowed := ev.UserID == userID
	if !allowed {
		for _, a := range ev.Attendees {
			if a.UserID == userID {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return calendarEventAccessDeniedError()
	}

	if err := s.repo.DeleteReminder(ctx, eventID, userID); err != nil {
		return err
	}

	s.publishReminderEvent("calendar.reminder.deleted", eventID, userID, nil)
	return nil
}

func (s *CalendarService) ensureAttendeesWithinScope(ctx context.Context, requesterID int64, attendeeIDs []int64) error {
	if err := ensureUsersExist(ctx, s.userRepo, attendeeIDs); err != nil {
		return err
	}
	seen := make(map[int64]bool, len(attendeeIDs))
	for _, attendeeID := range attendeeIDs {
		if attendeeID <= 0 {
			return apperrors.BadRequest("invalid attendee_id")
		}
		if seen[attendeeID] {
			continue
		}
		seen[attendeeID] = true
		if err := ensureUserScopedAccess(ctx, s.userRepo, requesterID, attendeeID); err != nil {
			return err
		}
	}
	return nil
}

func (s *CalendarService) StartReminderWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkAndSendCustomReminders(ctx)
		}
	}
}

func (s *CalendarService) checkAndSendCustomReminders(ctx context.Context) {
	s.dispatchMeetingChanges(ctx)
	now := time.Now()
	sendWindowStart := now
	sendWindowEnd := now.Add(60 * time.Second)

	maxMin, err := s.repo.GetMaxReminderMinutes(ctx)
	if err != nil {
		s.logger.Error("GetMaxReminderMinutes failed", zap.Error(err))
		return
	}

	horizon := now.Add(time.Duration(maxMin)*time.Minute + 2*time.Minute)
	lookback := now.Add(-24 * time.Hour)

	rows, err := s.repo.ListRemindersWithEvents(ctx, lookback, horizon)
	if err != nil {
		s.logger.Error("ListRemindersWithEvents failed", zap.Error(err))
		return
	}
	reminderEventIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if row.RecurrenceRule != nil {
			reminderEventIDs = append(reminderEventIDs, row.EventID)
		}
	}
	reminderExceptions, err := s.repo.ListEventExceptions(ctx, reminderEventIDs)
	if err != nil {
		s.logger.Error("ListEventExceptions failed", zap.Error(err))
		return
	}

	for _, row := range rows {
		ev := domain.Event{
			ID:                 row.EventID,
			UserID:             row.OwnerID,
			Title:              row.Title,
			Description:        row.Description,
			StartTime:          row.StartTime,
			EndTime:            row.EndTime,
			Timezone:           row.Timezone,
			IsOnline:           row.IsOnline,
			MeetingProvider:    row.MeetingProvider,
			MeetingRoomID:      row.MeetingRoomID,
			MeetingURL:         row.MeetingURL,
			LiveKitRoomName:    row.LiveKitRoomName,
			MeetingStatus:      row.MeetingStatus,
			Location:           row.Location,
			IsAllDay:           row.IsAllDay,
			RecurrenceRule:     row.RecurrenceRule,
			RecurrenceInterval: row.RecurrenceInterval,
			RecurrenceUntil:    row.RecurrenceUntil,
			Color:              row.Color,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
		}

		if stored, loadErr := s.repo.GetByID(ctx, ev.ID); loadErr == nil && stored != nil {
			ev.RoomID, ev.Room, ev.Attendees = stored.RoomID, stored.Room, stored.Attendees
		}
		occs := applyCalendarExceptions([]domain.Event{ev}, reminderExceptions, lookback, horizon)
		if len(occs) == 0 {
			continue
		}

		mins := row.MinutesBefore
		if mins < 0 {
			mins = 0
		}

		for _, occ := range occs {
			remindAt := occ.StartTime.Add(-time.Duration(mins) * time.Minute)
			if remindAt.Before(sendWindowStart) || !remindAt.Before(sendWindowEnd) {
				continue
			}

			ok, err := s.repo.TryMarkReminderSent(ctx, row.ReminderID, occ.StartTime)
			if err != nil {
				s.logger.Warn("TryMarkReminderSent failed", zap.Error(err))
				continue
			}
			if !ok {
				continue
			}

			go s.sendCustomReminder(row.ReminderUserID, occ, mins)
		}
	}
}

func (s *CalendarService) sendCustomReminder(userID int64, event domain.Event, minutesBefore int) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return
	}

	countdownText := FormatCountdownRU(minutesBefore)
	tzName := strings.TrimSpace(event.Timezone)
	if tzName == "" {
		tzName = "Asia/Almaty"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	timeLocal := event.StartTime.In(loc).Format("15:04")

	if s.meetingProviderService != nil {
		s.meetingProviderService.NormalizeEventMeetingInfo(&event)
	}

	title := fmt.Sprintf("Напоминание: «%s»", event.Title)
	var content string
	metadata := map[string]string{
		"event_id":         event.ID.String(),
		"event_title":      event.Title,
		"start_time":       event.StartTime.Format(time.RFC3339),
		"start_time_local": event.StartTime.In(loc).Format(time.RFC3339),
		"minutes_before":   strconv.Itoa(minutesBefore),
		"countdown_text":   countdownText,
		"timezone":         tzName,
	}

	if stored, err := s.repo.GetByID(ctx, event.ID); err == nil && stored != nil {
		event.Attendees = stored.Attendees
	}
	mode := ""
	for _, a := range event.Attendees {
		if a.UserID == userID && a.Attendance != nil {
			mode = *a.Attendance
		}
	}
	metadata["location"] = event.MeetingLocation()
	hasMeeting := mode != "offline" && (event.IsOnline || event.MeetingProvider == "qmeet" || event.MeetingProvider == "livekit") &&
		event.MeetingURL != nil && strings.TrimSpace(*event.MeetingURL) != ""

	if hasMeeting {
		meetingURL := strings.TrimSpace(*event.MeetingURL)
		metadata["meeting_url"] = meetingURL
		metadata["deep_link"] = meetingURL
		metadata["action_url"] = meetingURL
		metadata["action"] = "join_call"
		if event.MeetingRoomID != nil {
			metadata["room_id"] = *event.MeetingRoomID
		}
		if event.MeetingProvider != "" {
			metadata["meeting_provider"] = event.MeetingProvider
		}
		content = fmt.Sprintf("Встреча «%s» начнется %s (в %s). Ссылка QMeet готова к подключению", event.Title, countdownText, timeLocal)
	} else {
		locStr := event.MeetingLocation()
		if locStr != "" {
			content = fmt.Sprintf("Встреча «%s» начнется %s (в %s). Локация: %s", event.Title, countdownText, timeLocal, locStr)
		} else {
			content = fmt.Sprintf("Встреча «%s» начнется %s (в %s)", event.Title, countdownText, timeLocal)
		}
		metadata["action"] = "open_event"
	}

	notifReq := domain.CreateNotificationRequest{
		UserID:   userID,
		Type:     "calendar_reminder",
		Title:    title,
		Content:  content,
		Metadata: metadata,
	}
	reqTemplate := &domain.NotificationTemplate{Source: "Встреча «%s» начнется %s (в %s)", Args: []any{event.Title, domain.NotificationCountdown(minutesBefore), timeLocal}}
	if hasMeeting {
		reqTemplate.Source = "Встреча «%s» начнется %s (в %s). Ссылка QMeet готова к подключению"
	} else if location := event.MeetingLocation(); location != "" {
		reqTemplate.Source = "Встреча «%s» начнется %s (в %s). Локация: %s"
		reqTemplate.Args = append(reqTemplate.Args, location)
	}
	notifReq.TitleTemplate = &domain.NotificationTemplate{Source: "Напоминание: «%s»", Args: []any{event.Title}}
	notifReq.ContentTemplate = reqTemplate
	_ = s.notifService.CreateAndSendNotification(ctx, notifReq)

	if mode == "offline" {
		event.IsOnline = false
		event.MeetingURL = nil
	}
	s.sendMeetingEmail(ctx, event, userID, title, false, false)
}

func expandRecurring(e domain.Event, from, to time.Time) []domain.Event {
	if e.RecurrenceRule == nil || *e.RecurrenceRule == "" {
		return []domain.Event{e}
	}
	if !to.After(from) {
		return nil
	}

	interval := e.RecurrenceInterval
	if interval <= 0 {
		interval = 1
	}

	seriesUntil := to
	if e.RecurrenceUntil != nil && e.RecurrenceUntil.Before(seriesUntil) {
		seriesUntil = *e.RecurrenceUntil
	}

	duration := e.EndTime.Sub(e.StartTime)
	out := make([]domain.Event, 0, 16)
	// Seek close to the requested range. A hard cap starting at occurrence zero
	// made long-lived daily series disappear from current calendar views.
	k := recurrenceStartIndex(e, from.Add(-duration), interval)

	for len(out) < 3000 {
		var cur time.Time
		switch *e.RecurrenceRule {
		case "DAILY":
			cur = e.StartTime.AddDate(0, 0, k*interval)
		case "WEEKLY":
			cur = e.StartTime.AddDate(0, 0, k*7*interval)
		case "MONTHLY":
			// Calculate from original StartTime to preserve the day (e.g. 31st)
			cur = addMonthsKeepDay(e.StartTime, k*interval)
		default:
			return []domain.Event{e}
		}

		if cur.After(seriesUntil) {
			break
		}

		occStart := cur
		occEnd := cur.Add(duration)

		// Use a half-open interval [from, to): an event ending exactly at the
		// range start or beginning exactly at the range end does not overlap it.
		if occEnd.After(from) && occStart.Before(to) {
			copyEv := e
			copyEv.StartTime = occStart
			copyEv.EndTime = occEnd
			copyEv.OccurrenceID = fmt.Sprintf("%s:%d", e.ID, occStart.Unix())
			out = append(out, copyEv)
		}

		step := interval
		if *e.RecurrenceRule == "WEEKLY" {
			step *= 7
		}
		if !cur.Before(to) || k >= int(^uint(0)>>1)/step-1 {
			break
		}
		k++
	}

	return out
}

func recurrenceStartIndex(e domain.Event, earliest time.Time, interval int) int {
	if !earliest.After(e.StartTime) {
		return 0
	}
	var k int
	switch *e.RecurrenceRule {
	case "DAILY":
		k = int(earliest.Sub(e.StartTime).Hours()/24)/interval - 2
	case "WEEKLY":
		k = int(earliest.Sub(e.StartTime).Hours()/(24*7))/interval - 2
	case "MONTHLY":
		local := earliest.In(e.StartTime.Location())
		months := (local.Year()-e.StartTime.Year())*12 + int(local.Month()-e.StartTime.Month())
		k = months/interval - 2
	}
	if k < 0 {
		return 0
	}
	return k
}

func addMonthsKeepDay(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	hh, mm, ss := t.Clock()
	ns := t.Nanosecond()
	loc := t.Location()

	mInt := int(m) + months
	y += (mInt - 1) / 12
	mInt = (mInt-1)%12 + 1

	last := lastDayOfMonth(y, time.Month(mInt), loc)
	if d > last {
		d = last
	}

	return time.Date(y, time.Month(mInt), d, hh, mm, ss, ns, loc)
}

func lastDayOfMonth(y int, m time.Month, loc *time.Location) int {
	firstNext := time.Date(y, m, 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	last := firstNext.AddDate(0, 0, -1)
	return last.Day()
}

func normalizeRecurrence(rule *string) *string {
	if rule == nil {
		return nil
	}
	v := strings.ToUpper(strings.TrimSpace(*rule))
	if v == "" || v == "NONE" || v == "NULL" {
		return nil
	}
	return &v
}

func validateRecurrence(rule *string) error {
	if rule == nil {
		return nil
	}
	switch *rule {
	case "DAILY", "WEEKLY", "MONTHLY":
		return nil
	default:
		return fmt.Errorf("invalid recurrence_rule: %s (allowed: DAILY, WEEKLY, MONTHLY)", *rule)
	}
}

// GetCalendarGrid retrieves events in calendar grid format (day/week/month view)
func (s *CalendarService) GetCalendarGrid(ctx context.Context, userID int64, req domain.CalendarGridRequest) (*domain.CalendarGridResponse, error) {
	startDate, endDate, tz, err := resolveCalendarGridRange(req)
	if err != nil {
		return nil, err
	}

	// Get user's calendars with visibility settings
	calendars, err := s.repo.GetCalendarsWithSettings(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get calendars", err)
	}

	// Filter calendar IDs based on visibility and request
	var calendarIDs []uuid.UUID
	visibleCalendars := make([]domain.UserCalendar, 0)

	if len(req.CalendarIDs) > 0 {
		// Only calendars owned by the authenticated user may be selected.
		requested := make(map[uuid.UUID]struct{}, len(req.CalendarIDs))
		for _, id := range req.CalendarIDs {
			requested[id] = struct{}{}
		}
		for _, c := range calendars {
			if _, ok := requested[c.ID]; ok {
				calendarIDs = append(calendarIDs, c.ID)
				visibleCalendars = append(visibleCalendars, c.UserCalendar)
			}
		}
	} else {
		// Use all visible calendars
		for _, c := range calendars {
			if c.IsVisible {
				calendarIDs = append(calendarIDs, c.ID)
				visibleCalendars = append(visibleCalendars, c.UserCalendar)
			}
		}
	}

	// An empty personal selection still keeps invitations from other organizers
	// visible; owned events remain controlled by the selected calendar layers.
	events, err := s.repo.GetEventsForGrid(ctx, userID, startDate, endDate, calendarIDs)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get events", err)
	}
	events, err = s.expandEventsWithExceptions(ctx, events, startDate, endDate)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to expand calendar events", err)
	}

	// Get group events if requested
	var groupEvents []domain.GroupEventInfo
	var groupCalendars []domain.GroupCalendarWithSettings
	if req.IncludeGroup {
		groupCalendars, err = s.repo.GetGroupCalendarsWithSettings(ctx, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to get group calendars", err)
		}
		groupIDs := make([]uuid.UUID, 0, len(groupCalendars))
		for _, groupCalendar := range groupCalendars {
			if groupCalendar.IsVisible {
				groupIDs = append(groupIDs, groupCalendar.GroupID)
			}
		}
		if len(groupIDs) > 0 {
			groupEvents, err = s.repo.GetGroupEventsForGrid(ctx, userID, startDate, endDate, groupIDs)
			if err != nil {
				return nil, apperrors.InternalWithErr("failed to get group events", err)
			}
			groupEvents, err = s.expandGroupCalendarGridEventsWithExceptions(ctx, groupEvents, startDate, endDate)
			if err != nil {
				return nil, apperrors.InternalWithErr("failed to expand group calendar events", err)
			}
		}
	}

	return &domain.CalendarGridResponse{
		View:           req.View,
		Date:           req.Date,
		StartDate:      startDate,
		EndDate:        endDate,
		Timezone:       tz.String(),
		Events:         events,
		Calendars:      visibleCalendars,
		GroupCalendars: groupCalendars,
		GroupEvents:    groupEvents,
	}, nil
}

func (s *CalendarService) GetGroupCalendarsWithSettings(ctx context.Context, userID int64) ([]domain.GroupCalendarWithSettings, error) {
	calendars, err := s.repo.GetGroupCalendarsWithSettings(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get group calendars", err)
	}
	return calendars, nil
}

func (s *CalendarService) UpdateGroupCalendarVisibility(ctx context.Context, userID int64, groupID uuid.UUID, req domain.UpdateCalendarVisibilityRequest) error {
	if err := validateCalendarVisibilityRequest(req); err != nil {
		return err
	}
	updated, err := s.repo.UpdateGroupCalendarVisibility(ctx, userID, groupID, req.IsVisible, req.DisplayOrder)
	if err != nil {
		return apperrors.InternalWithErr("failed to update group calendar visibility", err)
	}
	if !updated {
		return apperrors.NotFound("group calendar not found")
	}
	return nil
}

// GetCalendarsWithSettings retrieves user's calendars with visibility settings
func (s *CalendarService) GetCalendarsWithSettings(ctx context.Context, userID int64) ([]domain.UserCalendarWithSettings, error) {
	calendars, err := s.repo.GetCalendarsWithSettings(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get calendars", err)
	}
	return calendars, nil
}

// UpdateCalendarVisibility updates calendar visibility settings
func (s *CalendarService) UpdateCalendarVisibility(ctx context.Context, userID int64, calendarID uuid.UUID, req domain.UpdateCalendarVisibilityRequest) error {
	if err := validateCalendarVisibilityRequest(req); err != nil {
		return err
	}
	// Verify calendar belongs to user
	calendars, err := s.repo.ListCalendars(ctx, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to get calendars", err)
	}

	found := false
	for _, c := range calendars {
		if c.ID == calendarID {
			found = true
			break
		}
	}

	if !found {
		return apperrors.NotFound("calendar not found")
	}

	// Update visibility settings
	if err := s.repo.UpdateCalendarVisibility(ctx, userID, calendarID, req.IsVisible, req.DisplayOrder); err != nil {
		return apperrors.InternalWithErr("failed to update calendar visibility", err)
	}

	return nil
}

func validateCalendarVisibilityRequest(req domain.UpdateCalendarVisibilityRequest) error {
	if req.IsVisible == nil && req.DisplayOrder == nil {
		return apperrors.BadRequest("at least one setting is required")
	}
	if req.DisplayOrder != nil && (*req.DisplayOrder < 0 || *req.DisplayOrder > 10000) {
		return apperrors.BadRequest("display_order must be between 0 and 10000")
	}
	return nil
}

func resolveCalendarGridRange(req domain.CalendarGridRequest) (time.Time, time.Time, *time.Location, error) {
	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		return time.Time{}, time.Time{}, nil, apperrors.BadRequest("invalid date format, expected YYYY-MM-DD")
	}
	tz := time.UTC
	if req.Timezone != "" {
		tz, err = time.LoadLocation(req.Timezone)
		if err != nil {
			return time.Time{}, time.Time{}, nil, apperrors.BadRequest("invalid timezone")
		}
	}

	var startDate, endDate time.Time
	switch req.View {
	case domain.CalendarViewDay:
		startDate = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, tz)
		endDate = startDate.AddDate(0, 0, 1)
	case domain.CalendarViewWeek:
		weekday := date.Weekday()
		if weekday == time.Sunday {
			weekday = 7
		}
		startDate = time.Date(date.Year(), date.Month(), date.Day()-(int(weekday)-1), 0, 0, 0, 0, tz)
		endDate = startDate.AddDate(0, 0, 7)
	case domain.CalendarViewMonth:
		startDate = time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, tz)
		endDate = startDate.AddDate(0, 1, 0)
	default:
		return time.Time{}, time.Time{}, nil, apperrors.BadRequest("invalid view type")
	}
	return startDate, endDate, tz, nil
}

func expandCalendarGridEvents(events []domain.Event, from, to time.Time) []domain.Event {
	result := make([]domain.Event, 0, len(events))
	for _, event := range events {
		if event.RecurrenceRule == nil || *event.RecurrenceRule == "" {
			result = append(result, event)
			continue
		}
		result = append(result, expandRecurring(event, from, to)...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartTime.Before(result[j].StartTime) })
	return result
}

func expandGroupCalendarGridEvents(events []domain.GroupEventInfo, from, to time.Time) []domain.GroupEventInfo {
	result := make([]domain.GroupEventInfo, 0, len(events))
	for _, groupEvent := range events {
		if groupEvent.RecurrenceRule == nil || *groupEvent.RecurrenceRule == "" {
			result = append(result, groupEvent)
			continue
		}
		for _, occurrence := range expandRecurring(groupEvent.Event, from, to) {
			copyEvent := groupEvent
			copyEvent.Event = occurrence
			result = append(result, copyEvent)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartTime.Before(result[j].StartTime) })
	return result
}

// GenerateQMeetLink generates and persists a QMeet video conference link for an event
func (s *CalendarService) GenerateQMeetLink(ctx context.Context, userID int64, eventID uuid.UUID) (*domain.CreateQMeetLinkResponse, error) {
	if s == nil || s.repo == nil {
		return nil, apperrors.Internal("calendar service is not configured")
	}

	event, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, calendarEventNotFoundError()
	}

	if event.UserID != userID {
		return nil, calendarEventAccessDeniedError()
	}
	if event.IsOnline && event.MeetingProvider == "qmeet" && event.MeetingRoomID != nil && event.MeetingURL != nil && strings.TrimSpace(*event.MeetingURL) != "" {
		return &domain.CreateQMeetLinkResponse{EventID: event.ID, MeetingProvider: "qmeet", RoomName: *event.MeetingRoomID, MeetingURL: *event.MeetingURL, Status: event.MeetingStatus, IsOnline: event.IsOnline}, nil
	}

	var qmeetInfo domain.CalendarMeetingInfo
	if s.meetingProviderService != nil {
		qmeetInfo = s.meetingProviderService.GenerateQMeetRoomInfo(event.ID, event.Title)
	} else {
		roomName := fmt.Sprintf("qmeet-%s", event.ID.String())
		url := fmt.Sprintf("https://meet.qmeet.kz/web/calls/calendar/%s", roomName)
		qmeetInfo = domain.CalendarMeetingInfo{
			Provider:        "qmeet",
			MeetingRoomID:   &roomName,
			MeetingURL:      &url,
			LiveKitRoomName: &roomName,
			Status:          "created",
		}
	}

	roomID := ""
	if qmeetInfo.MeetingRoomID != nil {
		roomID = *qmeetInfo.MeetingRoomID
	}
	meetingURL := ""
	if qmeetInfo.MeetingURL != nil {
		meetingURL = *qmeetInfo.MeetingURL
	}
	livekitRoom := ""
	if qmeetInfo.LiveKitRoomName != nil {
		livekitRoom = *qmeetInfo.LiveKitRoomName
	}

	if err := s.repo.UpdateEventMeetingInfo(ctx, event.ID, userID, "qmeet", roomID, meetingURL, livekitRoom, "created", true); err != nil {
		return nil, apperrors.InternalWithErr("Не удалось сохранить ссылку QMeet", err)
	}

	event.IsOnline = true
	event.MeetingProvider = "qmeet"
	event.MeetingRoomID = &roomID
	event.MeetingURL = &meetingURL
	event.LiveKitRoomName = &livekitRoom
	event.MeetingStatus = "created"
	event.UpdatedAt = time.Now().UTC()

	s.publishCalendarEvent("calendar.event.meeting_updated", event, event.ReminderMinutes)

	return &domain.CreateQMeetLinkResponse{
		EventID:         event.ID,
		MeetingProvider: "qmeet",
		RoomName:        roomID,
		MeetingURL:      meetingURL,
		Status:          "created",
		IsOnline:        true,
	}, nil
}

// GetTimezones returns list of corporate and international timezones
func (s *CalendarService) GetTimezones(_ context.Context) ([]domain.CalendarTimezoneItem, error) {
	return GetSupportedTimezones(), nil
}

// ConvertTimezones converts event or specified time range across timezones
func (s *CalendarService) ConvertTimezones(ctx context.Context, userID int64, req domain.TimezoneConvertRequest) (*domain.TimezoneConvertResponse, error) {
	if len(req.TargetTimezones) == 0 || len(req.TargetTimezones) > 50 {
		return nil, apperrors.BadRequest("Укажите от 1 до 50 часовых поясов")
	}
	var startTime, endTime time.Time
	sourceTZ := strings.TrimSpace(req.SourceTimezone)

	if req.EventID != nil {
		if s == nil || s.repo == nil {
			return nil, apperrors.Internal("calendar service is not configured")
		}
		event, err := s.repo.GetByID(ctx, *req.EventID)
		if err != nil {
			return nil, err
		}
		if event == nil {
			return nil, calendarEventNotFoundError()
		}
		if !userCanAccessEvent(userID, event) {
			return nil, calendarEventAccessDeniedError()
		}
		startTime = event.StartTime
		endTime = event.EndTime
		if sourceTZ == "" {
			sourceTZ = strings.TrimSpace(event.Timezone)
		}
	} else {
		if req.StartTime == nil || req.EndTime == nil {
			return nil, apperrors.BadRequest("Необходимо указать start_time и end_time либо event_id")
		}
		startTime = *req.StartTime
		endTime = *req.EndTime
	}

	if sourceTZ == "" {
		sourceTZ = "Asia/Almaty"
	}
	if !endTime.After(startTime) {
		return nil, apperrors.BadRequest("end_time должен быть позже start_time")
	}

	sourceItem, err := ConvertTimezoneItem(startTime, endTime, sourceTZ)
	if err != nil {
		return nil, err
	}

	targets := make([]domain.TimezoneConvertedItem, 0, len(req.TargetTimezones))
	for _, targetTZ := range req.TargetTimezones {
		targetTZ = strings.TrimSpace(targetTZ)
		if targetTZ == "" {
			return nil, apperrors.BadRequest("Некорректный часовой пояс")
		}
		targetItem, err := ConvertTimezoneItem(startTime, endTime, targetTZ)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *targetItem)
	}

	return &domain.TimezoneConvertResponse{
		Source:  *sourceItem,
		Targets: targets,
	}, nil
}

// GetReminder returns reminder information for an event and user
func (s *CalendarService) GetReminder(ctx context.Context, userID int64, eventID uuid.UUID) (*domain.EventReminderInfo, error) {
	if s == nil || s.repo == nil {
		return nil, apperrors.Internal("calendar service is not configured")
	}

	rem, err := s.repo.GetReminderByEventAndUser(ctx, eventID, userID)
	if err != nil {
		return nil, err
	}
	if rem == nil {
		return nil, apperrors.NotFound("Напоминание не найдено")
	}

	return &domain.EventReminderInfo{
		EventID:       rem.EventID,
		UserID:        rem.UserID,
		MinutesBefore: rem.MinutesBefore,
		CreatedAt:     rem.CreatedAt,
		LastSentAt:    rem.LastSentAt,
	}, nil
}

// GetReminderPresets returns recommended and standard reminder presets
func (s *CalendarService) GetReminderPresets(_ context.Context) ([]domain.ReminderPresetItem, error) {
	return GetReminderPresets(), nil
}
