package service

import (
	"context"
	"net/url"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) ContactActions(ctx context.Context, actor, target int64) (*domain.ContactActions, error) {
	profile, err := s.GetContactProfile(ctx, actor, target)
	if err != nil {
		return nil, err
	}
	return profile.Actions, nil
}

func buildContactActions(actor int64, profile *domain.ContactProfile) *domain.ContactActions {
	actions := &domain.ContactActions{
		Chat:    domain.ContactChatAction{Label: "Написать сообщение"},
		Email:   domain.ContactEmailAction{Label: "Отправить email"},
		Meeting: domain.ContactMeetingAction{Label: "Встреча", AttendeeIDs: []int64{}},
	}
	if actor == profile.ID {
		actions.Chat.DisabledReason = "self_contact"
		actions.Meeting.DisabledReason = "self_contact"
	} else {
		peer := profile.ID
		actions.Chat.Enabled = true
		actions.Chat.Method = "POST"
		actions.Chat.Endpoint = "/api/v1/conversations"
		actions.Chat.PeerID = &peer
		actions.Meeting.Enabled = true
		actions.Meeting.Method = "POST"
		actions.Meeting.Endpoint = "/api/v1/calendar/events"
		name := strings.TrimSpace(profile.FullName)
		if name == "" {
			name = strings.TrimSpace(profile.Username)
		}
		actions.Meeting.Title = "Встреча"
		if name != "" {
			actions.Meeting.Title += ": " + name
		}
		actions.Meeting.AttendeeIDs = []int64{profile.ID}
		actions.Meeting.AvailabilityEndpoint = "/api/v1/calendar/events/check-availability"
		actions.Meeting.SuggestTimesEndpoint = "/api/v1/calendar/events/suggest-times"
	}
	// A malformed legacy work address must not become an unsafe mailto URI.
	// Use the first valid single address, preferring the dedicated work contact.
	candidates := []string{}
	if profile.WorkEmail != nil {
		candidates = append(candidates, *profile.WorkEmail)
	}
	candidates = append(candidates, profile.Email)
	for _, candidate := range candidates {
		address := strings.TrimSpace(candidate)
		if domain.ValidContactEmail(address) {
			actions.Email.Enabled = true
			actions.Email.Address = address
			actions.Email.MailtoURL = "mailto:" + url.PathEscape(address)
			break
		}
	}
	if !actions.Email.Enabled {
		actions.Email.DisabledReason = "email_unavailable"
	}
	return actions
}
