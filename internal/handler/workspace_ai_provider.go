package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

func WorkspaceAICompletion(provider *service.WorkspaceAIProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := middleware.GetUserID(c)
		if !ok || userID <= 0 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 512*1024))
		if err != nil {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		requestID := c.GetHeader("Idempotency-Key")
		if requestID == "" || len(requestID) > 100 {
			respondJSON(c, http.StatusBadRequest, gin.H{"code": "AI_INVALID_REQUEST", "error": "Idempotency-Key is required"})
			return
		}
		c.Header("Cache-Control", "no-store")
		body, err := provider.Complete(c.Request.Context(), userID, requestID, raw, func(event []byte) error {
			c.Header("Content-Type", "text/event-stream")
			c.Header("X-Accel-Buffering", "no")
			_, writeErr := c.Writer.Write(event)
			c.Writer.Flush()
			return writeErr
		})
		if err != nil {
			if c.Writer.Written() {
				// Reuse the public error mapper while preserving the SSE framing.
				writer := c.Writer
				c.Writer = &workspaceAIErrorStream{ResponseWriter: writer}
				WriteAICallError(c, err)
				c.Writer = writer
				c.Writer.Flush()
				return
			}
			if errors.Is(err, domain.ErrAIPaidCallInvalidRequest) {
				respondJSON(c, http.StatusBadRequest, gin.H{"code": "AI_INVALID_REQUEST", "error": "Invalid completion request"})
				return
			}
			WriteAICallError(c, err)
			return
		}
		if !c.Writer.Written() {
			c.Data(http.StatusOK, "application/json", body)
		}
	}
}

func (h *AlemHandler) WorkspaceAIAudio(c *gin.Context) {
	if h.workspaceProvider == nil {
		WriteAICallError(c, domain.ErrAIAccountingDisabled)
		return
	}
	userID, ok := middleware.GetUserID(c)
	if !ok || userID <= 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024*1024))
	if err != nil {
		c.AbortWithStatus(http.StatusRequestEntityTooLarge)
		return
	}
	result, err := h.workspaceProvider.Audio(c.Request.Context(), userID, c.GetHeader("Idempotency-Key"), c.GetHeader("X-Workspace-AI-Provider"), c.Param("operation"), c.GetHeader("Content-Type"), c.Query("output_format"), body)
	if err != nil {
		if errors.Is(err, domain.ErrAIPaidCallInvalidRequest) {
			respondJSON(c, http.StatusBadRequest, gin.H{"code": "AI_INVALID_REQUEST", "error": "Invalid speech request"})
			return
		}
		WriteAICallError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, result.ContentType, result.Body)
}

func (h *AlemHandler) WorkspaceAIJobGrant(c *gin.Context) {
	if h.workspaceProvider == nil {
		WriteAICallError(c, domain.ErrAIAccountingDisabled)
		return
	}
	userID, ok := middleware.GetUserID(c)
	if !ok || userID <= 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	var body struct {
		JobID string `json:"job_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	if c.ShouldBindJSON(&body) != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	token, err := h.workspaceProvider.IssueJobGrant(c.Request.Context(), userID, body.JobID)
	if err != nil {
		WriteAICallError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, gin.H{"job_token": token, "job_api_path": "/api/internal/ai/jobs/" + body.JobID})
}

func (h *AlemHandler) AuthenticateWorkspaceAIJob() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.workspaceProvider == nil {
			WriteAICallError(c, domain.ErrAIAccountingDisabled)
			c.Abort()
			return
		}
		authorization := c.GetHeader("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") || !strings.HasPrefix(c.GetHeader("Idempotency-Key"), c.Param("job_id")+":") {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		ctx, userID, err := h.workspaceProvider.AuthenticateJobGrant(c.Request.Context(), strings.TrimPrefix(authorization, "Bearer "), c.Param("job_id"))
		if err != nil {
			WriteAICallError(c, err)
			c.Abort()
			return
		}
		c.Set("user_id", userID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

type workspaceAIErrorStream struct{ gin.ResponseWriter }

func (w *workspaceAIErrorStream) WriteHeader(int) {}
func (w *workspaceAIErrorStream) Write(body []byte) (int, error) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		payload = map[string]any{"code": "AI_ACCOUNTING_UNAVAILABLE"}
	}
	payload["message"] = payload["error"]
	event, _ := json.Marshal(gin.H{"error": payload})
	_, err := w.ResponseWriter.Write(append(append([]byte("data: "), event...), '\n', '\n'))
	return len(body), err
}
