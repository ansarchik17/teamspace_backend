package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type NotificationHandler struct {
	service        *service.NotificationService
	liveKitService *service.LiveKitService
	logger         *zap.Logger
}

const (
	activeCallRingingWindow  = privateCallRingingTimeout
	activeCallFallbackWindow = 2 * time.Hour
)

func NewNotificationHandler(service *service.NotificationService, lkService *service.LiveKitService, logger *zap.Logger) *NotificationHandler {
	return &NotificationHandler{
		service:        service,
		liveKitService: lkService,
		logger:         logger.With(zap.String("handler", "notification")),
	}
}

// GetMyNotifications
// @Summary Get user notifications
// @Tags notifications
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Success 200 {array} domain.Notification
// @Router /notifications [get]
func (h *NotificationHandler) GetMyNotifications(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, err := h.service.GetUserNotifications(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, list)
}

// MarkAsRead
// @Summary Mark notification as read
// @Tags notifications
// @Security BearerAuth
// @Param id path string true "Notification ID"
// @Success 200
// @Router /notifications/{id}/read [post]
func (h *NotificationHandler) MarkAsRead(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid id"))
		return
	}

	if err := h.service.MarkAsRead(c.Request.Context(), id, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "ok"})
}

func (h *NotificationHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

// MarkAllAsRead
// @Summary Mark all notifications as read
// @Tags notifications
// @Security BearerAuth
// @Success 200
// @Router /notifications/read-all [post]
func (h *NotificationHandler) MarkAllAsRead(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	if err := h.service.MarkAllAsRead(c.Request.Context(), userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "ok"})
}

// SendIncomingCallPush godoc
// @Summary Send incoming-call style push (FCM data + high priority)
// @Tags notifications
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.SendIncomingCallPushRequest true "Incoming call push payload"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /notifications/incoming-call [post]
func (h *NotificationHandler) SendIncomingCallPush(c *gin.Context) {
	fromUserID := middleware.MustGetUserID(c)

	var req domain.SendIncomingCallPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	req.FromName = strings.TrimSpace(req.FromName)
	req.RoomID = strings.TrimSpace(req.RoomID)
	req.ChatID = strings.TrimSpace(req.ChatID)

	if req.RoomID == "" {
		h.respondError(c, apperrors.Validation("room_id is required"))
		return
	}

	if err := h.service.SendIncomingCallPush(
		c.Request.Context(),
		req.ToUserID,
		fromUserID,
		req.FromName,
		req.CallID,
		req.RoomID,
		req.ChatID,
		"", // priority
		req.IsVideo == nil || *req.IsVideo,
	); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "ok"})
}

// GetMyCalls
// @Summary Get call history for current user
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Param kind query string false "incoming|outgoing|all" default(all)
// @Success 200 {array} domain.CallHistory
// @Router /calls [get]
func (h *NotificationHandler) GetMyCalls(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	kind := strings.TrimSpace(c.DefaultQuery("kind", "all"))
	switch kind {
	case "incoming", "outgoing", "all":
	default:
		kind = "all"
	}

	list, err := h.service.GetUserCallHistory(c.Request.Context(), userID, limit, offset, kind)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	// Enrich with LiveKit status for potentially active calls
	for i := range list {
		// Populate RoomName (frontend requirement)
		list[i].RoomName = list[i].RoomID

		// Check if call is actually active in LiveKit
		// Optimization: Only check calls created within last 24 hours
		if time.Since(list[i].CreatedAt) < 24*time.Hour && h.liveKitService != nil {
			participants, err := h.liveKitService.ListParticipants(c.Request.Context(), list[i].RoomID)
			if err == nil {
				// If query successful
				count := len(participants)
				list[i].ParticipantsCount = count

				if isGroupCallRoom(list[i].RoomID) {
					if count >= 2 {
						list[i].Status = "ongoing"
					} else if list[i].Status == "accepted" || list[i].Status == "ongoing" {
						list[i].Status = "ended"
					}
				} else if count > 0 {
					list[i].Status = "ongoing"
				}
			}
		}
	}

	respondJSON(c, http.StatusOK, list)
}

// GetMyActiveCalls
// @Summary Get active or ringing calls for current user
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Success 200 {array} domain.CallHistory
// @Router /calls/active [get]
func (h *NotificationHandler) GetMyActiveCalls(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	list, err := h.service.GetUserActiveCalls(c.Request.Context(), userID, limit)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	now := time.Now()
	active := make([]domain.CallHistory, 0, len(list))
	for i := range list {
		call := list[i]
		call.RoomName = call.RoomID

		participantsCount := 0
		var liveKitErr error
		if h.liveKitService != nil && strings.TrimSpace(call.RoomID) != "" {
			participants, err := h.liveKitService.ListParticipants(c.Request.Context(), call.RoomID)
			if err == nil {
				participantsCount = len(participants)
			} else {
				liveKitErr = err
			}
		}

		status, ok := resolveActiveCallStatus(call, participantsCount, liveKitErr, now)
		if !ok {
			continue
		}

		call.Status = status
		call.ParticipantsCount = participantsCount
		active = append(active, call)
	}

	respondJSON(c, http.StatusOK, active)
}

// SendCallCancelPush
// @Summary Send call cancellation push (to stop ringing or close room)
// @Tags notifications
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CancelCallRequest true "Cancel call payload"
// @Success 200 {object} map[string]string
// @Router /notifications/call-cancel [post]
func (h *NotificationHandler) SendCallCancelPush(c *gin.Context) {
	var req domain.CancelCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	callID, err := uuid.Parse(req.CallID)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid call_id"))
		return
	}

	err = h.service.SendIncomingCallCancelPush(c.Request.Context(), req.ToUserID, callID, "", req.Reason)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "ok"})
}

func resolveActiveCallStatus(call domain.CallHistory, participantsCount int, liveKitErr error, now time.Time) (string, bool) {
	if isGroupCallRoom(call.RoomID) {
		if participantsCount >= 2 {
			return "ongoing", true
		}

		isRecent := now.Sub(call.CreatedAt) <= activeCallRingingWindow
		switch call.Status {
		case "initiated":
			if isRecent {
				return "initiated", true
			}
		case "accepted", "ongoing":
			return "", false
		case "missed":
			if isRecent {
				return "initiated", true
			}
		}
		return "", false
	}

	if participantsCount > 0 {
		return "ongoing", true
	}

	isRecent := now.Sub(call.CreatedAt) <= activeCallRingingWindow
	switch call.Status {
	case "initiated":
		if isRecent {
			return "initiated", true
		}
	case "accepted", "ongoing":
		if liveKitErr != nil && now.Sub(call.UpdatedAt) <= activeCallFallbackWindow {
			return "ongoing", true
		}
	}

	return "", false
}

func isGroupCallRoom(roomName string) bool {
	return strings.HasPrefix(strings.TrimSpace(roomName), "call_group_")
}
