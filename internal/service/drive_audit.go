package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"time"
)

// Records issuance of a download stream, not receipt of all bytes by the client.
// Public visitors use actor_id NULL in storage (0 in the existing API).
func (s *DriveService) auditDriveDownload(ctx context.Context, userID int64, file *domain.DriveFile, version int, public bool) error {
	path, _ := s.buildFilePath(ctx, file.FolderID, file.RealName)
	metadata, _ := json.Marshal(map[string]interface{}{"version": version, "public_link": public})
	err := s.repo.LogDriveActivity(ctx, &domain.DriveActivity{
		ID: uuid.New(), Action: domain.DriveActivityActionDownloaded,
		ResourceType: domain.ResourceFile, ResourceID: file.ID, ResourceName: file.RealName,
		OwnerID: file.OwnerID, ActorID: userID, FolderID: file.FolderID,
		ResourcePath: path, Metadata: metadata, CreatedAt: time.Now(),
	})
	if err != nil {
		return apperrors.InternalWithErr("record drive download failed", err)
	}
	return nil
}
