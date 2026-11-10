package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type CalendarMeetingCreateRequest struct {
	EventID           uuid.UUID
	Title             string
	StartTime         time.Time
	EndTime           time.Time
	Organizer         *domain.User
	Attendees         []*domain.User
	TotalParticipants int
}

type CalendarMeetingProvider interface {
	Name() string
	CreateMeeting(ctx context.Context, req CalendarMeetingCreateRequest) (*domain.CalendarMeetingInfo, error)
}

type CalendarMeetingProviderService struct {
	liveKitProvider     CalendarMeetingProvider
	liveKitService      *LiveKitService
	ciscoProvider       CalendarMeetingProvider
	liveKitParticipants int
	meetingBaseURL      string
	logger              *zap.Logger
}

func normalizeCalendarMeetingBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return ""
	}
	switch {
	case strings.HasSuffix(base, "/web/calls/calendar"):
		return base
	case strings.HasSuffix(base, "/web"):
		return base + "/calls/calendar"
	case strings.HasSuffix(base, "/calls/calendar"):
		return base
	default:
		return base + "/web/calls/calendar"
	}
}

func NewCalendarMeetingProviderService(
	cfg config.CalendarMeetingsConfig,
	liveKitService *LiveKitService,
	logger *zap.Logger,
) *CalendarMeetingProviderService {
	threshold := cfg.LiveKitMaxParticipants
	if threshold <= 0 {
		threshold = 5
	}

	return &CalendarMeetingProviderService{
		liveKitProvider:     NewLiveKitCalendarMeetingProvider(liveKitService),
		liveKitService:      liveKitService,
		ciscoProvider:       NewCiscoCalendarMeetingProvider(cfg, logger),
		liveKitParticipants: threshold,
		meetingBaseURL:      normalizeCalendarMeetingBaseURL(cfg.MeetingBaseURL),
		logger:              logger.With(zap.String("service", "calendar_meeting_provider")),
	}
}

func (s *CalendarMeetingProviderService) GenerateLiveKitJoinToken(roomName string, userID int64, userName string, tokenCtx LiveKitTokenContext) (string, string, error) {
	if s == nil || s.liveKitService == nil {
		return "", "", fmt.Errorf("livekit service is not configured")
	}

	token, err := s.liveKitService.GenerateToken(roomName, userID, userName, tokenCtx)
	if err != nil {
		return "", "", err
	}

	return token, s.liveKitService.ResolveClientURL(userID, "", ""), nil
}

func (s *CalendarMeetingProviderService) CountLiveKitParticipants(ctx context.Context, roomName string) (int, error) {
	if s == nil || s.liveKitService == nil {
		return 0, fmt.Errorf("livekit service is not configured")
	}

	participants, err := s.liveKitService.ListParticipants(ctx, roomName)
	if err != nil {
		if strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "not found") {
			return 0, nil
		}
		return 0, err
	}

	return len(participants), nil
}

func (s *CalendarMeetingProviderService) BuildMeetingURL(roomName string) *string {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" || s.meetingBaseURL == "" {
		return nil
	}

	meetingURL := s.meetingBaseURL + "/" + url.PathEscape(roomName)
	if s.logger != nil {
		s.logger.Info("calendar_meeting_url_built",
			zap.String("room_name", roomName),
			zap.String("meeting_url", meetingURL),
		)
	}
	return &meetingURL
}

