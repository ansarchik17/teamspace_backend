package service

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

var standaloneUploadLeaf = regexp.MustCompile(`^[1-9][0-9]*_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}(\.[A-Za-z0-9]+)?$`)

// Ownership is encoded by generateObjectName using the authenticated user ID.
// Match the exact server-generated shape; never clean paths or trust a client
// user_id, public visibility, a download signature, or an unknown namespace.
func ownsStandaloneUpload(userID int64, objectName string) bool {
	parts := strings.Split(objectName, "/")
	if userID <= 0 || len(parts) != 3 || parts[1] != strconv.FormatInt(userID, 10) {
		return false
	}
	switch parts[0] {
	case "file", "image", "video", "document", "pdf", "audio", "voice", "ai-uploads":
		return standaloneUploadLeaf.MatchString(parts[2])
	default:
		return false
	}
}

// DeleteFileForUser deletes only a caller's standalone upload. In particular,
// an uploader cannot bypass Drive permissions, trash or deletion approval by
// passing the underlying object key, including keys retained in old versions.
func (s *FileService) DeleteFileForUser(ctx context.Context, userID int64, objectName string) error {
	if userID <= 0 {
		return apperrors.Unauthorized("unauthorized file access")
	}
	if !ownsStandaloneUpload(userID, objectName) {
		return apperrors.Forbidden("no permission to delete file")
	}
	if s.driveRepo == nil {
		return apperrors.Internal("file ownership check unavailable")
	}
	managed, err := s.driveRepo.IsManagedFileObject(ctx, objectName)
	if err != nil {
		return apperrors.InternalWithErr("file ownership check unavailable", err)
	}
	if managed {
		return apperrors.Forbidden("delete file through its owning resource")
	}
	return s.DeleteFile(ctx, objectName)
}
