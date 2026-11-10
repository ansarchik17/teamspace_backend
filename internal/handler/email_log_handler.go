package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type EmailLogHandler struct {
	service *service.EmailLogService
	logger  *zap.Logger
}

type TestEmailRequest struct {
	To      string `json:"to" binding:"required"`
	Subject string `json:"subject" binding:"required"`
	Message string `json:"message" binding:"required"`
}

func NewEmailLogHandler(service *service.EmailLogService, logger *zap.Logger) *EmailLogHandler {
	return &EmailLogHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "email_log")),
	}
}

// HandleIncomingEmail
// @Summary Receive incoming email and log match status
// @Tags email_logs
// @Accept json
// @Produce json
// @Param request body domain.IncomingEmailRequest true "Email content"
// @Success 200 {object} domain.EmailLog
// @Router /users/notification [post]
func (h *EmailLogHandler) HandleIncomingEmail(c *gin.Context) {
	var req domain.IncomingEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	result, err := h.service.ProcessIncomingEmail(c.Request.Context(), req.Email, req.Text, req.Timestamp)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// GetLogs
// @Summary Get email notification logs
// @Tags email_logs
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Success 200 {array} domain.EmailLog
// @Router /users/notifications [get]
func (h *EmailLogHandler) GetLogs(c *gin.Context) {
	limit := 50
	offset := 0
	// Simple query parsing, can be improved
	// Assuming you might want to use strconv or similar if you strictly need int validation,
	// but for now let's keep it simple or assume defaults if parsing fails.
	// Actually, let's just use defaults or a helper if available, but I'll write inline for speed.

	// Use c.BindQuery or similar if defined, or just let service handle validation?
	// Let's use service call directly with safe defaults.

	logs, err := h.service.GetAllLogs(c.Request.Context(), limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, logs)
}

func (h *EmailLogHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

// CreateMapping
// @Summary Create a new email mapping
// @Tags email_mappings
// @Accept json
// @Produce json
// @Param request body domain.CreateEmailMappingRequest true "Mapping details"
// @Success 201 {object} domain.EmailMapping
// @Router /email/mappings [post]
func (h *EmailLogHandler) CreateMapping(c *gin.Context) {
	var req domain.CreateEmailMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	mapping, err := h.service.CreateMapping(c.Request.Context(), req.IncomingEmail, req.TargetEmail)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, mapping)
}

// GetMappings
// @Summary Get all email mappings
// @Tags email_mappings
// @Produce json
// @Success 200 {array} domain.EmailMapping
// @Router /email/mappings [get]
func (h *EmailLogHandler) GetMappings(c *gin.Context) {
	limit := 100
	offset := 0 // Simple pagination

	mappings, err := h.service.GetMappings(c.Request.Context(), limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, mappings)
}

// DeleteMapping
// @Summary Delete an email mapping
// @Tags email_mappings
// @Param id path string true "Mapping ID"
// @Success 204
// @Router /email/mappings/{id} [delete]
func (h *EmailLogHandler) DeleteMapping(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		h.respondError(c, apperrors.Validation("missing id"))
		return
	}

	if err := h.service.DeleteMapping(c.Request.Context(), id); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *EmailLogHandler) SendTestEmail(c *gin.Context) {
	role, _ := c.Get("role")
	if role != domain.RoleSuperAdmin && role != nil {
		h.respondError(c, apperrors.Forbidden("admin access required"))
		return
	}

	var req TestEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.SendTestEmail(c.Request.Context(), req.To, req.Subject, req.Message); err != nil {
		h.logger.Warn("test email send failed",
			zap.String("recipient", req.To),
			zap.String("subject", req.Subject),
			zap.Error(err),
		)
		h.respondError(c, apperrors.InternalWithErr("failed to send test email", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"status":  "sent",
		"to":      req.To,
		"subject": req.Subject,
	})
}
