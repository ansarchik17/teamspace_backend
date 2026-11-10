package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func securityAuditFilter(c *gin.Context, export bool) (domain.SecurityAuditFilter, error) {
	f := domain.SecurityAuditFilter{Action: c.Query("action"), IP: c.Query("ip"), Outcome: c.Query("outcome")}
	var err error
	f.Limit, err = strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil {
		return f, err
	}
	if export {
		f.Limit = 10000
	} else if f.Limit > 100 {
		return f, fmt.Errorf("invalid limit")
	}
	f.BeforeID, err = strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil {
		return f, err
	}
	f.MaxID, err = strconv.ParseInt(c.DefaultQuery("snapshot_id", "0"), 10, 64)
	if err != nil {
		return f, err
	}
	if raw := c.Query("actor_id"); raw != "" {
		id, e := strconv.ParseInt(raw, 10, 64)
		if e != nil {
			return f, e
		}
		f.ActorID = &id
	}
	for key, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if raw := c.Query(key); raw != "" {
			v, e := time.Parse(time.RFC3339, raw)
			if e != nil {
				return f, e
			}
			*dst = &v
		}
	}
	if !f.Valid() {
		return f, fmt.Errorf("invalid audit filter")
	}
	return f, nil
}
func (h *OrganizationHandler) RecordSecurityEvent(ctx context.Context, event domain.SecurityAuditEvent) error {
	return h.service.RecordSecurityEvent(ctx, event)
}
func (h *OrganizationHandler) SecurityAudit(c *gin.Context)       { h.securityAudit(c, false) }
func (h *OrganizationHandler) ExportSecurityAudit(c *gin.Context) { h.securityAudit(c, true) }
func (h *OrganizationHandler) securityAudit(c *gin.Context, export bool) {
	f, err := securityAuditFilter(c, export)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid audit filter"))
		return
	}
	format := c.DefaultQuery("format", "csv")
	if export && format != "csv" && format != "json" {
		h.respondError(c, apperrors.Validation("invalid export format"))
		return
	}
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	platform := strings.HasPrefix(c.FullPath(), "/api/v1/platform/")
	page, err := h.service.SecurityAudit(c.Request.Context(), middleware.MustGetUserID(c), org, platform, f)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	if !export {
		respondJSON(c, http.StatusOK, page)
		return
	}
	if page.NextBeforeID != nil {
		h.respondError(c, apperrors.Conflict("audit export exceeds limit; narrow the filters"))
		return
	}
	// Build the bounded export before writing any response bytes.
	if format == "json" {
		data, err := json.Marshal(page)
		if err != nil {
			h.respondError(c, apperrors.InternalWithErr("audit export failed", err))
			return
		}
		c.Header("Content-Disposition", `attachment; filename="security-audit.json"`)
		c.Data(http.StatusOK, "application/json; charset=utf-8", data)
		return
	}
	var buf strings.Builder
	buf.WriteString("\ufeff")
	writer := csv.NewWriter(&buf)
	_ = writer.Write([]string{"id", "created_at", "organization_id", "actor_id", "action", "outcome", "ip", "request_id", "method", "route", "status", "administration_audit_id", "object_type", "object_id"})
	for _, e := range page.Items {
		org, actor, ip, admin := "", "", "", ""
		if e.OrganizationID != nil {
			org = e.OrganizationID.String()
		}
		if e.ActorID != nil {
			actor = strconv.FormatInt(*e.ActorID, 10)
		}
		if e.IP != nil {
			ip = *e.IP
		}
		if e.AdministrationAuditID != nil {
			admin = strconv.FormatInt(*e.AdministrationAuditID, 10)
		}
		cells := []string{strconv.FormatInt(e.ID, 10), e.CreatedAt.UTC().Format(time.RFC3339Nano), org, actor, e.Action, e.Outcome, ip, e.RequestID, e.Method, e.Route, strconv.Itoa(e.Status), admin, e.ObjectType, e.ObjectID}
		for i, v := range cells {
			cells[i] = securityCSVCell(v)
		}
		_ = writer.Write(cells)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		h.respondError(c, apperrors.InternalWithErr("audit export failed", err))
		return
	}
	c.Header("Content-Disposition", `attachment; filename="security-audit.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(buf.String()))
}
func securityCSVCell(v string) string {
	if strings.ContainsAny(strings.TrimLeft(v, " "), "\r\n") {
		return "'" + v
	}
	if len(strings.TrimLeft(v, " ")) > 0 && strings.ContainsRune("=+-@\t", rune(strings.TrimLeft(v, " ")[0])) {
		return "'" + v
	}
	return v
}
