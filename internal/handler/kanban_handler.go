package handler

import (
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func boardID(board *domain.Board) string {
	if board == nil {
		return ""
	}
	return board.ID.String()
}

type KanbanHandler struct {
	svc    *service.KanbanService
	logger *zap.Logger
}

func NewKanbanHandler(svc *service.KanbanService, logger *zap.Logger) *KanbanHandler {
	return &KanbanHandler{svc: svc,
		logger: logger}
}

// CreateBoard godoc
// @Summary Создать новую доску
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body service.CreateBoardRequest true "Запрос на создание доски"
// @Success 201 {object} domain.Board
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards [post]
func (h *KanbanHandler) CreateBoard(c *gin.Context) {
	var req service.CreateBoardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	board, err := h.svc.CreateBoard(c.Request.Context(), userID, req)
	if err != nil {
		h.logger.Error("create board failed",
			zap.Int64("user_id", userID),
			zap.String("board_name", req.Name),
			zap.Error(err),
		)
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	h.logger.Info("board created",
		zap.Int64("user_id", userID),
		zap.String("board_name", req.Name),
	)

	respondJSON(c, http.StatusCreated, board)
}

// ListBoards godoc
// @Summary Получить список досок пользователя
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.Board
// @Failure 500 {object} map[string]any
// @Router /kanban/boards [get]
func (h *KanbanHandler) ListBoards(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	boards, err := h.svc.ListUserBoards(c.Request.Context(), userID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, boards)
}

// ListMyDocumentologAssignments godoc
// @Summary Получить мои поручения из Documentolog
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.Task
// @Router /kanban/documentolog/assignments [get]
func (h *KanbanHandler) ListMyDocumentologAssignments(c *gin.Context) {
	tasks, err := h.svc.ListMyDocumentologAssignments(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, tasks)
}

// UpdateBoard godoc
// @Summary Обновить доску
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param request body service.UpdateBoardRequest true "Данные для обновления доски"
// @Success 200 {object} domain.Board
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id} [put]
func (h *KanbanHandler) UpdateBoard(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	var req service.UpdateBoardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}

	userID := middleware.MustGetUserID(c)
	board, err := h.svc.UpdateBoard(c.Request.Context(), userID, boardID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, board)
}

// DeleteBoard godoc
// @Summary Удалить доску
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id} [delete]
func (h *KanbanHandler) DeleteBoard(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.DeleteBoard(c.Request.Context(), userID, boardID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// GetBoard godoc
// @Summary Получить детали доски (колонки, задачи, спринты)
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Success 200 {object} domain.BoardResponse
// @Failure 400 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id} [get]
func (h *KanbanHandler) GetBoard(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	userID := middleware.MustGetUserID(c)
	resp, err := h.svc.GetBoardDetails(c.Request.Context(), userID, id)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetBoardTasksByUser godoc
// @Summary Получить задачи доски по пользователям
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param date_from query string false "Дата начала периода (RFC3339 или YYYY-MM-DD)"
// @Param date_to query string false "Дата конца периода (RFC3339 или YYYY-MM-DD)"
// @Success 200 {object} domain.BoardTasksByUserResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/tasks-by-user [get]
func (h *KanbanHandler) GetBoardTasksByUser(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_from"})
		return
	}
	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_to"})
		return
	}

	userID := middleware.MustGetUserID(c)
	resp, err := h.svc.GetBoardTasksByUser(c.Request.Context(), userID, boardID, dateFrom, dateTo)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// GetAssistantBoardAnalysis godoc
// @Summary Lightweight-анализ сотрудников по доске
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param date_from query string false "Дата начала периода (RFC3339 или YYYY-MM-DD)"
// @Param date_to query string false "Дата конца периода (RFC3339 или YYYY-MM-DD)"
// @Success 200 {object} domain.AssistantBoardTasksResponse
// @Router /kanban/assistant/boards/{id}/analysis [get]
func (h *KanbanHandler) GetAssistantBoardAnalysis(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_from"})
		return
	}
	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_to"})
		return
	}

	userID := middleware.MustGetUserID(c)
	resp, err := h.svc.GetAssistantBoardAnalysis(c.Request.Context(), userID, boardID, nil, dateFrom, dateTo)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// GetAssistantBoardUserTasks godoc
// @Summary Lightweight-задачи конкретного сотрудника по доске
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param userId path int true "ID пользователя"
// @Param date_from query string false "Дата начала периода (RFC3339 или YYYY-MM-DD)"
// @Param date_to query string false "Дата конца периода (RFC3339 или YYYY-MM-DD)"
// @Success 200 {object} domain.AssistantBoardTasksResponse
// @Router /kanban/assistant/boards/{id}/users/{userId}/tasks [get]
func (h *KanbanHandler) GetAssistantBoardUserTasks(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	targetUserID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID пользователя"})
		return
	}

	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_from"})
		return
	}
	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_to"})
		return
	}

	userID := middleware.MustGetUserID(c)
	resp, err := h.svc.GetAssistantBoardAnalysis(c.Request.Context(), userID, boardID, &targetUserID, dateFrom, dateTo)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// GetAssistantBoardMyTasks godoc