func (s *CalendarMeetingProviderService) NormalizeEventMeetingInfo(event *domain.Event) {
	if event == nil || !event.IsOnline {
		return
	}

	if strings.TrimSpace(event.MeetingProvider) == "" || event.MeetingProvider == "none" {
		if value := strings.TrimSpace(stringPtrValue(event.LiveKitRoomName)); value != "" {
			event.MeetingProvider = "livekit"
		}
	}

	if strings.TrimSpace(event.MeetingProvider) == "" || event.MeetingProvider == "none" {
		if legacyRoomName := legacyCalendarRoomName(event.Location); legacyRoomName != "" {
			event.MeetingProvider = "livekit"
			event.LiveKitRoomName = &legacyRoomName
		}
	}

	if event.MeetingProvider == "livekit" || event.MeetingProvider == "qmeet" {
		if strings.TrimSpace(stringPtrValue(event.LiveKitRoomName)) == "" {
			if roomID := strings.TrimSpace(stringPtrValue(event.MeetingRoomID)); roomID != "" {
				event.LiveKitRoomName = &roomID
			} else if legacyRoomName := legacyCalendarRoomName(event.Location); legacyRoomName != "" {
				event.LiveKitRoomName = &legacyRoomName
			}
		}
		if canonicalURL := s.BuildMeetingURL(stringPtrValue(event.LiveKitRoomName)); canonicalURL != nil {
			event.MeetingURL = canonicalURL
		}
		if strings.TrimSpace(event.MeetingStatus) == "" {
			event.MeetingStatus = "created"
		}
	}

	if event.MeetingProvider == "cisco" && strings.TrimSpace(stringPtrValue(event.MeetingURL)) == "" {
		if joinURL := strings.TrimSpace(stringPtrValue(event.CiscoJoinURL)); joinURL != "" {
			event.MeetingURL = &joinURL
		}
	}
}

// GenerateQMeetRoomInfo generates QMeet room details and join URL for an event
func (s *CalendarMeetingProviderService) GenerateQMeetRoomInfo(eventID uuid.UUID, title string) domain.CalendarMeetingInfo {
	roomName := fmt.Sprintf("qmeet-%s", eventID.String())
	meetingURL := ""
	if s != nil {
		if built := s.BuildMeetingURL(roomName); built != nil {
			meetingURL = *built
		}
	}
	if meetingURL == "" {
		meetingURL = "https://meet.qmeet.kz/web/calls/calendar/" + url.PathEscape(roomName)
	}
	return domain.CalendarMeetingInfo{
		Provider:        "qmeet",
		MeetingRoomID:   &roomName,
		MeetingURL:      &meetingURL,
		LiveKitRoomName: &roomName,
		Status:          "created",
	}
}

func legacyCalendarRoomName(location *string) string {
	value := strings.TrimSpace(stringPtrValue(location))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return ""
	}
	if !strings.HasPrefix(value, "calendar-") {
		return ""
	}
	if strings.ContainsAny(value, " \n\r\t/\\") {
		return ""
	}
	return value
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *CalendarMeetingProviderService) DetermineMeetingInfo(req CalendarMeetingCreateRequest) domain.CalendarMeetingInfo {
	if req.TotalParticipants <= 0 {
		req.TotalParticipants = 1
	}
	if req.TotalParticipants <= s.liveKitParticipants {
		if info, err := s.liveKitProvider.CreateMeeting(context.Background(), req); err == nil && info != nil {
			return *info
		}
		return domain.CalendarMeetingInfo{Provider: "livekit", Status: "failed"}
	}
	return domain.CalendarMeetingInfo{Provider: "cisco", Status: "pending"}
}

func (s *CalendarMeetingProviderService) Provision(ctx context.Context, req CalendarMeetingCreateRequest) (*domain.CalendarMeetingInfo, error) {
	if req.TotalParticipants <= 0 {
		req.TotalParticipants = 1
	}
	if req.TotalParticipants <= s.liveKitParticipants {
		return s.liveKitProvider.CreateMeeting(ctx, req)
	}
	return s.ciscoProvider.CreateMeeting(ctx, req)
}

type LiveKitCalendarMeetingProvider struct {
	liveKitService *LiveKitService
}

func NewLiveKitCalendarMeetingProvider(liveKitService *LiveKitService) *LiveKitCalendarMeetingProvider {
	return &LiveKitCalendarMeetingProvider{liveKitService: liveKitService}
}

func (p *LiveKitCalendarMeetingProvider) Name() string {
	return "livekit"
}

