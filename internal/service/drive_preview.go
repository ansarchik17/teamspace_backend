package service

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// The public descriptor and office page use the same revocable capability as
// native media, including targets inside a shared folder.
func (s *DriveService) GetPublicFilePreview(ctx context.Context, token uuid.UUID, target ...PublicLinkTarget) (*domain.DrivePreview, error) {
	item, err := s.GetPublicItemByToken(ctx, token, target...)
	if err != nil {
		return nil, err
	}
	if item.File == nil {
		return nil, apperrors.BadRequest("link target is not a file")
	}
	p := s.describeFilePreview(item.File)
	p.AuthRequired = false
	p.DownloadURL = s.publicLinkDownloadURL(token, item.File.ID)
	if p.Supported {
		base := strings.TrimRight(s.publicURL, "/") + "/api/v1/public/drive/files/" + token.String() + "/preview"
		if p.Kind == "office" {
			base += "/office"
		}
		p.PreviewURL = fmt.Sprintf("%s?resource_type=file&resource_id=%s", base, item.File.ID)
	}
	return p, nil
}

func (s *DriveService) BuildPublicOfficePreview(ctx context.Context, token uuid.UUID, target ...PublicLinkTarget) (*OnlyOfficeEditorConfig, error) {
	p, err := s.GetPublicFilePreview(ctx, token, target...)
	if err != nil {
		return nil, err
	}
	if p.Kind != "office" {
		return nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "office preview unavailable for this format", http.StatusUnsupportedMediaType)
	}
	base := os.Getenv("ONLYOFFICE_DOWNLOAD_BASE_URL")
	if base == "" {
		base = s.publicURL
	}
	fileType := resolveOnlyOfficeFileType(p.Name, p.MIMEType)
	return prepareOnlyOfficeConfig(&OnlyOfficeEditorConfig{
		DocumentType: resolveOnlyOfficeDocType(fileType), FileType: fileType,
		DocumentKey: fmt.Sprintf("%s-%s-v%d", token, p.FileID, p.Version),
		Title:       p.Name, Mode: "view", UserID: "guest", UserName: "Гость", CanEdit: false,
		DocumentURL: fmt.Sprintf("%s/api/v1/public/drive/files/%s/download?resource_type=file&resource_id=%s", strings.TrimRight(base, "/"), token, p.FileID),
	})
}

type drivePreviewFormat struct{ kind, mime string }

