package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type fakeCalendarInviteResponderRepo struct {
	event          *domain.Event
	getByIDErr     error
	isAttendee     bool
	isAttendeeErr  error
	removeErr      error
	updateErr      error
	previousStatus string
	statusChanged  bool
	deleteRemErr   error
	markReadErr    error
	removedEventID uuid.UUID
	removedUserID  int64
	updateEventID  uuid.UUID
	updateUserID   int64
	updateStatus   string
	updateComment  *string
	deleteEventID  uuid.UUID
	deleteUserID   int64
	markReadUserID int64
	markReadEvent  string
}

func (f *fakeCalendarInviteResponderRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Event, error) {
	return f.event, f.getByIDErr
}

func (f *fakeCalendarInviteResponderRepo) IsAttendee(_ context.Context, _ uuid.UUID, _ int64) (bool, error) {
	return f.isAttendee, f.isAttendeeErr
}

func (f *fakeCalendarInviteResponderRepo) RemoveAttendee(_ context.Context, eventID uuid.UUID, userID int64) error {
	f.removedEventID = eventID
	f.removedUserID = userID
	return f.removeErr
}

func (f *fakeCalendarInviteResponderRepo) DeleteReminder(_ context.Context, eventID uuid.UUID, userID int64) error {
	f.deleteEventID = eventID
	f.deleteUserID = userID
	return f.deleteRemErr
}

func (f *fakeCalendarInviteResponderRepo) UpdateAttendeeStatus(_ context.Context, eventID uuid.UUID, userID int64, status string, comment *string) (string, bool, error) {
	f.updateEventID = eventID
	f.updateUserID = userID
	f.updateStatus = status
	f.updateComment = comment
	return f.previousStatus, f.statusChanged || f.previousStatus == "", f.updateErr
}

func (f *fakeCalendarInviteResponderRepo) MarkInviteNotificationsAsRead(_ context.Context, userID int64, eventID string) error {
	f.markReadUserID = userID
	f.markReadEvent = eventID
	return f.markReadErr
}

type fakeCalendarInviteUserRepo struct {
	user *domain.User
	err  error
}

func (f *fakeCalendarInviteUserRepo) GetByID(_ context.Context, _ int64) (*domain.User, error) {
	return f.user, f.err
}

type fakeNotificationSender struct {
	created []domain.CreateNotificationRequest
}

func (f *fakeNotificationSender) CreateAndSendNotification(_ context.Context, req domain.CreateNotificationRequest) error {
	f.created = append(f.created, req)
	return nil
}

func (f *fakeNotificationSender) SendCalendarEventEmail(string, string, string, string, time.Time, time.Time) {
}

func (f *fakeNotificationSender) SendCalendarEventNotification(context.Context, int64, *int64, string, string, string, string, time.Time, time.Time, string) {
}

func (f *fakeNotificationSender) SendCalendarInviteEmail(context.Context, uuid.UUID, int64, *int64, string, string, string, time.Time, time.Time, bool, bool, int) {
}