// @Summary Lightweight-мои задачи по доске
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param date_from query string false "Дата начала периода (RFC3339 или YYYY-MM-DD)"
// @Param date_to query string false "Дата конца периода (RFC3339 или YYYY-MM-DD)"
// @Success 200 {object} domain.AssistantBoardTasksResponse
// @Router /kanban/assistant/boards/{id}/my-tasks [get]
func (h *KanbanHandler) GetAssistantBoardMyTasks(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_from"})
		return
	}
	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_to"})
		return
	}

	userID := middleware.MustGetUserID(c)
	resp, err := h.svc.GetAssistantBoardMyTasks(c.Request.Context(), userID, boardID, dateFrom, dateTo)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// SetBoardBackgroundPreset godoc
// @Summary Установить preset-цвет доски
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param request body service.SetBoardPresetRequest true "Данные цвета"
// @Success 200 {object} domain.BoardBackground
// @Router /kanban/boards/{id}/background/preset [put]
func (h *KanbanHandler) SetBoardBackgroundPreset(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	var req service.SetBoardPresetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	bg, err := h.svc.SetBoardBackgroundPreset(c.Request.Context(), userID, boardID, req.PresetID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, bg)
}

// UploadBoardBackgroundImage godoc
// @Summary Загрузить кастомное фото доски
// @Tags kanban
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param file formData file true "Файл для фона"
// @Success 200 {object} domain.BoardBackground
// @Router /kanban/boards/{id}/background/image [post]
func (h *KanbanHandler) UploadBoardBackgroundImage(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Файл не найден (поле 'file')"})
		return
	}

	if fileHeader.Size > 10*1024*1024 {
		respondJSON(c, http.StatusRequestEntityTooLarge, gin.H{"error": "Размер файла не должен превышать 10 MB"})
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Недопустимый формат файла. Допускаются только JPG, PNG и WEBP"})
		return
	}

	fileReader, err := fileHeader.Open()
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Не удалось прочитать файл"})
		return
	}
	defer fileReader.Close()
	userID := middleware.MustGetUserID(c)

	bg, err := h.svc.UploadBoardBackgroundImage(c.Request.Context(), userID, boardID, filepath.Base(fileHeader.Filename), fileHeader.Size, contentType, fileReader)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, bg)
}

// RemoveBoardBackgroundImage godoc
// @Summary Удалить кастомное фото доски
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Success 200 {object} domain.BoardBackground
// @Router /kanban/boards/{id}/background/image [delete]
func (h *KanbanHandler) RemoveBoardBackgroundImage(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	userID := middleware.MustGetUserID(c)
	bg, err := h.svc.RemoveBoardBackgroundImage(c.Request.Context(), userID, boardID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, bg)
}

// GetBoardBackgroundImage godoc
// @Summary Скачать кастомное фото доски
// @Tags kanban
// @Security BearerAuth
// @Produce octet-stream
// @Param id path string true "ID доски (UUID)"
// @Router /kanban/boards/{id}/background/image/download [get]
func (h *KanbanHandler) GetBoardBackgroundImage(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	userID := middleware.MustGetUserID(c)
	content, err := h.svc.GetBoardBackgroundImage(c.Request.Context(), userID, boardID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	defer content.Stream.Close()

	c.DataFromReader(http.StatusOK, content.Size, content.ContentType, content.Stream, nil)
}

// AddMembers godoc
// @Summary Добавить участников к доске
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param request body service.AddMembersRequest true "Список участников"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/members [post]
func (h *KanbanHandler) AddMembers(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	var req service.AddMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.AddMembers(c.Request.Context(), userID, id, req); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "added", "count": len(req.Members)})
}

