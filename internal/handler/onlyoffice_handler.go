package handler

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

// OnlyOfficeEditorLink godoc
// @Summary  Signed OnlyOffice editor URL
// @Description Checks auth/permission and returns a short-lived editor URL for browser or mobile WebView. Supports documents, spreadsheets and presentations; edit requests from viewers fall back to view.
// @Tags    drive
// @Security BearerAuth
// @Produce json
// @Param   file_id query string true  "File UUID"
// @Param   mode    query string false "view|edit|comment|review (DOCX only); default: view. Write modes require Drive editor access; viewers fall back to view."
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router  /drive/onlyoffice/editor-link [get]
func (h *DriveHandler) OnlyOfficeEditorLink(c *gin.Context) {
	setPublicLinkHeaders(c)
	start := time.Now()
	userID := h.getUserID(c)
	if userID <= 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized"))
		return
	}

	rawFileID := c.Query("file_id")
	fileID, err := uuid.Parse(rawFileID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file_id"))
		return
	}

	mode := normalizeOnlyOfficeMode(c.DefaultQuery("mode", "view"))
	cfg, err := h.drive.BuildOnlyOfficeConfig(c.Request.Context(), userID, fileID, mode, h.jwtSecret, h.publicURL)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	token, err := service.GenerateOnlyOfficeEditorToken(fileID, userID, cfg.Mode, h.jwtSecret)
	if err != nil {
		h.respondError(c, apperrors.Internal("failed to generate editor token"))
		return
	}

	editorURL := strings.TrimRight(h.publicURL, "/") + "/api/v1/drive/onlyoffice/editor?" + url.Values{
		"file_id": []string{fileID.String()},
		"token":   []string{token},
	}.Encode()

	h.logger.Info("onlyoffice_editor_link_built",
		zap.String("file_id", fileID.String()),
		zap.Int64("user_id", userID),
		zap.String("mode", cfg.Mode),
		zap.String("document_key", cfg.DocumentKey),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	respondJSON(c, http.StatusOK, gin.H{"url": editorURL, "mode": cfg.Mode})
}

