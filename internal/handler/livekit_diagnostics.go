package handler

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"github.com/west6ide/workspace-backend/internal/telemetry"
)

func (h *LiveKitHandler) requestIDFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, exists := c.Get("request_id"); exists {
		if requestID, ok := value.(string); ok {
			return strings.TrimSpace(requestID)
		}
	}
	return ""
}

func (h *LiveKitHandler) callDeviceIDFromRequest(c *gin.Context) string {
	if c == nil {
		return ""
	}
	headers := []string{
		"X-Call-Device-ID",
		"X-Device-ID",
		"X-Web-Device-ID",
	}
	for _, header := range headers {
		if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
			return value
		}
	}
	for _, queryKey := range []string{"device_id", "web_device_id"} {
		if value := strings.TrimSpace(c.Query(queryKey)); value != "" {
			return value
		}
	}
	if linkedDeviceID := currentLinkedDeviceID(c); linkedDeviceID != "" {
		return linkedDeviceID
	}
	return ""
}

func (h *LiveKitHandler) callSessionIDFromRequest(c *gin.Context) string {
	if c == nil {
		return ""
	}
	headers := []string{
		"X-Call-Session-ID",
		"X-Session-ID",
	}
	for _, header := range headers {
		if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
			return value
		}
	}
	for _, queryKey := range []string{"session_id", "call_session_id"} {
		if value := strings.TrimSpace(c.Query(queryKey)); value != "" {
			return value
		}
	}
	return ""
}

func (h *LiveKitHandler) tokenContextFromRequest(c *gin.Context) service.LiveKitTokenContext {
	if c == nil {
		return service.LiveKitTokenContext{}
	}

	return service.LiveKitTokenContext{
		RequestID: h.requestIDFromContext(c),
		DeviceID:  h.callDeviceIDFromRequest(c),
		SessionID: h.callSessionIDFromRequest(c),
		UserAgent: strings.TrimSpace(c.Request.UserAgent()),
	}
}

func (h *LiveKitHandler) traceFromRequest(c *gin.Context, userID int64, callID string, roomName string, identity string) telemetry.TraceContext {
	tokenCtx := h.tokenContextFromRequest(c)
	return telemetry.TraceContext{
		CallID:    strings.TrimSpace(callID),
		RoomName:  strings.TrimSpace(roomName),
		RequestID: tokenCtx.RequestID,
		UserID:    userID,
		Identity:  strings.TrimSpace(identity),
		SessionID: strings.TrimSpace(tokenCtx.SessionID),
		DeviceID:  strings.TrimSpace(tokenCtx.DeviceID),
	}
}

func (h *LiveKitHandler) recordCallEvent(c *gin.Context, kind string, userID int64, callID string, roomName string, identity string, status string, details map[string]any) {
	if h.telemetry == nil {
		return
	}
	h.telemetry.RecordEvent(kind, h.traceFromRequest(c, userID, callID, roomName, identity), status, details)
}

func (h *LiveKitHandler) incrementTelemetry(name string, labels map[string]string) {
	if h.telemetry == nil {
		return
	}
	h.telemetry.IncrementCounter(name, labels)
}

func (h *LiveKitHandler) telemetrySnapshot(c *gin.Context) (telemetry.Snapshot, *apperrors.AppError) {
	if h.telemetry == nil || !h.telemetry.Enabled() {
		return telemetry.Snapshot{}, apperrors.NotFound("call telemetry")
	}

	limit := 100
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsedLimit, err := strconv.Atoi(raw)
		if err != nil || parsedLimit <= 0 {
			return telemetry.Snapshot{}, apperrors.BadRequest("invalid limit")
		}
		if parsedLimit > 500 {
			parsedLimit = 500
		}
		limit = parsedLimit
	}

	snapshot := h.telemetry.Snapshot(telemetry.SnapshotFilter{
		CallID:   strings.TrimSpace(c.Query("call_id")),
		RoomName: strings.TrimSpace(c.Query("room_name")),
		Kind:     strings.TrimSpace(c.Query("kind")),
		Limit:    limit,
	})
	return snapshot, nil
}

func (h *LiveKitHandler) GetTelemetry(c *gin.Context) {
	snapshot, appErr := h.telemetrySnapshot(c)
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}

	respondJSON(c, http.StatusOK, snapshot)
}

func liveKitDebugAllowed(c *gin.Context) bool {
	if gin.Mode() == gin.DebugMode {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "development") {
		return true
	}
	if role, ok := c.Get("role"); ok {
		switch v := role.(type) {
		case domain.Role:
			return v == domain.RoleSuperAdmin
		case string:
			return v == string(domain.RoleSuperAdmin)
		}
	}
	return false
}

func (h *LiveKitHandler) GetLiveKitDebug(c *gin.Context) {
	if !liveKitDebugAllowed(c) {
		h.respondError(c, apperrors.Forbidden("LiveKit debug is available only for dev/admin"))
		return
	}

	roomName := strings.TrimSpace(c.Query("room_name"))
	egressID := strings.TrimSpace(c.Query("egress_id"))
	if roomName == "" && egressID == "" {
		h.respondError(c, apperrors.Validation("room_name or egress_id is required"))
		return
	}

	response := gin.H{}

	if roomName != "" {
		participants, err := h.service.DescribeRoomParticipants(c.Request.Context(), roomName)
		if err != nil {
			response["participants_error"] = err.Error()
		} else {
			response["room_name"] = roomName
			response["participants"] = participants
			response["participants_count"] = len(participants)
		}
	}

	if egressID != "" {
		info, err := h.service.GetEgressInfo(c.Request.Context(), egressID)
		if err != nil {
			response["egress_error"] = err.Error()
		} else {
			response["egress"] = gin.H{
				"egress_id":  info.EgressId,
				"room_name":  info.RoomName,
				"status":     info.Status.String(),
				"error":      info.Error,
				"started_at": info.StartedAt,
				"ended_at":   info.EndedAt,
				"updated_at": info.UpdatedAt,
			}
		}
	}

	respondJSON(c, http.StatusOK, response)
}
