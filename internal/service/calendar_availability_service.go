package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
)

type calendarBusyInterval struct {
	start time.Time
	end   time.Time
}

const maxAvailabilityParticipants = 100
const maxFreeBusySlots = 5000
const maxFreeBusyCells = 100000

type calendarSuggestConfig struct {
	date             string
	timezone         string
	location         *time.Location
	checkedUserIDs   []int64
	includeOrganizer bool
	duration         time.Duration
	durationMinutes  int
	workdayStart     string
	workdayEnd       string
	slotStep         time.Duration
	slotStepMinutes  int
	workdayStartUTC  time.Time
	workdayEndUTC    time.Time
	limit            int
	allowPartial     bool
	excludeEventID   *uuid.UUID
}

type CalendarAvailabilityService struct {
	calendarRepo     *repository.CalendarRepository
	userRepo         *repository.UserRepository
	workdayStartHour int
	workdayEndHour   int
}

func NewCalendarAvailabilityService(
	calendarRepo *repository.CalendarRepository,
	userRepo *repository.UserRepository,
	cfg config.CalendarMeetingsConfig,
) *CalendarAvailabilityService {
	startHour := cfg.WorkdayStartHour
	endHour := cfg.WorkdayEndHour
	if startHour < 0 || startHour > 23 {
		startHour = 9
	}
	if endHour <= startHour || endHour > 24 {
		endHour = 18
	}
	return &CalendarAvailabilityService{
		calendarRepo:     calendarRepo,
		userRepo:         userRepo,
		workdayStartHour: startHour,
		workdayEndHour:   endHour,
	}
}

func (s *CalendarAvailabilityService) loadExpandedBusyRows(
	ctx context.Context,
	userIDs []int64,
	from, to time.Time,
	excludeEventID *uuid.UUID,
	excludeOccurrence ...*time.Time,
) ([]repository.CalendarConflictRow, error) {
	if len(userIDs) == 0 {
		return []repository.CalendarConflictRow{}, nil
	}

	queryExcludeID := excludeEventID
	var occurrenceStart *time.Time
	if len(excludeOccurrence) > 0 {
		occurrenceStart = excludeOccurrence[0]
	}
	if occurrenceStart != nil {
		queryExcludeID = nil
	}
	rawRows, err := s.calendarRepo.ListBusyEventsInRange(ctx, userIDs, from, to, queryExcludeID)
	if err != nil {
		return nil, err
	}

	var recurringIDs []uuid.UUID
	recurringSeen := make(map[uuid.UUID]bool)
	for _, row := range rawRows {
		if row.RecurrenceRule != nil && *row.RecurrenceRule != "" && !recurringSeen[row.EventID] {
			recurringSeen[row.EventID] = true
			recurringIDs = append(recurringIDs, row.EventID)
		}
	}

	var exceptions []domain.CalendarEventException
	if len(recurringIDs) > 0 {
		exs, err := s.calendarRepo.ListEventExceptions(ctx, recurringIDs)
		if err != nil {
			return nil, err
		}
		exceptions = exs
	}

	return expandAvailabilityRows(rawRows, exceptions, from, to, excludeEventID, occurrenceStart), nil
}

func expandAvailabilityRows(rawRows []repository.CalendarConflictRow, exceptions []domain.CalendarEventException, from, to time.Time, excludeEventID *uuid.UUID, occurrenceStart *time.Time) []repository.CalendarConflictRow {
	exceptionsByEvent := make(map[uuid.UUID][]domain.CalendarEventException)
	for _, ex := range exceptions {
		if excludeEventID != nil && occurrenceStart != nil && ex.EventID == *excludeEventID && ex.OccurrenceDate.Equal(*occurrenceStart) {
			continue
		}
		exceptionsByEvent[ex.EventID] = append(exceptionsByEvent[ex.EventID], ex)
	}
	if excludeEventID != nil && occurrenceStart != nil {
		exceptionsByEvent[*excludeEventID] = append(exceptionsByEvent[*excludeEventID], domain.CalendarEventException{
			EventID: *excludeEventID, OccurrenceDate: *occurrenceStart, ExceptionType: "deleted",
		})
	}

	result := make([]repository.CalendarConflictRow, 0, len(rawRows))
	for _, row := range rawRows {
		if row.RecurrenceRule == nil || *row.RecurrenceRule == "" {
			if row.StartTime.Before(to) && row.EndTime.After(from) {
				result = append(result, row)
			}
			continue
		}

		ev := domain.Event{
			ID:                 row.EventID,
			UserID:             row.UserID,
			Title:              row.EventTitle,
			StartTime:          row.StartTime,
			EndTime:            row.EndTime,
			RecurrenceRule:     row.RecurrenceRule,
			RecurrenceInterval: row.RecurrenceInterval,
			RecurrenceUntil:    row.RecurrenceUntil,
			IsAllDay:           row.IsAllDay,
		}

		eventExs := exceptionsByEvent[row.EventID]
		expandedOccs := applyCalendarExceptions([]domain.Event{ev}, eventExs, from, to)

		for _, occ := range expandedOccs {
			occRow := row
			occRow.StartTime = occ.StartTime
			occRow.EndTime = occ.EndTime
			occRow.EventTitle = occ.Title
			result = append(result, occRow)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if !result[i].StartTime.Equal(result[j].StartTime) {
			return result[i].StartTime.Before(result[j].StartTime)
		}
		return result[i].UserID < result[j].UserID
	})

	return result
}

