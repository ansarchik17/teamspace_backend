package service

import "github.com/west6ide/workspace-backend/internal/domain"

// Invitees need names and RSVP statuses, not the other guests' private
// profile fields or free-form response notes. The organizer keeps full detail.
func redactCalendarAttendees(attendees []domain.EventAttendee, viewerID, organizerID int64) {
	if viewerID == organizerID {
		return
	}
	for i := range attendees {
		if attendees[i].UserID == viewerID {
			continue
		}
		attendees[i].Comment = nil
		if user := attendees[i].User; user != nil {
			attendees[i].User = &domain.UserResponse{
				ID: user.ID, Username: user.Username,
				FirstName: user.FirstName, LastName: user.LastName,
				FullName: user.FullName, AvatarURL: user.AvatarURL,
				IsActive: user.IsActive,
			}
		}
	}
}
