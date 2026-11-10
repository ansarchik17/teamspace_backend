package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type RecentRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewRecentRepository(db *pgxpool.Pool, logger *zap.Logger) *RecentRepository {
	return &RecentRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "recent")),
	}
}

// Touch обновляет или создает запись о доступе к ресурсу.
func (r *RecentRepository) Touch(ctx context.Context, userID int64, t domain.ResourceType, resourceID uuid.UUID) error {
	q := `
       INSERT INTO user_recent_items (user_id, resource_type, resource_id, last_accessed_at)
       VALUES ($1, $2, $3, $4)
       ON CONFLICT (user_id, resource_type, resource_id)
       DO UPDATE SET last_accessed_at = EXCLUDED.last_accessed_at
    `
	_, err := r.db.Exec(ctx, q, userID, t, resourceID, time.Now())
	if err != nil {
		return fmt.Errorf("touch recent item: %w", err)
	}
	return nil
}

// ListRecent возвращает список элементов.
func (r *RecentRepository) ListRecent(ctx context.Context, userID int64, limit, offset int) ([]domain.RecentItem, error) {

	q := `
       SELECT 
          r.last_accessed_at,
          r.resource_type,
          f.id, f.owner_id, f.parent_id, f.name, f.created_at, f.updated_at,
          fl.id, fl.owner_id, fl.folder_id, fl.real_name, fl.object_name, fl.source_system_code, fl.source_system_name,
          fl.external_message_id, fl.size, fl.mime_type, fl.version, fl.onlyoffice_key, fl.created_at, fl.updated_at
       FROM user_recent_items r
       LEFT JOIN folders f ON r.resource_type = 'folder' AND r.resource_id = f.id
       LEFT JOIN files fl  ON r.resource_type = 'file'   AND r.resource_id = fl.id
       WHERE r.user_id = $1
         AND (
             (r.resource_type = 'folder' AND f.deleted_at IS NULL AND f.id IS NOT NULL) OR
             (r.resource_type = 'file'   AND fl.deleted_at IS NULL AND fl.id IS NOT NULL)
         )
       ORDER BY r.last_accessed_at DESC
       LIMIT $2 OFFSET $3
    `

	rows, err := r.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query recent: %w", err)
	}
	defer rows.Close()

	items := make([]domain.RecentItem, 0)

	for rows.Next() {
		var (
			ri                                       domain.RecentItem
			resType                                  domain.ResourceType
			fID, flID                                *uuid.UUID
			fOwner                                   *int64
			fParent                                  *uuid.UUID
			fName                                    *string
			fCr, fUp                                 *time.Time
			flOwner                                  *int64
			flFolder                                 *uuid.UUID
			flName, flObj, flMime                    *string
			flSourceCode, flSourceName, flExternalID *string
			flSize                                   *int64
			flVer                                    *int
			flKey                                    *string
			flCr, flUp                               *time.Time
		)

		err := rows.Scan(
			&ri.LastAccessedAt, &resType,
			&fID, &fOwner, &fParent, &fName, &fCr, &fUp,
			&flID, &flOwner, &flFolder, &flName, &flObj, &flSourceCode, &flSourceName, &flExternalID, &flSize, &flMime, &flVer, &flKey, &flCr, &flUp,
		)
		if err != nil {
			r.logger.Warn("failed to scan recent item", zap.Error(err))
			continue
		}

		ri.Type = resType

		if resType == domain.ResourceFolder && fID != nil {
			ri.Folder = &domain.Folder{
				ID: *fID, OwnerID: *fOwner, ParentID: fParent, Name: *fName, CreatedAt: *fCr, UpdatedAt: *fUp,
			}
			items = append(items, ri)
		} else if resType == domain.ResourceFile && flID != nil {
			ri.File = &domain.DriveFile{
				ID: *flID, OwnerID: *flOwner, FolderID: flFolder, RealName: *flName, ObjectName: *flObj,
				SourceSystemCode: flSourceCode, SourceSystemName: flSourceName, ExternalMessageID: flExternalID,
				Size: *flSize, MimeType: *flMime, Version: *flVer, Key: flKey, CreatedAt: *flCr, UpdatedAt: *flUp,
			}
			items = append(items, ri)
		}
	}

	return items, nil
}
