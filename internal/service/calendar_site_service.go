package service

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *CalendarService) roomScope(ctx context.Context, userID int64, manage bool) (uuid.UUID, error) {
	if s.repo == nil {
		return uuid.Nil, apperrors.Forbidden("company membership required")
	}
	org, allowed, err := s.repo.MeetingRoomScope(ctx, userID)
	if err != nil {
		return org, err
	}
	if manage && !allowed {
		return org, apperrors.Forbidden("only the company director or an administrator can manage meeting rooms")
	}
	return org, nil
}
func (s *CalendarService) getCompanyRoom(ctx context.Context, userID int64, id uuid.UUID) (*domain.MeetingRoom, error) {
	org, err := s.roomScope(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	room, err := s.repo.GetMeetingRoomByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if room == nil || room.OrganizationID == nil || *room.OrganizationID != org {
		return nil, apperrors.RoomNotFound()
	}
	return room, nil
}
func (s *CalendarService) ListSites(ctx context.Context, userID int64, includeInactive bool) ([]domain.MeetingSite, error) {
	org, err := s.roomScope(ctx, userID, includeInactive)
	if err != nil {
		return nil, err
	}
	return s.repo.ListMeetingSites(ctx, org, includeInactive)
}
func (s *CalendarService) GetSite(ctx context.Context, userID int64, id uuid.UUID) (*domain.MeetingSite, error) {
	org, err := s.roomScope(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	return s.repo.GetMeetingSite(ctx, org, id)
}
func validateSite(site *domain.MeetingSite) error {
	site.Name = strings.TrimSpace(site.Name)
	if site.Name == "" || len([]rune(site.Name)) > 255 || len(site.Address) > 4000 || len([]rune(site.City)) > 255 || len([]rune(site.WorkingHours)) > 100 {
		return apperrors.Validation("invalid site fields")
	}
	if site.Timezone == "" {
		site.Timezone = "Asia/Almaty"
	}
	return ValidateCalendarTimezone(site.Timezone)
}
func validateRoomFields(room *domain.MeetingRoom) error {
	if len([]rune(room.Name)) > 255 || len(room.Equipment) > 100 || (room.Location != nil && len([]rune(*room.Location)) > 255) || (room.Color != nil && len([]rune(*room.Color)) > 50) {
		return apperrors.Validation("invalid room fields")
	}
	if room.PhotoURL != nil && *room.PhotoURL != "" {
		u, err := url.Parse(*room.PhotoURL)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || len(*room.PhotoURL) > 2048 {
			return apperrors.Validation("invalid room photo URL")
		}
	}
	return nil
}
func (s *CalendarService) CreateSite(ctx context.Context, userID int64, req domain.CreateMeetingSiteRequest) (*domain.MeetingSite, error) {
	org, err := s.roomScope(ctx, userID, true)
	if err != nil {
		return nil, err
	}
	item := &domain.MeetingSite{ID: uuid.New(), OrganizationID: org, Name: req.Name, Address: req.Address, City: req.City, Timezone: req.Timezone, WorkingHours: req.WorkingHours, IsActive: true}
	if req.IsActive != nil {
		item.IsActive = *req.IsActive
	}
	if err = validateSite(item); err != nil {
		return nil, err
	}
	if err = s.repo.SaveMeetingSite(ctx, item, true); err != nil {
		return nil, err
	}
	return item, nil
}
func (s *CalendarService) UpdateSite(ctx context.Context, userID int64, id uuid.UUID, req domain.UpdateMeetingSiteRequest) (*domain.MeetingSite, error) {
	org, err := s.roomScope(ctx, userID, true)
	if err != nil {
		return nil, err
	}
	item, err := s.repo.GetMeetingSite(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		item.Name = *req.Name
	}
	if req.Address != nil {
		item.Address = *req.Address
	}
	if req.City != nil {
		item.City = *req.City
	}
	if req.Timezone != nil {
		item.Timezone = *req.Timezone
	}
	if req.WorkingHours != nil {
		item.WorkingHours = *req.WorkingHours
	}
	if req.IsActive != nil {
		item.IsActive = *req.IsActive
	}
	if err = validateSite(item); err != nil {
		return nil, err
	}
	if err = s.repo.SaveMeetingSite(ctx, item, false); err != nil {
		return nil, err
	}
	s.dispatchMeetingChanges(ctx)
	return item, nil
}
func (s *CalendarService) DeleteSite(ctx context.Context, userID int64, id uuid.UUID) error {
	org, err := s.roomScope(ctx, userID, true)
	if err != nil {
		return err
	}
	return s.repo.DeleteMeetingSite(ctx, org, id)
}

func calendarOperationError(err error, message string) error {
	if apperrors.IsAppError(err) {
		return err
	}
	return apperrors.InternalWithErr(message, err)
}
func (s *CalendarService) ListUnassignedRooms(ctx context.Context, userID int64) ([]domain.MeetingRoom, error) {
	if err := requireRoomAdmin(ctx, s.userRepo, userID); err != nil {
		return nil, err
	}
	return s.repo.ListUnassignedMeetingRooms(ctx)
}