// OnlyOfficeEditorPage godoc
// @Summary  OnlyOffice HTML editor page
// @Description Returns HTML page that initialises DocsAPI.DocEditor with a signed config.
//
//	Requires signed editor token from /editor-link; no Bearer header is needed.
//
// @Tags    drive
// @Param token query string true "Signed editor token from editor-link"
// @Produce  text/html
// @Param   file_id query string true  "File UUID"
// @Success 200 {string} string "HTML"
// @Failure 400 {string} string "Bad request"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Router  /drive/onlyoffice/editor [get]
func (h *DriveHandler) OnlyOfficeEditorPage(c *gin.Context) {
	setPublicLinkHeaders(c)
	start := time.Now()

	rawFileID := c.Query("file_id")
	fileID, err := uuid.Parse(rawFileID)
	if err != nil {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8",
			[]byte(ooErrorHTML(apiMessage(c, "Ошибка"), apiMessage(c, "Неверный file_id."))))
		return
	}

	mode := normalizeOnlyOfficeMode(c.DefaultQuery("mode", "view"))
	userID := h.getUserID(c)
	token := strings.TrimSpace(c.Query("token"))
	if token != "" {
		claims, err := service.ValidateOnlyOfficeEditorToken(token, h.jwtSecret)
		if err != nil {
			c.Data(http.StatusUnauthorized, "text/html; charset=utf-8",
				[]byte(ooErrorHTML(apiMessage(c, "Ошибка"), apiMessage(c, "Сессия редактора истекла. Откройте документ заново."))))
			return
		}
		if claims.FileID != fileID.String() {
			c.Data(http.StatusUnauthorized, "text/html; charset=utf-8",
				[]byte(ooErrorHTML(apiMessage(c, "Ошибка"), apiMessage(c, "Некорректная сессия редактора."))))
			return
		}
		userID = claims.UserID
		mode = normalizeOnlyOfficeMode(claims.Mode)
	}
	if userID <= 0 {
		c.Data(http.StatusUnauthorized, "text/html; charset=utf-8",
			[]byte(ooErrorHTML(apiMessage(c, "Ошибка"), apiMessage(c, "Нет доступа. Попробуйте снова."))))
		return
	}

	cfg, err := h.drive.BuildOnlyOfficeConfig(
		c.Request.Context(),
		userID,
		fileID,
		mode,
		h.jwtSecret,
		h.publicURL,
	)
	if err != nil {
		appErr := apperrors.ToAppError(err)
		h.logger.Warn("onlyoffice editor page denied",
			zap.String("file_id", rawFileID),
			zap.Int64("user_id", userID),
			zap.String("mode", mode),
			zap.Error(err),
		)
		statusCode := appErr.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusForbidden
		}
		c.Data(statusCode, "text/html; charset=utf-8",
			[]byte(ooErrorHTML(apiMessage(c, "Ошибка"), apperrors.ResponseBodyLocale(appErr, middleware.RequestLocale(c)).Error.Message)))
		return
	}

	html := buildOnlyOfficeHTML(cfg, h.publicURL)
	h.logger.Info("onlyoffice editor page served",
		zap.String("file_id", rawFileID),
		zap.Int64("user_id", userID),
		zap.String("mode", cfg.Mode),
		zap.String("doc_key", cfg.DocumentKey),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	// HTML редактора не кэшируем — в нём signed token с 20-мин TTL.
	setPublicLinkHeaders(c)
	setDrivePreviewEmbedding(c, true)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// OnlyOfficeSignedDownload godoc
// @Summary  Signed file download for OnlyOffice DocumentServer
// @Description Validates HMAC-signed token and streams the file. Used as document.url by DocumentServer.
//
//	Not accessible with user Bearer token — only with signed token from OnlyOfficeEditorPage.
//
// @Tags    drive
// @Produce  application/octet-stream
// @Param   file_id query string true "File UUID"
// @Param   token   query string true "Signed OO download token"
// @Success 200 {file} binary
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router  /drive/onlyoffice/download [get]
func (h *DriveHandler) OnlyOfficeSignedDownload(c *gin.Context) {
	start := time.Now()
	remoteIP := c.ClientIP()
	userAgent := c.GetHeader("User-Agent")
	requestID := c.GetHeader("X-Request-ID")

	rawFileID := c.Query("file_id")
	token := c.Query("token")

	fileID, err := uuid.Parse(rawFileID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file_id"))
		return
	}

	tokenClaims, tokenErr := service.ValidateOnlyOfficeToken(token, h.jwtSecret)
	tokenUserID := int64(0)
	if tokenClaims != nil {
		tokenUserID = tokenClaims.UserID
	}

	h.logger.Info("onlyoffice_download_start",
		zap.String("request_id", requestID),
		zap.String("file_id", rawFileID),
		zap.Bool("token_valid", tokenErr == nil),
		zap.Int64("token_user_id", tokenUserID),
		zap.String("remote_ip", remoteIP),
		zap.String("user_agent", userAgent),
	)

	reader, mimeType, size, filename, err := h.drive.DownloadFileForOnlyOffice(
		c.Request.Context(),
		fileID,
		token,
		h.jwtSecret,
	)
	if err != nil {
		appErr := apperrors.ToAppError(err)
		h.logger.Warn("onlyoffice_download_denied",
			zap.String("request_id", requestID),
			zap.String("file_id", rawFileID),
			zap.Bool("token_valid", tokenErr == nil),
			zap.Int64("token_user_id", tokenUserID),
			zap.String("remote_ip", remoteIP),
			zap.Error(err),
		)
		h.respondError(c, appErr)
		return
	}
	defer func() {
		if rc, ok := reader.(interface{ Close() error }); ok {
			_ = rc.Close()
		}
	}()

	// Resolve proper Content-Type for office formats
	contentType := resolveOfficeMimeType(mimeType, filename)

	h.logger.Info("onlyoffice_download_complete",
		zap.String("request_id", requestID),
		zap.String("file_id", rawFileID),
		zap.String("content_type", contentType),
		zap.Int64("content_length", size),
		zap.String("remote_ip", remoteIP),
		zap.String("user_agent", userAgent),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", contentDispositionInline(filename))
	c.Header("Accept-Ranges", "bytes")
	// Небольшой кэш — DocumentServer может повторно запросить при tab-переключении
	c.Header("Cache-Control", "private, max-age=300")
	if size > 0 {
		c.Header("Content-Length", fmt.Sprintf("%d", size))
	}
	c.Status(http.StatusOK)

	if rc, ok := reader.(interface {
		Read(p []byte) (n int, err error)
	}); ok {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := rc.Read(buf)
			if n > 0 {
				if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
					break
				}
			}
			if readErr != nil {
				break
			}
		}
	}
}

// ─────────────────────────────────────────────
// HTML builder
// ─────────────────────────────────────────────

