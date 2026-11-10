package handler

import (
	"context"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type collaboraDiscovery struct {
	Apps []struct {
		Name    string `xml:"name,attr"`
		Actions []struct {
			Name   string `xml:"name,attr"`
			Ext    string `xml:"ext,attr"`
			URLSrc string `xml:"urlsrc,attr"`
		} `xml:"action"`
	} `xml:"net-zone>app"`
}

type collaboraLaunchPage struct {
	ActionURL              template.URL
	Token                  string
	AccessTokenExpiresAtMs int64
}

// Start every embedded session with a light compact toolbar and a closed sidebar.
// Ignore saved UI state so existing browser preferences do not restore the
// oversized tabbed layout; users can still open the sidebar during a session.
// The compact menubar can wrap the last-save label onto a second row. Reserve
// room for both rows instead of centering them inside Collabora's 34px header.
// Use Collabora's CSS variables, not overrides of its internal DOM selectors.
var collaboraLaunchTemplate = template.Must(template.New("collabora-launch").Parse(`<!doctype html>
<html lang="ru">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Открытие документа</title></head>
<body>
<form id="launch" action="{{.ActionURL}}" method="post" enctype="multipart/form-data">
<input type="hidden" name="access_token" value="{{.Token}}">
<input type="hidden" name="access_token_ttl" value="{{.AccessTokenExpiresAtMs}}">
<input type="hidden" name="ui_defaults" value="UIMode=compact;UITheme=light;TextSidebar=false;SpreadsheetSidebar=false;PresentationSidebar=false;SavedUIState=false">
<input type="hidden" name="css_variables" value="--header-height=64px;--default-font-size=12px;--medium-font-size=14px;--header-font-size=14px">
</form>
<script>document.getElementById('launch').submit()</script>
</body></html>`))

func (h *DriveHandler) CollaboraEditorPage(c *gin.Context) {
	userID := h.getUserID(c)
	if userID <= 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized"))
		return
	}
	fileID, err := uuid.Parse(c.Query("file_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file_id"))
		return
	}
	session, err := h.drive.BuildWOPISession(c.Request.Context(), userID, fileID, c.DefaultQuery("mode", "view"), h.jwtSecret)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	h.serveCollaboraLaunch(c, session)
}

func (h *DriveHandler) serveCollaboraLaunch(c *gin.Context, session *service.WOPISession) {
	collaboraURL := strings.TrimRight(strings.TrimSpace(os.Getenv("COLLABORA_URL")), "/")
	if collaboraURL == "" {
		c.String(http.StatusServiceUnavailable, "Collabora Online is not configured")
		return
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(session.Title)), ".")
	if ext == "" {
		c.String(http.StatusUnsupportedMediaType, "Unsupported office file")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	actionURL, err := collaboraActionURL(ctx, collaboraURL, ext)
	if err != nil {
		c.String(http.StatusBadGateway, "Collabora discovery is unavailable")
		return
	}
	parsedAction, err := url.Parse(actionURL)
	collaboraOrigin, originErr := url.Parse(collaboraURL)
	if err != nil || originErr != nil || parsedAction.Scheme != collaboraOrigin.Scheme || !strings.EqualFold(parsedAction.Host, collaboraOrigin.Host) {
		c.String(http.StatusBadGateway, "Invalid Collabora discovery response")
		return
	}
	h.useCollaboraWorkspaceHost(c, parsedAction, collaboraOrigin)
	// Collabora reads WOPISrc from the cool.html action URL query. The access
	// token, its TTL and UI defaults belong in the multipart form body.
	query := parsedAction.Query()
	query.Set("WOPISrc", session.WOPISrc)
	// In Collabora's browser initializer any non-empty darkTheme query value
	// enables dark mode, including the string "false", and replaces ui_defaults.
	// Set light mode only through UITheme=light in the launch form above.
	query.Del("darkTheme")
	parsedAction.RawQuery = query.Encode()
	setDrivePreviewEmbedding(c, true)
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; form-action "+parsedAction.Scheme+"://"+parsedAction.Host+"; frame-ancestors 'self'")
	c.Header("X-Frame-Options", "SAMEORIGIN")
	err = collaboraLaunchTemplate.Execute(c.Writer, collaboraLaunchPage{
		ActionURL: template.URL(parsedAction.String()), Token: session.AccessToken,
		AccessTokenExpiresAtMs: session.AccessTokenExpiresAtMs,
	})
	if err != nil {
		h.logger.Error("failed to render Collabora launch page", zap.Error(err))
	}
}