func (p *LiveKitCalendarMeetingProvider) CreateMeeting(_ context.Context, req CalendarMeetingCreateRequest) (*domain.CalendarMeetingInfo, error) {
	roomName := buildCalendarMeetingRoomName(req)
	roomID := roomName
	return &domain.CalendarMeetingInfo{
		Provider:        "livekit",
		MeetingRoomID:   &roomID,
		LiveKitRoomName: &roomName,
		Status:          "created",
	}, nil
}

type CiscoCalendarMeetingProvider struct {
	schedulerURL string
	timeout      time.Duration
	logger       *zap.Logger
}

func NewCiscoCalendarMeetingProvider(cfg config.CalendarMeetingsConfig, logger *zap.Logger) *CiscoCalendarMeetingProvider {
	timeout := cfg.ProviderTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &CiscoCalendarMeetingProvider{
		schedulerURL: strings.TrimSpace(cfg.CiscoSchedulerURL),
		timeout:      timeout,
		logger:       logger.With(zap.String("provider", "cisco_calendar")),
	}
}

func (p *CiscoCalendarMeetingProvider) Name() string {
	return "cisco"
}

func (p *CiscoCalendarMeetingProvider) CreateMeeting(ctx context.Context, req CalendarMeetingCreateRequest) (*domain.CalendarMeetingInfo, error) {
	if p.schedulerURL == "" {
		return &domain.CalendarMeetingInfo{Provider: "cisco", Status: "pending"}, nil
	}

	type participant struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	hostName := strings.TrimSpace(req.Organizer.FirstName + " " + req.Organizer.LastName)
	if hostName == "" {
		hostName = req.Organizer.Username
	}

	participants := make([]participant, 0, len(req.Attendees))
	for _, attendee := range req.Attendees {
		if attendee == nil {
			continue
		}
		name := strings.TrimSpace(attendee.FirstName + " " + attendee.LastName)
		if name == "" {
			name = attendee.Username
		}
		participants = append(participants, participant{Name: name, Email: attendee.Email})
	}

	payload := map[string]any{
		"meeting_name": req.Title,
		"from_date":    req.StartTime.Format("2006-01-02T15:04:05"),
		"to_date":      req.EndTime.Format("2006-01-02T15:04:05"),
		"host": map[string]string{
			"name":  strings.TrimSpace(hostName),
			"email": req.Organizer.Email,
		},
		"participants": participants,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	reqCtx := ctx
	cancel := func() {}
	if p.timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, p.timeout)
	}
	defer cancel()

	client := &http.Client{
		Timeout: p.timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.schedulerURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("cisco scheduler returned status %d", resp.StatusCode)
	}

	var apiResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	meetingID := meetingProviderStringValue(apiResp["meeting_id"])
	if meetingID == "" {
		meetingID = meetingProviderStringValue(apiResp["id"])
	}
	meetingURL := meetingProviderStringValue(apiResp["url"])
	if meetingURL == "" {
		meetingURL = meetingProviderStringValue(apiResp["meeting_url"])
	}

	info := &domain.CalendarMeetingInfo{
		Provider: "cisco",
		Status:   "created",
	}
	if meetingID != "" {
		info.MeetingRoomID = &meetingID
		info.CiscoMeetingID = &meetingID
	}
	if meetingURL != "" {
		info.MeetingURL = &meetingURL
		info.CiscoJoinURL = &meetingURL
	}
	return info, nil
}

func buildCalendarMeetingRoomName(req CalendarMeetingCreateRequest) string {
	slug := sanitizeMeetingSlug(req.Title)
	if slug == "" {
		slug = "calendar-event"
	}
	return fmt.Sprintf("calendar-%s-%s", slug, req.EventID.String()[:8])
}

func sanitizeMeetingSlug(value string) string {
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", "_", "-", ".", "-", ",", "-", ":", "-")
	value = replacer.Replace(strings.ToLower(strings.TrimSpace(value)))
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
		}
	}
	return strings.Trim(builder.String(), "-")
}

func meetingProviderStringValue(value any) string {
	parsed, _ := value.(string)
	return parsed
}
