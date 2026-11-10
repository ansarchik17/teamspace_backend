package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

const maxConvertToTextFileSize = 50 * 1024 * 1024

type ContentExtractionHandler struct {
	service *service.ContentExtractionService
	logger  *zap.Logger
}

func NewContentExtractionHandler(service *service.ContentExtractionService, logger *zap.Logger) *ContentExtractionHandler {
	return &ContentExtractionHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "content_extraction")),
	}
}

// ConvertFile
// @Summary Convert uploaded file to text
// @Description Extracts text from .xlsx, .pptx, .docx, .pdf, and .txt files and returns it as a downloadable .txt file.
// @Tags tools
// @Accept multipart/form-data
// @Produce text/plain
// @Param file formData file true "File to convert (txt, xlsx, docx, pptx, pdf)"
// @Success 200 {file} string "Converted text"
// @Failure 400 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /tools/convert-to-text [post]
func (h *ContentExtractionHandler) ConvertFile(c *gin.Context) {
	// Limit the request body before multipart parsing; MaxMultipartMemory is not
	// a security boundary and must not be used as one.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxConvertToTextFileSize+1024*1024)
	file, err := c.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.Validation("file is required"))
		return
	}

	// Validate size.
	if file.Size > maxConvertToTextFileSize {
		h.respondError(c, apperrors.Validation("file too large (max 50MB)"))
		return
	}

	content, err := h.service.ExtractText(file)
	if err != nil {
		h.logger.Error("Failed to extract text", zap.Error(err), zap.String("filename", file.Filename))
		switch {
		case errors.Is(err, service.ErrNoExtractableText()):
			h.respondError(c, apperrors.Validation("no extractable text found"))
		case strings.Contains(err.Error(), "not a valid zip file"):
			h.respondError(c, apperrors.Validation("file is corrupted or has an invalid office document structure"))
		case strings.Contains(err.Error(), "unsupported file format"):
			h.respondError(c, apperrors.Validation(err.Error()))
		default:
			h.respondError(c, apperrors.Internal("failed to convert file: "+err.Error()))
		}
		return
	}

	// Return as a downloadable .txt file
	c.Header("Content-Disposition", "attachment; filename=converted.txt")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(content))
}

func (h *ContentExtractionHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
