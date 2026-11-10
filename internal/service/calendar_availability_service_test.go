package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func TestRedactCalendarConflictsKeepsOnlyOwnEventDetails(t *testing.T) {
	eventID := uuid.New()
	start := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	conflicts := []domain.CalendarConflictItem{
		{UserID: 1, EventID: eventID, EventTitle: "My event", StartTime: start, EndTime: start.Add(time.Hour)},
		{UserID: 2, EventID: eventID, EventTitle: "Private event", StartTime: start, EndTime: start.Add(time.Hour)},
	}
	redactCalendarConflicts(conflicts, 1)
	if conflicts[0].EventID != eventID || conflicts[0].EventTitle != "My event" {
		t.Fatal("own event details were changed")
	}
	if conflicts[1].EventID != uuid.Nil || conflicts[1].EventTitle != "Занято" || !conflicts[1].StartTime.IsZero() || !conflicts[1].EndTime.IsZero() {
		t.Fatal("another user's event details were exposed")
	}
}

func TestMergeParticipantIDs(t *testing.T) {
	got := mergeParticipantIDs(38, []int64{6, 38, 6, 36, 0}, true)
	want := []int64{38, 6, 36}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestParseWorkdayTime(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Almaty")
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, loc)
	got, err := parseWorkdayTime(day, "09:30", loc)
	if err != nil {
		t.Fatalf("parseWorkdayTime() error = %v", err)
	}
	if got.Hour() != 9 || got.Minute() != 30 {
		t.Fatalf("got = %v, want 09:30", got)
	}
}

func TestCollectSlotConflicts(t *testing.T) {
	slotStart := time.Date(2026, 5, 29, 6, 0, 0, 0, time.UTC)
	slotEnd := slotStart.Add(time.Hour)
	eventID := uuid.MustParse("8ecce32a-7cab-4cad-8f2b-94842a7da02c")

	conflicts := collectSlotConflicts([]repository.CalendarConflictRow{
		{
			UserID:     38,
			UserName:   "User 38",
			Username:   "u38",
			Email:      "u38@test",
			EventID:    eventID,
			EventTitle: "Busy",
			StartTime:  slotStart.Add(-time.Hour),
			EndTime:    slotEnd,
			Status:     "accepted",
		},
	}, 38, slotStart, slotEnd)

	if len(conflicts) != 1 {
		t.Fatalf("len(conflicts) = %d, want 1", len(conflicts))
	}
	if conflicts[0].OverlapMinutes != 60 {
		t.Fatalf("overlap = %d, want 60", conflicts[0].OverlapMinutes)
	}
}

func TestScorePartialSuggestedSlot(t *testing.T) {
	slot := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	base := scoreSuggestedSlot(slot, slot, 0)
	partial := scorePartialSuggestedSlot(slot, slot, 0, 1, 3, 2)
	if partial >= base {
		t.Fatalf("partial score = %d, expected less than full score %d", partial, base)
	}
}

func TestRedactFreeBusyInterval(t *testing.T) {
	eventID := uuid.New()
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 28, 11, 0, 0, 0, time.UTC)

	// Own event: keeps eventID and title
	own := domain.CalendarBusyIntervalItem{
		StartTime:  start,
		EndTime:    end,
		Status:     "busy",
		EventID:    &eventID,
		EventTitle: "Top Secret Project",
	}
	redactedOwn := redactFreeBusyInterval(own, 10, 10)
	if redactedOwn.EventID == nil || *redactedOwn.EventID != eventID || redactedOwn.EventTitle != "Top Secret Project" {
		t.Fatalf("own event was unexpectedly modified: %+v", redactedOwn)
	}

	// Other user busy: eventID masked to nil, title masked to "Занято", times preserved for timeline
	other := domain.CalendarBusyIntervalItem{
		StartTime:  start,
		EndTime:    end,
		Status:     "busy",
		EventID:    &eventID,
		EventTitle: "Confidential Strategy Meeting",
	}
	redactedOther := redactFreeBusyInterval(other, 20, 10)
	if redactedOther.EventID != nil {
		t.Fatalf("other user's event ID was exposed: %v", redactedOther.EventID)
	}
	if redactedOther.EventTitle != "Занято" {
		t.Fatalf("other user's event title was exposed: %s", redactedOther.EventTitle)
	}
	if !redactedOther.StartTime.Equal(start) || !redactedOther.EndTime.Equal(end) {
		t.Fatalf("other user's interval times were changed: %v - %v", redactedOther.StartTime, redactedOther.EndTime)
	}

	// Other user tentative: title masked to "Предварительно"
	otherTentative := domain.CalendarBusyIntervalItem{
		StartTime:  start,
		EndTime:    end,
		Status:     "tentative",
		EventID:    &eventID,
		EventTitle: "Tentative Appointment",
	}
	redactedTentative := redactFreeBusyInterval(otherTentative, 20, 10)
	if redactedTentative.EventID != nil || redactedTentative.EventTitle != "Предварительно" {
		t.Fatalf("tentative title mask expected, got: %s", redactedTentative.EventTitle)
	}
}

