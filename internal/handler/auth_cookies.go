package handler

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/auth"
)

type authCookieReport struct {
	AccessSet         bool
	RefreshSet        bool
	AccessMaxAge      int
	RefreshMaxAge     int
	Secure            bool
	HttpOnly          bool
	SameSite          http.SameSite
	SameSiteLabel     string
	Path              string
	RefreshTokenEmpty bool
}

func (r authCookieReport) cookieNames() []string {
	names := make([]string, 0, 2)
	if r.AccessSet {
		names = append(names, "access_token")
	}
	if r.RefreshSet {
		names = append(names, "refresh_token")
	}
	return names
}

func setAuthCookies(c *gin.Context, tokens *auth.TokenPair) authCookieReport {
	report := authCookieReport{
		HttpOnly: true,
		Path:     "/",
	}
	if c == nil || tokens == nil || strings.TrimSpace(tokens.AccessToken) == "" {
		return report
	}

	secure := requestIsSecure(c)
	sameSite, sameSiteLabel := authCookieSameSite(secure)
	c.SetSameSite(sameSite)

	accessMaxAge := int(time.Until(tokens.ExpiresAt).Seconds())
	if accessMaxAge <= 0 && tokens.ExpiresIn > 0 {
		accessMaxAge = int(tokens.ExpiresIn)
	}
	if accessMaxAge < 0 {
		accessMaxAge = 0
	}

	report.AccessSet = true
	report.AccessMaxAge = accessMaxAge
	report.Secure = secure
	report.SameSite = sameSite
	report.SameSiteLabel = sameSiteLabel
	c.SetCookie("access_token", tokens.AccessToken, accessMaxAge, "/", "", secure, true)
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		report.RefreshSet = true
		report.RefreshMaxAge = refreshTokenCookieMaxAge()
		c.SetCookie("refresh_token", tokens.RefreshToken, report.RefreshMaxAge, "/", "", secure, true)
	} else {
		report.RefreshTokenEmpty = true
	}

	return report
}

func clearAuthCookies(c *gin.Context) authCookieReport {
	secure := requestIsSecure(c)
	sameSite, sameSiteLabel := authCookieSameSite(secure)
	c.SetSameSite(sameSite)

	c.SetCookie("access_token", "", -1, "/", "", secure, true)
	c.SetCookie("refresh_token", "", -1, "/", "", secure, true)
	return authCookieReport{
		AccessSet:     true,
		RefreshSet:    true,
		AccessMaxAge:  -1,
		RefreshMaxAge: -1,
		Secure:        secure,
		HttpOnly:      true,
		SameSite:      sameSite,
		SameSiteLabel: sameSiteLabel,
		Path:          "/",
	}
}

func requestIsSecure(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if c.Request.TLS != nil {
		return true
	}

	forwardedProto := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")))
	if forwardedProto == "https" {
		return true
	}

	forwardedSSL := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Forwarded-Ssl")))
	return forwardedSSL == "on" || forwardedSSL == "1" || forwardedSSL == "true"
}

func authCookieSameSite(secure bool) (http.SameSite, string) {
	if secure {
		return http.SameSiteNoneMode, "None"
	}
	return http.SameSiteLaxMode, "Lax"
}

func refreshTokenCookieMaxAge() int {
	return durationEnvSeconds("JWT_REFRESH_TOKEN_EXPIRY", 30*24*time.Hour)
}

func durationEnvSeconds(key string, fallback time.Duration) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return int(fallback.Seconds())
	}

	if duration, ok := parseFlexibleDuration(raw); ok {
		return int(duration.Seconds())
	}

	return int(fallback.Seconds())
}

func parseFlexibleDuration(raw string) (time.Duration, bool) {
	if raw == "" {
		return 0, false
	}

	if duration, err := time.ParseDuration(raw); err == nil {
		return duration, true
	}

	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0, false
		}
		return time.Duration(days) * 24 * time.Hour, true
	}

	return 0, false
}