func buildConflictAlert(item domain.CalendarConflictItem, requesterID int64, contexts ...context.Context) domain.CalendarConflictAlert {
	ctx := uiContext(contexts)
	isTentative := strings.EqualFold(item.Status, "tentative")
	severity := "error"
	alertType := "overlap"
	if isTentative {
		severity = "warning"
		alertType = "tentative"
	}

	var message string
	isOwn := item.UserID == requesterID
	if !isOwn {
		item.EventTitle = i18n.UIMessage(ctx, "Занято")
		item.Username = ""
		if isTentative {
			item.EventTitle = i18n.UIMessage(ctx, "Предварительно")
		}
	}
	timeFormat := "15:04"

	if isOwn {
		if isTentative {
			message = i18n.UIMessage(ctx, "У вас предварительная встреча: '%s' (%s - %s)",
				item.EventTitle, item.StartTime.Format(timeFormat), item.EndTime.Format(timeFormat))
		} else {
			message = i18n.UIMessage(ctx, "У вас уже запланировано событие: '%s' (%s - %s, наложение %d мин)",
				item.EventTitle, item.StartTime.Format(timeFormat), item.EndTime.Format(timeFormat), item.OverlapMinutes)
		}
	} else {
		displayName := item.UserName
		if displayName == "" {
			displayName = item.Username
		}
		if displayName == "" {
			displayName = i18n.UIMessage(ctx, "Участник %d", item.UserID)
		}

		if isTentative {
			message = i18n.UIMessage(ctx, "Участник %s имеет предварительную запись (tentative, наложение %d мин)",
				displayName, item.OverlapMinutes)
		} else {
			message = i18n.UIMessage(ctx, "Участник %s занят в это время (наложение %d мин)",
				displayName, item.OverlapMinutes)
		}
	}

	var eventIDPtr *uuid.UUID
	if isOwn && item.EventID != uuid.Nil {
		idCopy := item.EventID
		eventIDPtr = &idCopy
	}

	return domain.CalendarConflictAlert{
		Type:           alertType,
		Severity:       severity,
		UserID:         item.UserID,
		UserName:       item.UserName,
		Username:       item.Username,
		EventID:        eventIDPtr,
		EventTitle:     item.EventTitle,
		StartTime:      item.StartTime,
		EndTime:        item.EndTime,
		OverlapMinutes: item.OverlapMinutes,
		Message:        message,
	}
}

func redactFreeBusyInterval(item domain.CalendarBusyIntervalItem, userID, requesterID int64, contexts ...context.Context) domain.CalendarBusyIntervalItem {
	ctx := uiContext(contexts)
	if userID == requesterID {
		return item
	}
	item.EventID = nil
	if strings.EqualFold(item.Status, "tentative") {
		item.EventTitle = i18n.UIMessage(ctx, "Предварительно")
	} else {
		item.EventTitle = i18n.UIMessage(ctx, "Занято")
	}
	return item
}

func (s *CalendarAvailabilityService) CheckAvailability(
	ctx context.Context,
	requesterID int64,
	req domain.CalendarAvailabilityRequest,
) (*domain.CalendarAvailabilityResponse, error) {
	return s.checkAvailability(ctx, requesterID, req, nil)
}

func (s *CalendarAvailabilityService) checkAvailability(ctx context.Context, requesterID int64, req domain.CalendarAvailabilityRequest, occurrenceStart *time.Time) (*domain.CalendarAvailabilityResponse, error) {
	if !req.EndTime.After(req.StartTime) {
		return nil, apperrors.BadRequest("end_time must be later than start_time")
	}

	userIDs, err := s.resolveParticipantIDs(ctx, requesterID, req.AttendeeIDs, true)
	if err != nil {
		return nil, err
	}

	busyRows, err := s.loadExpandedBusyRows(ctx, userIDs, req.StartTime, req.EndTime, req.ExcludeEventID, occurrenceStart)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check calendar availability", err)
	}

	conflicts := make([]domain.CalendarConflictItem, 0, len(busyRows))
	for _, row := range busyRows {
		if !row.StartTime.Before(req.EndTime) || !row.EndTime.After(req.StartTime) {
			continue
		}
		overlapStart := maxTime(row.StartTime, req.StartTime)
		overlapEnd := minTime(row.EndTime, req.EndTime)
		overlapMinutes := int(overlapEnd.Sub(overlapStart).Minutes())
		conflicts = append(conflicts, domain.CalendarConflictItem{
			UserID:         row.UserID,
			UserName:       row.UserName,
			Username:       row.Username,
			Email:          row.Email,
			AvatarURL:      row.AvatarURL,
			EventID:        row.EventID,
			EventTitle:     row.EventTitle,
			StartTime:      row.StartTime,
			EndTime:        row.EndTime,
			Status:         row.Status,
			OverlapMinutes: overlapMinutes,
		})
	}

	alerts := make([]domain.CalendarConflictAlert, 0, len(conflicts))
	hasHard := false
	hasTentative := false
	for _, c := range conflicts {
		alert := buildConflictAlert(c, requesterID, ctx)
		if alert.Severity == "error" {
			hasHard = true
		} else {
			hasTentative = true
		}
		alerts = append(alerts, alert)
	}

	enrichedConflicts := enrichAvailabilityConflicts(conflicts, req.StartTime, req.EndTime)
	redactCalendarConflicts(enrichedConflicts, requesterID, ctx)
	groupedConflicts := groupAvailabilityConflicts(enrichedConflicts)
	conflictedUserIDs := collectConflictedUserIDs(enrichedConflicts)

	slots := make([]domain.CalendarSuggestedSlot, 0)
	partialSlots := make([]domain.CalendarSuggestedSlot, 0)
	if len(enrichedConflicts) > 0 {
		duration := req.EndTime.Sub(req.StartTime)
		searchFrom := req.StartTime.UTC()
		nowUTC := time.Now().UTC()
		if searchFrom.Before(nowUTC) {
			searchFrom = nowUTC
		}
		searchTo := searchFrom.Add(7 * 24 * time.Hour)

		slots, partialSlots, err = s.suggestSlots(ctx, userIDs, searchFrom, searchTo, duration, req.ExcludeEventID, req.Timezone, 5, 30*time.Minute, true, occurrenceStart)
		if err != nil {
			return nil, err
		}
	}

	if err := s.attachSuggestedSlotUsers(ctx, userIDs, slots, partialSlots); err != nil {
		return nil, err
	}
	redactCalendarSlotConflicts(partialSlots, requesterID, ctx)

	return &domain.CalendarAvailabilityResponse{
		IsAvailable:           len(enrichedConflicts) == 0,
		CheckedUsersCount:     len(userIDs),
		ConflictedUsersCount:  len(conflictedUserIDs),
		CheckedUserIDs:        append([]int64(nil), userIDs...),
		OrganizerIncluded:     containsInt64(userIDs, requesterID),
		Conflicts:             enrichedConflicts,
		ConflictsByUser:       groupedConflicts,
		Alerts:                alerts,
		HasHardConflicts:      hasHard,
		HasTentativeConflicts: hasTentative,
		SuggestedSlots:        slots,
		PartialSuggestedSlots: partialSlots,
	}, nil
}

