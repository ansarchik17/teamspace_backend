package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *DriveRepository) AddFavorite(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO drive_favorites (user_id, resource_type, resource_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, resource_type, resource_id) DO NOTHING
	`, userID, resourceType, resourceID)
	if err != nil {
		return fmt.Errorf("add drive favorite: %w", err)
	}
	return nil
}

func (r *DriveRepository) RemoveFavorite(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM drive_favorites
		WHERE user_id = $1 AND resource_type = $2 AND resource_id = $3
	`, userID, resourceType, resourceID)
	if err != nil {
		return fmt.Errorf("remove drive favorite: %w", err)
	}
	return nil
}

func (r *DriveRepository) ListFavoriteRefs(ctx context.Context, userID int64) ([]domain.DriveFavoriteRef, error) {
	rows, err := r.db.Query(ctx, `
		SELECT resource_type, resource_id, created_at
		FROM drive_favorites
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list drive favorites: %w", err)
	}
	defer rows.Close()

	refs := make([]domain.DriveFavoriteRef, 0)
	for rows.Next() {
		var ref domain.DriveFavoriteRef
		if err := rows.Scan(&ref.ResourceType, &ref.ResourceID, &ref.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan drive favorite: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate drive favorites: %w", err)
	}
	return refs, nil
}

func (r *DriveRepository) GetDriveSummary(ctx context.Context, userID int64) (*domain.DriveSummary, error) {
	var summary domain.DriveSummary
	err := r.db.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT SUM(size) FROM files WHERE owner_id = $1 AND scope = 'drive' AND deleted_at IS NULL), 0),
			(SELECT COUNT(*) FROM files WHERE owner_id = $1 AND scope = 'drive' AND deleted_at IS NULL),
			(SELECT COUNT(*) FROM folders WHERE owner_id = $1 AND deleted_at IS NULL),
			(
				SELECT COUNT(*) FROM permissions p
				WHERE p.user_id = $1
				  AND (
					(p.resource_type = 'file' AND EXISTS (
						SELECT 1 FROM files f WHERE f.id = p.resource_id AND f.deleted_at IS NULL AND f.scope = 'drive' AND f.owner_id <> $1
					)) OR
					(p.resource_type = 'folder' AND EXISTS (
						SELECT 1 FROM folders d WHERE d.id = p.resource_id AND d.deleted_at IS NULL AND d.owner_id <> $1
					))
				  )
			),
			(SELECT COUNT(*) FROM drive_favorites WHERE user_id = $1),
			(
				SELECT
					(SELECT COUNT(*) FROM files f
					 WHERE f.owner_id = $1 AND f.scope = 'drive' AND f.deleted_at > NOW() - INTERVAL '720 hours'
					   AND (f.folder_id IS NULL OR EXISTS (SELECT 1 FROM folders pf WHERE pf.id = f.folder_id AND pf.deleted_at IS NULL)))
					+
					(SELECT COUNT(*) FROM folders d
					 WHERE d.owner_id = $1 AND d.deleted_at > NOW() - INTERVAL '720 hours'
					   AND (d.parent_id IS NULL OR EXISTS (SELECT 1 FROM folders pd WHERE pd.id = d.parent_id AND pd.deleted_at IS NULL)))
			)
	`, userID).Scan(
		&summary.UsedBytes,
		&summary.FilesCount,
		&summary.FoldersCount,
		&summary.SharedCount,
		&summary.FavoritesCount,
		&summary.TrashCount,
	)
	if err != nil {
		return nil, fmt.Errorf("get drive summary: %w", err)
	}
	return &summary, nil
}