func TestRespondToInviteWithDeps_AcceptedUpdatesAttendeeStatus(t *testing.T) {
	t.Parallel()

	eventID := uuid.New()
	comment := "be there"
	repo := &fakeCalendarInviteResponderRepo{
		event:      &domain.Event{ID: eventID, UserID: 77, Title: "Weekly sync"},
		isAttendee: true,
	}

	err := respondToInviteWithDeps(
		context.Background(),
		11,
		eventID,
		&domain.RespondToInviteRequest{Status: "accepted", Comment: &comment},
		repo,
		&fakeCalendarInviteUserRepo{},
		&fakeNotificationSender{},
		false,
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if repo.updateEventID != eventID || repo.updateUserID != 11 {
		t.Fatalf("expected attendee status update for %s/%d, got %s/%d", eventID, 11, repo.updateEventID, repo.updateUserID)
	}
	if repo.updateStatus != "accepted" {
		t.Fatalf("expected accepted status, got %q", repo.updateStatus)
	}
	if repo.updateComment == nil || *repo.updateComment != comment {
		t.Fatalf("expected comment %q, got %#v", comment, repo.updateComment)
	}
	if repo.removedUserID != 0 {
		t.Fatalf("did not expect attendee removal on accept")
	}
	if repo.markReadUserID != 11 || repo.markReadEvent != eventID.String() {
		t.Fatalf("expected invite notifications marked read for user %d and event %s", 11, eventID)
	}
}

func TestRespondToInviteWithDeps_DeclinedMarksAttendeeDeclined(t *testing.T) {
	t.Parallel()

	eventID := uuid.New()
	repo := &fakeCalendarInviteResponderRepo{
		event:      &domain.Event{ID: eventID, UserID: 77, Title: "Weekly sync"},
		isAttendee: true,
	}
	notifier := &fakeNotificationSender{}
	userRepo := &fakeCalendarInviteUserRepo{
		user: &domain.User{FirstName: "Hamza", LastName: "Kabylbek"},
	}

	err := respondToInviteWithDeps(
		context.Background(),
		11,
		eventID,
		&domain.RespondToInviteRequest{Status: "declined"},
		repo,
		userRepo,
		notifier,
		false,
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Now declined updates status instead of removing attendee
	if repo.updateEventID != eventID || repo.updateUserID != 11 {
		t.Fatalf("expected attendee status update for %s/%d, got %s/%d", eventID, 11, repo.updateEventID, repo.updateUserID)
	}
	if repo.updateStatus != "declined" {
		t.Fatalf("expected declined status, got %q", repo.updateStatus)
	}
	if repo.removedUserID != 0 {
		t.Fatalf("did not expect attendee removal on decline")
	}
	if len(notifier.created) != 1 {
		t.Fatalf("expected organizer notification on decline, got %d", len(notifier.created))
	}
	if notifier.created[0].Metadata["event_id"] != eventID.String() {
		t.Fatalf("expected decline notification to carry event_id %s, got %q", eventID, notifier.created[0].Metadata["event_id"])
	}
	if repo.markReadUserID != 11 || repo.markReadEvent != eventID.String() {
		t.Fatalf("expected invite notifications marked read for user %d and event %s", 11, eventID)
	}
}

func TestRespondToInviteWithDeps_ForbiddenForNonAttendee(t *testing.T) {
	t.Parallel()

	eventID := uuid.New()
	repo := &fakeCalendarInviteResponderRepo{
		event:      &domain.Event{ID: eventID, UserID: 77, Title: "Weekly sync"},
		isAttendee: false,
	}

	err := respondToInviteWithDeps(
		context.Background(),
		11,
		eventID,
		&domain.RespondToInviteRequest{Status: "accepted"},
		repo,
		&fakeCalendarInviteUserRepo{},
		&fakeNotificationSender{},
		false,
	)
	if err == nil {
		t.Fatal("expected forbidden error, got nil")
	}

	appErr := apperrors.ToAppError(err)
	if appErr.Code != apperrors.ErrCodeForbidden {
		t.Fatalf("expected forbidden error code, got %s", appErr.Code)
	}
	if repo.updateUserID != 0 || repo.removedUserID != 0 || repo.markReadUserID != 0 {
		t.Fatal("did not expect any attendee updates or notification mutations for non-attendee")
	}
}

func TestRespondToInviteWithDeps_TentativeUpdatesStatus(t *testing.T) {
	t.Parallel()

	eventID := uuid.New()
	comment := "might be late"
	repo := &fakeCalendarInviteResponderRepo{
		event:      &domain.Event{ID: eventID, UserID: 77, Title: "Weekly sync"},
		isAttendee: true,
	}

	err := respondToInviteWithDeps(
		context.Background(),
		11,
		eventID,
		&domain.RespondToInviteRequest{Status: "tentative", Comment: &comment},
		repo,
		&fakeCalendarInviteUserRepo{},
		&fakeNotificationSender{},
		false,
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if repo.updateEventID != eventID || repo.updateUserID != 11 {
		t.Fatalf("expected attendee status update for %s/%d, got %s/%d", eventID, 11, repo.updateEventID, repo.updateUserID)
	}
	if repo.updateStatus != "tentative" {
		t.Fatalf("expected tentative status, got %q", repo.updateStatus)
	}
	if repo.updateComment == nil || *repo.updateComment != comment {
		t.Fatalf("expected comment %q, got %#v", comment, repo.updateComment)
	}
	if repo.removedUserID != 0 {
		t.Fatalf("did not expect attendee removal on tentative")
	}
}

func TestRespondToInviteWithDeps_InvalidStatus(t *testing.T) {
	t.Parallel()

	eventID := uuid.New()
	repo := &fakeCalendarInviteResponderRepo{
		event:      &domain.Event{ID: eventID, UserID: 77, Title: "Weekly sync"},
		isAttendee: true,
	}

	err := respondToInviteWithDeps(
		context.Background(),
		11,
		eventID,
		&domain.RespondToInviteRequest{Status: "maybe"},
		repo,
		&fakeCalendarInviteUserRepo{},
		&fakeNotificationSender{},
		false,
	)
	if err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}

	appErr := apperrors.ToAppError(err)
	if appErr.Code != apperrors.ErrCodeBadRequest {
		t.Fatalf("expected bad request error code, got %s", appErr.Code)
	}
}

func TestRespondToInviteWithDeps_RepeatedDeclineDoesNotNotifyAgain(t *testing.T) {
	eventID := uuid.New()
	repo := &fakeCalendarInviteResponderRepo{
		event: &domain.Event{ID: eventID, UserID: 77}, isAttendee: true,
		previousStatus: "declined", statusChanged: false,
	}
	notifier := &fakeNotificationSender{}
	changed, err := respondToInviteWithDepsChanged(context.Background(), 11, eventID,
		&domain.RespondToInviteRequest{Status: "declined"}, repo, &fakeCalendarInviteUserRepo{}, notifier, false)
	if err != nil || changed || len(notifier.created) != 0 {
		t.Fatalf("repeated decline should be idempotent: changed=%v notifications=%d err=%v", changed, len(notifier.created), err)
	}
}

func TestRespondToInviteWithDeps_RejectsOversizedComment(t *testing.T) {
	comment := strings.Repeat("x", 1001)
	repo := &fakeCalendarInviteResponderRepo{event: &domain.Event{ID: uuid.New(), UserID: 77}, isAttendee: true}
	err := respondToInviteWithDeps(context.Background(), 11, repo.event.ID,
		&domain.RespondToInviteRequest{Status: "accepted", Comment: &comment}, repo, nil, nil, false)
	if apperrors.ToAppError(err).Code != apperrors.ErrCodeBadRequest || repo.updateUserID != 0 {
		t.Fatalf("oversized comment must be rejected before mutation: %v", err)
	}
}