func (s *CalendarAvailabilityService) GetFreeBusy(
	ctx context.Context,
	requesterID int64,
	req domain.CalendarFreeBusyRequest,
) (*domain.CalendarFreeBusyResponse, error) {
	if req.StartTime.IsZero() || req.EndTime.IsZero() {
		return nil, apperrors.BadRequest("start_time and end_time are required")
	}
	if !req.EndTime.After(req.StartTime) {
		return nil, apperrors.BadRequest("end_time must be later than start_time")
	}
	if req.EndTime.Sub(req.StartTime) > 90*24*time.Hour {
		return nil, apperrors.BadRequest("requested range cannot exceed 90 days")
	}

	attendeeIDs := req.AttendeeIDs
	if len(attendeeIDs) == 0 && len(req.UserIDs) > 0 {
		attendeeIDs = req.UserIDs
	}
	if len(attendeeIDs) > maxAvailabilityParticipants {
		return nil, apperrors.BadRequest("at most 100 participants are allowed")
	}
	includeOrganizer := true
	if req.IncludeOrganizer != nil {
		includeOrganizer = *req.IncludeOrganizer
	}

	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "Asia/Almaty"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, apperrors.BadRequest("invalid timezone")
	}

	slotStepMinutes := req.SlotStepMinutes
	if slotStepMinutes == 0 {
		slotStepMinutes = 30
	}
	if slotStepMinutes < 5 || slotStepMinutes > 240 {
		return nil, apperrors.BadRequest("slot_step_minutes must be between 5 and 240")
	}
	slotDuration := time.Duration(slotStepMinutes) * time.Minute
	participantIDs := mergeParticipantIDs(requesterID, attendeeIDs, includeOrganizer)
	slotCount := int((req.EndTime.Sub(req.StartTime)-1)/slotDuration) + 1
	if slotCount > maxFreeBusySlots || slotCount*len(participantIDs) > maxFreeBusyCells {
		return nil, apperrors.BadRequest("free/busy grid is too large; reduce range or participants, or increase slot_step_minutes")
	}
	userIDs, err := s.resolveParticipantIDs(ctx, requesterID, attendeeIDs, includeOrganizer)
	if err != nil {
		return nil, err
	}

	busyRows, err := s.loadExpandedBusyRows(ctx, userIDs, req.StartTime, req.EndTime, req.ExcludeEventID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load free/busy data", err)
	}

	users, err := s.userRepo.GetByIDs(ctx, userIDs)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load users", err)
	}
	usersByID := make(map[int64]*domain.User, len(users))
	for _, u := range users {
		if u != nil {
			usersByID[u.ID] = u
		}
	}

	rowsByUser := make(map[int64][]repository.CalendarConflictRow, len(userIDs))
	for _, row := range busyRows {
		rowsByUser[row.UserID] = append(rowsByUser[row.UserID], row)
	}

	userFreeBusyList := make([]domain.CalendarUserFreeBusy, 0, len(userIDs))
	allAlerts := make([]domain.CalendarConflictAlert, 0)
	conflictedUsersSet := make(map[int64]bool)

	for _, uid := range userIDs {
		u := usersByID[uid]
		userName := fmt.Sprintf("User %d", uid)
		var username, email string
		var avatarURL *string
		if u != nil {
			userName = buildCalendarUserFullName(u)
			username = u.Username
			email = u.Email
			avatarURL = u.AvatarURL
		}
		if uid != requesterID {
			if userName == username || userName == email {
				userName = fmt.Sprintf("User %d", uid)
			}
			username, email = "", ""
		}

		uRows := rowsByUser[uid]
		intervals := make([]domain.CalendarBusyIntervalItem, 0, len(uRows))
		busyCount := 0
		tentativeCount := 0

		for _, row := range uRows {
			cStart := maxTime(row.StartTime, req.StartTime)
			cEnd := minTime(row.EndTime, req.EndTime)
			if !cEnd.After(cStart) {
				continue
			}

			dur := int(cEnd.Sub(cStart).Minutes())

			status := "busy"
			if strings.EqualFold(row.Status, "tentative") {
				status = "tentative"
				tentativeCount++
			} else {
				busyCount++
			}

			conflictedUsersSet[uid] = true

			conflictItem := domain.CalendarConflictItem{
				UserID:         uid,
				UserName:       userName,
				Username:       username,
				Email:          email,
				AvatarURL:      avatarURL,
				EventID:        row.EventID,
				EventTitle:     row.EventTitle,
				StartTime:      row.StartTime,
				EndTime:        row.EndTime,
				Status:         row.Status,
				OverlapMinutes: dur,
			}
			allAlerts = append(allAlerts, buildConflictAlert(conflictItem, requesterID, ctx))

			item := domain.CalendarBusyIntervalItem{
				StartTime:   cStart.In(location),
				EndTime:     cEnd.In(location),
				Status:      status,
				EventTitle:  row.EventTitle,
				IsRecurring: row.RecurrenceRule != nil && *row.RecurrenceRule != "",
			}
			if row.EventID != uuid.Nil {
				idCopy := row.EventID
				item.EventID = &idCopy
			}
			intervals = append(intervals, redactFreeBusyInterval(item, uid, requesterID, ctx))
		}

		userFreeBusyList = append(userFreeBusyList, domain.CalendarUserFreeBusy{
			UserID:                  uid,
			UserName:                userName,
			Username:                username,
			Email:                   email,
			AvatarURL:               avatarURL,
			TotalBusyMinutes:        totalBusyMinutes(intervals),
			BusyIntervalsCount:      busyCount,
			TentativeIntervalsCount: tentativeCount,
			BusyIntervals:           intervals,
		})
	}

	gridStart := req.StartTime.In(location)
	gridEnd := req.EndTime.In(location)
	var timelineGrid []domain.CalendarTimelineSlot

	freeSlotsCount := 0
	busySlotsCount := 0

	slotUserMap := make(map[int64]domain.CalendarSlotUser, len(userIDs))
	busySlotStates := freeBusySlotStates(rowsByUser, gridStart, gridEnd, slotDuration, slotCount)
	for _, uid := range userIDs {
		u := usersByID[uid]
		name := fmt.Sprintf("User %d", uid)
		if u != nil {
			name = buildCalendarUserFullName(u)
			if uid != requesterID && (name == u.Username || name == u.Email) {
				name = fmt.Sprintf("User %d", uid)
			}
		}
		slotUserMap[uid] = domain.CalendarSlotUser{UserID: uid, FullName: name}
	}

	for slotIndex, slotStart := 0, gridStart; slotStart.Before(gridEnd); slotIndex, slotStart = slotIndex+1, slotStart.Add(slotDuration) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		slotEnd := slotStart.Add(slotDuration)
		if slotEnd.After(gridEnd) {
			slotEnd = gridEnd
		}

		var availableIDs, busyIDs, tentativeIDs []int64
		for _, uid := range userIDs {
			var state uint8
			if states := busySlotStates[uid]; states != nil {
				state = states[slotIndex]
			}
			if state == 2 {
				busyIDs = append(busyIDs, uid)
			} else if state == 1 {
				tentativeIDs = append(tentativeIDs, uid)
			} else {
				availableIDs = append(availableIDs, uid)
			}
		}

		slotStatus := "free"
		if len(busyIDs) > 0 {
			slotStatus = "busy"
			busySlotsCount++
		} else if len(tentativeIDs) > 0 {
			slotStatus = "tentative"
			busySlotsCount++
		} else {
			freeSlotsCount++
		}

		total := len(userIDs)
		var occupancyRate float64
		if total > 0 {
			occupancyRate = float64(len(busyIDs)+len(tentativeIDs)) / float64(total)
		}

		availUsers := make([]domain.CalendarSlotUser, 0, len(availableIDs))
		for _, id := range availableIDs {
			availUsers = append(availUsers, slotUserMap[id])
		}
		busyUsers := make([]domain.CalendarSlotUser, 0, len(busyIDs))
		for _, id := range busyIDs {
			busyUsers = append(busyUsers, slotUserMap[id])
		}
		tentativeUsers := make([]domain.CalendarSlotUser, 0, len(tentativeIDs))
		for _, id := range tentativeIDs {
			tentativeUsers = append(tentativeUsers, slotUserMap[id])
		}

		timelineGrid = append(timelineGrid, domain.CalendarTimelineSlot{
			StartTime:        slotStart,
			EndTime:          slotEnd,
			Status:           slotStatus,
			TotalUsers:       total,
			FreeCount:        len(availableIDs),
			BusyCount:        len(busyIDs),
			TentativeCount:   len(tentativeIDs),
			OccupancyRate:    occupancyRate,
			AvailableUserIDs: availableIDs,
			BusyUserIDs:      busyIDs,
			TentativeUserIDs: tentativeIDs,
			AvailableUsers:   availUsers,
			BusyUsers:        busyUsers,
			TentativeUsers:   tentativeUsers,
		})
	}

	var mutualFreeSlots []domain.CalendarTimeInterval
	var currentFreeStart, currentFreeEnd time.Time
	inFree := false

	for _, slot := range timelineGrid {
		if slot.Status == "free" {
			if !inFree {
				inFree = true
				currentFreeStart = slot.StartTime
				currentFreeEnd = slot.EndTime
			} else {
				currentFreeEnd = slot.EndTime
			}
		} else {
			if inFree {
				dur := int(currentFreeEnd.Sub(currentFreeStart).Minutes())
				if dur > 0 {
					mutualFreeSlots = append(mutualFreeSlots, domain.CalendarTimeInterval{
						StartTime:       currentFreeStart,
						EndTime:         currentFreeEnd,
						DurationMinutes: dur,
					})
				}
				inFree = false
			}
		}
	}
	if inFree {
		dur := int(currentFreeEnd.Sub(currentFreeStart).Minutes())
		if dur > 0 {
			mutualFreeSlots = append(mutualFreeSlots, domain.CalendarTimeInterval{
				StartTime:       currentFreeStart,
				EndTime:         currentFreeEnd,
				DurationMinutes: dur,
			})
		}
	}

	return &domain.CalendarFreeBusyResponse{
		StartTime:       req.StartTime.In(location),
		EndTime:         req.EndTime.In(location),
		Timezone:        timezone,
		SlotStepMinutes: slotStepMinutes,
		Users:           userFreeBusyList,
		MutualFreeSlots: mutualFreeSlots,
		TimelineGrid:    timelineGrid,
		Alerts:          allAlerts,
		Summary: domain.CalendarFreeBusySummary{
			TotalUsers:           len(userIDs),
			AllFree:              len(conflictedUsersSet) == 0,
			ConflictedUsersCount: len(conflictedUsersSet),
			TotalSlotsCount:      len(timelineGrid),
			FreeSlotsCount:       freeSlotsCount,
			BusySlotsCount:       busySlotsCount,
		},
	}, nil
}