// RemoveMember godoc
// @Summary Удалить участника с доски
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param userId path int true "ID пользователя"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/members/{userId} [delete]
func (h *KanbanHandler) RemoveMember(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	targetUserID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID пользователя"})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.RemoveMember(c.Request.Context(), userID, boardID, targetUserID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "removed"})
}

// UpdateMemberRole godoc
// @Summary Изменить роль участника
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param userId path int true "ID пользователя"
// @Param request body service.UpdateMemberRoleRequest true "Новая роль"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/members/{userId}/role [put]
func (h *KanbanHandler) UpdateMemberRole(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	targetUserID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID пользователя"})
		return
	}

	var req service.UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.UpdateMemberRole(c.Request.Context(), userID, boardID, targetUserID, req.Role); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "updated"})
}

// UpdateMemberTeamRole godoc
// @Summary Изменить роль участника в команде
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param userId path int true "ID пользователя"
// @Param request body service.UpdateMemberTeamRoleRequest true "Новая роль в команде"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/members/{userId}/team-role [put]
func (h *KanbanHandler) UpdateMemberTeamRole(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	targetUserID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID пользователя"})
		return
	}

	var req service.UpdateMemberTeamRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.UpdateMemberTeamRole(c.Request.Context(), userID, boardID, targetUserID, req.TeamRole); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "updated"})
}

// CreateSprint godoc
// @Summary Создать новый спринт
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param request body service.CreateSprintRequest true "Данные спринта"
// @Success 201 {object} domain.Sprint
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/sprints [post]
func (h *KanbanHandler) CreateSprint(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	var req service.CreateSprintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	sprint, err := h.svc.CreateSprint(c.Request.Context(), userID, id, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusCreated, sprint)
}

// CreateGoal godoc
// @Summary Создать новую цель внутри доски
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param request body service.CreateGoalRequest true "Данные цели"
// @Success 201 {object} domain.Goal
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/goals [post]
func (h *KanbanHandler) CreateGoal(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	var req service.CreateGoalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	goal, err := h.svc.CreateGoal(c.Request.Context(), userID, id, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusCreated, goal)
}

func (h *KanbanHandler) UpdateGoal(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	goalID, err := uuid.Parse(c.Param("goalId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID цели"})
		return
	}
	var req service.UpdateGoalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	goal, err := h.svc.UpdateGoal(c.Request.Context(), userID, boardID, goalID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, goal)
}

func (h *KanbanHandler) DeleteGoal(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	goalID, err := uuid.Parse(c.Param("goalId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID цели"})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.DeleteGoal(c.Request.Context(), userID, boardID, goalID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListMyGoals godoc
// @Summary Получить все цели пользователя по всем доступным доскам
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.GoalBoardItem
// @Failure 500 {object} map[string]any
// @Router /kanban/goals/my [get]
func (h *KanbanHandler) ListMyGoals(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	goals, err := h.svc.ListMyGoals(c.Request.Context(), userID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, goals)
}

// CreateProject godoc
// @Summary Создать новый проект внутри доски
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param request body service.CreateProjectRequest true "Данные проекта"
// @Success 201 {object} domain.Project
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/projects [post]
func (h *KanbanHandler) CreateProject(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	var req service.CreateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	project, err := h.svc.CreateProject(c.Request.Context(), userID, id, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusCreated, project)
}

func (h *KanbanHandler) UpdateProject(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID проекта"})
		return
	}
	var req service.UpdateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	project, err := h.svc.UpdateProject(c.Request.Context(), userID, boardID, projectID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, project)
}

func (h *KanbanHandler) DeleteProject(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID проекта"})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.DeleteProject(c.Request.Context(), userID, boardID, projectID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *KanbanHandler) UpdateSprint(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	sprintID, err := uuid.Parse(c.Param("sprintId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID спринта"})
		return
	}
	var req service.UpdateSprintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	sprint, err := h.svc.UpdateSprint(c.Request.Context(), userID, boardID, sprintID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, sprint)
}

// DeleteSprint godoc
// @Summary Удалить спринт
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param sprintId path string true "ID спринта (UUID)"
// @Success 204
// @Failure 400 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/sprints/{sprintId} [delete]
func (h *KanbanHandler) DeleteSprint(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	sprintID, err := uuid.Parse(c.Param("sprintId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID спринта"})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.DeleteSprint(c.Request.Context(), userID, boardID, sprintID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	c.Status(http.StatusNoContent)
}

// AddAssistantBoardMember godoc
// @Summary Lightweight-добавление участника в доску
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param request body service.AssistantBoardMemberRequest true "Участник доски"
// @Success 200 {object} map[string]string
// @Router /kanban/assistant/boards/{id}/members [post]
func (h *KanbanHandler) AddAssistantBoardMember(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	var req service.AssistantBoardMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.AddAssistantBoardMember(c.Request.Context(), userID, boardID, req); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "added"})
}

