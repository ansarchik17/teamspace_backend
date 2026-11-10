package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
)

const guestMeetingID = "1a8c56d9-114e-44b4-8bb0-4ac2445b419e"
const otherGuestMeetingID = "21bbeb26-65f3-487e-af9a-776db5f81739"

func guestRouter(base string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/api/v1/meet-guest/:meetingId/*action", NewMeetGuestHandler(config.QMeetConfig{BaseURL: base, Secret: meetTestSecret, Issuer: "test-workspace"}))
	return router
}

func guestRequest(router *gin.Engine, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://a.teamspace.kz/api/v1/meet-guest/"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://a.teamspace.kz")
	request.Header.Set("Authorization", "Bearer must-not-be-forwarded")
	request.Header.Set("X-Workspace-Assertion", "untrusted-assertion")
	request.AddCookie(&http.Cookie{Name: "qmeet_session", Value: "untrusted-browser-session"})
	if cookie != nil {
		request.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	return result
}

func TestMeetGuestAdmissionLifecycleAndCredentialIsolation(t *testing.T) {
	admitted := false
	created, revoked := 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("X-Workspace-Assertion"))
		require.Empty(t, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/app/v1/auth/guest":
			created++
			require.Empty(t, r.Header.Get("Cookie"))
			body, _ := io.ReadAll(r.Body)
			require.JSONEq(t, `{"name":"Гость Тест"}`, string(body))
			http.SetCookie(w, &http.Cookie{Name: "qmeet_session", Value: "server-guest-session", Path: "/"})
			_, _ = w.Write([]byte(`{"id":"guest-42","name":"Гость Тест","is_guest":true}`))
		case "/api/app/v1/calls/" + guestMeetingID + "/join-requests", "/api/app/v1/calls/" + guestMeetingID + "/join-requests/me":
			require.Equal(t, "qmeet_session=server-guest-session", r.Header.Get("Cookie"))
			if admitted {
				_, _ = w.Write([]byte(`{"status":"admitted"}`))
			} else {
				_, _ = w.Write([]byte(`{"status":"pending"}`))
			}
		case "/api/app/v1/calls/" + guestMeetingID + "/join":
			require.Equal(t, "qmeet_session=server-guest-session", r.Header.Get("Cookie"))
			body, _ := io.ReadAll(r.Body)
			require.JSONEq(t, `{}`, string(body))
			if !admitted {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"detail":"Ожидайте допуска организатора"}`))
				return
			}
			_, _ = w.Write([]byte(`{"role":"guest","meeting_id":"` + guestMeetingID + `","participant_token":"admitted-only-token"}`))
		case "/api/app/v1/auth/logout":
			revoked++
			require.Equal(t, "qmeet_session=server-guest-session", r.Header.Get("Cookie"))
			w.WriteHeader(204)
		default:
			t.Errorf("Unexpected upstream route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	router := guestRouter(upstream.URL)
	initial := guestRequest(router, "GET", guestMeetingID+"/session", "", nil)
	require.Equal(t, 200, initial.Code)
	require.JSONEq(t, `{"guest":null}`, initial.Body.String())
	first := guestRequest(router, "POST", guestMeetingID+"/session", `{"name":"  Гость   Тест  ","role":"organizer","user_id":"host"}`, nil)
	require.Equal(t, 200, first.Code, first.Body.String())
	require.JSONEq(t, `{"guest":{"name":"Гость Тест","status":"pending"}}`, first.Body.String())
	require.NotContains(t, first.Body.String(), "server-guest-session")
	require.NotContains(t, first.Body.String(), "participant_token")
	cookies := first.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	require.Equal(t, meetGuestCookie, cookie.Name)
	require.Equal(t, "/api/v1/meet-guest/"+guestMeetingID, cookie.Path)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, int(meetGuestTTL.Seconds()), cookie.MaxAge)
	require.Empty(t, cookie.Domain)
	// Retrying a lost response reuses the same guest; it never creates an account with a caller role/name.
	retry := guestRequest(router, "POST", guestMeetingID+"/session", `{"name":"Another name"}`, cookie)
	require.Equal(t, 200, retry.Code)
	require.Equal(t, 1, created)
	pending := guestRequest(router, "GET", guestMeetingID+"/join-requests/me?role=organizer", "", cookie)
	require.Contains(t, pending.Body.String(), "pending")
	blocked := guestRequest(router, "POST", guestMeetingID+"/join", `{"role":"organizer"}`, cookie)
	require.Equal(t, 403, blocked.Code)
	require.NotContains(t, blocked.Body.String(), "participant_token")
	// Even manually copying the HttpOnly cookie cannot cross the meeting boundary.
	other := guestRequest(router, "POST", otherGuestMeetingID+"/join", `{}`, cookie)
	require.Equal(t, 401, other.Code)
	admitted = true
	joined := guestRequest(router, "POST", guestMeetingID+"/join", `{}`, cookie)
	require.Equal(t, 200, joined.Code)
	require.Contains(t, joined.Body.String(), "admitted-only-token")
	leave := guestRequest(router, "DELETE", guestMeetingID+"/session", "", cookie)
	require.Equal(t, 204, leave.Code)
	require.Equal(t, 1, revoked)
	require.Equal(t, -1, leave.Result().Cookies()[0].MaxAge)
}

func TestMeetGuestRejectsTamperedExpiredAndWorkspaceCookies(t *testing.T) {
	router := guestRouter("http://127.0.0.1:1") // Invalid cookies must never reach QMeet.
	for _, cookie := range []string{"tampered", signGuestTestCookie(t, "wrong-audience", time.Now().Add(time.Hour)), signGuestTestCookie(t, "workspace-meet-guest", time.Now().Add(-time.Minute))} {
		result := guestRequest(router, "POST", guestMeetingID+"/join", `{}`, &http.Cookie{Name: meetGuestCookie, Value: cookie})
		require.Equal(t, 401, result.Code)
	}
}

func signGuestTestCookie(t *testing.T, audience string, expiry time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, meetGuestClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "test-workspace", Subject: "guest-42", Audience: jwt.ClaimStrings{audience}, ExpiresAt: jwt.NewNumericDate(expiry)}, MeetingID: guestMeetingID, Name: "Гость", Session: "server-guest-session"}).SignedString([]byte(meetTestSecret))
	require.NoError(t, err)
	return token
}

func TestMeetGuestRejectsUnknownRoutesNamesAndCrossSiteRequests(t *testing.T) {
	router := guestRouter("http://127.0.0.1:1")
	for _, path := range []string{guestMeetingID + "/waiting-room", guestMeetingID + "/auth/workspace/link", guestMeetingID + "/records", "invalid/session"} {
		require.Equal(t, 404, guestRequest(router, "POST", path, `{}`, nil).Code)
	}
	for _, name := range []string{" ", "x", strings.Repeat("Я", 101)} {
		body, _ := json.Marshal(map[string]string{"name": name})
		require.Equal(t, 422, guestRequest(router, "POST", guestMeetingID+"/session", string(body), nil).Code)
	}
	request := httptest.NewRequest("POST", "https://a.teamspace.kz/api/v1/meet-guest/"+guestMeetingID+"/session", strings.NewReader(`{"name":"Гость"}`))
	request.Header.Set("Origin", "https://attacker.example")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	require.Equal(t, 403, result.Code)
}

func TestMeetGuestMissingMeetingRevokesSessionAndIssuesNoCookie(t *testing.T) {
	revoked := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/app/v1/auth/guest":
			http.SetCookie(w, &http.Cookie{Name: "qmeet_session", Value: "guest-session"})
			_, _ = w.Write([]byte(`{"id":"guest-42","name":"Гость","is_guest":true}`))
		case "/api/app/v1/auth/logout":
			revoked = true
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"detail":"Активная встреча не найдена"}`))
		}
	}))
	defer upstream.Close()
	result := guestRequest(guestRouter(upstream.URL), "POST", guestMeetingID+"/session", `{"name":"Гость"}`, nil)
	require.Equal(t, 404, result.Code)
	require.Empty(t, result.Result().Cookies())
	require.True(t, revoked)
}

func TestMeetGuestCreationIsRateLimitedAndRejectsFullAccountResponse(t *testing.T) {
	created := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		created++
		_, _ = w.Write([]byte(`{"id":"host","name":"Гость","is_guest":false}`))
	}))
	defer upstream.Close()
	router := guestRouter(upstream.URL)
	for range 5 {
		result := guestRequest(router, "POST", guestMeetingID+"/session", `{"name":"Гость"}`, nil)
		require.Equal(t, 502, result.Code)
		require.Empty(t, result.Result().Cookies())
	}
	require.Equal(t, 429, guestRequest(router, "POST", guestMeetingID+"/session", `{"name":"Гость"}`, nil).Code)
	require.Equal(t, 5, created)
}