// Sum the union of occupied intervals, including tentative reservations.
func totalBusyMinutes(intervals []domain.CalendarBusyIntervalItem) int {
	if len(intervals) == 0 {
		return 0
	}
	ordered := append([]domain.CalendarBusyIntervalItem(nil), intervals...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].StartTime.Before(ordered[j].StartTime) })
	start, end := ordered[0].StartTime, ordered[0].EndTime
	var duration time.Duration
	for _, item := range ordered[1:] {
		if item.StartTime.After(end) {
			duration += end.Sub(start)
			start, end = item.StartTime, item.EndTime
		} else if item.EndTime.After(end) {
			end = item.EndTime
		}
	}
	return int((duration + end.Sub(start)) / time.Minute)
}

// Difference arrays avoid scanning every event for every timeline slot.
func freeBusySlotStates(rowsByUser map[int64][]repository.CalendarConflictRow, from, to time.Time, step time.Duration, count int) map[int64][]uint8 {
	result := make(map[int64][]uint8, len(rowsByUser))
	for uid, rows := range rowsByUser {
		hard, tentative := make([]int, count+1), make([]int, count+1)
		for _, row := range rows {
			start, end := maxTime(row.StartTime, from), minTime(row.EndTime, to)
			if !end.After(start) {
				continue
			}
			first := int(start.Sub(from) / step)
			last := int((end.Sub(from)-1)/step) + 1
			diff := hard
			if strings.EqualFold(row.Status, "tentative") {
				diff = tentative
			}
			diff[first]++
			diff[last]--
		}
		states := make([]uint8, count)
		hardCount, tentativeCount := 0, 0
		for i := range states {
			hardCount += hard[i]
			tentativeCount += tentative[i]
			if hardCount > 0 {
				states[i] = 2
			} else if tentativeCount > 0 {
				states[i] = 1
			}
		}
		result[uid] = states
	}
	return result
}