// RemoveAssistantBoardMember godoc
// @Summary Lightweight-удаление участника из доски
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param userId path int true "ID пользователя"
// @Success 200 {object} map[string]string
// @Router /kanban/assistant/boards/{id}/members/{userId} [delete]
func (h *KanbanHandler) RemoveAssistantBoardMember(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	targetUserID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID пользователя"})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.RemoveMember(c.Request.Context(), userID, boardID, targetUserID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "removed"})
}

// ListUserSprints godoc
// @Summary Получить список всех спринтов пользователя (со всех досок)
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.Sprint
// @Failure 500 {object} map[string]any
// @Router /kanban/sprints [get]
func (h *KanbanHandler) ListUserSprints(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	sprints, err := h.svc.ListUserSprints(c.Request.Context(), userID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, sprints)
}

// CreateTask godoc
// @Summary Создать новую задачу
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body service.CreateTaskRequest true "Данные задачи"
// @Success 201 {object} domain.Task
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks [post]
func (h *KanbanHandler) CreateTask(c *gin.Context) {
	var req service.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	task, err := h.svc.CreateTask(c.Request.Context(), userID, req)
	if err != nil {
		fmt.Printf("❌ ОШИБКА СОЗДАНИЯ ЗАДАЧИ: %v\n", err)
		h.logger.Error("failed to create task", zap.Error(err))

		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusCreated, task)
}

// CreateAssistantTask godoc
// @Summary Lightweight-создание задачи
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body service.AssistantCreateTaskRequest true "Данные задачи"
// @Success 201 {object} domain.AssistantTask
// @Router /kanban/assistant/tasks [post]
func (h *KanbanHandler) CreateAssistantTask(c *gin.Context) {
	var req service.AssistantCreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}

	userID := middleware.MustGetUserID(c)
	task, err := h.svc.CreateAssistantTask(c.Request.Context(), userID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusCreated, task)
}

// MoveTask godoc
// @Summary Переместить задачу в другую колонку
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body service.MoveTaskRequest true "Данные перемещения"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/move [put]
func (h *KanbanHandler) MoveTask(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	var req service.MoveTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.MoveTask(c.Request.Context(), userID, taskID, req.ToColumnID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "moved"})
}

// UpdateTask godoc
// @Summary Обновить задачу
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body domain.UpdateTaskPatch true "Данные для обновления"
// @Success 200 {object} domain.Task
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id} [put]
func (h *KanbanHandler) UpdateTask(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	var req domain.UpdateTaskPatch
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	updated, err := h.svc.UpdateTask(c.Request.Context(), userID, taskID, &req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, updated)
}

// UpdateTaskStatus godoc
// @Summary Обновить статус задачи (подзадачи)
// @Description Меняет статус на один из: К ВЫПОЛНЕНИЮ, В РАБОТЕ, В ПРОЦЕССЕ ПРОВЕРКИ, ГОТОВО
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body service.UpdateTaskStatusRequest true "Новый статус"
// @Success 200 {object} domain.Task
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/status [put]
func (h *KanbanHandler) UpdateTaskStatus(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	var req service.UpdateTaskStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	updated, err := h.svc.UpdateTaskStatus(c.Request.Context(), userID, taskID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, updated)
}

// AssignAssistantTask godoc
// @Summary Назначить или изменить исполнителя задачи
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body service.AssistantAssignTaskRequest true "Исполнитель"
// @Success 200 {object} domain.AssistantTask
// @Router /kanban/assistant/tasks/{id}/assignee [put]
func (h *KanbanHandler) AssignAssistantTask(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	var req service.AssistantAssignTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	updated, err := h.svc.AssignAssistantTask(c.Request.Context(), userID, taskID, req.AssigneeID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, updated)
}

// UpdateAssistantTaskStatus godoc
// @Summary Lightweight-смена статуса задачи
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body service.UpdateTaskStatusRequest true "Новый статус"
// @Success 200 {object} domain.AssistantTask
// @Router /kanban/assistant/tasks/{id}/status [put]
func (h *KanbanHandler) UpdateAssistantTaskStatus(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	var req service.UpdateTaskStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	updated, err := h.svc.UpdateAssistantTaskStatus(c.Request.Context(), userID, taskID, req.Status)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, updated)
}

