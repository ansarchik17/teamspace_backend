package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type meetingContractService struct {
	calendarHandlerService
	requester int64
	filter    domain.RoomFilter
	response  domain.RespondToInviteRequest
}

func (s *meetingContractService) GetRoomForUser(_ context.Context, user int64, _ uuid.UUID) (*domain.MeetingRoom, error) {
	s.requester = user
	return nil, apperrors.RoomNotFound()
}
func (s *meetingContractService) ListRooms(_ context.Context, filter domain.RoomFilter) ([]domain.MeetingRoom, error) {
	s.filter = filter
	return []domain.MeetingRoom{}, nil
}
func (s *meetingContractService) RespondToInvite(_ context.Context, user int64, _ uuid.UUID, req *domain.RespondToInviteRequest) error {
	s.requester = user
	s.response = *req
	return nil
}
func TestCalendarMeetingContractRequesterAndFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &meetingContractService{}
	h := newCalendarHandlerWithStub(svc)
	roomID, siteID := uuid.New(), uuid.New()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/calendar/rooms/"+roomID.String(), nil)
	ctx.Params = gin.Params{{Key: "id", Value: roomID.String()}}
	setGinUserID(ctx, 42)
	h.GetRoom(ctx)
	require.Equal(t, 404, rec.Code)
	require.Equal(t, int64(42), svc.requester)
	rec = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/calendar/rooms?site_id="+siteID.String()+"&min_capacity=8&include_inactive=true", nil)
	setGinUserID(ctx, 42)
	h.ListRooms(ctx)
	require.Equal(t, 200, rec.Code)
	require.Equal(t, int64(42), svc.filter.RequesterID)
	require.Equal(t, siteID, *svc.filter.SiteID)
	require.Equal(t, 8, *svc.filter.MinCapacity)
	require.False(t, svc.filter.OnlyActive)
}
func TestCalendarMeetingContractResponseAttendance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"status":"accepted","attendance":"online"}`, `{"status":"accepted","attendance":null}`} {
		svc := &meetingContractService{}
		h := newCalendarHandlerWithStub(svc)
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		id := uuid.New()
		ctx.Request = httptest.NewRequest(http.MethodPatch, "/calendar/events/"+id.String()+"/response", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Params = gin.Params{{Key: "id", Value: id.String()}}
		setGinUserID(ctx, 42)
		h.RespondToInvite(ctx)
		require.Equal(t, 200, rec.Code)
		require.Equal(t, int64(42), svc.requester)
		require.True(t, svc.response.AttendanceSet)
		if strings.Contains(body, "online") {
			require.Equal(t, "online", *svc.response.Attendance)
		} else {
			require.Nil(t, svc.response.Attendance)
		}
	}
}