func (s *CalendarAvailabilityService) SuggestTimes(
	ctx context.Context,
	requesterID int64,
	req domain.CalendarSuggestTimesRequest,
) (*domain.CalendarSuggestTimesResponse, error) {
	cfg, err := s.buildSuggestConfig(ctx, requesterID, req)
	if err != nil {
		return nil, err
	}

	suggestedSlots, partialSlots, err := s.suggestSlots(
		ctx,
		cfg.checkedUserIDs,
		cfg.workdayStartUTC,
		cfg.workdayEndUTC,
		cfg.duration,
		cfg.excludeEventID,
		cfg.timezone,
		cfg.limit,
		cfg.slotStep,
		cfg.allowPartial,
	)
	if err != nil {
		return nil, err
	}

	if err := s.attachSuggestedSlotUsers(ctx, cfg.checkedUserIDs, suggestedSlots, partialSlots); err != nil {
		return nil, err
	}
	redactCalendarSlotConflicts(partialSlots, requesterID, ctx)

	if cfg.limit > 0 {
		if len(suggestedSlots) >= cfg.limit {
			partialSlots = []domain.CalendarSuggestedSlot{}
		} else {
			remaining := cfg.limit - len(suggestedSlots)
			if remaining < len(partialSlots) {
				partialSlots = partialSlots[:remaining]
			}
		}
	}

	markSuggestedSlotType(suggestedSlots, "full")
	markSuggestedSlotType(partialSlots, "partial")
	slots := mergeSuggestedSlots(suggestedSlots, partialSlots, cfg.limit)

	message := fmt.Sprintf("Найдено %d свободных вариантов времени", len(suggestedSlots))
	if len(suggestedSlots) == 1 {
		message = "Найдено 1 свободное время"
	}
	if len(suggestedSlots) == 0 && len(partialSlots) > 0 {
		message = fmt.Sprintf("Полностью свободное время не найдено, доступно %d частичных вариантов", len(partialSlots))
	}

	return &domain.CalendarSuggestTimesResponse{
		Date:            cfg.date,
		Timezone:        cfg.timezone,
		DurationMinutes: cfg.durationMinutes,
		Slots:           slots,
		Message:         message,
	}, nil
}

func (s *CalendarAvailabilityService) resolveParticipantIDs(ctx context.Context, requesterID int64, attendeeIDs []int64, includeOrganizer bool) ([]int64, error) {
	if len(attendeeIDs) > maxAvailabilityParticipants {
		return nil, apperrors.BadRequest("at most 100 participants are allowed")
	}
	for _, id := range attendeeIDs {
		if id <= 0 {
			return nil, apperrors.BadRequest("participant IDs must be positive")
		}
	}
	userIDs := mergeParticipantIDs(requesterID, attendeeIDs, includeOrganizer)
	if len(userIDs) > maxAvailabilityParticipants {
		return nil, apperrors.BadRequest("at most 100 participants are allowed, including the organizer")
	}
	if len(userIDs) == 0 {
		return nil, apperrors.BadRequest("at least one participant must be provided")
	}

	if err := ensureUsersExist(ctx, s.userRepo, userIDs); err != nil {
		return nil, err
	}
	for _, userID := range userIDs {
		if err := ensureUserScopedAccess(ctx, s.userRepo, requesterID, userID); err != nil {
			return nil, err
		}
	}

	return userIDs, nil
}

func redactCalendarConflicts(conflicts []domain.CalendarConflictItem, requesterID int64, contexts ...context.Context) {
	ctx := uiContext(contexts)
	for i := range conflicts {
		if conflicts[i].UserID == requesterID {
			continue
		}
		conflicts[i].EventID = uuid.Nil
		conflicts[i].EventTitle = i18n.UIMessage(ctx, "Занято")
		conflicts[i].StartTime = time.Time{}
		conflicts[i].EndTime = time.Time{}
	}
}

func redactCalendarSlotConflicts(slots []domain.CalendarSuggestedSlot, requesterID int64, contexts ...context.Context) {
	ctx := uiContext(contexts)
	for i := range slots {
		redactCalendarConflicts(slots[i].Conflicts, requesterID, ctx)
	}
}

func mergeParticipantIDs(requesterID int64, attendeeIDs []int64, includeOrganizer bool) []int64 {
	userIDs := make([]int64, 0, len(attendeeIDs)+1)
	seen := map[int64]bool{}
	if includeOrganizer && requesterID > 0 {
		userIDs = append(userIDs, requesterID)
		seen[requesterID] = true
	}
	for _, attendeeID := range attendeeIDs {
		if attendeeID <= 0 || seen[attendeeID] {
			continue
		}
		seen[attendeeID] = true
		userIDs = append(userIDs, attendeeID)
	}
	return userIDs
}

