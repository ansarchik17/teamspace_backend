package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
)

// Resolve short item URLs through the owning task; authorization is never bypassed.
func (h *KanbanHandler) resolveChecklistTask(c *gin.Context) {
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil || itemID == uuid.Nil {
		respondAbortJSON(c, 400, gin.H{"error": "Неверный ID пункта"})
		return
	}
	taskID, err := h.svc.ChecklistTaskID(c.Request.Context(), middleware.MustGetUserID(c), itemID)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondAbortJSON(c, app.StatusCode, app)
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "id", Value: taskID.String()})
	c.Next()
}

// RegisterChecklistRoutes keeps the frontend URL and the legacy URL equivalent.
func (h *KanbanHandler) RegisterChecklistRoutes(group *gin.RouterGroup) {
	group.PATCH("/checklist-items/:itemId", h.resolveChecklistTask, h.UpdateChecklistItem)
	group.PUT("/checklist-items/:itemId", h.resolveChecklistTask, h.UpdateChecklistItem)
	group.DELETE("/checklist-items/:itemId", h.resolveChecklistTask, h.DeleteChecklistItem)
	for _, path := range []string{"/tasks/:id/checklist-items", "/tasks/:id/checklist"} {
		group.GET(path, h.ListChecklist)
		group.POST(path, h.CreateChecklistItem)
		group.PUT(path+"/:itemId", h.UpdateChecklistItem)
		group.PATCH(path+"/:itemId", h.UpdateChecklistItem)
		group.DELETE(path+"/:itemId", h.DeleteChecklistItem)
	}
}

func checklistIDs(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil || taskID == uuid.Nil {
		respondJSON(c, 400, gin.H{"error": "Неверный ID задачи"})
		return uuid.Nil, uuid.Nil, false
	}
	var itemID uuid.UUID
	if c.Param("itemId") != "" {
		itemID, err = uuid.Parse(c.Param("itemId"))
		if err != nil || itemID == uuid.Nil {
			respondJSON(c, 400, gin.H{"error": "Неверный ID пункта"})
			return uuid.Nil, uuid.Nil, false
		}
	}
	return taskID, itemID, true
}
func checklistResponse(c *gin.Context, status int, result any, err error) {
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, status, result)
}
func (h *KanbanHandler) ListChecklist(c *gin.Context) {
	taskID, _, ok := checklistIDs(c)
	if !ok {
		return
	}
	items, err := h.svc.ListChecklist(c.Request.Context(), middleware.MustGetUserID(c), taskID)
	checklistResponse(c, http.StatusOK, items, err)
}

func (h *KanbanHandler) ListTaskFiles(c *gin.Context) {
	taskID, _, ok := checklistIDs(c)
	if !ok {
		return
	}
	items, err := h.svc.ListTaskFiles(c.Request.Context(), middleware.MustGetUserID(c), taskID)
	if items == nil {
		items = []*domain.TaskAttachment{}
	}
	checklistResponse(c, http.StatusOK, items, err)
}
func (h *KanbanHandler) CreateChecklistItem(c *gin.Context) {
	taskID, _, ok := checklistIDs(c)
	if !ok {
		return
	}
	var req domain.CreateTaskChecklistItem
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, 400, gin.H{"error": "Неверный формат пункта"})
		return
	}
	item, err := h.svc.CreateChecklistItem(c.Request.Context(), middleware.MustGetUserID(c), taskID, req)
	checklistResponse(c, http.StatusCreated, item, err)
}
func (h *KanbanHandler) UpdateChecklistItem(c *gin.Context) {
	taskID, itemID, ok := checklistIDs(c)
	if !ok {
		return
	}
	var req domain.UpdateTaskChecklistItem
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, 400, gin.H{"error": "Неверный формат пункта"})
		return
	}
	item, err := h.svc.UpdateChecklistItem(c.Request.Context(), middleware.MustGetUserID(c), taskID, itemID, req)
	checklistResponse(c, http.StatusOK, item, err)
}
func (h *KanbanHandler) DeleteChecklistItem(c *gin.Context) {
	taskID, itemID, ok := checklistIDs(c)
	if !ok {
		return
	}
	err := h.svc.DeleteChecklistItem(c.Request.Context(), middleware.MustGetUserID(c), taskID, itemID)
	checklistResponse(c, http.StatusOK, gin.H{"status": "deleted"}, err)
}