// Explicit allowlist: never render uploaded HTML, SVG or arbitrary MIME types inline.
var drivePreviewFormats = map[string]drivePreviewFormat{
	".pdf": {"pdf", "application/pdf"},
	".jpg": {"image", "image/jpeg"}, ".jpeg": {"image", "image/jpeg"},
	".png": {"image", "image/png"}, ".gif": {"image", "image/gif"},
	".webp": {"image", "image/webp"}, ".avif": {"image", "image/avif"},
	".mp4": {"video", "video/mp4"}, ".webm": {"video", "video/webm"}, ".ogv": {"video", "video/ogg"},
	".mp3": {"audio", "audio/mpeg"}, ".m4a": {"audio", "audio/mp4"},
	".wav": {"audio", "audio/wav"}, ".ogg": {"audio", "audio/ogg"}, ".flac": {"audio", "audio/flac"},
	".doc": {"office", "application/msword"}, ".docx": {"office", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	".xls": {"office", "application/vnd.ms-excel"}, ".xlsx": {"office", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	".ppt": {"office", "application/vnd.ms-powerpoint"}, ".pptx": {"office", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	".odt": {"office", "application/vnd.oasis.opendocument.text"}, ".ods": {"office", "application/vnd.oasis.opendocument.spreadsheet"},
	".odp": {"office", "application/vnd.oasis.opendocument.presentation"}, ".rtf": {"office", "application/rtf"},
}

func classifyDrivePreview(name, storedMIME string) drivePreviewFormat {
	f, ok := drivePreviewFormats[strings.ToLower(filepath.Ext(name))]
	if !ok {
		return drivePreviewFormat{"unsupported", "application/octet-stream"}
	}
	// A generic MIME is common for office archives and old uploads. For native
	// formats, conflicting concrete metadata must not turn active content inline.
	if f.kind != "office" {
		mt, _, err := mime.ParseMediaType(storedMIME)
		if storedMIME != "" && err != nil {
			return drivePreviewFormat{"unsupported", "application/octet-stream"}
		}
		switch mt {
		case "", "application/octet-stream":
		case f.mime:
		case "audio/x-wav":
			if f.mime != "audio/wav" {
				return drivePreviewFormat{"unsupported", "application/octet-stream"}
			}
		case "audio/x-flac":
			if f.mime != "audio/flac" {
				return drivePreviewFormat{"unsupported", "application/octet-stream"}
			}
		default:
			return drivePreviewFormat{"unsupported", "application/octet-stream"}
		}
	}
	return f
}

func (s *DriveService) GetFilePreview(ctx context.Context, userID int64, id uuid.UUID) (*domain.DrivePreview, error) {
	meta, err := s.canAccessFile(ctx, userID, id, domain.RoleViewer)
	if err != nil {
		return nil, err
	}
	return s.describeFilePreview(meta), nil
}

func (s *DriveService) describeFilePreview(meta *domain.DriveFile) *domain.DrivePreview {
	f := classifyDrivePreview(meta.RealName, meta.MimeType)
	base := strings.TrimRight(s.publicURL, "/") + "/api/v1/drive/files/" + meta.ID.String()
	p := &domain.DrivePreview{FileID: meta.ID, Name: meta.RealName, Size: meta.Size, Version: meta.Version,
		Kind: f.kind, MIMEType: f.mime, Supported: f.kind != "unsupported", AuthRequired: true, DownloadURL: base + "/download"}
	switch f.kind {
	case "unsupported":
		p.Reason = "unsupported_format"
	case "office":
		p.PreviewURL = base + "/preview/office"
	default:
		p.PreviewURL = base + "/preview/content"
	}
	return p
}

// OpenFilePreview rechecks inherited ACL before opening storage, including HEAD/Range requests.
func (s *DriveService) OpenFilePreview(ctx context.Context, userID int64, id uuid.UUID) (io.ReadSeekCloser, *domain.DrivePreview, error) {
	meta, err := s.canAccessFile(ctx, userID, id, domain.RoleViewer)
	if err != nil {
		return nil, nil, err
	}
	return s.openNativePreview(ctx, meta)
}

func (s *DriveService) openNativePreview(ctx context.Context, meta *domain.DriveFile) (io.ReadSeekCloser, *domain.DrivePreview, error) {
	p := s.describeFilePreview(meta)
	if !p.Supported || p.Kind == "office" {
		return nil, nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "native preview unavailable for this format", http.StatusUnsupportedMediaType)
	}
	reader, _, size, err := s.fileSvc.DownloadFile(ctx, meta.ObjectName)
	if err != nil {
		return nil, nil, err
	}
	seeker, ok := reader.(io.ReadSeekCloser)
	if !ok {
		reader.Close()
		return nil, nil, apperrors.Internal("storage does not support seekable preview")
	}
	p.Size = size
	return seeker, p, nil
}

// Public previews retain DRV-004 expiry/revocation checks for every new request.
func (s *DriveService) OpenPublicFilePreview(ctx context.Context, token uuid.UUID, target ...PublicLinkTarget) (io.ReadSeekCloser, *domain.DrivePreview, error) {
	link, err := s.resolvePublicLinkTarget(ctx, token, target...)
	if err != nil {
		return nil, nil, err
	}
	if link.ResourceType != "file" {
		return nil, nil, apperrors.BadRequest("link is not for a file")
	}
	meta, err := s.repo.GetFileByID(ctx, link.ResourceID)
	if err != nil {
		return nil, nil, apperrors.Internal("file lookup failed")
	}
	if meta == nil || meta.DeletedAt != nil {
		return nil, nil, apperrors.NotFound("file not found")
	}
	reader, p, err := s.openNativePreview(ctx, meta)
	if err != nil {
		return nil, nil, err
	}
	p.AuthRequired = false
	p.PreviewURL = fmt.Sprintf("%s/api/v1/public/drive/files/%s/preview", strings.TrimRight(s.publicURL, "/"), token)
	p.DownloadURL = s.publicLinkDownloadURL(token, meta.ID)
	p.PreviewURL += fmt.Sprintf("?resource_type=file&resource_id=%s", meta.ID)
	return reader, p, nil
}
