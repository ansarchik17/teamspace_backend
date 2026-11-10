package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func (h *AuthHandler) SetInvitationService(s *auth.InvitationService) { h.invitations = s }

func (h *AuthHandler) invitationReady(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	if h.invitations == nil {
		h.respondError(c, apperrors.New("INVITATIONS_UNAVAILABLE", "Приглашения временно недоступны", 503))
		return false
	}
	return true
}
func (h *AuthHandler) invitationBody(c *gin.Context, out any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	err := d.Decode(out)
	if err == nil {
		var extra any
		if d.Decode(&extra) != io.EOF {
			err = io.ErrUnexpectedEOF
		}
	}
	if err == nil {
		err = binding.Validator.ValidateStruct(out)
	}
	if err != nil {
		h.respondError(c, apperrors.Validation("Проверьте данные запроса"))
		return false
	}
	return true
}
func (h *AuthHandler) invitationScope(c *gin.Context) (*uuid.UUID, bool) {
	if c.Param("organization_id") == "" {
		return nil, true
	}
	id, err := uuid.Parse(c.Param("organization_id"))
	if err != nil || id == uuid.Nil {
		h.respondError(c, apperrors.Validation("Некорректная организация"))
		return nil, false
	}
	return &id, true
}
func (h *AuthHandler) invitationID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("invitation_id"))
	if err != nil || id == uuid.Nil {
		h.respondError(c, apperrors.Validation("Некорректное приглашение"))
		return id, false
	}
	return id, true
}

// ListInvitations lists personal invitations for company administrators.
// @Summary List organization invitations
// @Tags organization-invitations
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Page size (1-100)" default(25)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} domain.InvitationPage
// @Router /organizations/current/invitations [get]
// @Router /platform/organizations/{organization_id}/invitations [get]
func (h *AuthHandler) ListInvitations(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	org, ok := h.invitationScope(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	out, err := h.invitations.List(c.Request.Context(), middleware.MustGetUserID(c), org, limit, offset)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, 200, out)
}

// CreateInvitation issues and emails a personal invitation.
// @Summary Invite an employee
// @Tags organization-invitations
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateInvitationRequest true "Recipient and optional department"
// @Success 201 {object} auth.IssuedInvitation
// @Router /organizations/current/invitations [post]
// @Router /platform/organizations/{organization_id}/invitations [post]
func (h *AuthHandler) CreateInvitation(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	org, ok := h.invitationScope(c)
	if !ok {
		return
	}
	var req domain.CreateInvitationRequest
	if !h.invitationBody(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	out, err := h.invitations.Issue(ctx, middleware.MustGetUserID(c), org, nil, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, 201, out)
}

// ResendInvitation rotates the link only when delivery succeeds.
// @Summary Resend an invitation
// @Tags organization-invitations
// @Security BearerAuth
// @Produce json
// @Param invitation_id path string true "Invitation UUID"
// @Success 200 {object} auth.IssuedInvitation
// @Router /organizations/current/invitations/{invitation_id}/resend [post]
// @Router /platform/organizations/{organization_id}/invitations/{invitation_id}/resend [post]
func (h *AuthHandler) ResendInvitation(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	org, ok := h.invitationScope(c)
	if !ok {
		return
	}
	id, ok := h.invitationID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	out, err := h.invitations.Issue(ctx, middleware.MustGetUserID(c), org, &id, domain.CreateInvitationRequest{})
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, 200, out)
}

// RevokeInvitation invalidates an unused invitation.
// @Summary Revoke an invitation
// @Tags organization-invitations
// @Security BearerAuth
// @Param invitation_id path string true "Invitation UUID"
// @Success 204
// @Router /organizations/current/invitations/{invitation_id} [delete]
// @Router /platform/organizations/{organization_id}/invitations/{invitation_id} [delete]
func (h *AuthHandler) RevokeInvitation(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	org, ok := h.invitationScope(c)
	if !ok {
		return
	}
	id, ok := h.invitationID(c)
	if !ok {
		return
	}
	if err := h.invitations.Revoke(c.Request.Context(), middleware.MustGetUserID(c), org, id); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(204)
}

type invitationTokenRequest struct {
	Token string `json:"token" binding:"required,max=100"`
}
type invitationAcceptRequest struct {
	Token string `json:"token" binding:"required,max=100"`
	Code  string `json:"code" binding:"required,len=6"`
}

// PreviewInvitation is read-only and does not expose the full recipient email.
// @Summary Preview an invitation
// @Tags organization-invitations
// @Accept json
// @Produce json
// @Param request body invitationTokenRequest true "Invitation secret"
// @Success 200 {object} auth.InvitationPreview
// @Router /auth/invitations/preview [post]
func (h *AuthHandler) PreviewInvitation(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	var req invitationTokenRequest
	if !h.invitationBody(c, &req) {
		return
	}
	out, err := h.invitations.Preview(c.Request.Context(), req.Token)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 200, out)
}

// SendInvitationCode sends an independent email ownership challenge.
// @Summary Send an invitation confirmation code
// @Tags organization-invitations
// @Accept json
// @Produce json
// @Param request body invitationTokenRequest true "Invitation secret"
// @Success 200 {object} map[string]interface{}
// @Router /auth/invitations/code [post]
func (h *AuthHandler) SendInvitationCode(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	var req invitationTokenRequest
	if !h.invitationBody(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := h.invitations.SendCode(ctx, req.Token); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 200, gin.H{"sent": true, "retry_after": 60})
}

// RegisterByInvitation registers a new account after email confirmation.
// @Summary Register by invitation
// @Tags organization-invitations
// @Accept json
// @Produce json
// @Param request body auth.InvitationRegistrationRequest true "Registration and email confirmation"
// @Success 201 {object} domain.InvitationResult
// @Router /auth/invitations/register [post]
func (h *AuthHandler) RegisterByInvitation(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	var req auth.InvitationRegistrationRequest
	if !h.invitationBody(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	out, err := h.invitations.Register(ctx, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 201, out)
}

// AcceptInvitation connects the authenticated account without moving it from another company.
// @Summary Accept an invitation
// @Tags organization-invitations
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body invitationAcceptRequest true "Invitation and confirmation code"
// @Success 200 {object} domain.InvitationResult
// @Router /auth/invitations/accept [post]
func (h *AuthHandler) AcceptInvitation(c *gin.Context) {
	if !h.invitationReady(c) {
		return
	}
	var req invitationAcceptRequest
	if !h.invitationBody(c, &req) {
		return
	}
	out, err := h.invitations.Accept(c.Request.Context(), req.Token, req.Code, middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 200, out)
}