// Path-based Collabora is proxied at /office on both the platform and tenant
// hosts. Keep its browser URL on the embedding Workspace host so Collabora's
// frame-ancestors policy allows the parent page. WOPISrc remains canonical.
// Discovery is always fetched from the configured origin and validated before
// rewriting; arbitrary request hosts and separately hosted editors are ignored.
func (h *DriveHandler) useCollaboraWorkspaceHost(c *gin.Context, action, collaboraOrigin *url.URL) {
	workspaceOrigin, err := url.Parse(h.publicURL)
	if err != nil || workspaceOrigin.Host == "" || workspaceOrigin.User != nil ||
		workspaceOrigin.Scheme != collaboraOrigin.Scheme ||
		!strings.EqualFold(workspaceOrigin.Host, collaboraOrigin.Host) ||
		strings.Trim(collaboraOrigin.Path, "/") == "" {
		return
	}
	baseHost := strings.ToLower(workspaceOrigin.Hostname())
	slug, isTenant, valid := middleware.TenantSlugFromHost(c.Request.Host, baseHost)
	if !valid {
		return
	}
	var browserHost string
	if isTenant {
		// Limit the rewrite to a single DNS label under our configured domain.
		for _, ch := range slug {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return
			}
		}
		if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") || len(slug) > 63 {
			return
		}
		browserHost = slug + "." + baseHost
	} else if strings.EqualFold(c.Request.Host, workspaceOrigin.Host) {
		return // Already on the canonical origin.
	} else {
		browserHost = "www." + baseHost
	}
	if port := workspaceOrigin.Port(); port != "" {
		browserHost += ":" + port
	}
	if !strings.EqualFold(c.Request.Host, browserHost) {
		return
	}
	action.Host = browserHost
}

func collaboraActionURL(ctx context.Context, base, ext string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("invalid COLLABORA_URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/hosting/discovery", nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Collabora discovery returned %s", resp.Status)
	}
	var discovery collaboraDiscovery
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&discovery); err != nil {
		return "", err
	}
	for _, app := range discovery.Apps {
		for _, action := range app.Actions {
			if action.Name == "edit" && strings.EqualFold(action.Ext, ext) && action.URLSrc != "" {
				return action.URLSrc, nil
			}
		}
	}
	return "", fmt.Errorf("Collabora does not support .%s", ext)
}

func (h *DriveHandler) WOPICheckFileInfo(c *gin.Context) {
	claims, fileID, ok := h.wopiRequest(c)
	if !ok {
		return
	}
	meta, err := h.drive.GetWOPIFile(c.Request.Context(), *claims, fileID)
	if err != nil {
		h.wopiError(c, err)
		return
	}
	userName, err := h.drive.GetWOPIUserName(c.Request.Context(), *claims)
	if err != nil {
		h.wopiError(c, err)
		return
	}
	canWrite := claims.CanWrite && claims.PublicToken == ""
	respondJSON(c, http.StatusOK, gin.H{
		"BaseFileName": filepath.Base(meta.RealName), "OwnerId": fmt.Sprint(meta.OwnerID),
		"UserId": fmt.Sprint(claims.UserID), "UserFriendlyName": userName,
		"Size": meta.Size, "Version": fmt.Sprint(meta.Version), "LastModifiedTime": meta.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"UserCanWrite": canWrite, "UserCanRename": false, "UserCanNotWriteRelative": true,
		"SupportsLocks": canWrite, "SupportsGetLock": canWrite, "PostMessageOrigin": "",
	})
}

func (h *DriveHandler) WOPIGetFile(c *gin.Context) {
	claims, fileID, ok := h.wopiRequest(c)
	if !ok {
		return
	}
	reader, meta, err := h.drive.OpenWOPIFile(c.Request.Context(), *claims, fileID)
	if err != nil {
		h.wopiError(c, err)
		return
	}
	defer reader.Close()
	c.Header("Content-Type", wopiMime(meta.MimeType, meta.RealName))
	c.Header("Content-Length", fmt.Sprint(meta.Size))
	c.Header("X-WOPI-ItemVersion", fmt.Sprint(meta.Version))
	c.Header("Cache-Control", "no-store")
	_, _ = io.Copy(c.Writer, reader)
}