func (s *CalendarAvailabilityService) attachSuggestedSlotUsers(ctx context.Context, userIDs []int64, slotGroups ...[]domain.CalendarSuggestedSlot) error {
	if len(userIDs) == 0 {
		return nil
	}

	users, err := s.userRepo.GetByIDs(ctx, userIDs)
	if err != nil {
		return apperrors.InternalWithErr("failed to load users for suggested slots", err)
	}

	usersByID := make(map[int64]domain.CalendarSlotUser, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		usersByID[user.ID] = domain.CalendarSlotUser{
			UserID:   user.ID,
			FullName: buildCalendarUserFullName(user),
		}
	}

	for _, slots := range slotGroups {
		for i := range slots {
			slots[i].AvailableUsers = mapSlotUsers(slots[i].AvailableUserIDs, usersByID)
			slots[i].ConflictedUsers = mapSlotUsers(slots[i].ConflictedUserIDs, usersByID)
		}
	}

	return nil
}

func mapSlotUsers(userIDs []int64, usersByID map[int64]domain.CalendarSlotUser) []domain.CalendarSlotUser {
	if len(userIDs) == 0 {
		return nil
	}

	users := make([]domain.CalendarSlotUser, 0, len(userIDs))
	for _, userID := range userIDs {
		if user, ok := usersByID[userID]; ok {
			users = append(users, user)
			continue
		}
		users = append(users, domain.CalendarSlotUser{
			UserID:   userID,
			FullName: fmt.Sprintf("User %d", userID),
		})
	}

	return users
}

func markSuggestedSlotType(slots []domain.CalendarSuggestedSlot, slotType string) {
	for i := range slots {
		slots[i].Type = slotType
	}
}

func mergeSuggestedSlots(fullSlots, partialSlots []domain.CalendarSuggestedSlot, limit int) []domain.CalendarSuggestedSlot {
	total := len(fullSlots) + len(partialSlots)
	if limit > 0 && total > limit {
		total = limit
	}
	merged := make([]domain.CalendarSuggestedSlot, 0, total)
	merged = append(merged, fullSlots...)
	if limit > 0 && len(merged) >= limit {
		return merged[:limit]
	}
	merged = append(merged, partialSlots...)
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func buildCalendarUserFullName(user *domain.User) string {
	if user == nil {
		return ""
	}

	fullName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if fullName != "" {
		return fullName
	}
	if fullNameLocal := strings.TrimSpace(calendarPtrStringValue(user.FullNameLocal)); fullNameLocal != "" {
		return fullNameLocal
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		return username
	}
	if email := strings.TrimSpace(user.Email); email != "" {
		return email
	}
	return fmt.Sprintf("User %d", user.ID)
}

func calendarPtrStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *CalendarAvailabilityService) suggestSlots(
	ctx context.Context,
	userIDs []int64,
	searchFrom time.Time,
	searchTo time.Time,
	duration time.Duration,
	excludeEventID *uuid.UUID,
	timezone string,
	limit int,
	slotStep time.Duration,
	allowPartial bool,
	excludeOccurrence ...*time.Time,
) ([]domain.CalendarSuggestedSlot, []domain.CalendarSuggestedSlot, error) {
	if duration <= 0 {
		return []domain.CalendarSuggestedSlot{}, []domain.CalendarSuggestedSlot{}, nil
	}
	if limit <= 0 {
		limit = 5
	}
	if slotStep <= 0 {
		slotStep = 30 * time.Minute
	}

	location := time.UTC
	if timezone != "" {
		if loc, err := time.LoadLocation(timezone); err == nil {
			location = loc
		}
	} else {
		location = time.Local
	}

	if searchFrom.Before(time.Now().UTC()) {
		searchFrom = time.Now().UTC()
	}
	if !searchTo.After(searchFrom) {
		searchTo = searchFrom.Add(7 * 24 * time.Hour)
	}

	busyRows, err := s.loadExpandedBusyRows(ctx, userIDs, searchFrom, searchTo, excludeEventID, excludeOccurrence...)
	if err != nil {
		return nil, nil, apperrors.InternalWithErr("failed to load busy intervals", err)
	}

	intervalsByUser := make(map[int64][]calendarBusyInterval, len(userIDs))
	for _, row := range busyRows {
		intervalsByUser[row.UserID] = append(intervalsByUser[row.UserID], calendarBusyInterval{start: row.StartTime.UTC(), end: row.EndTime.UTC()})
	}
	for userID := range intervalsByUser {
		sort.Slice(intervalsByUser[userID], func(i, j int) bool {
			return intervalsByUser[userID][i].start.Before(intervalsByUser[userID][j].start)
		})
	}

	fullCandidates := make([]domain.CalendarSuggestedSlot, 0, limit*3)
	partialCandidates := make([]domain.CalendarSuggestedSlot, 0, limit*6)
	cursor := searchFrom.In(location)
	horizon := searchTo.In(location)
	if horizon.Before(cursor) {
		horizon = cursor.Add(7 * 24 * time.Hour)
	}

	daysToScan := int(horizon.Sub(cursor).Hours()/24) + 1
	if daysToScan < 7 {
		daysToScan = 7
	}
	if daysToScan > 30 {
		daysToScan = 30
	}

	for day := 0; day < daysToScan && len(fullCandidates) < limit*3; day++ {
		dayStart := time.Date(cursor.Year(), cursor.Month(), cursor.Day()+day, s.workdayStartHour, 0, 0, 0, location)
		dayEnd := time.Date(cursor.Year(), cursor.Month(), cursor.Day()+day, s.workdayEndHour, 0, 0, 0, location)
		if dayEnd.Before(dayStart) {
			continue
		}
		if day == 0 && cursor.After(dayStart) {
			dayStart = cursor
		}
		if dayStart.After(horizon) {
			break
		}

		for slotStart := roundUpTime(dayStart, slotStep); slotStart.Add(duration).Before(dayEnd) || slotStart.Add(duration).Equal(dayEnd); slotStart = slotStart.Add(slotStep) {
			if slotStart.UTC().Before(time.Now().UTC()) {
				continue
			}
			slotEnd := slotStart.Add(duration)
			available := make([]int64, 0, len(userIDs))
			conflicted := make([]int64, 0)
			slotConflicts := make([]domain.CalendarConflictItem, 0)
			for _, userID := range userIDs {
				intervals := intervalsByUser[userID]
				if !hasBusyOverlap(intervals, slotStart.UTC(), slotEnd.UTC()) {
					available = append(available, userID)
					continue
				}
				conflicts := collectSlotConflicts(busyRows, userID, slotStart.UTC(), slotEnd.UTC())
				conflicted = append(conflicted, userID)
				slotConflicts = append(slotConflicts, conflicts...)
			}
			if len(conflicted) > 0 {
				if allowPartial && len(available) > 0 {
					partialCandidates = append(partialCandidates, domain.CalendarSuggestedSlot{
						StartTime:         slotStart.UTC(),
						EndTime:           slotEnd.UTC(),
						Timezone:          location.String(),
						LocalStartTime:    slotStart,
						LocalEndTime:      slotEnd,
						Score:             scorePartialSuggestedSlot(slotStart, cursor, day, len(available), len(userIDs), len(conflicted)),
						Label:             "Доступны не все участники",
						AvailableUserIDs:  append([]int64(nil), available...),
						ConflictedUserIDs: append([]int64(nil), conflicted...),
						Conflicts:         slotConflicts,
					})
				}
				continue
			}
			if len(available) == 0 {
				continue
			}
			label := buildSuggestedSlotLabel(slotStart, cursor)
			fullCandidates = append(fullCandidates, domain.CalendarSuggestedSlot{
				StartTime:         slotStart.UTC(),
				EndTime:           slotEnd.UTC(),
				Timezone:          location.String(),
				LocalStartTime:    slotStart,
				LocalEndTime:      slotEnd,
				Score:             scoreSuggestedSlot(slotStart, cursor, day),
				Label:             label,
				AvailableUserIDs:  append([]int64(nil), available...),
				ConflictedUserIDs: append([]int64(nil), conflicted...),
			})
		}
	}

	sort.SliceStable(fullCandidates, func(i, j int) bool {
		if !fullCandidates[i].StartTime.Equal(fullCandidates[j].StartTime) {
			return fullCandidates[i].StartTime.Before(fullCandidates[j].StartTime)
		}
		if fullCandidates[i].Score != fullCandidates[j].Score {
			return fullCandidates[i].Score > fullCandidates[j].Score
		}
		return fullCandidates[i].EndTime.Before(fullCandidates[j].EndTime)
	})

	sort.SliceStable(partialCandidates, func(i, j int) bool {
		if !partialCandidates[i].StartTime.Equal(partialCandidates[j].StartTime) {
			return partialCandidates[i].StartTime.Before(partialCandidates[j].StartTime)
		}
		if partialCandidates[i].Score != partialCandidates[j].Score {
			return partialCandidates[i].Score > partialCandidates[j].Score
		}
		return len(partialCandidates[i].AvailableUserIDs) > len(partialCandidates[j].AvailableUserIDs)
	})

	if len(fullCandidates) > limit {
		fullCandidates = fullCandidates[:limit]
	}
	if len(partialCandidates) > limit {
		partialCandidates = partialCandidates[:limit]
	}
	return fullCandidates, partialCandidates, nil
}

