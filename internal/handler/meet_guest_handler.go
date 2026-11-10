package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

const meetGuestCookie = "workspace_meet_guest"
const meetGuestTTL = 4 * time.Hour

type meetGuestClaims struct {
	jwt.RegisteredClaims
	MeetingID string `json:"meeting_id"`
	Name      string `json:"name"`
	Session   string `json:"session"`
}

// Guest access uses QMeet's existing guest identities and organizer admission.
// A signed, HttpOnly cookie exposes only this meeting's guest API, never Workspace auth.
func NewMeetGuestHandler(cfg config.QMeetConfig) gin.HandlerFunc {
	base, parseErr := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	createLimit := middleware.NewRateLimiter(nil, zap.NewNop(), 5, time.Minute, true).Limit()
	pollLimit := middleware.NewRateLimiter(nil, zap.NewNop(), 120, time.Minute, true).Limit()
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		meetingID := c.Param("meetingId")
		parsedID, err := uuid.Parse(meetingID)
		if err != nil || parsedID.String() != strings.ToLower(meetingID) {
			respondJSON(c, 404, gin.H{"message": apiMessage(c, "Ссылка на встречу некорректна")})
			return
		}
		meetingID = parsedID.String()
		action := c.Param("action")
		method := c.Request.Method
		if !((action == "/session" && (method == "GET" || method == "POST" || method == "DELETE")) || (action == "/join-requests/me" && method == "GET") || (action == "/join" && method == "POST")) {
			respondJSON(c, 404, gin.H{"message": apiMessage(c, "Действие гостевого входа не найдено")})
			return
		}
		if parseErr != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || len(cfg.Secret) < 32 || cfg.Issuer == "" {
			respondJSON(c, 503, gin.H{"message": apiMessage(c, "Гостевой вход Meet ещё не настроен")})
			return
		}
		if method != "GET" {
			origin := c.GetHeader("Origin")
			originURL, originErr := url.Parse(origin)
			if c.GetHeader("Sec-Fetch-Site") == "cross-site" || (origin != "" && (originErr != nil || !strings.EqualFold(originURL.Host, c.Request.Host) || (originURL.Scheme != "http" && originURL.Scheme != "https"))) {
				respondJSON(c, 403, gin.H{"message": apiMessage(c, "Откройте ссылку встречи в TeamSpace")})
				return
			}
		}
		if method == "POST" && action == "/session" {
			createLimit(c)
		} else {
			pollLimit(c)
		}
		if c.IsAborted() {
			return
		}
		cookiePath := "/api/v1/meet-guest/" + meetingID
		setCookie := func(value string, maxAge int) {
			http.SetCookie(c.Writer, &http.Cookie{Name: meetGuestCookie, Value: value, Path: cookiePath, HttpOnly: true, Secure: requestIsSecure(c), SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
		}
		claims := &meetGuestClaims{}
		cookie, cookieErr := c.Cookie(meetGuestCookie)
		if cookieErr == nil {
			token, tokenErr := jwt.ParseWithClaims(cookie, claims, func(_ *jwt.Token) (any, error) { return []byte(cfg.Secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(cfg.Issuer), jwt.WithAudience("workspace-meet-guest"), jwt.WithExpirationRequired())
			if tokenErr != nil || !token.Valid || claims.MeetingID != meetingID || claims.Subject == "" || claims.Session == "" {
				claims = &meetGuestClaims{}
			}
		}
		upstream := func(path, method string, body []byte, session string) (*http.Response, []byte, error) {
			target := *base
			target.Path = strings.TrimRight(base.Path, "/") + "/api/app/v1" + path
			request, err := http.NewRequestWithContext(c.Request.Context(), method, target.String(), bytes.NewReader(body))
			if err != nil {
				return nil, nil, err
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
			request.Header.Set("X-Forwarded-For", c.ClientIP())
			// Never forward caller cookies, authorization, assertions, identity or role.
			if session != "" {
				request.AddCookie(&http.Cookie{Name: "qmeet_session", Value: session})
			}
			response, err := client.Do(request)
			if err != nil {
				return nil, nil, err
			}
			defer response.Body.Close()
			payload, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
			if err != nil || len(payload) > 1<<20 {
				return nil, nil, io.ErrUnexpectedEOF
			}
			return response, payload, nil
		}
		checkResponse := func(response *http.Response, payload []byte, err error) bool {
			if err != nil {
				respondJSON(c, 502, gin.H{"message": apiMessage(c, "Meet временно недоступен. Попробуйте ещё раз.")})
				return false
			}
			if response.StatusCode < 300 {
				return true
			}
			status := response.StatusCode
			if status >= 300 && status < 400 {
				status = 502
			}
			message := "Не удалось выполнить гостевой вход"
			var detail struct {
				Detail string `json:"detail"`
			}
			if json.Unmarshal(payload, &detail) == nil && detail.Detail != "" {
				message = detail.Detail
			}
			if status == 401 {
				setCookie("", -1)
				message = "Сессия гостя истекла. Введите имя и отправьте запрос снова."
			}
			respondJSON(c, status, gin.H{"code": "meet_guest_error", "message": message})
			return false
		}
		if method == "DELETE" {
			setCookie("", -1)
			if claims.Session != "" {
				_, _, _ = upstream("/auth/logout", "POST", []byte(`{}`), claims.Session)
			}
			c.Status(204)
			return
		}
		if method == "GET" && action == "/session" && claims.Session == "" {
			respondJSON(c, 200, gin.H{"guest": nil})
			return
		}
		if method == "POST" && action == "/session" && claims.Session == "" {
			var input struct {
				Name string `json:"name"`
			}
			body := http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
			if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") || json.NewDecoder(body).Decode(&input) != nil {
				respondJSON(c, 400, gin.H{"message": apiMessage(c, "Введите имя гостя")})
				return
			}
			name := strings.Join(strings.Fields(input.Name), " ")
			if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
				respondJSON(c, 422, gin.H{"message": apiMessage(c, "Имя должно содержать от 2 до 100 символов")})
				return
			}
			bodyJSON, _ := json.Marshal(gin.H{"name": name})
			response, payload, err := upstream("/auth/guest", "POST", bodyJSON, "")
			if !checkResponse(response, payload, err) {
				return
			}
			var user struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				IsGuest bool   `json:"is_guest"`
			}
			if json.Unmarshal(payload, &user) != nil || !user.IsGuest || user.ID == "" {
				respondJSON(c, 502, gin.H{"message": apiMessage(c, "Не удалось создать гостевую сессию")})
				return
			}
			var session string
			for _, upstreamCookie := range response.Cookies() {
				if upstreamCookie.Name == "qmeet_session" {
					session = upstreamCookie.Value
				}
			}
			if session == "" {
				respondJSON(c, 502, gin.H{"message": apiMessage(c, "Не удалось создать гостевую сессию")})
				return
			}
			now := time.Now()
			claims = &meetGuestClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: cfg.Issuer, Subject: user.ID, Audience: jwt.ClaimStrings{"workspace-meet-guest"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(meetGuestTTL))}, MeetingID: meetingID, Name: user.Name, Session: session}
			// Bind to the meeting and request admission before handing a session to the browser.
			response, payload, err = upstream("/calls/"+meetingID+"/join-requests", "POST", []byte(`{}`), session)
			if !checkResponse(response, payload, err) {
				_, _, _ = upstream("/auth/logout", "POST", []byte(`{}`), session)
				return
			}
			signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(cfg.Secret))
			if err != nil {
				respondJSON(c, 500, gin.H{"message": apiMessage(c, "Не удалось сохранить гостевую сессию")})
				return
			}
			setCookie(signed, int(meetGuestTTL.Seconds()))
			var waiting struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(payload, &waiting) != nil {
				respondJSON(c, 502, gin.H{"message": apiMessage(c, "Не удалось прочитать запрос на вход")})
				return
			}
			respondJSON(c, 200, gin.H{"guest": gin.H{"name": claims.Name, "status": waiting.Status}})
			return
		}
		if claims.Session == "" {
			respondJSON(c, 401, gin.H{"message": apiMessage(c, "Введите имя, чтобы войти гостем")})
			return
		}
		path := "/calls/" + meetingID + "/join-requests/me"
		upstreamMethod := "GET"
		if action == "/join" {
			path = "/calls/" + meetingID + "/join"
			upstreamMethod = "POST"
		}
		response, payload, err := upstream(path, upstreamMethod, []byte(`{}`), claims.Session)
		if !checkResponse(response, payload, err) {
			return
		}
		if action == "/join" {
			var session struct {
				Role string `json:"role"`
			}
			if json.Unmarshal(payload, &session) != nil || session.Role != "guest" {
				respondJSON(c, 502, gin.H{"message": apiMessage(c, "Не удалось подтвердить гостевой доступ")})
				return
			}
			c.Data(200, "application/json", payload)
			return
		}
		var waiting struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(payload, &waiting) != nil {
			respondJSON(c, 502, gin.H{"message": apiMessage(c, "Не удалось прочитать запрос на вход")})
			return
		}
		respondJSON(c, 200, gin.H{"guest": gin.H{"name": claims.Name, "status": waiting.Status}})
	}
}