func (h *DriveHandler) WOPIPutFile(c *gin.Context) {
	claims, fileID, ok := h.wopiRequest(c)
	if !ok {
		return
	}
	if !claims.CanWrite || claims.PublicToken != "" {
		c.Status(http.StatusForbidden)
		return
	}
	if c.GetHeader("X-WOPI-Override") != "PUT" {
		c.Status(http.StatusNotImplemented)
		return
	}
	lock, err := h.drive.GetWOPILock(c.Request.Context(), fileID)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	if lock == "" || lock != c.GetHeader("X-WOPI-Lock") {
		c.Header("X-WOPI-Lock", lock)
		c.Status(http.StatusConflict)
		return
	}
	if c.Request.ContentLength > service.MaxFileSize {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	limited := http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxFileSize+1)
	defer limited.Close()
	tmp, err := os.CreateTemp("", "drive-wopi-*")
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	defer func() { _ = os.Remove(tmp.Name()); _ = tmp.Close() }()
	size, err := io.Copy(tmp, limited)
	if err != nil || size > service.MaxFileSize {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	version, err := h.drive.SaveWOPIFile(c.Request.Context(), *claims, fileID, size, tmp)
	if err != nil {
		h.wopiError(c, err)
		return
	}
	c.Header("X-WOPI-ItemVersion", fmt.Sprint(version))
	c.Status(http.StatusOK)
}

func (h *DriveHandler) WOPILock(c *gin.Context) {
	claims, fileID, ok := h.wopiRequest(c)
	if !ok {
		return
	}
	if !claims.CanWrite || claims.PublicToken != "" {
		c.Status(http.StatusForbidden)
		return
	}
	lock := c.GetHeader("X-WOPI-Lock")
	if lock == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	var current string
	var matched bool
	var err error
	switch c.GetHeader("X-WOPI-Override") {
	case "LOCK":
		current, matched, err = h.drive.AcquireWOPILock(c.Request.Context(), fileID, lock)
	case "REFRESH_LOCK":
		matched, err = h.drive.RefreshWOPILock(c.Request.Context(), fileID, lock)
		if matched {
			current = lock
		} else {
			current, err = h.drive.GetWOPILock(c.Request.Context(), fileID)
		}
	case "UNLOCK":
		matched, err = h.drive.ReleaseWOPILock(c.Request.Context(), fileID, lock)
		if matched {
			current = lock
		} else {
			current, err = h.drive.GetWOPILock(c.Request.Context(), fileID)
		}
	case "GET_LOCK":
		current, err = h.drive.GetWOPILock(c.Request.Context(), fileID)
		matched = err == nil
	case "UNLOCK_AND_RELOCK":
		oldLock := c.GetHeader("X-WOPI-OldLock")
		if oldLock == "" || oldLock == lock {
			c.Status(http.StatusBadRequest)
			return
		}
		current, matched, err = h.drive.ReplaceWOPILock(c.Request.Context(), fileID, oldLock, lock)
	default:
		c.Status(http.StatusNotImplemented)
		return
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	if !matched {
		c.Header("X-WOPI-Lock", current)
		c.Status(http.StatusConflict)
		return
	}
	if c.GetHeader("X-WOPI-Override") == "GET_LOCK" {
		c.Header("X-WOPI-Lock", current)
	}
	c.Status(http.StatusOK)
}

func (h *DriveHandler) wopiRequest(c *gin.Context) (*service.WOPIAccessClaims, uuid.UUID, bool) {
	fileID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return nil, uuid.Nil, false
	}
	token := c.Query("access_token")
	if token == "" {
		token = strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	}
	claims, err := service.ValidateWOPIAccessToken(token, h.jwtSecret)
	if err != nil || claims.FileID != fileID.String() {
		c.Header("WWW-Authenticate", "Bearer")
		c.Status(http.StatusUnauthorized)
		return nil, uuid.Nil, false
	}
	return claims, fileID, true
}

func (h *DriveHandler) wopiError(c *gin.Context, err error) {
	appErr := apperrors.ToAppError(err)
	status := appErr.StatusCode
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	c.Status(status)
}

func wopiMime(value, name string) string {
	if value != "" && value != "application/octet-stream" {
		return value
	}
	if inferred := mime.TypeByExtension(filepath.Ext(name)); inferred != "" {
		return inferred
	}
	return "application/octet-stream"
}