func enrichAvailabilityConflicts(conflicts []domain.CalendarConflictItem, slotStart, slotEnd time.Time) []domain.CalendarConflictItem {
	if len(conflicts) == 0 {
		return []domain.CalendarConflictItem{}
	}

	enriched := make([]domain.CalendarConflictItem, 0, len(conflicts))
	for _, conflict := range conflicts {
		overlapStart := maxTime(conflict.StartTime, slotStart)
		overlapEnd := minTime(conflict.EndTime, slotEnd)
		if !overlapEnd.After(overlapStart) {
			continue
		}
		overlapMinutes := int(overlapEnd.Sub(overlapStart).Minutes())
		conflict.OverlapMinutes = overlapMinutes
		enriched = append(enriched, conflict)
	}
	return enriched
}

func groupAvailabilityConflicts(conflicts []domain.CalendarConflictItem) []domain.CalendarConflictsByUser {
	if len(conflicts) == 0 {
		return []domain.CalendarConflictsByUser{}
	}

	grouped := make(map[int64]*domain.CalendarConflictsByUser, len(conflicts))
	order := make([]int64, 0, len(conflicts))
	for _, conflict := range conflicts {
		group, ok := grouped[conflict.UserID]
		if !ok {
			group = &domain.CalendarConflictsByUser{
				UserID:    conflict.UserID,
				UserName:  conflict.UserName,
				Username:  conflict.Username,
				Email:     conflict.Email,
				AvatarURL: conflict.AvatarURL,
				Conflicts: make([]domain.CalendarConflictItem, 0, 1),
			}
			grouped[conflict.UserID] = group
			order = append(order, conflict.UserID)
		}
		group.Conflicts = append(group.Conflicts, conflict)
	}

	result := make([]domain.CalendarConflictsByUser, 0, len(order))
	for _, userID := range order {
		result = append(result, *grouped[userID])
	}
	return result
}

