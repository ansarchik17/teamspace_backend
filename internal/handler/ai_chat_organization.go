package handler

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/repository"
	"net/http"
)

func aiOrganizationBody(c *gin.Context, body any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if err := c.ShouldBindJSON(body); err != nil {
		respondAppError(c, apperrors.Validation("invalid request body"))
		return false
	}
	return true
}
func (h *AlemHandler) PatchChat(c *gin.Context) {
	var body struct {
		Title     *string         `json:"title"`
		Pinned    *bool           `json:"pinned"`
		Archived  *bool           `json:"archived"`
		SectionID json.RawMessage `json:"section_id"`
	}
	if !aiOrganizationBody(c, &body) {
		return
	}
	p := repository.AIChatPatch{Title: body.Title, Pinned: body.Pinned, Archived: body.Archived, SectionSet: len(body.SectionID) > 0}
	if p.SectionSet && !bytes.Equal(bytes.TrimSpace(body.SectionID), []byte("null")) {
		var id string
		if err := json.Unmarshal(body.SectionID, &id); err != nil {
			respondAppError(c, apperrors.Validation("invalid section id"))
			return
		}
		p.SectionID = &id
	}
	result, err := h.alemService.PatchChat(c.Request.Context(), c.GetInt64("user_id"), c.Param("id"), p)
	if err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}
func (h *AlemHandler) ListChatSections(c *gin.Context) {
	data, err := h.alemService.ChatSections(c.Request.Context(), c.GetInt64("user_id"))
	if err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, data)
}
func (h *AlemHandler) CreateChatSection(c *gin.Context) { h.saveChatSection(c, "") }
func (h *AlemHandler) UpdateChatSection(c *gin.Context) { h.saveChatSection(c, c.Param("sectionId")) }
func (h *AlemHandler) saveChatSection(c *gin.Context, id string) {
	var body repository.AIChatSectionInput
	if !aiOrganizationBody(c, &body) {
		return
	}
	data, err := h.alemService.SaveSection(c.Request.Context(), c.GetInt64("user_id"), id, body)
	if err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	respondJSON(c, status, data)
}
func (h *AlemHandler) DeleteChatSection(c *gin.Context) {
	err := h.alemService.DeleteSection(c.Request.Context(), c.GetInt64("user_id"), c.Param("sectionId"))
	if err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}