func buildOnlyOfficeHTML(cfg *service.OnlyOfficeEditorConfig, publicURL string) string {
	// api.js грузится через Nginx /onlyoffice/ прокси с Cache-Control: public, max-age=604800
	apiJSURL := "/onlyoffice/web-apps/apps/api/documents/api.js"

	config := cfg.BrowserConfig()
	if cfg.Token != "" {
		config["token"] = cfg.Token
	}
	if cfg.APIJSURL != "" {
		apiJSURL = cfg.APIJSURL
	}

	collaborationJSON, _ := json.Marshal(cfg.CollaborationURL)
	configJSON, _ := json.Marshal(config)
	safeConfigJSON := strings.ReplaceAll(string(configJSON), "</script>", "<\\/script>")
	title := html.EscapeString(cfg.Title)
	apiJSURL = html.EscapeString(apiJSURL)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="ru">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0" />
  <title>%s</title>
  <script>
    window.__onlyOfficeApiLoaded = false;
    window.__onlyOfficeApiFailed = false;
  </script>
  <script src="%s" onload="window.__onlyOfficeApiLoaded=true; window.bootOnlyOffice && window.bootOnlyOffice();" onerror="window.__onlyOfficeApiFailed=true; window.showOnlyOfficeError && window.showOnlyOfficeError('OnlyOffice api.js failed to load');"></script>
  <style>
    html, body, #placeholder {
      width: 100%%;
      height: 100%%;
      margin: 0;
      padding: 0;
      overflow: hidden;
      background: #f7f9fc;
    }
    #status {
      position: fixed;
      inset: 0;
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 2;
      font-family: sans-serif;
      color: #1f2937;
      background: #f7f9fc;
    }
    #error {
      display: none;
      position: fixed;
      inset: 0;
      z-index: 3;
      padding: 24px;
      box-sizing: border-box;
      overflow: auto;
      white-space: pre-wrap;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      color: #b91c1c;
      background: #fff5f5;
    }
  </style>