func (r *DriveRepository) ListTrash(ctx context.Context, userID int64) ([]domain.DriveTrashItem, error) {
	items := make([]domain.DriveTrashItem, 0)

	folderRows, err := r.db.Query(ctx, `
		SELECT d.id, d.owner_id, COALESCE(NULLIF(BTRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''), u.username, ''),
		       d.parent_id, d.name, d.is_public, d.created_at, d.updated_at, d.deleted_at,
		       0 AS files_count, FALSE AS has_subfolders
		FROM folders d
		JOIN users u ON u.id = d.owner_id
		WHERE d.owner_id = $1 AND d.deleted_at > NOW() - INTERVAL '720 hours'
		  AND (d.parent_id IS NULL OR EXISTS (
		      SELECT 1 FROM folders parent WHERE parent.id = d.parent_id AND parent.deleted_at IS NULL
		  ))
		ORDER BY d.deleted_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list trashed folders: %w", err)
	}
	for folderRows.Next() {
		var folder domain.Folder
		var deletedAt time.Time
		if err := folderRows.Scan(
			&folder.ID, &folder.OwnerID, &folder.OwnerFio, &folder.ParentID, &folder.Name, &folder.IsPublic,
			&folder.CreatedAt, &folder.UpdatedAt, &folder.DeletedAt, &folder.FilesCount, &folder.HasSubfolders,
		); err != nil {
			folderRows.Close()
			return nil, fmt.Errorf("scan trashed folder: %w", err)
		}
		if folder.DeletedAt != nil {
			deletedAt = *folder.DeletedAt
		}
		items = append(items, domain.DriveTrashItem{DeletedAt: deletedAt, DriveItem: domain.DriveItem{Type: domain.ResourceFolder, Folder: &folder}})
	}
	if err := folderRows.Err(); err != nil {
		folderRows.Close()
		return nil, fmt.Errorf("iterate trashed folders: %w", err)
	}
	folderRows.Close()

	fileRows, err := r.db.Query(ctx, `
		SELECT f.id, f.owner_id, COALESCE(NULLIF(BTRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''), u.username, ''),
		       f.folder_id, f.real_name, f.object_name, COALESCE(f.content_hash, ''),
		       f.source_system_code, f.source_system_name, f.external_message_id,
		       f.size, f.mime_type, f.version, f.scope, f.is_public, f.onlyoffice_key,
		       f.created_at, f.updated_at, f.deleted_at
		FROM files f
		JOIN users u ON u.id = f.owner_id
		WHERE f.owner_id = $1 AND f.scope = 'drive' AND f.deleted_at > NOW() - INTERVAL '720 hours'
		  AND (f.folder_id IS NULL OR EXISTS (
		      SELECT 1 FROM folders parent WHERE parent.id = f.folder_id AND parent.deleted_at IS NULL
		  ))
		ORDER BY f.deleted_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list trashed files: %w", err)
	}
	defer fileRows.Close()
	for fileRows.Next() {
		var file domain.DriveFile
		var deletedAt time.Time
		if err := fileRows.Scan(
			&file.ID, &file.OwnerID, &file.OwnerFio, &file.FolderID, &file.RealName, &file.ObjectName, &file.ContentHash,
			&file.SourceSystemCode, &file.SourceSystemName, &file.ExternalMessageID,
			&file.Size, &file.MimeType, &file.Version, &file.Scope, &file.IsPublic, &file.Key,
			&file.CreatedAt, &file.UpdatedAt, &file.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan trashed file: %w", err)
		}
		if file.DeletedAt != nil {
			deletedAt = *file.DeletedAt
		}
		items = append(items, domain.DriveTrashItem{DeletedAt: deletedAt, DriveItem: domain.DriveItem{Type: domain.ResourceFile, File: &file}})
	}
	if err := fileRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trashed files: %w", err)
	}

	return items, nil
}

var ErrDriveTrashUnavailable = errors.New("trashed item expired or unavailable")

// Restores are serialized with folder moves, with expiry and parent checks in SQL.
func (r *DriveRepository) RestoreFile(ctx context.Context, userID int64, fileID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724619203)`); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE files f SET deleted_at=NULL,updated_at=NOW()
 WHERE f.id=$1 AND f.owner_id=$2 AND f.scope='drive' AND f.deleted_at>clock_timestamp()-INTERVAL '720 hours'
 AND (f.folder_id IS NULL OR EXISTS(SELECT 1 FROM folders p WHERE p.id=f.folder_id AND p.deleted_at IS NULL))`, fileID, userID)
	if err != nil {
		return fmt.Errorf("restore file: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrDriveTrashUnavailable
	}
	return tx.Commit(ctx)
}

func (r *DriveRepository) RestoreFolderTree(ctx context.Context, userID int64, folderID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724619203)`); err != nil {
		return err
	}
	var deletedAt time.Time
	err = tx.QueryRow(ctx, `SELECT d.deleted_at FROM folders d WHERE d.id=$1 AND d.owner_id=$2
 AND d.deleted_at>clock_timestamp()-INTERVAL '720 hours'
 AND (d.parent_id IS NULL OR EXISTS(SELECT 1 FROM folders p WHERE p.id=d.parent_id AND p.deleted_at IS NULL)) FOR UPDATE`, folderID, userID).Scan(&deletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDriveTrashUnavailable
		}
		return fmt.Errorf("get trashed folder: %w", err)
	}
	// The deletion timestamp identifies this tree deletion. Independently trashed
	// descendants remain in the trash; descendants owned by collaborators return too.
	var ids []uuid.UUID
	rows, err := tx.Query(ctx, `WITH RECURSIVE tree AS (
 SELECT id FROM folders WHERE id=$1 AND deleted_at=$2
 UNION SELECT c.id FROM folders c JOIN tree p ON c.parent_id=p.id WHERE c.deleted_at=$2)
 SELECT id FROM tree`, folderID, deletedAt)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE folders SET deleted_at=NULL,updated_at=NOW() WHERE id=ANY($1) AND deleted_at=$2`, ids, deletedAt); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE files SET deleted_at=NULL,updated_at=NOW() WHERE folder_id=ANY($1) AND deleted_at=$2 AND scope='drive'`, ids, deletedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
