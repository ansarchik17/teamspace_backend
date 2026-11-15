package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type calendarIdentitySpy struct {
	stubCalendarHandlerService
	calls int
	actor int64
	force bool
	req   domain.CreateEventRequest
	batch domain.BatchCreateEventRequest
	err   error
}

func (s *calendarIdentitySpy) CreateEventWithOptions(_ context.Context, user int64, req *domain.CreateEventRequest, force bool) (*domain.Event, error) {
	s.calls++
	s.actor, s.req, s.force = user, *req, force
	return &domain.Event{UserID: user, Title: req.Title}, s.err
}

func (s *calendarIdentitySpy) BatchCreateEvents(_ context.Context, user int64, req *domain.BatchCreateEventRequest) ([]*domain.Event, error) {
	s.calls++
	s.actor, s.batch = user, *req
	return []*domain.Event{{UserID: user}}, s.err
}

func TestCalendarCreationUsesOnlyAuthenticatedOrganizer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"standard", "compatibility"} {
		for _, body := range []string{
			`{"title":"Own event","start_time":"2027-01-01T10:00:00Z","end_time":"2027-01-01T11:00:00Z","reminder_minutes":null}`,
			`{"title":"Own event","start_time":"2027-01-01T10:00:00Z","end_time":"2027-01-01T11:00:00Z","reminder_minutes":null,"user_id":7,"email":"other@example.invalid","organizer_id":7}`,
		} {
			spy := &calendarIdentitySpy{}
			h := &CalendarHandler{service: spy, logger: zap.NewNop()}
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Set("user_id", int64(42))
			c.Request = httptest.NewRequest(http.MethodPost, "/?force=true&user_id=7&email=other@example.invalid", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("X-User-Id", "7")
			c.Request.Header.Set("X-User-Email", "other@example.invalid")
			if endpoint == "standard" {
				h.CreateEvent(c)
			} else {
				h.CreatePublicEvent(c)
			}
			require.Equal(t, 201, response.Code, response.Body.String())
			require.Equal(t, 1, spy.calls)
			require.Equal(t, int64(42), spy.actor, "body, URL and headers cannot impersonate another organizer")
			require.True(t, spy.force)
			require.Equal(t, "Own event", spy.req.Title)
			require.True(t, spy.req.ReminderMinutesSet, "compatibility must preserve normal request decoding")
			require.Nil(t, spy.req.ReminderMinutes)
			var event domain.Event
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &event))
			require.Equal(t, int64(42), event.UserID)
		}
	}
}

func TestCalendarCreationFailsBeforeDispatchWithoutAuthentication(t *testing.T) {
	for _, user := range []int64{0, -1} {
		for _, endpoint := range []string{"standard", "compatibility", "batch"} {
			spy := &calendarIdentitySpy{}
			h := &CalendarHandler{service: spy, logger: zap.NewNop()}
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			if user != 0 {
				c.Set("user_id", user)
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/?user_id=42", strings.NewReader(`{"user_id":42}`))
			c.Request.Header.Set("Content-Type", "application/json")
			switch endpoint {
			case "standard":
				h.CreateEvent(c)
			case "compatibility":
				h.CreatePublicEvent(c)
			case "batch":
				h.BatchCreateEvents(c)
			}
			require.Equal(t, 401, response.Code, response.Body.String())
			require.Zero(t, spy.calls)
		}
	}
}

func TestCalendarBatchPassesAuthenticatedOrganizerAndPreservesDenial(t *testing.T) {
	spy := &calendarIdentitySpy{err: apperrors.Forbidden("cannot create events in another user's calendar")}
	h := &CalendarHandler{service: spy, logger: zap.NewNop()}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Set("user_id", int64(42))
	c.Request = httptest.NewRequest(http.MethodPost, "/?user_id=7", strings.NewReader(`{"date_from":"2027-01-01","date_to":"2027-01-02","items":[{"mail":"other@example.invalid","meeting_name":"Other calendar"}],"organizer_id":7}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-User-Id", "7")
	h.BatchCreateEvents(c)
	require.Equal(t, 403, response.Code, response.Body.String())
	require.Equal(t, 1, spy.calls)
	require.Equal(t, int64(42), spy.actor)
}