</head>
<body>
  <div id="status">Loading OnlyOffice editor...</div>
  <div id="error"></div>
  <div id="placeholder"></div>
  <script>
    console.log('[OnlyOffice] editor page loaded');

    function showError(msg) {
      if (window.parent !== window) window.parent.postMessage({type: 'drive-office-error'}, window.location.origin);
      var status = document.getElementById('status');
      if (status) status.style.display = 'none';
      var error = document.getElementById('error');
      if (error) {
        error.style.display = 'block';
        error.textContent = String(msg || 'Unknown OnlyOffice error');
      }
      console.error('[OnlyOffice] ' + msg);
    }
    window.showOnlyOfficeError = showError;

    window.onerror = function(message, source, lineno, colno, error) {
      showError('JS error: ' + message + '\n' + source + ':' + lineno + ':' + colno + '\n' + (error && error.stack ? error.stack : ''));
    };

    window.onunhandledrejection = function(event) {
      var reason = event && event.reason;
      showError('Unhandled promise rejection: ' + (reason && reason.stack ? reason.stack : reason));
    };

    var onlyOfficeBooted = false;
    var domReady = document.readyState === 'interactive' || document.readyState === 'complete';
    var config = %s;
    var collaborationURL = %s;
    var presenceTimer = null;
    var documentReady = false;
    var heartbeatBusy = false;

    function sendPresence(action) {
      if (!collaborationURL || config.editorConfig.mode !== 'edit') return;
      if (action === 'leave') {
        var body = new Blob([JSON.stringify({action: 'leave'})], {type: 'application/json'});
        if (navigator.sendBeacon) navigator.sendBeacon(collaborationURL, body);
        else fetch(collaborationURL, {method: 'POST', body: body, keepalive: true}).catch(function () {});
        return;
      }
      if (heartbeatBusy) return;
      heartbeatBusy = true;
      var controller = new AbortController();
      var timeout = setTimeout(function () { controller.abort(); }, 10000);
      fetch(collaborationURL, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({action: action}), cache: 'no-store', signal: controller.signal})
        .then(function (response) {
          if ([401, 403, 404, 409].indexOf(response.status) !== -1) {
            clearInterval(presenceTimer);
            presenceTimer = null;
            if (window.parent !== window) window.parent.postMessage({type: 'drive-office-session-ended'}, window.location.origin);
          }
        }).catch(function () {}).finally(function () { clearTimeout(timeout); heartbeatBusy = false; });
    }
    window.addEventListener('pagehide', function () { sendPresence('leave'); });
    window.addEventListener('pageshow', function () { if (documentReady) sendPresence('heartbeat'); });


    function attachOnlyOfficeEvents() {
      var existingEvents = config.events || {};
      existingEvents.onAppReady = function () {
        console.log('[OnlyOffice] onAppReady');
      };
      existingEvents.onDocumentReady = function () {
        console.log('[OnlyOffice] onDocumentReady');
        documentReady = true;
        sendPresence('heartbeat');
        if (!presenceTimer && collaborationURL && config.editorConfig.mode === 'edit') presenceTimer = setInterval(function () { sendPresence('heartbeat'); }, 30000);
        if (window.parent !== window) window.parent.postMessage({type: 'drive-office-ready'}, window.location.origin);
        var status = document.getElementById('status');
        if (status) status.style.display = 'none';
      };
      existingEvents.onError = function (event) {
        console.error('[OnlyOffice] onError', event);
        showError('OnlyOffice error: ' + JSON.stringify(event));
      };
      existingEvents.onRequestClose = function () {
        console.log('[OnlyOffice] onRequestClose');
      };
      existingEvents.onCollaborativeChanges = function () {
        console.log('[OnlyOffice] onCollaborativeChanges');
      };
      if (collaborationURL) existingEvents.onRequestUsers = function (event) {
        var data = event && event.data;
        if (!data || data.c !== 'info' || !Array.isArray(data.id)) return;
        var requests = [];
        for (var i = 0; i < data.id.length; i += 100) {
          var ids = data.id.slice(i, i + 100);
          requests.push(fetch(collaborationURL + '&ids=' + encodeURIComponent(ids.join(',')), {cache: 'no-store'})
            .then(function (response) { if (!response.ok) throw new Error('User information unavailable'); return response.json(); }));
        }
        Promise.all(requests).then(function (groups) {
            var users = [].concat.apply([], groups);
            if (window.editor && users.length) window.editor.setUsers({c: 'info', users: users});
          }).catch(function () {});
      };
      config.events = existingEvents;
    }

    window.bootOnlyOffice = function () {
      try {
        if (onlyOfficeBooted) return;
        if (!domReady) {
          console.log('[OnlyOffice] waiting DOMContentLoaded');
          return;
        }
        if (window.__onlyOfficeApiFailed) {
          showError('OnlyOffice api.js failed to load.');
          return;
        }
        if (!window.__onlyOfficeApiLoaded) {
          console.log('[OnlyOffice] waiting api.js');
          return;
        }
        console.log('[OnlyOffice] DocsAPI exists', !!window.DocsAPI);
        if (typeof DocsAPI === 'undefined' || !DocsAPI.DocEditor) {
          showError('DocsAPI is undefined. api.js was not loaded.');
          return;
        }

        onlyOfficeBooted = true;
        console.log('[OnlyOffice] init DocEditor');
        attachOnlyOfficeEvents();
        window.editor = new DocsAPI.DocEditor("placeholder", config);
      } catch (e) {
        onlyOfficeBooted = false;
        showError('Init exception: ' + (e && (e.stack || e.message) ? (e.stack || e.message) : e));
      }
    };
    if (!domReady) {
      document.addEventListener('DOMContentLoaded', function () {
        domReady = true;
        window.bootOnlyOffice();
      });
    }
    setTimeout(function () {
      if (!onlyOfficeBooted) window.bootOnlyOffice();
    }, 0);
    setTimeout(function () {
      if (!onlyOfficeBooted) {
        showError('OnlyOffice initialization timeout. apiLoaded=' + window.__onlyOfficeApiLoaded + ', apiFailed=' + window.__onlyOfficeApiFailed + ', hasDocsAPI=' + (typeof DocsAPI !== 'undefined'));
      }
    }, 15000);
  </script>
</body>
</html>`, title, apiJSURL, safeConfigJSON, string(collaborationJSON))
}

func normalizeOnlyOfficeMode(mode string) string {
	return service.NormalizeOnlyOfficeMode(mode)
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

func ooErrorHTML(title, message string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><title>%s</title></head>
<body style="font-family:sans-serif;padding:24px;color:#c0392b;">
  <h2>%s</h2><p>%s</p>
</body>
</html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}

func resolveOfficeMimeType(mimeType, filename string) string {
	ext := ""
	if idx := strings.LastIndex(filename, "."); idx >= 0 {
		ext = strings.ToLower(filename[idx+1:])
	}
	switch ext {
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "doc":
		return "application/msword"
	case "xls":
		return "application/vnd.ms-excel"
	case "ppt":
		return "application/vnd.ms-powerpoint"
	case "odt":
		return "application/vnd.oasis.opendocument.text"
	case "ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case "odp":
		return "application/vnd.oasis.opendocument.presentation"
	}
	if mimeType != "" && mimeType != "application/octet-stream" {
		return mimeType
	}
	return "application/octet-stream"
}

func sanitizeFilename(name string) string {
	r := strings.NewReplacer(`"`, `'`, "\n", "", "\r", "")
	return r.Replace(name)
}

func contentDispositionInline(filename string) string {
	safeName := sanitizeFilename(filename)
	escaped := url.PathEscape(safeName)
	return fmt.Sprintf(`inline; filename="%s"; filename*=UTF-8''%s`, safeName, escaped)
}