func TestBuildConflictAlerts(t *testing.T) {
	eventID := uuid.New()
	start := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)

	// Hard conflict for own event
	ownConflict := domain.CalendarConflictItem{
		UserID:         10,
		UserName:       "Organizer",
		EventID:        eventID,
		EventTitle:     "Sprint Planning",
		StartTime:      start,
		EndTime:        end,
		Status:         "accepted",
		OverlapMinutes: 45,
	}
	alertOwn := buildConflictAlert(ownConflict, 10)
	if alertOwn.Severity != "error" || alertOwn.Type != "overlap" {
		t.Fatalf("expected error/overlap alert, got: %+v", alertOwn)
	}
	if alertOwn.EventID == nil || *alertOwn.EventID != eventID {
		t.Fatalf("expected own event ID in alert, got: %v", alertOwn.EventID)
	}
	if alertOwn.EventTitle != "Sprint Planning" {
		t.Fatalf("expected own event title in alert, got: %s", alertOwn.EventTitle)
	}

	// Tentative conflict for other participant: warning severity, eventID nil
	otherTentative := domain.CalendarConflictItem{
		UserID:         20,
		UserName:       "Alice Cooper",
		Username:       "alice",
		EventID:        eventID,
		EventTitle:     "Private Dentist",
		StartTime:      start,
		EndTime:        end,
		Status:         "tentative",
		OverlapMinutes: 30,
	}
	alertOther := buildConflictAlert(otherTentative, 10)
	if alertOther.Severity != "warning" || alertOther.Type != "tentative" {
		t.Fatalf("expected warning/tentative alert, got: %+v", alertOther)
	}
	if alertOther.EventID != nil {
		t.Fatalf("other user's event ID should be nil in alert, got: %v", alertOther.EventID)
	}
	if alertOther.EventTitle != "Предварительно" {
		t.Fatalf("private title leaked: %+v", alertOther)
	}
	for _, status := range []string{"accepted", "pending", "tentative"} {
		otherTentative.Status = status
		alert := buildConflictAlert(otherTentative, 10)
		encoded, err := json.Marshal(alert)
		if err != nil || strings.Contains(string(encoded), "Private Dentist") || strings.Contains(string(encoded), eventID.String()) {
			t.Fatalf("private event leaked in serialized alert: %s (%v)", encoded, err)
		}
	}
	if alertOther.OverlapMinutes != 30 {
		t.Fatalf("expected 30 overlap minutes, got: %d", alertOther.OverlapMinutes)
	}
}

func TestTotalBusyMinutesUnionsIntervals(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	items := []domain.CalendarBusyIntervalItem{
		{StartTime: start.Add(30 * time.Minute), EndTime: start.Add(90 * time.Minute), Status: "tentative"},
		{StartTime: start, EndTime: start.Add(time.Hour), Status: "busy"},
		{StartTime: start.Add(15 * time.Minute), EndTime: start.Add(20 * time.Minute), Status: "busy"},
		{StartTime: start.Add(2 * time.Hour), EndTime: start.Add(3 * time.Hour), Status: "busy"},
	}
	if got := totalBusyMinutes(items); got != 150 {
		t.Fatalf("expected 150 occupied minutes, got %d", got)
	}
	if totalBusyMinutes(nil) != 0 {
		t.Fatal("empty schedule must have zero occupied minutes")
	}
}

func TestFreeBusySlotStatesBoundariesAndPriority(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	rows := map[int64][]repository.CalendarConflictRow{1: {
		{StartTime: start.Add(-time.Hour), EndTime: start, Status: "accepted"},
		{StartTime: start.Add(15 * time.Minute), EndTime: start.Add(45 * time.Minute), Status: "tentative"},
		{StartTime: start.Add(30 * time.Minute), EndTime: start.Add(time.Hour), Status: "pending"},
		{StartTime: start.Add(90 * time.Minute), EndTime: start.Add(3 * time.Hour), Status: "accepted"},
	}}
	got := freeBusySlotStates(rows, start, start.Add(2*time.Hour), 30*time.Minute, 4)[1]
	want := []uint8{1, 2, 0, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slot %d: got %v, want %v", i, got, want)
		}
	}
}