// DeleteTask godoc
// @Summary Удалить задачу
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id} [delete]
func (h *KanbanHandler) DeleteTask(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.DeleteTask(c.Request.Context(), userID, taskID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// AttachFile godoc
// @Summary Прикрепить файл к задаче
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body service.AttachFileRequest true "ID файла"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/attachments [post]
func (h *KanbanHandler) AttachFile(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	var req service.AttachFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.AttachFile(c.Request.Context(), userID, taskID, req.FileID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "attached"})
}

// CreateColumn godoc
// @Summary Создать колонку
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body service.CreateColumnRequest true "Данные колонки"
// @Success 201 {object} domain.KanbanColumn
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/columns [post]
func (h *KanbanHandler) CreateColumn(c *gin.Context) {
	var req service.CreateColumnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	col, err := h.svc.CreateColumn(c.Request.Context(), userID, req)
	if err != nil {
		fmt.Printf("❌ CreateColumn Error: %v\n", err)
		h.logger.Error("failed to create column", zap.Error(err))

		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusCreated, col)
}

// DeleteColumn godoc
// @Summary Удалить колонку
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID колонки (UUID)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/columns/{id} [delete]
func (h *KanbanHandler) DeleteColumn(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID колонки"})
		return
	}
	userID := middleware.MustGetUserID(c)

	if err := h.svc.DeleteColumn(c.Request.Context(), userID, id); err != nil {
		fmt.Printf("❌ DeleteColumn Error: %v\n", err)
		h.logger.Error("failed to delete column", zap.Error(err))

		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// SearchTasks godoc
// @Summary Расширенный поиск задач (фильтры как в Jira)
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param q query string false "Текст для поиска (название/описание)"
// @Param priorities query []string false "Список приоритетов" collectionFormat="multi"
// @Param author_ids query []int false "Авторы (created_by)" collectionFormat="multi"
// @Param statuses query []string false "Статусы или ключи/названия колонок" collectionFormat="multi"
// @Param status query string false "Один статус"
// @Param limit query int false "Размер страницы: 1–200, по умолчанию 100"
// @Param offset query int false "Смещение, по умолчанию 0"
// @Param assignee_ids query []int false "Список ID исполнителей" collectionFormat="multi"
// @Param column_ids query []string false "Список ID колонок (UUID)" collectionFormat="multi"
// @Param board_id query string false "ID доски (UUID)"
// @Param date_from query string false "Дата 'С' (RFC3339)"
// @Param date_to query string false "Дата 'По' (RFC3339)"
// @Router /kanban/tasks/search [get]
func (h *KanbanHandler) SearchTasks(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req service.SearchTasksRequest
	// Gin automatically parses arrays like ?priorities=A&priorities=B into slice fields
	if err := c.ShouldBindQuery(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверные параметры запроса", "details": err.Error()})
		return
	}

	tasks, err := h.svc.SearchTasks(c.Request.Context(), userID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, tasks)
}

// GetMyTodoTasks godoc
// @Summary Мои задачи (TODO)
// @Description Получает все задачи пользователя, которые находятся в колонке TODO
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.Task
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/my-todo [get]
func (h *KanbanHandler) GetMyTodoTasks(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	tasks, err := h.svc.GetMyTodoTasks(c.Request.Context(), userID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, tasks)
}

// GetMyTasksByStatus godoc
// @Summary Мои задачи по статусам
// @Description Возвращает все задачи пользователя из всех доступных досок по статусам TODO, IN_PROGRESS и TEST.
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.MyTasksByStatusResponse
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/my-by-status [get]
func (h *KanbanHandler) GetMyTasksByStatus(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	resp, err := h.svc.GetMyTasksByStatus(c.Request.Context(), userID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetMyTaskBoardsSummary godoc
// @Summary Мои задачи по доскам
// @Description Возвращает агрегированную сводку по задачам пользователя без привязки к одной доске: на какой доске сколько задач и их распределение по статусам.
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param date_from query string false "Дата начала периода (RFC3339 или YYYY-MM-DD)"
// @Param date_to query string false "Дата конца периода (RFC3339 или YYYY-MM-DD)"
// @Success 200 {object} domain.UserTaskBoardsSummaryResponse
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/my-summary-by-board [get]
func (h *KanbanHandler) GetMyTaskBoardsSummary(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_from"})
		return
	}
	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_to"})
		return
	}
	resp, err := h.svc.GetMyTaskBoardsSummaryFiltered(c.Request.Context(), userID, dateFrom, dateTo)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// GetTask godoc
// @Summary Получить детали задачи
// @Description Возвращает полную информацию о задаче, включая исполнителей и вложения
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID задачи (UUID) или номер задачи (например AB-1)"
// @Success 200 {object} domain.Task
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id} [get]
func (h *KanbanHandler) GetTask(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	taskRef := c.Param("id")

	var task *domain.Task
	var err error
	if taskID, parseErr := uuid.Parse(taskRef); parseErr == nil {
		task, err = h.svc.GetTask(c.Request.Context(), userID, taskID)
	} else {
		task, err = h.svc.GetTaskByKey(c.Request.Context(), userID, taskRef)
	}
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, task)
}

// GetBoardStats godoc
// @Summary Получить статистику доски AlemBoard
// @Description Возвращает один JSON с отдельными блоками статистики по задачам, колонкам, срокам, исполнителям, активности и worklog.
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID доски (UUID)"
// @Param date_from query string false "Дата начала периода (RFC3339 или YYYY-MM-DD)"
// @Param date_to query string false "Дата конца периода (RFC3339 или YYYY-MM-DD)"
// @Success 200 {object} domain.BoardStatsResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/boards/{id}/stats [get]
func (h *KanbanHandler) GetBoardStats(c *gin.Context) {
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}

	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_from"})
		return
	}
	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_to"})
		return
	}

	userID := middleware.MustGetUserID(c)
	stats, err := h.svc.GetBoardStats(c.Request.Context(), userID, boardID, dateFrom, dateTo)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, stats)
}

