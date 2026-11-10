package service

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func documentMIME(format string) string {
	switch format {
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	}
	return ""
}

func documentName(name, format string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", apperrors.BadRequest("invalid document name")
	}
	ext := filepath.Ext(name)
	if ext == "" {
		name += "." + format
	} else if !strings.EqualFold(ext, "."+format) {
		return "", apperrors.BadRequest("filename extension must match document format")
	}
	if len([]rune(name)) > 200 || strings.TrimSuffix(name, filepath.Ext(name)) == "" {
		return "", apperrors.BadRequest("invalid document name")
	}
	return name, nil
}

func (s *DriveService) CreateDocument(ctx context.Context, userID int64, req domain.CreateDocumentRequest) (*domain.DriveFile, error) {
	if userID <= 0 {
		return nil, apperrors.Unauthorized("unauthorized")
	}
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	if (req.TemplateID == nil) == (req.Format == "") {
		return nil, apperrors.BadRequest("provide exactly one of format or template_id")
	}
	if req.FolderID != nil {
		if _, err := s.canWorkInFolder(ctx, userID, *req.FolderID, domain.RoleEditor); err != nil {
			return nil, err
		}
	}
	var source *domain.DriveFile
	if req.TemplateID != nil {
		org, err := s.repo.DocumentOrganization(ctx, userID, false)
		if err != nil {
			return nil, err
		}
		source, req.Format, err = s.repo.DocumentTemplateSource(ctx, org, *req.TemplateID)
		if err != nil {
			return nil, err
		}
	}
	if documentMIME(req.Format) == "" {
		return nil, apperrors.BadRequest("format must be docx, xlsx or pptx")
	}
	name, err := documentName(req.Name, req.Format)
	if err != nil {
		return nil, err
	}
	if source != nil {
		if err := s.checkDriveUpload(ctx, userID, source.Size, "file"); err != nil {
			return nil, err
		}
		reader, _, size, err := s.fileSvc.DownloadFile(ctx, source.ObjectName)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		// A template is an explicit publication to the company. Source ACLs are not
		// copied; the new file belongs to its creator and inherits its target folder.
		return s.saveDriveFile(ctx, userID, req.FolderID, name, size, documentMIME(req.Format), reader, nil, nil, nil, true)
	}
	content, err := blankOfficeDocument(req.Format)
	if err != nil {
		return nil, apperrors.InternalWithErr("create blank document", err)
	}
	return s.saveDriveFile(ctx, userID, req.FolderID, name, int64(len(content)), documentMIME(req.Format), bytes.NewReader(content), nil, nil, nil, true)
}

func (s *DriveService) ListDocumentTemplates(ctx context.Context, userID int64, limit, offset int) ([]domain.DocumentTemplate, error) {
	org, err := s.repo.DocumentOrganization(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListDocumentTemplates(ctx, org, limit, offset)
}

func (s *DriveService) PublishDocumentTemplate(ctx context.Context, userID int64, req domain.PublishDocumentTemplateRequest) (*domain.DocumentTemplate, error) {
	org, err := s.repo.DocumentOrganization(ctx, userID, true)
	if err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 200 || strings.IndexFunc(req.Name, unicode.IsControl) >= 0 {
		return nil, apperrors.BadRequest("invalid template name")
	}
	file, err := s.canAccessFile(ctx, userID, req.FileID, domain.RoleOwner)
	if err != nil {
		return nil, err
	}
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(file.RealName), "."))
	if documentMIME(format) == "" {
		return nil, apperrors.BadRequest("template must be docx, xlsx or pptx")
	}
	item := &domain.DocumentTemplate{OrganizationID: org, FileID: file.ID, Name: req.Name, Format: format, CreatedBy: userID}
	if err := s.repo.PublishDocumentTemplate(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *DriveService) DeleteDocumentTemplate(ctx context.Context, userID int64, id uuid.UUID) error {
	org, err := s.repo.DocumentOrganization(ctx, userID, true)
	if err != nil {
		return err
	}
	return s.repo.DeleteDocumentTemplate(ctx, org, id)
}