func (s *CalendarAvailabilityService) buildSuggestConfig(
	ctx context.Context,
	requesterID int64,
	req domain.CalendarSuggestTimesRequest,
) (*calendarSuggestConfig, error) {
	if req.DurationMinutes == 0 {
		return nil, apperrors.BadRequest("duration_minutes is required")
	}
	if req.DurationMinutes < 5 || req.DurationMinutes > 480 {
		return nil, apperrors.BadRequest("duration_minutes must be between 5 and 480")
	}

	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "Asia/Almaty"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, apperrors.BadRequest("invalid timezone")
	}

	includeOrganizer := true
	if req.IncludeOrganizer != nil {
		includeOrganizer = *req.IncludeOrganizer
	}
	allowPartial := true
	if req.AllowPartial != nil {
		allowPartial = *req.AllowPartial
	}

	workdayStart := strings.TrimSpace(req.WorkdayStart)
	if workdayStart == "" {
		workdayStart = "09:00"
	}
	workdayEnd := strings.TrimSpace(req.WorkdayEnd)
	if workdayEnd == "" {
		workdayEnd = "18:00"
	}

	slotStepMinutes := req.SlotStepMinutes
	if slotStepMinutes <= 0 {
		slotStepMinutes = 30
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}

	date := strings.TrimSpace(req.Date)
	var localDate time.Time
	switch {
	case date != "":
		localDate, err = time.ParseInLocation("2006-01-02", date, location)
		if err != nil {
			return nil, apperrors.BadRequest("date must be in YYYY-MM-DD format")
		}
	case req.PreferredStart != nil:
		localDate = req.PreferredStart.In(location)
		date = localDate.Format("2006-01-02")
	default:
		return nil, apperrors.BadRequest("date is required")
	}

	workdayStartTime, err := parseWorkdayTime(localDate, workdayStart, location)
	if err != nil {
		return nil, apperrors.BadRequest("invalid workday_start")
	}
	workdayEndTime, err := parseWorkdayTime(localDate, workdayEnd, location)
	if err != nil {
		return nil, apperrors.BadRequest("invalid workday_end")
	}
	if !workdayEndTime.After(workdayStartTime) {
		return nil, apperrors.BadRequest("workday_end must be later than workday_start")
	}

	userIDs, err := s.resolveParticipantIDs(ctx, requesterID, req.AttendeeIDs, includeOrganizer)
	if err != nil {
		return nil, err
	}

	return &calendarSuggestConfig{
		date:             date,
		timezone:         timezone,
		location:         location,
		checkedUserIDs:   userIDs,
		includeOrganizer: includeOrganizer && containsInt64(userIDs, requesterID),
		duration:         time.Duration(req.DurationMinutes) * time.Minute,
		durationMinutes:  req.DurationMinutes,
		workdayStart:     workdayStart,
		workdayEnd:       workdayEnd,
		slotStep:         time.Duration(slotStepMinutes) * time.Minute,
		slotStepMinutes:  slotStepMinutes,
		workdayStartUTC:  workdayStartTime.UTC(),
		workdayEndUTC:    workdayEndTime.UTC(),
		limit:            limit,
		allowPartial:     allowPartial,
		excludeEventID:   req.ExcludeEventID,
	}, nil
}

func scoreSuggestedSlot(slotStart, preferredStart time.Time, dayOffset int) int {
	score := 100
	score -= dayOffset * 4
	diffSteps := int(slotStart.Sub(preferredStart).Abs() / (30 * time.Minute))
	score -= diffSteps
	hour := slotStart.Hour()
	switch {
	case hour >= 10 && hour <= 12:
		score += 3
	case hour >= 13 && hour <= 16:
		score += 1
	default:
		score -= 1
	}
	if score < 70 {
		score = 70
	}
	if score > 100 {
		score = 100
	}
	return score
}

func scorePartialSuggestedSlot(slotStart, preferredStart time.Time, dayOffset, availableCount, totalUsers, conflictedCount int) int {
	score := scoreSuggestedSlot(slotStart, preferredStart, dayOffset)
	if totalUsers > 0 {
		score -= (conflictedCount * 35) / totalUsers
		score += (availableCount * 10) / totalUsers
	}
	if score < 1 {
		score = 1
	}
	return score
}

func buildSuggestedSlotLabel(slotStart, preferredStart time.Time) string {
	slotDay := time.Date(slotStart.Year(), slotStart.Month(), slotStart.Day(), 0, 0, 0, 0, slotStart.Location())
	preferredDay := time.Date(preferredStart.Year(), preferredStart.Month(), preferredStart.Day(), 0, 0, 0, 0, preferredStart.Location())
	dayDiff := int(slotDay.Sub(preferredDay).Hours() / 24)

	switch {
	case dayDiff <= 0:
		return "Ближайшее свободное время"
	case dayDiff == 1:
		return "Оптимальное время завтра"
	default:
		return "Оптимальное время для всех"
	}
}

func roundUpTime(value time.Time, step time.Duration) time.Time {
	if step <= 0 {
		return value
	}
	rounded := value.Truncate(step)
	if rounded.Before(value) {
		rounded = rounded.Add(step)
	}
	return rounded
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func hasBusyOverlap(intervals []calendarBusyInterval, slotStart, slotEnd time.Time) bool {
	for _, interval := range intervals {
		if interval.start.Before(slotEnd) && interval.end.After(slotStart) {
			return true
		}
	}
	return false
}

func parseWorkdayTime(day time.Time, value string, location *time.Location) (time.Time, error) {
	parsed, err := time.ParseInLocation("15:04", value, location)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(day.Year(), day.Month(), day.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location), nil
}

func collectSlotConflicts(rows []repository.CalendarConflictRow, userID int64, slotStart, slotEnd time.Time) []domain.CalendarConflictItem {
	conflicts := make([]domain.CalendarConflictItem, 0, 1)
	for _, row := range rows {
		if row.UserID != userID {
			continue
		}
		overlapStart := maxTime(row.StartTime.UTC(), slotStart)
		overlapEnd := minTime(row.EndTime.UTC(), slotEnd)
		if !overlapEnd.After(overlapStart) {
			continue
		}
		conflicts = append(conflicts, domain.CalendarConflictItem{
			UserID:         row.UserID,
			UserName:       row.UserName,
			Username:       row.Username,
			Email:          row.Email,
			AvatarURL:      row.AvatarURL,
			EventID:        row.EventID,
			EventTitle:     row.EventTitle,
			StartTime:      row.StartTime,
			EndTime:        row.EndTime,
			Status:         row.Status,
			OverlapMinutes: int(overlapEnd.Sub(overlapStart).Minutes()),
		})
	}
	return conflicts
}

func collectConflictedUserIDs(conflicts []domain.CalendarConflictItem) []int64 {
	if len(conflicts) == 0 {
		return []int64{}
	}
	seen := make(map[int64]struct{}, len(conflicts))
	result := make([]int64, 0, len(conflicts))
	for _, conflict := range conflicts {
		if _, ok := seen[conflict.UserID]; ok {
			continue
		}
		seen[conflict.UserID] = struct{}{}
		result = append(result, conflict.UserID)
	}
	return result
}

func containsInt64(items []int64, target int64) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
