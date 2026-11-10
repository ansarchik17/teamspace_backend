package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type GoogleCalendarService struct {
	repo         *repository.GoogleCalendarRepository
	calendarRepo *repository.CalendarRepository
	logger       *zap.Logger
	oauthConfig  *oauth2.Config
}

func NewGoogleCalendarService(
	repo *repository.GoogleCalendarRepository,
	calendarRepo *repository.CalendarRepository,
	logger *zap.Logger,
	clientID, clientSecret, redirectURL string,
) *GoogleCalendarService {
	oauthConfig := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			calendar.CalendarScope, // Full access to Google Calendar
		},
		Endpoint: google.Endpoint,
	}

	return &GoogleCalendarService{
		repo:         repo,
		calendarRepo: calendarRepo,
		logger:       logger,
		oauthConfig:  oauthConfig,
	}
}

// GetAuthURL generates the Google OAuth URL for the user to authorize
func (s *GoogleCalendarService) GetAuthURL(userID int64) string {
	// Use userID as state to prevent CSRF attacks
	state := fmt.Sprintf("%d", userID)
	return s.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// HandleOAuthCallback handles the OAuth callback from Google
// For mobile clients, use redirectURL="" or "postmessage"
func (s *GoogleCalendarService) HandleOAuthCallback(ctx context.Context, userID int64, code string) error {
	// Clone config to avoid modifying the global one
	config := *s.oauthConfig

	// If the code comes from a mobile app (Android/iOS Google Sign In),
	// the redirect URI must be empty or "postmessage"
	// We'll try to detect this or assume based on the code format,
	// but simpler is to just try the empty redirect if the standard one fails,
	// OR create a separate method.
	// Let's modify this method to try standard exchange first, and if it fails with redirect_uri_mismatch, try empty.

	// Actually, for cleaner API, let's use a specific approach:
	// If we are calling this from the mobile app endpoint, we likely want no redirect URI.
	// But since we reused the endpoint, let's allow trying with empty redirectURI if the configured one fails.

	token, err := config.Exchange(ctx, code)
	if err != nil {
		// Try with empty redirect URL (typical for mobile/popup flows)
		config.RedirectURL = ""
		token, err = config.Exchange(ctx, code)
		if err != nil {
			s.logger.Error("Failed to exchange OAuth code", zap.Error(err))
			return apperrors.InternalWithErr("failed to exchange OAuth code", err)
		}
	}

	// Save tokens to database
	err = s.repo.UpdateGoogleTokens(
		ctx,
		userID,
		token.AccessToken,
		token.RefreshToken,
		token.Expiry,
	)
	if err != nil {
		s.logger.Error("Failed to save Google tokens", zap.Error(err))
		return apperrors.InternalWithErr("failed to save tokens", err)
	}

	// Get primary calendar ID
	calendarID, err := s.getPrimaryCalendarID(ctx, token)
	if err != nil {
		s.logger.Error("Failed to get primary calendar ID", zap.Error(err))
		return apperrors.InternalWithErr("failed to get calendar", err)
	}

	// Enable sync with the primary calendar
	err = s.repo.EnableGoogleCalendarSync(ctx, userID, true, calendarID)
	if err != nil {
		s.logger.Error("Failed to enable sync", zap.Error(err))
		return apperrors.InternalWithErr("failed to enable sync", err)
	}

	s.logger.Info("Google Calendar connected successfully",
		zap.Int64("user_id", userID),
		zap.String("calendar_id", calendarID),
	)

	return nil
}

// GetSyncStatus returns the current sync status for a user
func (s *GoogleCalendarService) GetSyncStatus(ctx context.Context, userID int64) (*domain.GoogleCalendarSyncStatusResponse, error) {
	status, err := s.repo.GetUserGoogleSyncStatus(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get sync status", err)
	}

	response := &domain.GoogleCalendarSyncStatusResponse{
		SyncEnabled:      status.GoogleCalendarSyncEnabled,
		GoogleCalendarID: status.GoogleCalendarID,
		IsConnected:      status.GoogleAccessToken != nil && status.GoogleRefreshToken != nil,
	}

	// If connected, get the email from Google
	if response.IsConnected && status.GoogleAccessToken != nil {
		token := &oauth2.Token{
			AccessToken:  *status.GoogleAccessToken,
			RefreshToken: *status.GoogleRefreshToken,
			Expiry:       *status.GoogleTokenExpiry,
		}

		// Get user info to retrieve email
		email, err := s.getGoogleEmail(ctx, token)
		if err == nil {
			response.GoogleEmail = &email
		}
	}

	return response, nil
}

// DisconnectGoogleCalendar disconnects Google Calendar integration
func (s *GoogleCalendarService) DisconnectGoogleCalendar(ctx context.Context, userID int64) error {
	err := s.repo.DisconnectGoogleCalendar(ctx, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to disconnect", err)
	}
	return nil
}

// SyncEventToGoogle syncs a single event to Google Calendar
func (s *GoogleCalendarService) SyncEventToGoogle(ctx context.Context, userID int64, event *domain.Event) error {
	s.logger.Info("Google Calendar sync started",
		zap.Int64("user_id", userID),
		zap.String("event_id", event.ID.String()),
		zap.String("event_title", event.Title),
	)

	// Get sync status
	status, err := s.repo.GetUserGoogleSyncStatus(ctx, userID)
	if err != nil {
		s.logger.Error("Google Calendar sync status load failed", zap.Error(err))
		return err
	}

	s.logger.Info("Google Calendar sync status loaded",
		zap.Bool("sync_enabled", status.GoogleCalendarSyncEnabled),
		zap.Bool("has_access_token", status.GoogleAccessToken != nil),
		zap.Bool("has_refresh_token", status.GoogleRefreshToken != nil),
		zap.String("calendar_id", func() string {
			if status.GoogleCalendarID != nil {
				return *status.GoogleCalendarID
			}
			return "nil"
		}()),
	)

	if !status.GoogleCalendarSyncEnabled {
		s.logger.Warn("Google Calendar sync disabled for user", zap.Int64("user_id", userID))
		return nil
	}

	if status.GoogleAccessToken == nil {
		s.logger.Warn("Google access token not found for user", zap.Int64("user_id", userID))
		return nil
	}

	// Create OAuth token
	token := &oauth2.Token{
		AccessToken:  *status.GoogleAccessToken,
		RefreshToken: *status.GoogleRefreshToken,
		Expiry:       *status.GoogleTokenExpiry,
	}

	s.logger.Info("Google token loaded",
		zap.Time("expiry", token.Expiry),
		zap.Bool("is_expired", token.Expiry.Before(time.Now())),
	)

	// Check if token needs refresh
	if token.Expiry.Before(time.Now()) {
		s.logger.Info("Google token refresh started")
		token, err = s.refreshToken(ctx, userID, token)
		if err != nil {
			s.logger.Error("Google token refresh failed", zap.Error(err))
			return err
		}
		s.logger.Info("Google token refreshed")
	}

	// Create Google Calendar service
	s.logger.Info("Google Calendar client creation started")
	srv, err := s.createCalendarService(ctx, token)
	if err != nil {
		s.logger.Error("Google Calendar client creation failed", zap.Error(err))
		return err
	}
	s.logger.Info("Google Calendar client created")

	// Check if event is already synced
	s.logger.Info("Google event mapping lookup started")
	mapping, err := s.repo.GetEventMapping(ctx, event.ID, userID)
	if err != nil {
		s.logger.Error("Google event mapping lookup failed", zap.Error(err))
		return err
	}

	if mapping != nil {
		s.logger.Info("Google event mapping found",
			zap.String("google_event_id", mapping.GoogleEventID),
			zap.String("google_calendar_id", mapping.GoogleCalendarID),
		)
	} else {
		s.logger.Info("Google event mapping not found, event create will be used")
	}

	calendarID := *status.GoogleCalendarID
	if calendarID == "" {
		calendarID = "primary"
	}

	s.logger.Info("Google Calendar ID selected", zap.String("calendar_id", calendarID))

	// Create or update the event in Google Calendar
	googleEvent := s.convertToGoogleEvent(event)

	if mapping == nil {
		// Create new event
		s.logger.Info("Google Calendar event create started")
		createdEvent, err := srv.Events.Insert(calendarID, googleEvent).Do()
		if err != nil {
			s.logger.Error("Google Calendar event create failed",
				zap.Error(err),
				zap.String("calendar_id", calendarID),
				zap.String("event_title", googleEvent.Summary),
			)
			return apperrors.InternalWithErr("failed to create event in Google Calendar", err)
		}

		s.logger.Info("Google Calendar event created",
			zap.String("google_event_id", createdEvent.Id),
		)

		// Save mapping
		s.logger.Info("Google event mapping save started")
		err = s.repo.CreateEventMapping(ctx, event.ID, userID, createdEvent.Id, calendarID)
		if err != nil {
			s.logger.Error("Google event mapping save failed", zap.Error(err))
		} else {
			s.logger.Info("Google event mapping saved")
		}

		s.logger.Info("Google Calendar event synced",
			zap.String("event_id", event.ID.String()),
			zap.String("google_event_id", createdEvent.Id),
		)
	} else {
		// Update existing event
		s.logger.Info("Google Calendar event update started",
			zap.String("google_event_id", mapping.GoogleEventID),
		)
		_, err := srv.Events.Update(calendarID, mapping.GoogleEventID, googleEvent).Do()
		if err != nil {
			s.logger.Error("Google Calendar event update failed",
				zap.Error(err),
				zap.String("calendar_id", calendarID),
				zap.String("google_event_id", mapping.GoogleEventID),
			)
			return apperrors.InternalWithErr("failed to update event in Google Calendar", err)
		}

		s.logger.Info("Google Calendar event updated",
			zap.String("event_id", event.ID.String()),
			zap.String("google_event_id", mapping.GoogleEventID),
		)
	}

	return nil
}

// DeleteEventFromGoogle deletes an event from Google Calendar
func (s *GoogleCalendarService) DeleteEventFromGoogle(ctx context.Context, userID int64, eventID uuid.UUID) error {
	// Get sync status
	status, err := s.repo.GetUserGoogleSyncStatus(ctx, userID)
	if err != nil {
		return err
	}

	if !status.GoogleCalendarSyncEnabled {
		return nil // Skip if sync is not enabled
	}

	// Get event mapping
	mapping, err := s.repo.GetEventMapping(ctx, eventID, userID)
	if err != nil || mapping == nil {
		return nil // Event was never synced, nothing to delete
	}

	// Create OAuth token
	token := &oauth2.Token{
		AccessToken:  *status.GoogleAccessToken,
		RefreshToken: *status.GoogleRefreshToken,
		Expiry:       *status.GoogleTokenExpiry,
	}

	// Create Google Calendar service
	srv, err := s.createCalendarService(ctx, token)
	if err != nil {
		return err
	}

	// Delete from Google Calendar
	err = srv.Events.Delete(mapping.GoogleCalendarID, mapping.GoogleEventID).Do()
	if err != nil {
		s.logger.Error("Failed to delete event from Google Calendar", zap.Error(err))
		// Continue anyway to delete mapping
	}

	// Delete mapping
	err = s.repo.DeleteEventMapping(ctx, eventID, userID)
	if err != nil {
		s.logger.Error("Failed to delete event mapping", zap.Error(err))
	}

	return nil
}

// Helper methods

func (s *GoogleCalendarService) createCalendarService(ctx context.Context, token *oauth2.Token) (*calendar.Service, error) {
	client := s.oauthConfig.Client(ctx, token)
	srv, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to create calendar service", err)
	}
	return srv, nil
}

