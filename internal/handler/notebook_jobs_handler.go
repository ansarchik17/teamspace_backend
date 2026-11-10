package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

// SetResearchJobs enables the asynchronous research routes. It must be called
// at startup, before serving requests.
func (h *NotebookHandler) SetResearchJobs(jobs *service.NotebookResearchJobService) {
	h.jobs = jobs
	if jobs != nil {
		jobs.SetErrorRenderer(renderNotebookJobError)
	}
}

// CreateAskJob godoc
// @Summary Start notebook research in the background
// @Description Same body and checks as /ask. Returns 202 with a queued job; a repeated operation_id returns the existing job with 200.
// @Description Progress: GET .../ask/jobs/{job_id} or the WebSocket event notebook.research.progress. One unfinished job per user (409 otherwise).
// @Tags notebook
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Chat ID"
// @Param request body service.NotebookResearchRequest true "Question or preset"
// @Success 202 {object} service.NotebookResearchJob
// @Success 200 {object} service.NotebookResearchJob
// @Router /notebook/chats/{id}/ask/jobs [post]
func (h *NotebookHandler) CreateAskJob(c *gin.Context) {
	if h.jobs == nil {
		h.respondError(c, apperrors.Internal("research service unavailable"))
		return
	}
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}
	var req service.NotebookResearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid research request"))
		return
	}
	notebookOperationID(c, &req)
	job, created, err := h.jobs.Create(c.Request.Context(), middleware.MustGetUserID(c), chatID, req)
	if err != nil {
		h.respondResearchError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	respondJSON(c, status, job)
}

// GetAskJob godoc
// @Summary Status, progress and result of a background notebook research job
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param id path string true "Chat ID"
// @Param job_id path string true "Job ID"
// @Success 200 {object} service.NotebookResearchJob
// @Router /notebook/chats/{id}/ask/jobs/{job_id} [get]
func (h *NotebookHandler) GetAskJob(c *gin.Context) {
	h.askJob(c, false)
}

// CancelAskJob godoc
// @Summary Cancel a background notebook research job
// @Description Returns the job; a finished job is returned unchanged. Completed paid steps stay charged.
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param id path string true "Chat ID"
// @Param job_id path string true "Job ID"
// @Success 200 {object} service.NotebookResearchJob
// @Router /notebook/chats/{id}/ask/jobs/{job_id} [delete]
func (h *NotebookHandler) CancelAskJob(c *gin.Context) {
	h.askJob(c, true)
}

func (h *NotebookHandler) askJob(c *gin.Context, cancel bool) {
	if h.jobs == nil {
		h.respondError(c, apperrors.Internal("research service unavailable"))
		return
	}
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}
	jobID, err := uuid.Parse(c.Param("job_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid job id"))
		return
	}
	userID := middleware.MustGetUserID(c)
	var job *service.NotebookResearchJob
	if cancel {
		job, err = h.jobs.Cancel(c.Request.Context(), userID, chatID, jobID)
	} else {
		job, err = h.jobs.Get(c.Request.Context(), userID, chatID, jobID)
	}
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, job)
}

func (h *NotebookHandler) respondResearchError(c *gin.Context, err error) {
	if IsAICallError(err) {
		WriteAICallError(c, err)
		return
	}
	h.respondError(c, apperrors.ToAppError(err))
}

// renderNotebookJobError produces exactly the error response the synchronous
// /ask route would have written, so clients handle both the same way. ctx is
// the job context, which keeps the request locale.
func renderNotebookJobError(ctx context.Context, err error) service.NotebookResearchJobError {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	(&NotebookHandler{}).respondResearchError(c, err)
	result := service.NotebookResearchJobError{HTTPStatus: recorder.Code, Body: json.RawMessage(recorder.Body.Bytes())}
	var body struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &body) == nil {
		result.Code, result.Message = body.Code, body.Message
		var nested struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body.Error, &nested) == nil && nested.Code != "" {
			result.Code, result.Message = nested.Code, nested.Message
		} else if result.Message == "" {
			_ = json.Unmarshal(body.Error, &result.Message)
		}
	}
	return result
}
