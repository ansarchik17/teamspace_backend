package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

const AppleCalDAVEndpoint = "https://caldav.icloud.com"

type AppleCalendarService struct {
	repo         *repository.AppleCalendarRepository
	calendarRepo *repository.CalendarRepository
	logger       *zap.Logger
}

func NewAppleCalendarService(
	repo *repository.AppleCalendarRepository,
	calendarRepo *repository.CalendarRepository,
	logger *zap.Logger,
) *AppleCalendarService {
	return &AppleCalendarService{
		repo:         repo,
		calendarRepo: calendarRepo,
		logger:       logger,
	}
}

// ConnectAppleCalendar validates credentials and connects Apple Calendar
func (s *AppleCalendarService) ConnectAppleCalendar(ctx context.Context, userID int64, email, password string) error {
	client, err := s.createClient(email, password)
	if err != nil {
		s.logger.Error("Failed to create CalDAV client", zap.Error(err))
		return apperrors.InternalWithErr("failed to create caldav client", err)
	}

	// Validate credentials by attempting discovery
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		s.logger.Error("Failed to find current user principal", zap.Error(err))
		return apperrors.BadRequest("failed to authenticate with Apple Calendar. Please check your credentials.")
	}

	homeSet, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		s.logger.Error("Failed to find calendar home set", zap.Error(err))
		return apperrors.InternalWithErr("failed to find calendar home", err)
	}

	calendars, err := client.FindCalendars(ctx, homeSet)
	if err != nil {
		s.logger.Error("Failed to find calendars", zap.Error(err))
		return apperrors.InternalWithErr("failed to find calendars", err)
	}

	if len(calendars) == 0 {
		return apperrors.BadRequest("no calendars found for this account")
	}

	// Default to the first calendar or try to find one named "Home" or "Work"
	selectedCalendar := calendars[0]
	for _, cal := range calendars {
		if strings.EqualFold(cal.Name, "Home") || strings.EqualFold(cal.Name, "Work") || strings.EqualFold(cal.Name, "Calendar") {
			selectedCalendar = cal
			break
		}
	}

	// Save credentials and enable sync
	err = s.repo.UpdateAppleCredentials(ctx, userID, email, password)
	if err != nil {
		return apperrors.InternalWithErr("failed to save apple credentials", err)
	}

	err = s.repo.EnableAppleCalendarSync(ctx, userID, true, selectedCalendar.Path)
	if err != nil {
		return apperrors.InternalWithErr("failed to enable apple sync", err)
	}

	s.logger.Info("Apple Calendar connected successfully",
		zap.Int64("user_id", userID),
		zap.String("calendar_path", selectedCalendar.Path),
	)

	return nil
}

// GetSyncStatus returns the current sync status for a user
func (s *AppleCalendarService) GetSyncStatus(ctx context.Context, userID int64) (*domain.AppleCalendarSyncStatusResponse, error) {
	status, err := s.repo.GetUserAppleSyncStatus(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get sync status", err)
	}

	isConnected := status.AppleCalendarEmail != nil && status.AppleAppSpecificPassword != nil

	return &domain.AppleCalendarSyncStatusResponse{
		SyncEnabled:     status.AppleCalendarSyncEnabled,
		AppleCalendarID: status.AppleCalendarID,
		AppleEmail:      status.AppleCalendarEmail,
		IsConnected:     isConnected,
	}, nil
}

// DisconnectAppleCalendar disconnects Apple Calendar integration
func (s *AppleCalendarService) DisconnectAppleCalendar(ctx context.Context, userID int64) error {
	err := s.repo.DisconnectAppleCalendar(ctx, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to disconnect", err)
	}
	return nil
}