func parseOptionalKanbanStatsTime(value string, endOfDay bool) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	if endOfDay {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return &parsed, nil
}

// UploadTaskFile godoc
// @Summary Загрузить файл в задачу
// @Description Загружает файл в S3/MinIO и прикрепляет его к задаче
// @Tags kanban
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param file formData file true "Файл для загрузки"
// @Success 201 {object} domain.TaskAttachment
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/files [post]
func (h *KanbanHandler) UploadTaskFile(c *gin.Context) {
	// 1. Парсим ID задачи
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	// 2. Получаем файл из form-data
	fileHeader, err := c.FormFile("file")
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Файл не найден (поле 'file')"})
		return
	}

	// 3. Открываем поток чтения файла (multipart)
	fileReader, err := fileHeader.Open()
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Не удалось прочитать файл"})
		return
	}
	defer fileReader.Close()

	userID := middleware.MustGetUserID(c)

	// Определяем Content-Type (если не пришел, ставим дефолт)
	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// 4. Вызываем Service напрямую (передаем аргументы, а не структуру)
	attachment, err := h.svc.UploadTaskFile(
		c.Request.Context(),
		taskID,
		userID,
		filepath.Base(fileHeader.Filename), // Очищаем имя от путей
		fileHeader.Size,
		contentType,
		fileReader,
	)

	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusCreated, attachment)
}

// UploadFile godoc
// @Summary Загрузить файл для Kanban (без привязки к задаче)
// @Tags kanban
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "Файл для загрузки"
// @Success 201 {object} domain.DriveFile
// @Failure 401 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/upload [post]
func (h *KanbanHandler) UploadFile(c *gin.Context) {
	// 1. Получаем файл из запроса
	fileHeader, err := c.FormFile("file")
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Файл не найден в запросе"})
		return
	}

	// 2. Открываем файл
	fileReader, err := fileHeader.Open()
	if err != nil {
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "Не удалось прочитать файл"})
		return
	}
	defer fileReader.Close()

	// 3. Определяем Content-Type
	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	userID := middleware.MustGetUserID(c)

	// 4. Вызываем Service
	driveFile, err := h.svc.UploadFile(
		c.Request.Context(),
		userID,
		filepath.Base(fileHeader.Filename),
		fileHeader.Size,
		contentType,
		fileReader,
	)

	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusCreated, driveFile)
}