func (s *GoogleCalendarService) getPrimaryCalendarID(ctx context.Context, token *oauth2.Token) (string, error) {
	srv, err := s.createCalendarService(ctx, token)
	if err != nil {
		return "", err
	}

	calList, err := srv.CalendarList.Get("primary").Do()
	if err != nil {
		return "", err
	}

	return calList.Id, nil
}

func (s *GoogleCalendarService) getGoogleEmail(ctx context.Context, token *oauth2.Token) (string, error) {
	srv, err := s.createCalendarService(ctx, token)
	if err != nil {
		return "", err
	}

	settings, err := srv.Settings.Get("timezone").Do()
	if err != nil {
		return "", err
	}

	// Try to get email from calendar settings
	calList, err := srv.CalendarList.Get("primary").Do()
	if err != nil {
		return "", err
	}

	if calList.Summary != "" {
		return calList.Summary, nil
	}

	return settings.Value, nil
}

func (s *GoogleCalendarService) refreshToken(ctx context.Context, userID int64, token *oauth2.Token) (*oauth2.Token, error) {
	tokenSource := s.oauthConfig.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to refresh token", err)
	}

	// Save new token
	err = s.repo.UpdateGoogleTokens(ctx, userID, newToken.AccessToken, newToken.RefreshToken, newToken.Expiry)
	if err != nil {
		s.logger.Error("Failed to save refreshed token", zap.Error(err))
	}

	return newToken, nil
}