// SyncEventToApple syncs a single event to Apple Calendar
func (s *AppleCalendarService) SyncEventToApple(ctx context.Context, userID int64, event *domain.Event) error {
	s.logger.Info("Apple Calendar sync started",
		zap.Int64("user_id", userID),
		zap.String("event_id", event.ID.String()),
		zap.String("event_title", event.Title),
	)

	// Get sync status
	status, err := s.repo.GetUserAppleSyncStatus(ctx, userID)
	if err != nil {
		s.logger.Error("Apple Calendar sync status load failed", zap.Error(err))
		return err
	}

	if !status.AppleCalendarSyncEnabled {
		s.logger.Warn("Apple Calendar sync disabled for user", zap.Int64("user_id", userID))
		return nil
	}

	if status.AppleCalendarEmail == nil || status.AppleAppSpecificPassword == nil {
		s.logger.Warn("Apple Calendar credentials not found for user", zap.Int64("user_id", userID))
		return nil
	}

	if status.AppleCalendarID == nil || *status.AppleCalendarID == "" {
		s.logger.Error("Apple Calendar ID not selected")
		return apperrors.Internal("no calendar selected")
	}

	// Generate ICS content
	cal := ics.NewCalendar()
	cal.SetMethod(ics.MethodRequest)

	// Ensure we preserve UID if updating, but we use event.ID as standard across systems
	// Apple might expect .ics filename to match UID
	eventUID := event.ID.String()

	vevent := cal.AddEvent(eventUID)
	vevent.SetCreatedTime(event.CreatedAt)
	vevent.SetDtStampTime(time.Now())
	vevent.SetModifiedAt(time.Now())
	vevent.SetSummary(event.Title)

	if event.Description != nil {
		vevent.SetDescription(*event.Description)
	}
	if event.Location != nil {
		vevent.SetLocation(*event.Location)
	}

	if event.IsAllDay {
		// All day event
		// Standard iCal: DTEND is exclusive.
		vevent.SetProperty(ics.ComponentPropertyDtStart, event.StartTime.Format("20060102"), ics.WithCN("VALUE=DATE"))
		vevent.SetProperty(ics.ComponentPropertyDtEnd, event.EndTime.Format("20060102"), ics.WithCN("VALUE=DATE"))
	} else {
		vevent.SetStartAt(event.StartTime)
		vevent.SetEndAt(event.EndTime)
	}

	icsStr := cal.Serialize()

	// Use simple HTTP PUT to upload ICS file
	// Ensure path construction is correct: Trim trailing slash from calendar path, ensure leading slash for filename
	calendarPath := strings.TrimRight(*status.AppleCalendarID, "/")
	if !strings.HasPrefix(calendarPath, "/") {
		calendarPath = "/" + calendarPath
	}

	// Create FULL URL properly
	// AppleCalDAVEndpoint is https://caldav.icloud.com
	// CalDAV paths usually start with /
	fullURL := fmt.Sprintf("%s%s/%s.ics", AppleCalDAVEndpoint, calendarPath, eventUID)

	s.logger.Info("Pushing to Apple Calendar", zap.String("url", fullURL))

	req, err := http.NewRequestWithContext(ctx, "PUT", fullURL, strings.NewReader(icsStr))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	// If it's an update, we might want to check ETag, but blindly overwriting is common for simple sync
	req.SetBasicAuth(*status.AppleCalendarEmail, *status.AppleAppSpecificPassword)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("Apple Calendar event push failed", zap.Error(err))
		return apperrors.InternalWithErr("failed to sync to apple calendar", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		s.logger.Error("Apple Calendar returned error",
			zap.Int("status_code", resp.StatusCode),
			zap.String("body", string(bodyBytes)),
		)
		return apperrors.Internal("apple calendar return error")
	}

	// Save/Update mapping
	// We use eventUID as apple_event_id
	err = s.repo.CreateEventMapping(ctx, event.ID, userID, eventUID, *status.AppleCalendarID)
	if err != nil {
		s.logger.Error("Apple event mapping save failed", zap.Error(err))
		// Log but don't fail the request since sync happened
	}

	s.logger.Info("Apple Calendar event synced")
	return nil
}

// DeleteEventFromApple deletes an event from Apple Calendar
func (s *AppleCalendarService) DeleteEventFromApple(ctx context.Context, userID int64, eventID uuid.UUID) error {
	status, err := s.repo.GetUserAppleSyncStatus(ctx, userID)
	if err != nil {
		return err
	}

	if !status.AppleCalendarSyncEnabled || status.AppleCalendarID == nil {
		return nil
	}

	if status.AppleCalendarEmail == nil || status.AppleAppSpecificPassword == nil {
		return nil
	}

	// Construct URL
	eventUID := eventID.String()
	calendarPath := strings.TrimRight(*status.AppleCalendarID, "/")
	if !strings.HasPrefix(calendarPath, "/") {
		calendarPath = "/" + calendarPath
	}
	fullURL := fmt.Sprintf("%s%s/%s.ics", AppleCalDAVEndpoint, calendarPath, eventUID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", fullURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(*status.AppleCalendarEmail, *status.AppleAppSpecificPassword)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("Failed to delete from Apple Calendar", zap.Error(err))
		// We continue to delete local mapping even if remote fails (best effort)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			// Already deleted
		} else if resp.StatusCode >= 300 {
			s.logger.Warn("Apple Calendar DELETE returned error", zap.Int("status", resp.StatusCode))
		}
	}

	return s.repo.DeleteEventMapping(ctx, eventID, userID)
}

func (s *AppleCalendarService) createClient(email, password string) (*caldav.Client, error) {
	// Use explicit HTTP client
	httpClient := &http.Client{
		Transport: &basicAuthTransport{
			Username: email,
			Password: password,
		},
		Timeout: 30 * time.Second,
	}
	// The endpoint for finding principal is the root usually
	client, err := caldav.NewClient(httpClient, AppleCalDAVEndpoint)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Helper transport for basic auth
type basicAuthTransport struct {
	Username string
	Password string
}

func (t *basicAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(t.Username, t.Password)
	return http.DefaultTransport.RoundTrip(req)
}