func TestFreeBusyRejectsInvalidOrOversizedRequestsBeforeDatabase(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	participants := make([]int64, 99)
	for i := range participants {
		participants[i] = int64(i + 2)
	}
	for _, tc := range []struct {
		name     string
		step     int
		duration time.Duration
		timezone string
		ids      []int64
	}{
		{name: "timezone", duration: time.Hour, timezone: "Invalid/Timezone"},
		{name: "negative step", duration: time.Hour, step: -1},
		{name: "small step", duration: time.Hour, step: 1},
		{name: "large step", duration: time.Hour, step: 241},
		{name: "slot limit", duration: 90 * 24 * time.Hour, step: 5},
		{name: "cell limit", duration: 30 * 24 * time.Hour, step: 30, ids: participants},
		{name: "participant limit", duration: time.Hour, ids: make([]int64, 101)},
		{name: "invalid ID", duration: time.Hour, ids: []int64{-1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &CalendarAvailabilityService{}
			_, err := s.GetFreeBusy(context.Background(), 1, domain.CalendarFreeBusyRequest{
				StartTime: start, EndTime: start.Add(tc.duration), Timezone: tc.timezone, SlotStepMinutes: tc.step, AttendeeIDs: tc.ids,
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			if appErr := apperrors.ToAppError(err); appErr.StatusCode != 400 {
				t.Fatalf("expected rejection before database access, got %+v", appErr)
			}
		})
	}
}

func TestAvailabilityExcludesOnlyEditedOccurrenceIncludingMovedOverride(t *testing.T) {
	id := uuid.New()
	rule := "DAILY"
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	next := start.AddDate(0, 0, 1)
	rows := []repository.CalendarConflictRow{{UserID: 1, EventID: id, EventTitle: "Series", StartTime: start, EndTime: start.Add(time.Hour), RecurrenceRule: &rule}}
	// The edited occurrence was already moved onto tomorrow's regular occurrence.
	exceptions := []domain.CalendarEventException{{EventID: id, OccurrenceDate: start, ExceptionType: "modified", EventData: &domain.OccurrenceDetails{
		Title: "Moved", StartTime: next, EndTime: next.Add(time.Hour),
	}}}
	got := expandAvailabilityRows(rows, exceptions, next, next.Add(time.Hour), &id, &start)
	if len(got) != 1 || got[0].EventTitle != "Series" || !got[0].StartTime.Equal(next) {
		t.Fatalf("other occurrence must remain a conflict: %+v", got)
	}
	got = expandAvailabilityRows(rows, nil, start, start.Add(time.Hour), &id, &start)
	if len(got) != 0 {
		t.Fatalf("edited occurrence must not conflict with itself: %+v", got)
	}
}

func TestRecurringEventsAndExceptionsInAvailability(t *testing.T) {
	rule := "WEEKLY"
	eventID := uuid.New()
	baseStart := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	baseEnd := baseStart.Add(time.Hour)

	// Series repeats weekly on Mondays 10:00 - 11:00 UTC
	event := domain.Event{
		ID:                 eventID,
		UserID:             1,
		Title:              "Weekly Sync",
		StartTime:          baseStart,
		EndTime:            baseEnd,
		RecurrenceRule:     &rule,
		RecurrenceInterval: 1,
	}

	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)

	// Without exceptions, Sept 28 occurrence exists
	occs := applyCalendarExceptions([]domain.Event{event}, nil, from, to)
	if len(occs) != 1 {
		t.Fatalf("expected 1 occurrence, got %d", len(occs))
	}
	expectedStart := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	if !occs[0].StartTime.Equal(expectedStart) {
		t.Fatalf("expected occurrence at %v, got %v", expectedStart, occs[0].StartTime)
	}

	// With deleted exception on Sept 28, occurrence is suppressed (slot is FREE!)
	deletedEx := domain.CalendarEventException{
		EventID:        eventID,
		OccurrenceDate: expectedStart,
		ExceptionType:  "deleted",
	}
	occsAfterDelete := applyCalendarExceptions([]domain.Event{event}, []domain.CalendarEventException{deletedEx}, from, to)
	if len(occsAfterDelete) != 0 {
		t.Fatalf("expected 0 occurrences after deletion exception, got %d", len(occsAfterDelete))
	}

	// With modified exception moving it to 14:00 - 15:00 UTC
	modifiedDetails := domain.OccurrenceDetails{
		Title:     "Weekly Sync Moved",
		StartTime: time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC),
	}
	modifiedEx := domain.CalendarEventException{
		EventID:        eventID,
		OccurrenceDate: expectedStart,
		ExceptionType:  "modified",
		EventData:      &modifiedDetails,
	}
	occsAfterModify := applyCalendarExceptions([]domain.Event{event}, []domain.CalendarEventException{modifiedEx}, from, to)
	if len(occsAfterModify) != 1 {
		t.Fatalf("expected 1 modified occurrence, got %d", len(occsAfterModify))
	}
	if !occsAfterModify[0].StartTime.Equal(modifiedDetails.StartTime) {
		t.Fatalf("expected modified start time %v, got %v", modifiedDetails.StartTime, occsAfterModify[0].StartTime)
	}
	if occsAfterModify[0].Title != "Weekly Sync Moved" {
		t.Fatalf("expected modified title, got %s", occsAfterModify[0].Title)
	}
}
