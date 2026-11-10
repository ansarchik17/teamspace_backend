package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestCompanyMeetingRoomsManagementAndIsolation(t *testing.T) {
	s, db := roomIntegrationService(t)
	ctx := context.Background()
	foreign := uuid.New()
	_, err := db.Exec(ctx, `INSERT INTO users(id,role,username,organization_id) VALUES(3,'admin','foreign',$1),(4,'user','delegate','11111111-1111-4111-8111-111111111111')`, foreign)
	require.NoError(t, err)
	_, err = db.Exec(ctx, "INSERT INTO organizations(id,name,owner_id) VALUES($1,'Other company',3)", foreign)
	require.NoError(t, err)
	site, err := s.CreateSite(ctx, 1, domain.CreateMeetingSiteRequest{Name: "Главный офис", Address: "ул. Достык 1", City: "Алматы", Timezone: "Asia/Almaty", WorkingHours: "09:00–19:00"})
	require.NoError(t, err)
	otherSite, err := s.CreateSite(ctx, 3, domain.CreateMeetingSiteRequest{Name: "Чужой офис"})
	require.NoError(t, err)
	location := "Блок Б, каб. 210"
	floor := 2
	room, err := s.CreateRoom(ctx, 1, domain.CreateMeetingRoomRequest{SiteID: &site.ID, Name: "Байконур", Capacity: 4, Floor: &floor, Location: &location, Equipment: []string{"projector"}})
	require.NoError(t, err)
	require.Equal(t, site.OrganizationID, *room.OrganizationID)
	require.Equal(t, site.Name, *room.SiteName)
	require.Equal(t, site.Address, *room.Address)
	_, err = s.CreateRoom(ctx, 2, domain.CreateMeetingRoomRequest{SiteID: &site.ID, Name: "Запрещено", Capacity: 2})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.CreateRoom(ctx, 1, domain.CreateMeetingRoomRequest{SiteID: &otherSite.ID, Name: "Чужая площадка", Capacity: 2})
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.GetRoomForUser(ctx, 3, room.ID)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.UpdateRoom(ctx, 3, room.ID, domain.UpdateMeetingRoomRequest{Name: &location})
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 404, apperrors.ToAppError(s.DeleteRoom(ctx, 3, room.ID)).StatusCode)
	_, err = s.BookRoom(ctx, 3, room.ID, domain.BookRoomRequest{Title: "Foreign", StartTime: time.Now().Add(time.Hour), EndTime: time.Now().Add(2 * time.Hour)})
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.GetSite(ctx, 3, site.ID)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	rooms, err := s.ListRooms(ctx, domain.RoomFilter{RequesterID: 3})
	require.NoError(t, err)
	require.Empty(t, rooms)
	_, err = s.GetRoomAvailability(ctx, 3, room.ID, domain.RoomAvailabilityQuery{})
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.CreateRoom(ctx, 1, domain.CreateMeetingRoomRequest{SiteID: &site.ID, Name: " Байконур ", Capacity: 2})
	require.Equal(t, apperrors.ErrorCode("ROOM_NAME_TAKEN"), apperrors.ToAppError(err).Code)
	err = s.DeleteSite(ctx, 1, site.ID)
	require.Equal(t, apperrors.ErrorCode("SITE_HAS_ROOMS"), apperrors.ToAppError(err).Code)
	// A director can administer the company's resources without platform role.
	_, err = db.Exec(ctx, `UPDATE organizations SET owner_id=2 WHERE id=$1`, site.OrganizationID)
	require.NoError(t, err)
	_, err = s.CreateRoom(ctx, 2, domain.CreateMeetingRoomRequest{SiteID: &site.ID, Name: "Директор", Capacity: 2})
	require.NoError(t, err)
	position := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organization_access_positions(id,organization_id,name,position_key) VALUES($1,$2,'Delegated','delegated')`, position, site.OrganizationID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, "INSERT INTO organization_access_assignments(organization_id,user_id,position_id) VALUES($2,4,$1)", position, site.OrganizationID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, "INSERT INTO organization_position_permissions(organization_id,position_id,application,permissions) VALUES($2,$1,'calendar',ARRAY['calendar.rooms.manage'])", position, site.OrganizationID)
	require.NoError(t, err)
	_, err = s.CreateRoom(ctx, 4, domain.CreateMeetingRoomRequest{SiteID: &site.ID, Name: "Делегированное право", Capacity: 2})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.CreateSite(ctx, 4, domain.CreateMeetingSiteRequest{Name: "Запрещённая площадка"})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.UpdateSite(ctx, 4, site.ID, domain.UpdateMeetingSiteRequest{Name: &location})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 403, apperrors.ToAppError(s.DeleteSite(ctx, 4, site.ID)).StatusCode)
	_, err = s.UpdateRoom(ctx, 4, room.ID, domain.UpdateMeetingRoomRequest{Name: &location})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 403, apperrors.ToAppError(s.DeleteRoom(ctx, 4, room.ID)).StatusCode)
	// Ordinary employees can still see availability and book an existing room.
	_, err = s.GetRoomAvailability(ctx, 4, room.ID, domain.RoomAvailabilityQuery{})
	require.NoError(t, err)
	start := time.Now().UTC().Add(3 * time.Hour)
	_, err = s.BookRoom(ctx, 4, room.ID, domain.BookRoomRequest{Title: "Бронь сотрудника", StartTime: start, EndTime: start.Add(time.Hour)})
	require.NoError(t, err)
	active := false
	_, err = s.UpdateRoom(ctx, 2, room.ID, domain.UpdateMeetingRoomRequest{IsActive: &active})
	require.NoError(t, err)
	rooms, err = s.ListRooms(ctx, domain.RoomFilter{RequesterID: 2, OnlyActive: false})
	require.NoError(t, err)
	require.Len(t, rooms, 2)
	rooms, err = s.ListRooms(ctx, domain.RoomFilter{RequesterID: 4, OnlyActive: true})
	require.NoError(t, err)
	require.Len(t, rooms, 1)
	// Removing delegation cannot retain administrative access.
	_, err = db.Exec(ctx, `DELETE FROM organization_position_permissions WHERE position_id=$1`, position)
	require.NoError(t, err)
	rooms, err = s.ListRooms(ctx, domain.RoomFilter{RequesterID: 4, OnlyActive: false})
	require.NoError(t, err)
	require.Len(t, rooms, 1)
	_, err = s.UpdateRoom(ctx, 4, room.ID, domain.UpdateMeetingRoomRequest{IsActive: &active})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.BookRoom(ctx, 2, room.ID, domain.BookRoomRequest{Title: "Disabled", StartTime: time.Now().Add(time.Hour), EndTime: time.Now().Add(2 * time.Hour)})
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
}

func TestCompanyMeetingHybridAttendanceSnapshotAndChangeQueue(t *testing.T) {
	s, db := roomIntegrationService(t)
	ctx := context.Background()
	roomID := roomIntegrationRoom(t, s)
	_, err := db.Exec(ctx, `UPDATE meeting_rooms SET capacity=1,location='Блок Б, каб. 210',floor=2 WHERE id=$1`, roomID)
	require.NoError(t, err)
	mode := "offline"
	start := time.Now().UTC().Add(3 * time.Hour)
	event, err := s.CreateEvent(ctx, 1, &domain.CreateEventRequest{Title: "Гибридная встреча", RoomID: &roomID, IsOnline: true, StartTime: start, EndTime: start.Add(time.Hour), AttendeeIDs: []int64{2}, DefaultAttendance: &mode})
	require.NoError(t, err)
	require.NotNil(t, event.Room)
	require.NotNil(t, event.MeetingURL)
	require.Equal(t, 2, event.AttendanceSummary.Offline)
	require.Contains(t, event.Warnings, "ROOM_CAPACITY_EXCEEDED")
	var reminders int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM event_reminders WHERE event_id=$1 AND minutes_before=15`, event.ID).Scan(&reminders))
	require.Equal(t, 2, reminders)
	_, err = db.Exec(ctx, `UPDATE meeting_rooms SET name='Новое название',location='Другой кабинет' WHERE id=$1`, roomID)
	require.NoError(t, err)
	invited, err := s.GetEvent(ctx, 2, event.ID)
	require.NoError(t, err)
	require.Equal(t, event.Room.Name, invited.Room.Name)
	require.Equal(t, "Блок Б, каб. 210", *invited.Room.Location)
	require.Equal(t, *event.MeetingURL, *invited.MeetingURL)
	_, err = s.GetEvent(ctx, 99, event.ID)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	mode = "online"
	_, err = db.Exec(ctx, `UPDATE event_attendees SET comment='Existing RSVP note' WHERE event_id=$1 AND user_id=2`, event.ID)
	require.NoError(t, err)
	require.NoError(t, s.RespondToInvite(ctx, 2, event.ID, &domain.RespondToInviteRequest{Status: "accepted", Attendance: &mode}))
	var savedComment string
	require.NoError(t, db.QueryRow(ctx, `SELECT comment FROM event_attendees WHERE event_id=$1 AND user_id=2`, event.ID).Scan(&savedComment))
	require.Equal(t, "Existing RSVP note", savedComment)
	invited, err = s.GetEvent(ctx, 1, event.ID)
	require.NoError(t, err)
	require.Equal(t, 1, invited.AttendanceSummary.Offline)
	require.Equal(t, 1, invited.AttendanceSummary.Online)
	require.Empty(t, invited.Warnings)
	disabled := false
	_, err = s.UpdateEventWithOptions(ctx, 1, event.ID, &domain.UpdateEventRequest{IsOnline: &disabled}, true)
	require.NoError(t, err)
	invited, err = s.GetEvent(ctx, 2, event.ID)
	require.NoError(t, err)
	require.Nil(t, invited.MeetingURL)
	require.Equal(t, 1, invited.AttendanceSummary.Unknown)
	err = s.RespondToInvite(ctx, 2, event.ID, &domain.RespondToInviteRequest{Status: "accepted", Attendance: &mode})
	require.Equal(t, apperrors.ErrorCode("ATTENDANCE_NOT_ALLOWED"), apperrors.ToAppError(err).Code)
	var notices int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM calendar_meeting_changes WHERE event_id=$1 AND kind='event.location_changed'`, event.ID).Scan(&notices))
	require.Equal(t, 2, notices)
	err = s.DeleteRoom(ctx, 1, roomID)
	require.Equal(t, apperrors.ErrorCode("ROOM_HAS_BOOKINGS"), apperrors.ToAppError(err).Code)
	_, err = s.UpdateRoom(ctx, 1, roomID, domain.UpdateMeetingRoomRequest{IsActive: &disabled})
	require.NoError(t, err)
	var bookings int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM room_bookings WHERE event_id=$1 AND status='confirmed' AND deleted_at IS NULL`, event.ID).Scan(&bookings))
	require.Equal(t, 1, bookings)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM calendar_meeting_changes WHERE event_id=$1 AND kind='room.disabled' AND user_id=1`, event.ID).Scan(&notices))
	require.Equal(t, 1, notices)
	// Explicit null removes a room, without leaving the old room name as a place.
	var update domain.UpdateEventRequest
	require.NoError(t, json.Unmarshal([]byte(`{"room_id":null}`), &update))
	_, err = s.UpdateEventWithOptions(ctx, 1, event.ID, &update, true)
	require.NoError(t, err)
	invited, err = s.GetEvent(ctx, 2, event.ID)
	require.NoError(t, err)
	require.Nil(t, invited.Room)
	require.Nil(t, invited.RoomID)
	require.False(t, invited.HasPlace())
	require.Equal(t, 2, invited.AttendanceSummary.Unknown)
	require.NoError(t, s.DeleteRoom(ctx, 1, roomID))
}

func TestCompanyMeetingRoomsLegacyMigration(t *testing.T) {
	ctx := context.Background()
	resolved, duplicate, unassigned, ambiguous := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s, db := roomIntegrationService(t, func(db *pgxpool.Pool) {
		_, err := db.Exec(ctx, `UPDATE users SET organization_id=NULL WHERE id=2`)
		require.NoError(t, err)
		_, err = db.Exec(ctx, `INSERT INTO meeting_rooms(id,name,created_at) VALUES($1,'Legacy',now()-interval '1 day'),($2,'Legacy',now()),($3,'Unassigned',now()),($4,'Ambiguous',now())`, resolved, duplicate, unassigned, ambiguous)
		require.NoError(t, err)
		for _, item := range []struct {
			room uuid.UUID
			user int64
		}{{resolved, 1}, {duplicate, 1}, {ambiguous, 1}, {ambiguous, 2}} {
			_, err = db.Exec(ctx, `INSERT INTO events(id,user_id,title,start_time,end_time,room_id)
			 VALUES($1,$2,'Old event',now(),now()+interval '1 hour',$3)`, uuid.New(), item.user, item.room)
			require.NoError(t, err)
		}
	})
	room, err := s.GetRoomForUser(ctx, 1, resolved)
	require.NoError(t, err)
	require.True(t, room.IsActive)
	require.Equal(t, "Основной офис", *room.SiteName)
	room, err = s.GetRoomForUser(ctx, 1, duplicate)
	require.NoError(t, err)
	require.NotEqual(t, "Legacy", room.Name)
	var snapshotName string
	require.NoError(t, db.QueryRow(ctx, `SELECT room_snapshot->>'name' FROM events WHERE room_id=$1`, duplicate).Scan(&snapshotName))
	require.Equal(t, "Legacy", snapshotName)
	report, err := s.ListUnassignedRooms(ctx, 1)
	require.NoError(t, err)
	ids := []uuid.UUID{}
	for _, item := range report {
		require.False(t, item.IsActive)
		ids = append(ids, item.ID)
	}
	require.Contains(t, ids, unassigned)
	require.Contains(t, ids, ambiguous)
	require.NotContains(t, ids, resolved)
	require.NotContains(t, ids, duplicate)
	_, err = s.GetRoomForUser(ctx, 1, unassigned)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
}

func TestCompanyMeetingICSPlaceAndLink(t *testing.T) {
	mode, link, location := "online", "https://meet.example.test/room", "Блок Б, каб. 210"
	site, address, floor := "Главный офис", "ул. Достык 1", 2
	event := domain.Event{IsOnline: true, MeetingURL: &link, Room: &domain.MeetingRoom{Name: "Байконур", SiteName: &site, Address: &address, Floor: &floor, Location: &location}, Attendees: []domain.EventAttendee{{Attendance: &mode}}}
	ics := BuildICS(ICSEventData{UID: uuid.NewString(), Method: ICSMethodRequest, Location: event.MeetingLocation(), URL: link, Description: meetingDescription(event)})
	ics = strings.ReplaceAll(ics, "\r\n ", "") // Unfold RFC 5545 content lines.
	require.Contains(t, ics, "LOCATION:Байконур\\, Главный офис\\, ул. Достык 1\\, 2 этаж\\, Блок Б\\, каб. 210\r\n")
	require.Contains(t, ics, "URL:"+link+"\r\n")
	require.Contains(t, ics, "DESCRIPTION:"+link+"\r\n")
}

func TestCompanyMeetingRoomsAvailabilityIncludesScopedRooms(t *testing.T) {
	s, db := roomIntegrationService(t)
	ctx := context.Background()
	roomID := roomIntegrationRoom(t, s)
	_, err := db.Exec(ctx, `UPDATE meeting_rooms SET equipment=ARRAY['projector'] WHERE id=$1`, roomID)
	require.NoError(t, err)
	_, err = s.CreateRoom(ctx, 1, domain.CreateMeetingRoomRequest{Name: "Small room", Capacity: 2})
	require.NoError(t, err)
	inactive := false
	_, err = s.CreateRoom(ctx, 1, domain.CreateMeetingRoomRequest{Name: "Disabled room", Capacity: 8, IsActive: &inactive})
	require.NoError(t, err)
	foreignOrg := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,name,owner_id) VALUES($1,'Foreign company',3)`, foreignOrg)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users(id,role,organization_id) VALUES(3,'admin',$1)`, foreignOrg)
	require.NoError(t, err)
	foreignSite, err := s.CreateSite(ctx, 3, domain.CreateMeetingSiteRequest{Name: "Foreign site"})
	require.NoError(t, err)
	foreignRoom, err := s.CreateRoom(ctx, 3, domain.CreateMeetingRoomRequest{Name: "Foreign room", SiteID: &foreignSite.ID, Capacity: 8})
	require.NoError(t, err)
	start := time.Now().UTC().Truncate(time.Second).Add(20 * time.Hour)
	query := domain.RoomAvailabilityQuery{StartTime: start, EndTime: start.Add(time.Hour)}
	listed, err := s.ListRooms(ctx, domain.RoomFilter{RequesterID: 2, OnlyActive: true})
	require.NoError(t, err)
	available, err := s.GetRoomsAvailability(ctx, 2, query)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Len(t, available, len(listed))
	for _, item := range available {
		require.NotEqual(t, foreignRoom.ID, item.Room.ID)
		require.True(t, item.IsAvailable)
	}
	capacity := 4
	query.Capacity, query.Equipment = &capacity, []string{"projector"}
	available, err = s.GetRoomsAvailability(ctx, 2, query)
	require.NoError(t, err)
	require.Len(t, available, 1)
	require.Equal(t, roomID, available[0].Room.ID)
	_, err = s.BookRoom(ctx, 1, roomID, domain.BookRoomRequest{Title: "Private meeting", StartTime: start, EndTime: start.Add(time.Hour)})
	require.NoError(t, err)
	available, err = s.GetRoomsAvailability(ctx, 2, query)
	require.NoError(t, err)
	require.Len(t, available, 1)
	require.False(t, available[0].IsAvailable)
	require.Len(t, available[0].Bookings, 1)
	require.Equal(t, "Занято", available[0].Bookings[0].Title)
	query.StartTime, query.EndTime = start.Add(time.Hour), start.Add(2*time.Hour)
	available, err = s.GetRoomsAvailability(ctx, 2, query)
	require.NoError(t, err)
	require.Len(t, available, 1)
	require.True(t, available[0].IsAvailable)
	_, err = s.UpdateRoom(ctx, 1, roomID, domain.UpdateMeetingRoomRequest{IsActive: &inactive})
	require.NoError(t, err)
	available, err = s.GetRoomsAvailability(ctx, 2, query)
	require.NoError(t, err)
	require.Empty(t, available)
}