func (s *GoogleCalendarService) convertToGoogleEvent(event *domain.Event) *calendar.Event {
	summary := event.Title
	if event.IsOnline {
		summary = fmt.Sprintf("[Online] %s", summary)
	}

	googleEvent := &calendar.Event{
		Summary:     summary,
		Location:    stringValue(event.Location),
		Description: stringValue(event.Description),
	}

	if event.IsAllDay {
		googleEvent.Start = &calendar.EventDateTime{
			Date: event.StartTime.Format("2006-01-02"),
		}
		googleEvent.End = &calendar.EventDateTime{
			Date: event.EndTime.Format("2006-01-02"),
		}
	} else {
		googleEvent.Start = &calendar.EventDateTime{
			DateTime: event.StartTime.Format(time.RFC3339),
		}
		googleEvent.End = &calendar.EventDateTime{
			DateTime: event.EndTime.Format(time.RFC3339),
		}
	}

	// Add recurrence if present
	if event.RecurrenceRule != nil && *event.RecurrenceRule != "" {
		googleEvent.Recurrence = []string{
			fmt.Sprintf("RRULE:FREQ=%s", *event.RecurrenceRule),
		}
	}

	// Set color if present
	if event.Color != nil {
		// Google Calendar has predefined color IDs (1-11)
		// We could map our colors to Google's color IDs
		googleEvent.ColorId = "1" // Default color
	}

	s.logger.Info("📝 Converted event to Google format",
		zap.String("summary", googleEvent.Summary),
		zap.String("location", googleEvent.Location),
		zap.Bool("is_all_day", event.IsAllDay),
		zap.String("start", func() string {
			if googleEvent.Start.DateTime != "" {
				return googleEvent.Start.DateTime
			}
			return googleEvent.Start.Date
		}()),
		zap.String("end", func() string {
			if googleEvent.End.DateTime != "" {
				return googleEvent.End.DateTime
			}
			return googleEvent.End.Date
		}()),
	)

	return googleEvent
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

/*
func (s *GoogleCalendarService) convertGoogleEventToEvent(googleEvent *calendar.Event, userID int64) (*domain.Event, error) {
	event := &domain.Event{
		ID:          uuid.New(),
		UserID:      userID,
		Title:       googleEvent.Summary,
		IsOrganizer: false, // Coming from Google, so not organizer in our system
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if googleEvent.Description != "" {
		desc := googleEvent.Description
		event.Description = &desc
	}

	if googleEvent.Location != "" {
		loc := googleEvent.Location
		event.Location = &loc
	}

	// Parse start/end times
	if googleEvent.Start.DateTime != "" {
		startTime, err := time.Parse(time.RFC3339, googleEvent.Start.DateTime)
		if err != nil {
			return nil, err
		}
		event.StartTime = startTime
		event.IsAllDay = false
	} else if googleEvent.Start.Date != "" {
		startTime, err := time.Parse("2006-01-02", googleEvent.Start.Date)
		if err != nil {
			return nil, err
		}
		event.StartTime = startTime
		event.IsAllDay = true
	}

	if googleEvent.End.DateTime != "" {
		endTime, err := time.Parse(time.RFC3339, googleEvent.End.DateTime)
		if err != nil {
			return nil, err
		}
		event.EndTime = endTime
	} else if googleEvent.End.Date != "" {
		endTime, err := time.Parse("2006-01-02", googleEvent.End.Date)
		if err != nil {
			return nil, err
		}
		event.EndTime = endTime
	}

	return event, nil
}
*/
