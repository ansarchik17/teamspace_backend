package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// Keep the bridge limited to the meeting UI; never expose internal/agent APIs.
var meetRoutes = map[string]*regexp.Regexp{
	http.MethodGet:  regexp.MustCompile(`^/(auth/me|records|calendar|calls/[a-zA-Z0-9-]+(?:/join-requests/me|/waiting-room)?)$`),
	http.MethodPost: regexp.MustCompile(`^/(auth/workspace/link|calendar|calendar/[a-zA-Z0-9-]+/start|calls|calls/[a-zA-Z0-9-]+/(join|join-requests|waiting-room/[a-zA-Z0-9-]+/(admit|deny)))$`),
}

type meetUserResolver func(context.Context, int64) (*domain.UserResponse, error)

func (h *AuthHandler) MeetProxy(cfg config.QMeetConfig) gin.HandlerFunc {
	return newMeetProxy(cfg, h.authService.GetCurrentUser)
}

func newMeetProxy(cfg config.QMeetConfig, resolve meetUserResolver) gin.HandlerFunc {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	base, parseErr := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		path := c.Param("path")
		route := meetRoutes[c.Request.Method]
		if route == nil || !route.MatchString(path) {
			respondJSON(c, http.StatusNotFound, gin.H{"message": apiMessage(c, "Действие Meet не найдено")})
			return
		}
		if parseErr != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || len(cfg.Secret) < 32 || cfg.Issuer == "" {
			respondJSON(c, http.StatusServiceUnavailable, gin.H{"code": "meet_not_configured", "message": apiMessage(c, "Meet ещё не подключён. Настройте соединение с QMeet на сервере.")})
			return
		}
		user, err := resolve(c.Request.Context(), middleware.MustGetUserID(c))
		if err != nil || user == nil || !user.IsActive {
			respondJSON(c, http.StatusForbidden, gin.H{"message": apiMessage(c, "Пользователь недоступен")})
			return
		}
		if strings.TrimSpace(user.Email) == "" {
			respondJSON(c, http.StatusUnprocessableEntity, gin.H{"message": apiMessage(c, "Для Meet добавьте email в профиль")})
			return
		}
		now := time.Now()
		claims := jwt.MapClaims{"iss": cfg.Issuer, "aud": "qmeet-workspace", "sub": strconv.FormatInt(user.ID, 10), "email": user.Email, "name": user.FullName, "iat": now.Unix(), "exp": now.Add(30 * time.Second).Unix()}
		assertion, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(cfg.Secret))
		if err != nil {
			respondJSON(c, http.StatusInternalServerError, gin.H{"message": apiMessage(c, "Не удалось подтвердить пользователя")})
			return
		}
		requestBody := c.Request.Body
		if requestBody == nil {
			requestBody = http.NoBody
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, requestBody, 64<<10))
		if err != nil {
			respondJSON(c, http.StatusRequestEntityTooLarge, gin.H{"message": apiMessage(c, "Запрос слишком большой")})
			return
		}
		target := *base
		target.Path = strings.TrimRight(base.Path, "/") + "/api/app/v1" + path
		target.RawQuery = c.Request.URL.RawQuery
		request, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), strings.NewReader(string(body)))
		if err != nil {
			respondJSON(c, http.StatusBadGateway, gin.H{"message": apiMessage(c, "Не удалось обратиться к QMeet")})
			return
		}
		// Do not forward browser cookies, Workspace tokens, or caller-supplied assertions.
		request.Header.Set("X-Workspace-Assertion", assertion)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			respondJSON(c, http.StatusBadGateway, gin.H{"code": "meet_unavailable", "message": apiMessage(c, "QMeet временно недоступен. Попробуйте ещё раз.")})
			return
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
		if err != nil || len(payload) > 8<<20 {
			respondJSON(c, http.StatusBadGateway, gin.H{"message": apiMessage(c, "Не удалось прочитать ответ QMeet")})
			return
		}
		if response.StatusCode >= 300 {
			status := response.StatusCode
			if status == http.StatusUnauthorized {
				status = http.StatusBadGateway // An upstream identity failure must not log out Workspace.
				if path == "/auth/workspace/link" {
					status = http.StatusUnprocessableEntity
				}
			}
			if status >= 300 && status < 400 {
				status = http.StatusBadGateway
			}
			message := "Не удалось выполнить запрос к QMeet"
			code := "meet_upstream_error"
			var data struct {
				Detail json.RawMessage `json:"detail"`
			}
			if json.Unmarshal(payload, &data) == nil {
				var detail string
				if json.Unmarshal(data.Detail, &detail) == nil && detail != "" {
					message = detail
				}
				var object struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if json.Unmarshal(data.Detail, &object) == nil && object.Code != "" {
					code = object.Code
					message = object.Message
				}
			}
			respondJSON(c, status, gin.H{"code": code, "message": message})
			return
		}
		c.Data(response.StatusCode, "application/json", payload)
	}
}
