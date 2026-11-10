package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
	ws "github.com/west6ide/workspace-backend/internal/websocket"
)

func buildRealtimeSessionKey(c *gin.Context) string {
	if c == nil {
		return ""
	}

	for _, candidate := range sessionIdentityCandidates(c) {
		if candidate != "" {
			return ws.BuildSessionKey(candidate)
		}
	}

	return ""
}

func sessionIdentityCandidates(c *gin.Context) []string {
	candidates := []string{
		headerOrQueryValue(c, "X-Client-Instance-ID", "client_instance_id"),
		headerOrQueryValue(c, "X-Session-ID", "session_id"),
		headerOrQueryValue(c, "X-Call-Session-ID", "call_session_id"),
		headerOrQueryValue(c, "X-Device-ID", "device_id"),
		headerOrQueryValue(c, "X-Web-Device-ID", "web_device_id"),
	}

	if claims, ok := middleware.GetClaims(c); ok && claims != nil && claims.Context != nil {
		if linkedDeviceID := strings.TrimSpace(claims.Context.LinkedDeviceID); linkedDeviceID != "" {
			candidates = append(candidates, "linked_device:"+linkedDeviceID)
		}
	}

	if token := strings.TrimSpace(middleware.ExtractToken(c)); token != "" {
		candidates = append(candidates, "token:"+token)
	}

	return candidates
}

func currentLinkedDeviceID(c *gin.Context) string {
	if c == nil {
		return ""
	}

	if claims, ok := middleware.GetClaims(c); ok && claims != nil && claims.Context != nil {
		if linkedDeviceID := strings.TrimSpace(claims.Context.LinkedDeviceID); linkedDeviceID != "" {
			return linkedDeviceID
		}
	}

	for _, candidate := range []struct {
		header string
		query  string
	}{
		{header: "X-Web-Device-ID", query: "web_device_id"},
		{header: "X-Device-ID", query: "device_id"},
	} {
		if value := strings.TrimSpace(c.GetHeader(candidate.header)); value != "" {
			return value
		}
		if value := strings.TrimSpace(c.Query(candidate.query)); value != "" {
			return value
		}
	}

	return ""
}

func headerOrQueryValue(c *gin.Context, headerName, queryName string) string {
	if c == nil {
		return ""
	}

	if value := strings.TrimSpace(c.GetHeader(headerName)); value != "" {
		return headerName + ":" + value
	}

	if value := strings.TrimSpace(c.Query(queryName)); value != "" {
		return queryName + ":" + value
	}

	return ""
}