// DownloadTaskFile godoc
// @Summary Скачать файл задачи
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "ID задачи (UUID)"
// @Param fileId path string true "ID файла (UUID)"
// @Success 200 {file} file "Поток файла"
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/files/{fileId}/download [get]
func (h *KanbanHandler) DownloadTaskFile(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	fileID, err := uuid.Parse(c.Param("fileId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID файла"})
		return
	}

	userID := middleware.MustGetUserID(c)

	// Получаем контент (поток) из сервиса
	content, err := h.svc.GetTaskFileContent(c.Request.Context(), userID, taskID, fileID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	// Важно закрыть поток после отправки, но Gin сделает это сам, если мы используем DataFromReader правильно,
	// однако для надежности закрываем, если Gin вдруг упадет до чтения.
	defer content.Stream.Close()

	// Устанавливаем заголовки для браузера
	extraHeaders := map[string]string{
		"Content-Disposition": mime.FormatMediaType("attachment", map[string]string{"filename": content.Filename}),
	}

	// Используем встроенный метод Gin для стриминга данных из Reader
	c.DataFromReader(
		http.StatusOK,
		content.Size,
		content.ContentType,
		content.Stream,
		extraHeaders,
	)
}

// DeleteTaskFile godoc
// @Summary Удалить файл из задачи
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "ID задачи (UUID)"
// @Param fileId path string true "ID файла (UUID)"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]any
// @Router /kanban/tasks/{id}/files/{fileId} [delete]
func (h *KanbanHandler) DeleteTaskFile(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	fileID, err := uuid.Parse(c.Param("fileId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID файла"})
		return
	}

	userID := middleware.MustGetUserID(c)

	if err := h.svc.DeleteTaskFile(c.Request.Context(), userID, taskID, fileID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// AddWorklog godoc
// @Summary Добавить ворклог к задаче
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body domain.TaskWorklog true "Данные ворклога"
// @Success 201 {object} domain.TaskWorklog
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/worklogs [post]
func (h *KanbanHandler) AddWorklog(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	var req domain.TaskWorklog
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	err = h.svc.AddWorklog(c.Request.Context(), userID, taskID, &req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusCreated, req)
}

// ListWorklogs godoc
// @Summary Список ворклогов задачи
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Success 200 {array} domain.TaskWorklog
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/worklogs [get]
func (h *KanbanHandler) ListWorklogs(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	userID := middleware.MustGetUserID(c)
	logs, err := h.svc.ListWorklogs(c.Request.Context(), userID, taskID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, logs)
}

// UpdateColumn godoc
// @Summary Обновить колонку (название)
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID колонки (UUID)"
// @Param request body service.UpdateColumnRequest true "Данные обновления"
// @Success 200 {object} domain.KanbanColumn
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/columns/{id} [put]
func (h *KanbanHandler) UpdateColumn(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID колонки"})
		return
	}

	var req service.UpdateColumnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	updated, err := h.svc.UpdateColumn(c.Request.Context(), userID, id, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, updated)
}

// MoveColumn godoc
// @Summary Переместить колонку (изменить порядок)
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID колонки (UUID)"
// @Param request body service.MoveColumnRequest true "Новая позиция"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/columns/{id}/move [put]
func (h *KanbanHandler) MoveColumn(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID колонки"})
		return
	}

	var req service.MoveColumnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.MoveColumn(c.Request.Context(), userID, id, req); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "moved"})
}

// GetTaskHistory godoc
// @Summary Получить историю изменений задачи (как в Jira)
// @Description Возвращает полную историю всех изменений задачи: создание, обновления, перемещения, назначения, удаления
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Success 200 {array} domain.TaskHistoryResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/history [get]
func (h *KanbanHandler) GetTaskHistory(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	userID := middleware.MustGetUserID(c)
	history, err := h.svc.GetTaskHistory(c.Request.Context(), userID, taskID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, history)
}

// CreateComment godoc
// @Summary Добавить комментарий к задаче
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param request body service.CreateCommentRequest true "Текст комментария"
// @Success 201 {object} domain.TaskComment
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/comments [post]
func (h *KanbanHandler) CreateComment(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	var req service.CreateCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	comment, err := h.svc.AddComment(c.Request.Context(), userID, taskID, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusCreated, comment)
}

// ListComments godoc
// @Summary Получить все комментарии задачи
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Success 200 {array} domain.TaskComment
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/comments [get]
func (h *KanbanHandler) ListComments(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	userID := middleware.MustGetUserID(c)
	comments, err := h.svc.ListComments(c.Request.Context(), userID, taskID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, comments)
}

// UpdateComment godoc
// @Summary Обновить текст комментария
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "ID комментария (UUID)"
// @Param request body service.UpdateCommentRequest true "Новый текст"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/comments/{id} [put]
func (h *KanbanHandler) UpdateComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID комментария"})
		return
	}

	var req service.UpdateCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса"})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.UpdateComment(c.Request.Context(), userID, commentID, req); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "updated"})
}

// DeleteComment godoc
// @Summary Удалить комментарий
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "ID комментария (UUID)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/comments/{id} [delete]
func (h *KanbanHandler) DeleteComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID комментария"})
		return
	}

	userID := middleware.MustGetUserID(c)
	if err := h.svc.DeleteComment(c.Request.Context(), userID, commentID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// UploadCommentPhoto godoc
// @Summary Загрузить фото и оставить комментарий
// @Tags kanban
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "ID задачи (UUID)"
// @Param file formData file false "Фото для комментария"
// @Param content formData string false "Текст комментария"
// @Success 201 {object} domain.TaskComment
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/tasks/{id}/comments/photo [post]
func (h *KanbanHandler) UploadCommentPhoto(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}

	content := c.PostForm("content")
	fileHeader, fileErr := c.FormFile("file")

	if content == "" && fileErr != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Необходимо указать текст комментария или прикрепить файл"})
		return
	}

	userID := middleware.MustGetUserID(c)

	// Если файла нет, просто создаем текстовый комментарий
	if fileErr != nil {
		comment, err := h.svc.AddComment(c.Request.Context(), userID, taskID, service.CreateCommentRequest{
			Content: content,
		})
		if err != nil {
			app := apperrors.ToAppError(err)
			respondJSON(c, app.StatusCode, app)
			return
		}
		respondJSON(c, http.StatusCreated, comment)
		return
	}

	// Если есть файл, обрабатываем его
	fileReader, err := fileHeader.Open()
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Не удалось открыть файл"})
		return
	}
	defer fileReader.Close()

	contentType := fileHeader.Header.Get("Content-Type")

	comment, err := h.svc.AddCommentWithPhoto(
		c.Request.Context(),
		userID,
		taskID,
		content,
		filepath.Base(fileHeader.Filename),
		fileHeader.Size,
		contentType,
		fileReader,
	)

	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusCreated, comment)
}

// CreateLabel godoc
// @Summary Create label for board
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Board ID"
// @Param request body service.CreateLabelRequest true "Label info"
// @Success 201 {object} domain.KanbanLabel
// @Failure 400 {object} map[string]any
// @Router /kanban/boards/{id}/labels [post]
func (h *KanbanHandler) CreateLabel(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	var req service.CreateLabelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	label, err := h.svc.CreateLabel(c.Request.Context(), userID, id, req)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusCreated, label)
}

// ListLabels godoc
// @Summary List labels for board
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "Board ID"
// @Success 200 {array} domain.KanbanLabel
// @Router /kanban/boards/{id}/labels [get]
func (h *KanbanHandler) ListLabels(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID доски"})
		return
	}
	userID := middleware.MustGetUserID(c)
	labels, err := h.svc.ListLabels(c.Request.Context(), userID, id)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, labels)
}

// DeleteLabel godoc
// @Summary Delete label
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "Label ID"
// @Success 200 {object} map[string]string
// @Router /kanban/labels/{id} [delete]
func (h *KanbanHandler) DeleteLabel(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID метки"})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.DeleteLabel(c.Request.Context(), userID, id); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// AddLabelToTask godoc
// @Summary Add label to task
// @Tags kanban
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Task ID"
// @Param request body service.AddLabelToTaskRequest true "Label ID"
// @Success 200 {object} map[string]string
// @Router /kanban/tasks/{id}/labels [post]
func (h *KanbanHandler) AddLabelToTask(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	var req service.AddLabelToTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат запроса: " + err.Error()})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.AddLabelToTask(c.Request.Context(), userID, id, req); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "added"})
}

// RemoveLabelFromTask godoc
// @Summary Remove label from task
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param id path string true "Task ID"
// @Param labelId path string true "Label ID"
// @Success 200 {object} map[string]string
// @Router /kanban/tasks/{id}/labels/{labelId} [delete]
func (h *KanbanHandler) RemoveLabelFromTask(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID задачи"})
		return
	}
	labelID, err := uuid.Parse(c.Param("labelId"))
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный ID метки"})
		return
	}
	userID := middleware.MustGetUserID(c)
	if err := h.svc.RemoveLabelFromTask(c.Request.Context(), userID, taskID, labelID); err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "removed"})
}
