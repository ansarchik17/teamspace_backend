package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type DriveRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewDriveRepository(db *pgxpool.Pool, logger *zap.Logger) *DriveRepository {
	return &DriveRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "drive")),
	}
}

func (r *DriveRepository) AcquireWOPILock(ctx context.Context, id uuid.UUID, value string, ttl time.Duration) (string, bool, error) {
	_, err := r.db.Exec(ctx, `INSERT INTO drive_wopi_locks(file_id,lock_value,expires_at,updated_at)
VALUES($1,$2,NOW()+($3 * INTERVAL '1 second'),NOW())
ON CONFLICT(file_id) DO UPDATE SET lock_value=EXCLUDED.lock_value,expires_at=EXCLUDED.expires_at,updated_at=NOW()
WHERE drive_wopi_locks.expires_at<=NOW() OR drive_wopi_locks.lock_value=EXCLUDED.lock_value`, id, value, ttl.Seconds())
	if err != nil {
		return "", false, err
	}
	current, err := r.GetWOPILock(ctx, id)
	if err != nil {
		return "", false, err
	}
	return current, current == value, nil
}

func (r *DriveRepository) GetWOPILock(ctx context.Context, id uuid.UUID) (string, error) {
	var value string
	err := r.db.QueryRow(ctx, `SELECT lock_value FROM drive_wopi_locks WHERE file_id=$1 AND expires_at>NOW()`, id).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = r.db.Exec(ctx, `DELETE FROM drive_wopi_locks WHERE file_id=$1 AND expires_at<=NOW()`, id)
		return "", nil
	}
	return value, err
}

func (r *DriveRepository) RefreshWOPILock(ctx context.Context, id uuid.UUID, value string, ttl time.Duration) (bool, error) {
	result, err := r.db.Exec(ctx, `UPDATE drive_wopi_locks SET expires_at=NOW()+($3 * INTERVAL '1 second'),updated_at=NOW()
WHERE file_id=$1 AND lock_value=$2 AND expires_at>NOW()`, id, value, ttl.Seconds())
	return err == nil && result.RowsAffected() == 1, err
}

func (r *DriveRepository) ReplaceWOPILock(ctx context.Context, id uuid.UUID, oldValue, newValue string, ttl time.Duration) (string, bool, error) {
	var current string
	err := r.db.QueryRow(ctx, `UPDATE drive_wopi_locks SET lock_value=$3,expires_at=NOW()+($4 * INTERVAL '1 second'),updated_at=NOW()
WHERE file_id=$1 AND lock_value=$2 AND expires_at>NOW() RETURNING lock_value`, id, oldValue, newValue, ttl.Seconds()).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		current, readErr := r.GetWOPILock(ctx, id)
		return current, false, readErr
	}
	return current, err == nil, err
}

func (r *DriveRepository) ReleaseWOPILock(ctx context.Context, id uuid.UUID, value string) (bool, error) {
	result, err := r.db.Exec(ctx, `DELETE FROM drive_wopi_locks WHERE file_id=$1 AND lock_value=$2`, id, value)
	return err == nil && result.RowsAffected() == 1, err
}

func (r *DriveRepository) CreateFolder(ctx context.Context, f *domain.Folder) error {
	q := `
       INSERT INTO folders (id, owner_id, parent_id, name, is_public, created_at, updated_at, deleted_at)
       VALUES ($1,$2,$3,$4,$5,$6,$7,NULL)
    `
	_, err := r.db.Exec(ctx, q, f.ID, f.OwnerID, f.ParentID, f.Name, f.IsPublic, f.CreatedAt, f.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create folder: %w", err)
	}
	return nil
}

func (r *DriveRepository) FolderNameExistsInParent(ctx context.Context, ownerID int64, parentID *uuid.UUID, name string, excludeID *uuid.UUID) (bool, error) {
	q := `
       SELECT EXISTS(
           SELECT 1
           FROM folders
           WHERE parent_id IS NOT DISTINCT FROM $1
             AND LOWER(BTRIM(name)) = LOWER(BTRIM($2))
             AND deleted_at IS NULL
             AND ($3::uuid IS NULL OR id <> $3)
             AND ($1::uuid IS NOT NULL OR owner_id = $4)
       )
    `

	var exists bool
	if err := r.db.QueryRow(ctx, q, parentID, name, excludeID, ownerID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check folder name exists in parent: %w", err)
	}
	return exists, nil
}

func (r *DriveRepository) GetFolderByID(ctx context.Context, id uuid.UUID) (*domain.Folder, error) {
	q := `
       SELECT f.id, f.owner_id, f.parent_id, f.name, f.is_public, f.created_at, f.updated_at, f.deleted_at,
              (
                  WITH RECURSIVE folder_tree AS (
                      SELECT id FROM folders WHERE id = f.id AND deleted_at IS NULL
                      UNION ALL
                      SELECT child.id FROM folders child JOIN folder_tree ON child.parent_id = folder_tree.id WHERE child.deleted_at IS NULL
                  )
                  SELECT COUNT(*) FROM files WHERE folder_id IN (SELECT id FROM folder_tree) AND deleted_at IS NULL AND scope = 'drive'
              ) AS files_count,
              EXISTS(SELECT 1 FROM folders WHERE parent_id = f.id AND deleted_at IS NULL) AS has_subfolders
       FROM folders f
       WHERE f.id=$1
    `
	var f domain.Folder
	err := r.db.QueryRow(ctx, q, id).Scan(
		&f.ID, &f.OwnerID, &f.ParentID, &f.Name, &f.IsPublic,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.FilesCount, &f.HasSubfolders,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("folder not found")
		}
		return nil, fmt.Errorf("get folder: %w", err)
	}
	return &f, nil
}

func (r *DriveRepository) ListFoldersByParent(ctx context.Context, parentID *uuid.UUID) ([]*domain.Folder, error) {
	q := `
       SELECT f.id, f.owner_id, f.parent_id, f.name, f.is_public, f.created_at, f.updated_at, f.deleted_at,
              (
                  WITH RECURSIVE folder_tree AS (
                      SELECT id FROM folders WHERE id = f.id AND deleted_at IS NULL
                      UNION ALL
                      SELECT child.id FROM folders child JOIN folder_tree ON child.parent_id = folder_tree.id WHERE child.deleted_at IS NULL
                  )
                  SELECT COUNT(*) FROM files WHERE folder_id IN (SELECT id FROM folder_tree) AND deleted_at IS NULL AND scope = 'drive'
              ) AS files_count,
              EXISTS(SELECT 1 FROM folders WHERE parent_id = f.id AND deleted_at IS NULL) AS has_subfolders
       FROM folders f
       WHERE f.parent_id IS NOT DISTINCT FROM $1
         AND f.deleted_at IS NULL
       ORDER BY f.name ASC
    `
	rows, err := r.db.Query(ctx, q, parentID)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer rows.Close()

	var out []*domain.Folder
	for rows.Next() {
		var f domain.Folder
		if err := rows.Scan(&f.ID, &f.OwnerID, &f.ParentID, &f.Name, &f.IsPublic, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.FilesCount, &f.HasSubfolders); err != nil {
			continue
		}
		out = append(out, &f)
	}
	return out, nil
}

func (r *DriveRepository) UpdateFolder(ctx context.Context, id uuid.UUID, name *string, parentID *uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockFolderDestination(ctx, tx, id, parentID); err != nil {
		return err
	}

	q := `
       UPDATE folders
       SET
          name       = COALESCE($2, name),
          parent_id  = COALESCE($3, parent_id),
          updated_at = $4
       WHERE id=$1 AND deleted_at IS NULL
    `
	res, err := tx.Exec(ctx, q, id, name, parentID, time.Now())
	if err != nil {
		return fmt.Errorf("update folder: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("folder not found")
	}
	return tx.Commit(ctx)
}

func (r *DriveRepository) MoveFolder(ctx context.Context, id uuid.UUID, parentID *uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockFolderDestination(ctx, tx, id, parentID); err != nil {
		return err
	}

	q := `
       UPDATE folders
       SET
          parent_id  = $2,
          updated_at = $3
       WHERE id=$1 AND deleted_at IS NULL
    `
	res, err := tx.Exec(ctx, q, id, parentID, time.Now())
	if err != nil {
		return fmt.Errorf("move folder: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("folder not found")
	}
	return tx.Commit(ctx)
}

func (r *DriveRepository) SetFolderPublicStatus(ctx context.Context, id uuid.UUID, isPublic bool) error {
	q := `UPDATE folders SET is_public=$2, updated_at=$3 WHERE id=$1 AND deleted_at IS NULL`
	_, err := r.db.Exec(ctx, q, id, isPublic, time.Now())
	if err != nil {
		return fmt.Errorf("set folder public status: %w", err)
	}
	return nil
}

func (r *DriveRepository) SoftDeleteFolder(ctx context.Context, id uuid.UUID) error {
	q := `UPDATE folders SET deleted_at=$2, updated_at=$2 WHERE id=$1 AND deleted_at IS NULL`
	now := time.Now()
	res, err := r.db.Exec(ctx, q, id, now)
	if err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}

func (r *DriveRepository) GetFileByID(ctx context.Context, id uuid.UUID) (*domain.DriveFile, error) {
	q := `
       SELECT id, owner_id, folder_id, real_name, object_name, COALESCE(content_hash, ''), source_system_code, source_system_name,
              external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE id=$1
    `
	var f domain.DriveFile
	err := r.db.QueryRow(ctx, q, id).Scan(
		&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.ContentHash, &f.SourceSystemCode, &f.SourceSystemName,
		&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
		&f.Key,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
	)
	if isUndefinedColumnErr(err, "content_hash") {
		legacyQ := `
       SELECT id, owner_id, folder_id, real_name, object_name, NULL::text AS source_system_code,
              NULL::text AS source_system_name, NULL::text AS external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE id=$1
    `
		err = r.db.QueryRow(ctx, legacyQ, id).Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.SourceSystemCode, &f.SourceSystemName,
			&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
			&f.Key,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
		)
		if err == nil {
			f.ContentHash = ""
		}
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("file not found")
		}
		return nil, fmt.Errorf("get file: %w", err)
	}
	return &f, nil
}

func (r *DriveRepository) GetFileByObjectName(ctx context.Context, objectName string) (*domain.DriveFile, error) {
	q := `
       SELECT id, owner_id, folder_id, real_name, object_name, COALESCE(content_hash, ''), source_system_code, source_system_name,
              external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE object_name=$1 AND deleted_at IS NULL
       LIMIT 1
    `
	var f domain.DriveFile
	err := r.db.QueryRow(ctx, q, objectName).Scan(
		&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.ContentHash, &f.SourceSystemCode, &f.SourceSystemName,
		&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
		&f.Key,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
	)
	if isUndefinedColumnErr(err, "content_hash") {
		legacyQ := `
       SELECT id, owner_id, folder_id, real_name, object_name, NULL::text AS source_system_code,
              NULL::text AS source_system_name, NULL::text AS external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE object_name=$1 AND deleted_at IS NULL
       LIMIT 1
    `
		err = r.db.QueryRow(ctx, legacyQ, objectName).Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.SourceSystemCode, &f.SourceSystemName,
			&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
			&f.Key,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
		)
		if err == nil {
			f.ContentHash = ""
		}
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("file not found")
		}
		return nil, fmt.Errorf("get file by object_name: %w", err)
	}
	return &f, nil
}

func isUndefinedColumnErr(err error, column string) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42703" && strings.Contains(pgErr.Message, column)
}

func (r *DriveRepository) GetActiveDriveFilesByIDs(ctx context.Context, ids []uuid.UUID) ([]*domain.DriveFile, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	q := `
       SELECT id, owner_id, folder_id, real_name, object_name, source_system_code, source_system_name,
              external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE id = ANY($1)
         AND scope = 'drive'
         AND deleted_at IS NULL
    `
	rows, err := r.db.Query(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("get active drive files by ids: %w", err)
	}
	defer rows.Close()

	var out []*domain.DriveFile
	for rows.Next() {
		var f domain.DriveFile
		if err := rows.Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.SourceSystemCode, &f.SourceSystemName,
			&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
			&f.Key,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active drive file by ids: %w", err)
		}
		out = append(out, &f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active drive files by ids: %w", err)
	}

	return out, nil
}

// The live ACL policy shared by Drive content-search permission checks. The existing
// ID-check API rejects empty lists; corpus search uses an empty list for all
// accessible files, never for all users' contents.
const driveFileAccessCTE = `
       WITH RECURSIVE requested AS (
           SELECT id AS file_id FROM files
           WHERE scope='drive' AND deleted_at IS NULL
           AND (cardinality($1::uuid[])=0 OR id=ANY($1::uuid[]))
       ),
       file_meta AS (
           SELECT f.id, f.owner_id, f.folder_id
           FROM files f
           JOIN requested r ON r.file_id = f.id
           WHERE f.scope = 'drive'
             AND f.deleted_at IS NULL
       ),
       folder_hierarchy AS (
           SELECT fm.id AS file_id, fd.id, fd.parent_id, fd.owner_id
           FROM file_meta fm
           JOIN folders fd ON fd.id = fm.folder_id
           WHERE fd.deleted_at IS NULL

           UNION

           SELECT fh.file_id, parent.id, parent.parent_id, parent.owner_id
           FROM folders parent
           JOIN folder_hierarchy fh ON parent.id = fh.parent_id
           WHERE parent.deleted_at IS NULL
       ),
       accessible AS (
           SELECT fm.id AS file_id
           FROM file_meta fm
           WHERE fm.owner_id = $2

           UNION

           SELECT fm.id AS file_id
           FROM file_meta fm
           JOIN permissions p
             ON p.resource_type = 'file'
            AND p.resource_id = fm.id
            AND p.user_id = $2

           UNION

           SELECT fh.file_id
           FROM folder_hierarchy fh
           JOIN permissions p
             ON p.resource_type = 'folder'
            AND p.resource_id = fh.id
            AND p.user_id = $2

           UNION

           SELECT fh.file_id
           FROM folder_hierarchy fh
           WHERE fh.owner_id = $2
       )
`

func (r *DriveRepository) ListAccessibleDriveFileIDs(ctx context.Context, userID int64, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	q := driveFileAccessCTE + ` SELECT DISTINCT file_id FROM accessible`
	rows, err := r.db.Query(ctx, q, ids, userID)
	if err != nil {
		return nil, fmt.Errorf("list accessible drive file ids: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan accessible drive file id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accessible drive file ids: %w", err)
	}

	return out, nil
}

func (r *DriveRepository) ListFilesByFolder(ctx context.Context, folderID *uuid.UUID) ([]*domain.DriveFile, error) {
	q := `
       SELECT id, owner_id, folder_id, real_name, object_name, source_system_code, source_system_name,
              external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE folder_id IS NOT DISTINCT FROM $1
         AND scope = 'drive'
         AND deleted_at IS NULL
       ORDER BY real_name ASC
    `
	rows, err := r.db.Query(ctx, q, folderID)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var out []*domain.DriveFile
	for rows.Next() {
		var f domain.DriveFile
		if err := rows.Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.SourceSystemCode, &f.SourceSystemName,
			&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
			&f.Key,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
		); err != nil {
			continue
		}
		out = append(out, &f)
	}
	return out, nil
}

func (r *DriveRepository) ListFileIDsInFolderTree(ctx context.Context, folderID uuid.UUID) ([]uuid.UUID, error) {
	q := `
       WITH RECURSIVE folder_tree AS (
           SELECT id
           FROM folders
           WHERE id = $1 AND deleted_at IS NULL
           UNION ALL
           SELECT child.id
           FROM folders child
           JOIN folder_tree parent ON child.parent_id = parent.id
           WHERE child.deleted_at IS NULL
       )
       SELECT id
       FROM files
       WHERE folder_id IN (SELECT id FROM folder_tree)
         AND scope = 'drive'
         AND deleted_at IS NULL
    `
	rows, err := r.db.Query(ctx, q, folderID)
	if err != nil {
		return nil, fmt.Errorf("list file ids in folder tree: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan file id in folder tree: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file ids in folder tree: %w", err)
	}
	return ids, nil
}

func (r *DriveRepository) CreateFileWithInitialVersion(ctx context.Context, file *domain.DriveFile, createdBy int64) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q1 := `
       INSERT INTO files (
          id, owner_id, folder_id, real_name, object_name, content_hash, size, mime_type, version, scope, is_public,
          onlyoffice_key, source_system_code, source_system_name, external_message_id, created_at, updated_at, deleted_at
        )
       VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,NULL)
     `
	_, err = tx.Exec(ctx, q1,
		file.ID, file.OwnerID, file.FolderID, file.RealName, file.ObjectName,
		file.ContentHash, file.Size, file.MimeType, file.Version, file.Scope, file.IsPublic,
		file.Key, file.SourceSystemCode, file.SourceSystemName, file.ExternalMessageID, file.CreatedAt, file.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert file: %w", err)
	}

	q2 := `
       INSERT INTO file_versions (id, file_id, version, object_name, size, created_by, created_at)
       VALUES ($1,$2,$3,$4,$5,$6,$7)
    `
	_, err = tx.Exec(ctx, q2,
		uuid.New(), file.ID, file.Version, file.ObjectName, file.Size, createdBy, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert file version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (r *DriveRepository) FindDuplicateDriveFileInTarget(ctx context.Context, ownerID int64, folderID *uuid.UUID, contentHash string) (*domain.DriveFile, error) {
	if strings.TrimSpace(contentHash) == "" {
		return nil, nil
	}

	q := `
       SELECT id, owner_id, folder_id, real_name, object_name, COALESCE(content_hash, ''), source_system_code, source_system_name,
              external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE scope = 'drive'
         AND deleted_at IS NULL
         AND content_hash = $1
         AND (
              (folder_id IS NOT DISTINCT FROM $2 AND $2 IS NOT NULL)
              OR
              (folder_id IS NULL AND $2 IS NULL AND owner_id = $3)
         )
       ORDER BY created_at ASC
       LIMIT 1
    `

	var f domain.DriveFile
	err := r.db.QueryRow(ctx, q, contentHash, folderID, ownerID).Scan(
		&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.ContentHash, &f.SourceSystemCode, &f.SourceSystemName,
		&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
		&f.Key,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find duplicate drive file in target: %w", err)
	}
	return &f, nil
}

func (r *DriveRepository) UpdateFile(ctx context.Context, id uuid.UUID, name *string, folderID *uuid.UUID) error {
	q := `
       UPDATE files
       SET
          real_name  = COALESCE($2, real_name),
          folder_id  = COALESCE($3, folder_id),
          updated_at = $4
       WHERE id=$1 AND deleted_at IS NULL
    `
	res, err := r.db.Exec(ctx, q, id, name, folderID, time.Now())
	if err != nil {
		return fmt.Errorf("update file: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("file not found")
	}
	return nil
}

func (r *DriveRepository) MoveFile(ctx context.Context, id uuid.UUID, folderID *uuid.UUID) error {
	q := `
       UPDATE files
       SET
          folder_id  = $2,
          updated_at = $3
       WHERE id=$1 AND deleted_at IS NULL
    `
	res, err := r.db.Exec(ctx, q, id, folderID, time.Now())
	if err != nil {
		return fmt.Errorf("move file: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("file not found")
	}
	return nil
}

func (r *DriveRepository) SetFilePublicStatus(ctx context.Context, id uuid.UUID, isPublic bool) error {
	q := `UPDATE files SET is_public=$2, updated_at=$3 WHERE id=$1 AND deleted_at IS NULL`
	_, err := r.db.Exec(ctx, q, id, isPublic, time.Now())
	if err != nil {
		return fmt.Errorf("set file public status: %w", err)
	}
	return nil
}

func (r *DriveRepository) SoftDeleteFile(ctx context.Context, id uuid.UUID) error {
	q := `UPDATE files SET deleted_at=$2, updated_at=$2 WHERE id=$1 AND deleted_at IS NULL`
	now := time.Now()
	res, err := r.db.Exec(ctx, q, id, now)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("file not found")
	}
	return nil
}

func (r *DriveRepository) UpdateFileContentNewVersion(ctx context.Context, fileID uuid.UUID, newObject string, newSize int64, editorID int64, onlyOfficeKey string) (int, error) {
	return r.AppendFileVersion(ctx, fileID, newObject, newSize, editorID, 0)
}

func (r *DriveRepository) GetPermissionRole(ctx context.Context, t domain.ResourceType, rid uuid.UUID, userID int64) (*domain.PermissionRole, error) {
	q := `SELECT role FROM permissions WHERE resource_type=$1 AND resource_id=$2 AND user_id=$3`
	var role domain.PermissionRole
	err := r.db.QueryRow(ctx, q, t, rid, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get permission: %w", err)
	}
	return &role, nil
}

func (r *DriveRepository) UpsertPermission(ctx context.Context, p *domain.Permission) error {
	q := `
       INSERT INTO permissions (resource_type, resource_id, user_id, role, created_at)
       VALUES ($1,$2,$3,$4,$5)
       ON CONFLICT (resource_type, resource_id, user_id)
       DO UPDATE SET role=EXCLUDED.role
    `
	_, err := r.db.Exec(ctx, q, p.ResourceType, p.ResourceID, p.UserID, p.Role, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert permission: %w", err)
	}
	return nil
}

func (r *DriveRepository) UpsertPermissionsBulk(ctx context.Context, t domain.ResourceType, rid uuid.UUID, userIDs []int64, role domain.PermissionRole, createdAt time.Time) error {
	if len(userIDs) == 0 {
		return nil
	}

	q := `
       INSERT INTO permissions (resource_type, resource_id, user_id, role, created_at)
       SELECT $1, $2, user_id, $3, $4
       FROM unnest($5::bigint[]) AS user_id
       ON CONFLICT (resource_type, resource_id, user_id)
       DO UPDATE SET role=EXCLUDED.role
    `
	_, err := r.db.Exec(ctx, q, t, rid, role, createdAt, userIDs)
	if err != nil {
		return fmt.Errorf("upsert permissions bulk: %w", err)
	}
	return nil
}

func (r *DriveRepository) DeletePermission(ctx context.Context, t domain.ResourceType, rid uuid.UUID, userID int64) error {
	q := `DELETE FROM permissions WHERE resource_type=$1 AND resource_id=$2 AND user_id=$3`
	_, err := r.db.Exec(ctx, q, t, rid, userID)
	if err != nil {
		return fmt.Errorf("delete permission: %w", err)
	}
	return nil
}

func (r *DriveRepository) DeletePermissionsBulk(ctx context.Context, t domain.ResourceType, rid uuid.UUID, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	q := `DELETE FROM permissions WHERE resource_type=$1 AND resource_id=$2 AND user_id = ANY($3::bigint[])`
	_, err := r.db.Exec(ctx, q, t, rid, userIDs)
	if err != nil {
		return fmt.Errorf("delete permissions bulk: %w", err)
	}
	return nil
}

func (r *DriveRepository) UserExists(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL)`, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check user exists: %w", err)
	}
	return exists, nil
}

func (r *DriveRepository) TransferFileOwnership(ctx context.Context, fileID uuid.UUID, currentOwnerID, newOwnerID int64, previousOwnerRole domain.PermissionRole) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now()

	res, err := tx.Exec(ctx, `
		UPDATE files
		SET owner_id=$2, updated_at=$3
		WHERE id=$1 AND owner_id=$4 AND deleted_at IS NULL
	`, fileID, newOwnerID, now, currentOwnerID)
	if err != nil {
		return fmt.Errorf("transfer file ownership: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("file not found")
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM permissions
		WHERE resource_type='file' AND resource_id=$1 AND user_id=$2
	`, fileID, newOwnerID); err != nil {
		return fmt.Errorf("cleanup new owner file permission: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO permissions (resource_type, resource_id, user_id, role, created_at)
		VALUES ('file', $1, $2, $3, $4)
		ON CONFLICT (resource_type, resource_id, user_id)
		DO UPDATE SET role=EXCLUDED.role
	`, fileID, currentOwnerID, previousOwnerRole, now); err != nil {
		return fmt.Errorf("grant previous owner file permission: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (r *DriveRepository) TransferFolderOwnership(ctx context.Context, folderID uuid.UUID, currentOwnerID, newOwnerID int64, previousOwnerRole domain.PermissionRole) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now()

	var rootExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM folders
			WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL
		)
	`, folderID, currentOwnerID).Scan(&rootExists); err != nil {
		return fmt.Errorf("check folder ownership: %w", err)
	}
	if !rootExists {
		return fmt.Errorf("folder not found")
	}

	_, err = tx.Exec(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id
			FROM folders
			WHERE id=$1 AND deleted_at IS NULL
			UNION ALL
			SELECT f.id
			FROM folders f
			JOIN subtree s ON f.parent_id = s.id
			WHERE f.deleted_at IS NULL
		)
		UPDATE folders
		SET owner_id=$2, updated_at=$3
		WHERE id IN (SELECT id FROM subtree)
		  AND owner_id=$4
		  AND deleted_at IS NULL
	`, folderID, newOwnerID, now, currentOwnerID)
	if err != nil {
		return fmt.Errorf("transfer folder subtree ownership: %w", err)
	}

	_, err = tx.Exec(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id
			FROM folders
			WHERE id=$1 AND deleted_at IS NULL
			UNION ALL
			SELECT f.id
			FROM folders f
			JOIN subtree s ON f.parent_id = s.id
			WHERE f.deleted_at IS NULL
		)
		UPDATE files
		SET owner_id=$2, updated_at=$3
		WHERE folder_id IN (SELECT id FROM subtree)
		  AND owner_id=$4
		  AND deleted_at IS NULL
	`, folderID, newOwnerID, now, currentOwnerID)
	if err != nil {
		return fmt.Errorf("transfer files in subtree ownership: %w", err)
	}

	_, err = tx.Exec(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id
			FROM folders
			WHERE id=$1 AND deleted_at IS NULL
			UNION ALL
			SELECT f.id
			FROM folders f
			JOIN subtree s ON f.parent_id = s.id
			WHERE f.deleted_at IS NULL
		),
		transferred_files AS (
			SELECT id
			FROM files
			WHERE folder_id IN (SELECT id FROM subtree)
			  AND owner_id=$2
			  AND deleted_at IS NULL
		)
		DELETE FROM permissions
		WHERE user_id=$3
		  AND (
			(resource_type='folder' AND resource_id IN (SELECT id FROM subtree))
			OR
			(resource_type='file' AND resource_id IN (SELECT id FROM transferred_files))
		  )
	`, folderID, newOwnerID, newOwnerID)
	if err != nil {
		return fmt.Errorf("cleanup new owner subtree permissions: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO permissions (resource_type, resource_id, user_id, role, created_at)
		VALUES ('folder', $1, $2, $3, $4)
		ON CONFLICT (resource_type, resource_id, user_id)
		DO UPDATE SET role=EXCLUDED.role
	`, folderID, currentOwnerID, previousOwnerRole, now); err != nil {
		return fmt.Errorf("grant previous owner folder permission: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

func (r *DriveRepository) ListUsersWithAccess(ctx context.Context, resourceType domain.ResourceType, resourceID uuid.UUID) ([]domain.UserPermission, error) {
	users, _, err := r.ListUsersWithAccessPaginated(ctx, resourceType, resourceID, 0, 0, "")
	return users, err
}

func (r *DriveRepository) ListUsersWithAccessPaginated(ctx context.Context, resourceType domain.ResourceType, resourceID uuid.UUID, limit, offset int, searchQuery string) ([]domain.UserPermission, int, error) {
	searchQuery = strings.TrimSpace(searchQuery)
	var args []interface{}
	args = append(args, resourceType, resourceID)

	whereClause := "WHERE u.deleted_at IS NULL"
	if searchQuery != "" {
		args = append(args, "%"+searchQuery+"%")
		argIdx := len(args)
		whereClause += fmt.Sprintf(" AND (u.first_name ILIKE $%d OR u.last_name ILIKE $%d OR u.email ILIKE $%d OR u.username ILIKE $%d)", argIdx, argIdx, argIdx, argIdx)
	}

	limitOffsetClause := ""
	if limit > 0 {
		args = append(args, limit)
		limitIdx := len(args)
		args = append(args, offset)
		offsetIdx := len(args)
		limitOffsetClause = fmt.Sprintf(" LIMIT $%d OFFSET $%d", limitIdx, offsetIdx)
	}

	q := fmt.Sprintf(`
       WITH RECURSIVE folder_hierarchy AS (
           SELECT id, parent_id, owner_id FROM folders 
           WHERE id = (
                 CASE 
                    WHEN $1 = 'file' THEN (SELECT folder_id FROM files WHERE id = $2)
                    ELSE $2
                END
           )
           UNION ALL
           SELECT f.id, f.parent_id, f.owner_id
           FROM folders f
           JOIN folder_hierarchy fh ON f.id = fh.parent_id
       ),
       all_permissions AS (
           SELECT user_id, role FROM permissions WHERE resource_type = $1 AND resource_id = $2
           UNION
           SELECT user_id, role FROM permissions 
           WHERE resource_type = 'folder' 
             AND resource_id IN (SELECT id FROM folder_hierarchy)
           UNION
           SELECT owner_id, 'owner'::text as role FROM (
               SELECT owner_id FROM folders WHERE id = $2 AND $1 = 'folder'
               UNION
               SELECT owner_id FROM files WHERE id = $2 AND $1 = 'file'
           ) as o
           UNION
           SELECT owner_id, 'owner'::text as role FROM folder_hierarchy
       )
       SELECT u.id, u.email, u.username, u.first_name, u.last_name, u.avatar_url, 
              (array_agg(ap.role ORDER BY CASE 
                  WHEN ap.role = 'owner' THEN 3 
                  WHEN ap.role = 'editor' THEN 2 
                  WHEN ap.role = 'viewer' THEN 1 
                  ELSE 0 END DESC))[1] as role,
              COUNT(*) OVER() as total_count
       FROM users u
       JOIN all_permissions ap ON u.id = ap.user_id
       %s
       GROUP BY u.id, u.email, u.username, u.first_name, u.last_name, u.avatar_url
       ORDER BY u.first_name, u.last_name
       %s
    `, whereClause, limitOffsetClause)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list users with access paginated: %w", err)
	}
	defer rows.Close()

	var users []domain.UserPermission
	totalCount := 0
	for rows.Next() {
		var up domain.UserPermission
		var total int
		err := rows.Scan(
			&up.ID, &up.Email, &up.Username, &up.FirstName, &up.LastName,
			&up.AvatarURL, &up.Role, &total,
		)
		if err != nil {
			continue
		}
		totalCount = total
		users = append(users, up)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users with access: %w", err)
	}
	return users, totalCount, nil
}

func (r *DriveRepository) ListFolderAccessReportEntries(ctx context.Context, rootFolderID uuid.UUID) ([]domain.FolderAccessReportEntry, error) {
	q := `
       WITH RECURSIVE root_path AS (
           SELECT
               f.id,
               f.parent_id,
               f.name,
               f.name::text AS full_path,
               0 AS depth
           FROM folders f
           WHERE f.id = $1
             AND f.deleted_at IS NULL

           UNION ALL

           SELECT
               parent.id,
               parent.parent_id,
               parent.name,
               (parent.name || ' / ' || rp.full_path)::text AS full_path,
               rp.depth + 1
           FROM root_path rp
           JOIN folders parent ON parent.id = rp.parent_id
           WHERE parent.deleted_at IS NULL
       ),
       selected_root AS (
           SELECT full_path
           FROM root_path
           ORDER BY depth DESC
           LIMIT 1
       ),
       folder_tree AS (
           SELECT
               f.id,
               f.parent_id,
               f.owner_id,
               f.name,
               sr.full_path::text AS path
           FROM folders f
           CROSS JOIN selected_root sr
           WHERE f.id = $1
             AND f.deleted_at IS NULL

           UNION ALL

           SELECT
               child.id,
               child.parent_id,
               child.owner_id,
               child.name,
               (ft.path || ' / ' || child.name)::text AS path
           FROM folders child
           JOIN folder_tree ft ON child.parent_id = ft.id
           WHERE child.deleted_at IS NULL
       ),
       folder_ancestors AS (
           SELECT
               ft.id AS folder_id,
               ft.id AS ancestor_id
           FROM folder_tree ft

           UNION ALL

           SELECT
               fa.folder_id,
               parent.parent_id AS ancestor_id
           FROM folder_ancestors fa
           JOIN folders parent ON parent.id = fa.ancestor_id
           WHERE parent.parent_id IS NOT NULL
             AND parent.deleted_at IS NULL
       ),
       folder_permissions AS (
           SELECT
               ft.id AS folder_id,
               ft.name AS folder_name,
               ft.path AS folder_path,
               p.user_id,
               p.role
           FROM folder_tree ft
           JOIN permissions p
             ON p.resource_type = 'folder'
            AND p.resource_id = ft.id

           UNION ALL

           SELECT
               ft.id AS folder_id,
               ft.name AS folder_name,
               ft.path AS folder_path,
               p.user_id,
               p.role
           FROM folder_tree ft
           JOIN folder_ancestors fa ON fa.folder_id = ft.id
           JOIN permissions p
             ON p.resource_type = 'folder'
            AND p.resource_id = fa.ancestor_id
           WHERE fa.ancestor_id <> ft.id

           UNION ALL

           SELECT
               ft.id AS folder_id,
               ft.name AS folder_name,
               ft.path AS folder_path,
               ft.owner_id AS user_id,
               'owner'::text AS role
           FROM folder_tree ft

           UNION ALL

           SELECT
               ft.id AS folder_id,
               ft.name AS folder_name,
               ft.path AS folder_path,
               ancestor.owner_id AS user_id,
               'owner'::text AS role
           FROM folder_tree ft
           JOIN folder_ancestors fa ON fa.folder_id = ft.id
           JOIN folders ancestor ON ancestor.id = fa.ancestor_id
           WHERE fa.ancestor_id <> ft.id
             AND ancestor.deleted_at IS NULL
       ),
       ranked_permissions AS (
           SELECT
               fp.folder_id,
               fp.folder_name,
               fp.folder_path,
               fp.user_id,
               fp.role,
               ROW_NUMBER() OVER (
                   PARTITION BY fp.folder_id, fp.user_id
                   ORDER BY CASE
                       WHEN fp.role = 'owner' THEN 3
                       WHEN fp.role = 'editor' THEN 2
                       WHEN fp.role = 'viewer' THEN 1
                       ELSE 0
                   END DESC
               ) AS rn
           FROM folder_permissions fp
       )
        SELECT
            rp.folder_id,
            rp.folder_name,
            rp.folder_path,
            u.id,
            COALESCE(NULLIF(TRIM(u.full_name_local), ''), NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), u.username) AS full_name,
            u.email,
            u.username,
            u.first_name,
            u.last_name,
            rp.role
       FROM ranked_permissions rp
       JOIN users u ON u.id = rp.user_id
       WHERE rp.rn = 1
         AND u.deleted_at IS NULL
        GROUP BY
            rp.folder_id,
            rp.folder_name,
            rp.folder_path,
            u.id,
            u.full_name_local,
            u.email,
            u.username,
            u.first_name,
            u.last_name,
            rp.role
        ORDER BY rp.folder_path ASC, u.first_name ASC, u.last_name ASC, u.email ASC
     `

	rows, err := r.db.Query(ctx, q, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("list folder access report entries: %w", err)
	}
	defer rows.Close()

	var entries []domain.FolderAccessReportEntry
	for rows.Next() {
		var entry domain.FolderAccessReportEntry
		if err := rows.Scan(
			&entry.FolderID,
			&entry.FolderName,
			&entry.FolderPath,
			&entry.UserID,
			&entry.FullName,
			&entry.Email,
			&entry.Username,
			&entry.FirstName,
			&entry.LastName,
			&entry.Role,
		); err != nil {
			return nil, fmt.Errorf("scan folder access report entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate folder access report entries: %w", err)
	}

	return entries, nil
}

func (r *DriveRepository) ListUsersWithoutAccessPaginated(
	ctx context.Context,
	requestingUserID int64,
	resourceType domain.ResourceType,
	resourceID uuid.UUID,
	query string,
	limit, offset int,
) ([]domain.UserBasic, int, error) {
	sqlQuery := `
        WITH RECURSIVE folder_hierarchy AS (
            SELECT id, parent_id, owner_id FROM folders 
            WHERE id = (
               CASE 
                   WHEN $1 = 'file' THEN (SELECT folder_id FROM files WHERE id = $2)
                   ELSE $2
               END
           )
           UNION ALL
           SELECT f.id, f.parent_id, f.owner_id
           FROM folders f
           JOIN folder_hierarchy fh ON f.id = fh.parent_id
       ),
        users_with_access AS (
           -- Direct
           SELECT user_id FROM permissions WHERE resource_type = $1 AND resource_id = $2
           UNION
           -- Inherited
           SELECT user_id FROM permissions WHERE resource_type = 'folder' AND resource_id IN (SELECT id FROM folder_hierarchy)
           UNION
           -- Owner
           SELECT owner_id FROM folders WHERE id = $2 AND $1 = 'folder'
           UNION
           SELECT owner_id FROM files WHERE id = $2 AND $1 = 'file'
           UNION
           -- Parent Owners
            SELECT owner_id FROM folder_hierarchy
        )
        SELECT u.id, u.email, u.username, u.first_name, u.last_name, u.avatar_url,
               COUNT(*) OVER() AS total_count
        FROM users u
        WHERE u.deleted_at IS NULL
          AND u.id NOT IN (SELECT user_id FROM users_with_access)
     `
	args := []interface{}{resourceType, resourceID}

	var role string
	var organizationID uuid.NullUUID
	err := r.db.QueryRow(ctx, "SELECT role, organization_id FROM users WHERE id = $1", requestingUserID).Scan(&role, &organizationID)
	if err != nil {
		return nil, 0, fmt.Errorf("get requesting user: %w", err)
	}

	if role != "admin" {
		if !organizationID.Valid {
			sqlQuery += " AND FALSE"
		} else {
			sqlQuery += fmt.Sprintf(" AND u.organization_id = $%d", len(args)+1)
			args = append(args, organizationID.UUID)
		}
	}

	if searchCondition, searchArgs, ok := buildUserSearchCondition(query, len(args)+1, userSearchFields{
		nameParts: []string{"u.first_name", "u.last_name"},
		fields:    []string{"u.username", "u.email", "u.iin", "u.full_name_local"},
	}); ok {
		sqlQuery += " AND " + searchCondition
		args = append(args, searchArgs...)
	}

	sqlQuery += `
        ORDER BY u.first_name, u.last_name
     `
	if limit > 0 {
		args = append(args, limit, offset)
		limitArgIdx := len(args) - 1
		offsetArgIdx := len(args)
		sqlQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", limitArgIdx, offsetArgIdx)
	}

	rows, err := r.db.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list users without access paginated: %w", err)
	}
	defer rows.Close()

	var users []domain.UserBasic
	totalCount := 0
	for rows.Next() {
		var ub domain.UserBasic
		var total int
		err := rows.Scan(
			&ub.ID, &ub.Email, &ub.Username, &ub.FirstName, &ub.LastName,
			&ub.AvatarURL, &total,
		)
		if err != nil {
			continue
		}
		totalCount = total
		users = append(users, ub)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users without access: %w", err)
	}

	return users, totalCount, nil
}

func (r *DriveRepository) ListRootFoldersByOwner(ctx context.Context, ownerID int64) ([]*domain.Folder, error) {
	return r.ListRootFoldersByOwnerPaginated(ctx, ownerID, -1, 0)
}

func (r *DriveRepository) ListRootFilesByOwner(ctx context.Context, ownerID int64) ([]*domain.DriveFile, error) {
	return r.ListRootFilesByOwnerPaginated(ctx, ownerID, -1, 0)
}

func (r *DriveRepository) ListSharedFolders(ctx context.Context, userID int64) ([]*domain.Folder, error) {
	return r.ListSharedFoldersPaginated(ctx, userID, -1, 0)
}

func (r *DriveRepository) ListSharedFiles(ctx context.Context, userID int64) ([]*domain.DriveFile, error) {
	return r.ListSharedFilesPaginated(ctx, userID, -1, 0)
}

func (r *DriveRepository) ListRootFoldersByOwnerPaginated(ctx context.Context, ownerID int64, limit, offset int) ([]*domain.Folder, error) {
	q := `
       SELECT f.id, f.owner_id, f.parent_id, f.name, f.is_public, f.created_at, f.updated_at, f.deleted_at,
              (
                  WITH RECURSIVE folder_tree AS (
                      SELECT id FROM folders WHERE id = f.id AND deleted_at IS NULL
                      UNION ALL
                      SELECT child.id FROM folders child JOIN folder_tree ON child.parent_id = folder_tree.id WHERE child.deleted_at IS NULL
                  )
                  SELECT COUNT(*) FROM files WHERE folder_id IN (SELECT id FROM folder_tree) AND deleted_at IS NULL AND scope = 'drive'
              ) AS files_count,
              EXISTS(SELECT 1 FROM folders WHERE parent_id = f.id AND deleted_at IS NULL) AS has_subfolders
       FROM folders f
       WHERE f.parent_id IS NULL
         AND f.owner_id = $1
         AND f.deleted_at IS NULL
       ORDER BY f.updated_at DESC
    `

	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := r.db.Query(ctx, q, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list root folders: %w", err)
	}
	defer rows.Close()

	var out []*domain.Folder
	for rows.Next() {
		var f domain.Folder
		if err := rows.Scan(&f.ID, &f.OwnerID, &f.ParentID, &f.Name, &f.IsPublic, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.FilesCount, &f.HasSubfolders); err != nil {
			continue
		}
		out = append(out, &f)
	}
	return out, nil
}

func (r *DriveRepository) ListRootFilesByOwnerPaginated(ctx context.Context, ownerID int64, limit, offset int) ([]*domain.DriveFile, error) {
	q := `
       SELECT id, owner_id, folder_id, real_name, object_name, source_system_code, source_system_name,
              external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE folder_id IS NULL
         AND owner_id = $1
         AND scope = 'drive'
         AND deleted_at IS NULL
       ORDER BY updated_at DESC
    `

	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := r.db.Query(ctx, q, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list root files: %w", err)
	}
	defer rows.Close()

	var out []*domain.DriveFile
	for rows.Next() {
		var f domain.DriveFile
		if err := rows.Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.SourceSystemCode, &f.SourceSystemName,
			&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
			&f.Key,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
		); err != nil {
			continue
		}
		out = append(out, &f)
	}
	return out, nil
}

func (r *DriveRepository) ListSharedFoldersPaginated(ctx context.Context, userID int64, limit, offset int) ([]*domain.Folder, error) {
	q := `
       SELECT 
           f.id, f.owner_id, f.parent_id, f.name, f.is_public, f.created_at, f.updated_at, f.deleted_at,
           COALESCE(u.first_name || ' ' || u.last_name, u.username, '') as owner_fio,
           (
               WITH RECURSIVE folder_tree AS (
                   SELECT id FROM folders WHERE id = f.id AND deleted_at IS NULL
                   UNION ALL
                   SELECT child.id FROM folders child JOIN folder_tree ON child.parent_id = folder_tree.id WHERE child.deleted_at IS NULL
               )
               SELECT COUNT(*) FROM files WHERE folder_id IN (SELECT id FROM folder_tree) AND deleted_at IS NULL AND scope = 'drive'
           ) AS files_count,
           EXISTS(SELECT 1 FROM folders WHERE parent_id = f.id AND deleted_at IS NULL) AS has_subfolders
       FROM permissions p
       JOIN folders f ON f.id = p.resource_id
       LEFT JOIN users u ON u.id = f.owner_id
       WHERE p.resource_type = 'folder'
         AND p.user_id = $1
         AND f.deleted_at IS NULL
       ORDER BY f.name ASC
    `
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list shared folders: %w", err)
	}
	defer rows.Close()

	var out []*domain.Folder
	for rows.Next() {
		var f domain.Folder
		if err := rows.Scan(&f.ID, &f.OwnerID, &f.ParentID, &f.Name, &f.IsPublic, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.OwnerFio, &f.FilesCount, &f.HasSubfolders); err != nil {
			continue
		}
		out = append(out, &f)
	}
	return out, nil
}

func (r *DriveRepository) ListSharedFilesPaginated(ctx context.Context, userID int64, limit, offset int) ([]*domain.DriveFile, error) {
	q := `
       SELECT 
           f.id, f.owner_id, f.folder_id, f.real_name, f.object_name, f.size, f.mime_type, f.version, f.scope, f.is_public,
           f.onlyoffice_key,
           f.created_at, f.updated_at, f.deleted_at,
           COALESCE(u.first_name || ' ' || u.last_name, u.username, '') as owner_fio
       FROM permissions p
       JOIN files f ON f.id = p.resource_id
       LEFT JOIN users u ON u.id = f.owner_id
       WHERE p.resource_type = 'file'
         AND p.user_id = $1
         AND f.scope = 'drive'
         AND f.deleted_at IS NULL
       ORDER BY f.real_name ASC
    `
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list shared files: %w", err)
	}
	defer rows.Close()

	var out []*domain.DriveFile
	for rows.Next() {
		var f domain.DriveFile
		if err := rows.Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
			&f.Key,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
			&f.OwnerFio,
		); err != nil {
			continue
		}
		out = append(out, &f)
	}
	return out, nil
}

func (r *DriveRepository) ListRecentlyUpdatedDriveFiles(ctx context.Context, limit int) ([]*domain.DriveFile, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.db.Query(ctx, `
       SELECT id, owner_id, folder_id, real_name, object_name, size, mime_type, version, scope, is_public,
              onlyoffice_key,
              created_at, updated_at, deleted_at
       FROM files
       WHERE scope = 'drive'
         AND deleted_at IS NULL
       ORDER BY updated_at DESC
       LIMIT $1
    `, limit)
	if err != nil {
		return nil, fmt.Errorf("list recently updated drive files: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.DriveFile, 0, limit)
	for rows.Next() {
		var f domain.DriveFile
		if err := rows.Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic,
			&f.Key,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
		); err != nil {
			continue
		}
		out = append(out, &f)
	}

	return out, nil
}

func (r *DriveRepository) FolderHasChildren(ctx context.Context, folderID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM folders WHERE parent_id=$1 AND deleted_at IS NULL
        ) OR EXISTS (
            SELECT 1 FROM files WHERE folder_id=$1 AND deleted_at IS NULL
        )
    `, folderID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("folder has children: %w", err)
	}
	return exists, nil
}

func (r *DriveRepository) SoftDeleteFolderTree(ctx context.Context, rootID uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724619203)`); err != nil {
		return err
	}

	now := time.Now()

	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND deleted_at IS NULL)`, rootID).Scan(&ok); err != nil {
		return fmt.Errorf("check root folder: %w", err)
	}
	if !ok {
		return fmt.Errorf("folder not found")
	}

	_, err = tx.Exec(ctx, `
        WITH RECURSIVE subfolders AS (
            SELECT id
            FROM folders
            WHERE id = $1 AND deleted_at IS NULL
            UNION ALL
            SELECT f.id
            FROM folders f
            JOIN subfolders sf ON f.parent_id = sf.id
            WHERE f.deleted_at IS NULL
        )
        UPDATE folders
        SET deleted_at = $2, updated_at = $2
        WHERE id IN (SELECT id FROM subfolders)
    `, rootID, now)
	if err != nil {
		return fmt.Errorf("delete folders tree: %w", err)
	}

	_, err = tx.Exec(ctx, `
        WITH RECURSIVE subfolders AS (
            SELECT id
            FROM folders
            WHERE id = $1
            UNION ALL
            SELECT f.id
            FROM folders f
            JOIN subfolders sf ON f.parent_id = sf.id
        )
        UPDATE files
        SET deleted_at = $2, updated_at = $2
        WHERE folder_id IN (SELECT id FROM subfolders)
          AND deleted_at IS NULL
    `, rootID, now)
	if err != nil {
		return fmt.Errorf("delete files in tree: %w", err)
	}

	_, err = tx.Exec(ctx, `
        WITH RECURSIVE subfolders AS (
            SELECT id
            FROM folders
            WHERE id = $1
            UNION ALL
            SELECT f.id
            FROM folders f
            JOIN subfolders sf ON f.parent_id = sf.id
        ),
        subfiles AS (
            SELECT id FROM files WHERE folder_id IN (SELECT id FROM subfolders)
        )
        DELETE FROM permissions
        WHERE (resource_type = 'folder' AND resource_id IN (SELECT id FROM subfolders))
           OR (resource_type = 'file'   AND resource_id IN (SELECT id FROM subfiles))
    `, rootID)
	if err != nil {
		return fmt.Errorf("delete permissions in tree: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// SearchParams - структура для передачи параметров поиска в репозиторий
type SearchParams struct {
	UserID      int64  // текущий пользователь (кто делает запрос)
	Query       string // поиск по имени
	FileType    string // фильтр по типу (folder,image,video,document)
	Limit       int    // пагинация
	Offset      int    // пагинация
	OwnerUserID *int64 // опционально: фильтр по владельцу или участнику
	UserFilter  string // "owner" или "participant" - роль OwnerUserID
}

func (r *DriveRepository) SearchItems(ctx context.Context, params SearchParams) ([]domain.DriveItem, error) {
	args := []interface{}{params.UserID}
	argIdx := 2

	nameFilter := "%"
	if params.Query != "" {
		nameFilter = "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(params.Query) + "%"
	}
	args = append(args, nameFilter)

	// Base query с учетом фильтрации по людям
	var ownerConditionFolder string
	var ownerConditionFile string
	if params.OwnerUserID != nil {
		switch params.UserFilter {
		case "owner":
			// Показать только файлы, где указанный пользователь - владелец
			ownerConditionFolder = fmt.Sprintf(" AND owner_id = $%d", argIdx+1)
			ownerConditionFile = fmt.Sprintf(" AND owner_id = $%d", argIdx+1)
			args = append(args, *params.OwnerUserID)
			argIdx++
		case "participant":
			// Показать только файлы, где указанный пользователь - участник (имеет permissions)
			ownerConditionFolder = fmt.Sprintf(" AND id IN (SELECT resource_id FROM permissions WHERE user_id = $%d AND resource_type = 'folder')", argIdx+1)
			ownerConditionFile = fmt.Sprintf(" AND id IN (SELECT resource_id FROM permissions WHERE user_id = $%d AND resource_type = 'file')", argIdx+1)
			args = append(args, *params.OwnerUserID)
			argIdx++
		}
	}

	querySQL := fmt.Sprintf(`
    WITH RECURSIVE accessible_folders AS (
        SELECT id, parent_id, owner_id
        FROM folders
        WHERE deleted_at IS NULL
          AND (
              owner_id = $1 
              OR id IN (SELECT resource_id FROM permissions WHERE user_id = $1 AND resource_type = 'folder')
              OR is_public = true
          )
        UNION
        SELECT f.id, f.parent_id, f.owner_id
        FROM folders f
        INNER JOIN accessible_folders af ON f.parent_id = af.id
        WHERE f.deleted_at IS NULL
    ),
    combined_items AS (
        -- 1. Folders
        SELECT 
            id, owner_id, parent_id, name as real_name, 
            ''::text as object_name, 0::bigint as size, ''::text as mime_type,
            is_public, created_at, updated_at, 'folder' as type, ARRAY[]::TEXT[] AS tags
        FROM folders
        WHERE deleted_at IS NULL
          AND id IN (SELECT id FROM accessible_folders)
          AND ($2 = '%%%%' OR name ILIKE $2)
		  %s

        UNION ALL

        -- 2. Files
        SELECT 
            id, owner_id, folder_id as parent_id, real_name, 
            object_name, size, mime_type, 
            is_public, created_at, updated_at, 'file' as type, tags
        FROM files
        WHERE deleted_at IS NULL
          AND scope = 'drive'
          AND (
              owner_id = $1 
              OR id IN (SELECT resource_id FROM permissions WHERE user_id = $1 AND resource_type = 'file')
              OR is_public = true
              OR folder_id IN (SELECT id FROM accessible_folders)
          )
          AND (real_name ILIKE $2 OR EXISTS (SELECT 1 FROM unnest(tags) AS tag WHERE tag ILIKE $2))
		  %s
    )
    SELECT * FROM combined_items
    WHERE 1=1
    `, ownerConditionFolder, ownerConditionFile)

	if params.FileType != "" {
		if params.FileType == "folder" {
			querySQL += " AND type = 'folder'"
		} else {
			querySQL += fmt.Sprintf(" AND type = 'file' AND mime_type ILIKE $%d", argIdx+1)

			mimePattern := params.FileType + "/%"
			if params.FileType == "document" || params.FileType == "application" {
				mimePattern = "application/%"
			}
			args = append(args, mimePattern)
			argIdx++
		}
	}

	querySQL += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", argIdx+1, argIdx+2)
	args = append(args, params.Limit, params.Offset)

	rows, err := r.db.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("search items query: %w", err)
	}
	defer rows.Close()

	items := make([]domain.DriveItem, 0)

	for rows.Next() {
		var (
			id, parentID               *uuid.UUID
			ownerID                    int64
			name, objName, mime, iType string
			tags                       []string
			size                       int64
			isPublic                   bool
			createdAt, updatedAt       time.Time
		)

		err := rows.Scan(
			&id, &ownerID, &parentID, &name,
			&objName, &size, &mime,
			&isPublic, &createdAt, &updatedAt, &iType, &tags,
		)
		if err != nil {
			return nil, fmt.Errorf("scan search item: %w", err)
		}

		if iType == "folder" {
			f := &domain.Folder{
				ID: *id, OwnerID: ownerID, ParentID: parentID, Name: name, IsPublic: isPublic, CreatedAt: createdAt, UpdatedAt: updatedAt,
			}
			items = append(items, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
		} else {
			fl := &domain.DriveFile{
				ID: *id, OwnerID: ownerID, FolderID: parentID, RealName: name,
				Tags: tags, ObjectName: objName, Size: size, MimeType: mime, IsPublic: isPublic,
				CreatedAt: createdAt, UpdatedAt: updatedAt,
			}
			items = append(items, domain.DriveItem{Type: domain.ResourceFile, File: fl})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search items: %w", err)
	}
	return items, nil
}

func (r *DriveRepository) CreateSharedLink(ctx context.Context, link *domain.DriveSharedLink) error {
	q := `
        INSERT INTO drive_shared_links (token, resource_type, resource_id, created_by, role, expires_at, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	_, err := r.db.Exec(ctx, q,
		link.Token, link.ResourceType, link.ResourceID, link.CreatedBy, link.Role, link.ExpiresAt, link.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create shared link: %w", err)
	}
	return nil
}

func (r *DriveRepository) GetSharedLinkByToken(ctx context.Context, token uuid.UUID) (*domain.DriveSharedLink, error) {
	q := `
        SELECT token, resource_type, resource_id, created_by, role, expires_at, created_at
        FROM drive_shared_links
        WHERE token = $1
    `
	var l domain.DriveSharedLink
	err := r.db.QueryRow(ctx, q, token).Scan(
		&l.Token, &l.ResourceType, &l.ResourceID, &l.CreatedBy, &l.Role, &l.ExpiresAt, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get shared link: %w", err)
	}
	return &l, nil
}

func (r *DriveRepository) DeleteSharedLink(ctx context.Context, token uuid.UUID) error {
	_, err := r.db.Exec(ctx, "DELETE FROM drive_shared_links WHERE token=$1", token)
	return err
}

func (r *DriveRepository) CopyFile(ctx context.Context, fileID uuid.UUID, targetFolderID *uuid.UUID, newOwnerID int64) (*domain.DriveFile, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	newFile, err := r.copyFileTx(ctx, tx, fileID, targetFolderID, newOwnerID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return newFile, nil
}

func (r *DriveRepository) CopyFolder(ctx context.Context, folderID uuid.UUID, targetParentID *uuid.UUID, newOwnerID int64) (*domain.Folder, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	newFolder, err := r.copyFolderTx(ctx, tx, folderID, targetParentID, newOwnerID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return newFolder, nil
}

func (r *DriveRepository) copyFileTx(ctx context.Context, tx pgx.Tx, sourceFileID uuid.UUID, targetFolderID *uuid.UUID, newOwnerID int64) (*domain.DriveFile, error) {
	var f domain.DriveFile
	q := `
        SELECT id, owner_id, folder_id, real_name, object_name, COALESCE(content_hash, ''), source_system_code, source_system_name,
               external_message_id, size, mime_type, version, scope, is_public, onlyoffice_key, created_at, updated_at
        FROM files
        WHERE id=$1 AND deleted_at IS NULL
    `
	err := tx.QueryRow(ctx, q, sourceFileID).Scan(
		&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.ContentHash, &f.SourceSystemCode, &f.SourceSystemName,
		&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Scope, &f.IsPublic, &f.Key, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get source file: %w", err)
	}

	newFileID := uuid.New()
	newKey := fmt.Sprintf("%s-v1", newFileID.String())
	now := time.Now()
	qInsert := `
        INSERT INTO files (id, owner_id, folder_id, real_name, object_name, content_hash, size, mime_type, version, scope, is_public, onlyoffice_key, source_system_code, source_system_name, external_message_id, created_at, updated_at, deleted_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, NULL)
    `
	_, err = tx.Exec(ctx, qInsert, newFileID, newOwnerID, targetFolderID, f.RealName, f.ObjectName, f.ContentHash, f.Size, f.MimeType, 1, f.Scope, false, &newKey, f.SourceSystemCode, f.SourceSystemName, f.ExternalMessageID, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert new file: %w", err)
	}

	qVersion := `
        INSERT INTO file_versions (id, file_id, version, object_name, size, created_by, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	_, err = tx.Exec(ctx, qVersion, uuid.New(), newFileID, 1, f.ObjectName, f.Size, newOwnerID, now)
	if err != nil {
		return nil, fmt.Errorf("insert new file version: %w", err)
	}

	newFile := &domain.DriveFile{
		ID:                newFileID,
		OwnerID:           newOwnerID,
		FolderID:          targetFolderID,
		RealName:          f.RealName,
		ObjectName:        f.ObjectName,
		ContentHash:       f.ContentHash,
		SourceSystemCode:  f.SourceSystemCode,
		SourceSystemName:  f.SourceSystemName,
		ExternalMessageID: f.ExternalMessageID,
		Size:              f.Size,
		MimeType:          f.MimeType,
		Version:           1,
		Scope:             f.Scope,
		IsPublic:          false,
		Key:               &newKey,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	return newFile, nil
}

func (r *DriveRepository) copyFolderTx(ctx context.Context, tx pgx.Tx, sourceFolderID uuid.UUID, targetParentID *uuid.UUID, newOwnerID int64) (*domain.Folder, error) {
	var f domain.Folder
	q := `SELECT id, owner_id, parent_id, name, is_public, created_at, updated_at, deleted_at FROM folders WHERE id=$1 AND deleted_at IS NULL`
	err := tx.QueryRow(ctx, q, sourceFolderID).Scan(&f.ID, &f.OwnerID, &f.ParentID, &f.Name, &f.IsPublic, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt)
	if err != nil {
		return nil, fmt.Errorf("get source folder: %w", err)
	}

	newFolderID := uuid.New()
	now := time.Now()
	qInsert := `
        INSERT INTO folders (id, owner_id, parent_id, name, is_public, created_at, updated_at, deleted_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
    `
	_, err = tx.Exec(ctx, qInsert, newFolderID, newOwnerID, targetParentID, f.Name, false, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert new folder: %w", err)
	}

	qSubfolders := `SELECT id FROM folders WHERE parent_id=$1 AND deleted_at IS NULL`
	rowsF, err := tx.Query(ctx, qSubfolders, sourceFolderID)
	if err != nil {
		return nil, fmt.Errorf("list subfolders: %w", err)
	}
	var subfolderIDs []uuid.UUID
	for rowsF.Next() {
		var subID uuid.UUID
		if err := rowsF.Scan(&subID); err != nil {
			rowsF.Close()
			return nil, err
		}
		subfolderIDs = append(subfolderIDs, subID)
	}
	rowsF.Close()
	if err := rowsF.Err(); err != nil {
		return nil, err
	}

	for _, subID := range subfolderIDs {
		_, err = r.copyFolderTx(ctx, tx, subID, &newFolderID, newOwnerID)
		if err != nil {
			return nil, err
		}
	}

	qFiles := `SELECT id FROM files WHERE folder_id=$1 AND scope='drive' AND deleted_at IS NULL`
	rowsFiles, err := tx.Query(ctx, qFiles, sourceFolderID)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	var fileIDs []uuid.UUID
	for rowsFiles.Next() {
		var fileID uuid.UUID
		if err := rowsFiles.Scan(&fileID); err != nil {
			rowsFiles.Close()
			return nil, err
		}
		fileIDs = append(fileIDs, fileID)
	}
	rowsFiles.Close()
	if err := rowsFiles.Err(); err != nil {
		return nil, err
	}

	for _, fileID := range fileIDs {
		_, err = r.copyFileTx(ctx, tx, fileID, &newFolderID, newOwnerID)
		if err != nil {
			return nil, err
		}
	}

	newFolder := &domain.Folder{
		ID:        newFolderID,
		OwnerID:   newOwnerID,
		ParentID:  targetParentID,
		Name:      f.Name,
		IsPublic:  false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return newFolder, nil
}

func (r *DriveRepository) LogDriveActivity(ctx context.Context, activity *domain.DriveActivity) error {
	if activity == nil {
		return nil
	}

	metadata := activity.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	q := `
        INSERT INTO drive_activity_logs (
            id, action, resource_type, resource_id, resource_name,
            owner_id, actor_id, folder_id, from_folder_id, to_folder_id,
            resource_path, from_path, to_path, metadata, created_at
        )
        VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,0),$8,$9,$10,$11,$12,$13,$14,$15)
    `
	_, err := r.db.Exec(
		ctx,
		q,
		activity.ID,
		activity.Action,
		activity.ResourceType,
		activity.ResourceID,
		activity.ResourceName,
		activity.OwnerID,
		activity.ActorID,
		activity.FolderID,
		activity.FromFolderID,
		activity.ToFolderID,
		activity.ResourcePath,
		activity.FromPath,
		activity.ToPath,
		metadata,
		activity.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("log drive activity: %w", err)
	}
	return nil
}

func (r *DriveRepository) GetDriveActivityAccessProfile(ctx context.Context, userID int64) (*domain.DriveActivityAccessProfile, error) {
	const query = `
		SELECT
			u.can_view_full_drive_activity,
			COALESCE(NULLIF(TRIM(u.position), ''), '')
		FROM users u
		WHERE u.id = $1
		  AND u.deleted_at IS NULL
	`

	var (
		manualOverride bool
		positionName   string
	)
	if err := r.db.QueryRow(ctx, query, userID).Scan(&manualOverride, &positionName); err != nil {
		return nil, fmt.Errorf("get drive activity access profile: %w", err)
	}

	positionBased := domain.PositionGrantsFullDriveActivity(positionName)
	return &domain.DriveActivityAccessProfile{
		CanViewAll:     manualOverride || positionBased,
		ManualOverride: manualOverride,
		PositionBased:  positionBased,
		PositionName:   positionName,
	}, nil
}

func (r *DriveRepository) ListDriveActivities(ctx context.Context, filter domain.DriveActivityListFilter) ([]domain.DriveActivityEntry, int, error) {
	limit := filter.Limit
	offset := filter.Offset
	folderID := filter.FolderID
	actorID := filter.ActorID
	viewerUserID := filter.ViewerUserID
	folderPathPrefix := strings.TrimSpace(filter.FolderPathPrefix)
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	baseSelect := `
        SELECT
            COUNT(*) OVER() AS total_count,
            l.id,
            l.action,
            l.resource_type,
            l.resource_id,
            l.resource_name,
            l.owner_id,
            COALESCE(l.actor_id, 0),
            COALESCE(NULLIF(TRIM(u.full_name_local), ''), NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), NULLIF(TRIM(u.username), ''), 'User ' || l.actor_id::text, 'Anonymous') AS actor_full_name,
            COALESCE(u.username, ''),
            COALESCE(u.email, ''),
            l.folder_id,
            l.from_folder_id,
            l.to_folder_id,
            l.resource_path,
            l.from_path,
            l.to_path,
            l.metadata,
            l.created_at
        FROM drive_activity_logs l
        LEFT JOIN users u ON u.id = l.actor_id
    `

	var (
		rows pgx.Rows
		err  error
	)

	if folderID == nil {
		q := `
            WITH RECURSIVE accessible_folders AS (
                SELECT id
                FROM folders
                WHERE deleted_at IS NULL
                  AND (
                      owner_id = $1
                      OR id IN (
                          SELECT resource_id
                          FROM permissions
                          WHERE user_id = $1
                            AND resource_type = 'folder'
                      )
                      OR is_public = true
                  )
                UNION
                SELECT f.id
                FROM folders f
                JOIN accessible_folders af ON f.parent_id = af.id
                WHERE f.deleted_at IS NULL
            ),
            accessible_files AS (
                SELECT f.id
                FROM files f
                WHERE f.scope = 'drive'
                  AND f.deleted_at IS NULL
                  AND (
                      f.owner_id = $1
                      OR f.id IN (
                          SELECT resource_id
                          FROM permissions
                          WHERE user_id = $1
                            AND resource_type = 'file'
                      )
                      OR f.is_public = true
                      OR f.folder_id IN (SELECT id FROM accessible_folders)
                  )
            ),
            folder_paths AS (
                SELECT f.id, f.parent_id, TRIM(f.name) AS path
                FROM folders f
                WHERE f.deleted_at IS NULL
                  AND f.parent_id IS NULL
                UNION ALL
                SELECT child.id, child.parent_id, fp.path || ' / ' || TRIM(child.name) AS path
                FROM folders child
                JOIN folder_paths fp ON child.parent_id = fp.id
                WHERE child.deleted_at IS NULL
            ),
            accessible_folder_paths AS (
                SELECT DISTINCT path
                FROM folder_paths
                WHERE id IN (SELECT id FROM accessible_folders)
            )
        ` + baseSelect + `
            WHERE (
                    l.owner_id = $1 OR l.actor_id = $1
                 OR (l.resource_type = 'folder' AND l.resource_id IN (SELECT id FROM accessible_folders))
                 OR (l.resource_type = 'file' AND l.resource_id IN (SELECT id FROM accessible_files))
                 OR l.folder_id IN (SELECT id FROM accessible_folders)
                 OR l.from_folder_id IN (SELECT id FROM accessible_folders)
                 OR l.to_folder_id IN (SELECT id FROM accessible_folders)
                 OR EXISTS (
                        SELECT 1
                        FROM accessible_folder_paths afp
                        WHERE l.resource_path = afp.path
                           OR l.from_path = afp.path
                           OR l.to_path = afp.path
                           OR l.resource_path LIKE afp.path || ' /%'
                           OR l.from_path LIKE afp.path || ' /%'
                           OR l.to_path LIKE afp.path || ' /%'
                 )
               )
        `
		args := []any{viewerUserID}
		if actorID != nil {
			q += ` AND l.actor_id = $2`
			args = append(args, *actorID)
		}
		q += `
            ORDER BY l.created_at DESC
            LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2) + `
        `
		args = append(args, limit, offset)
		rows, err = r.db.Query(ctx, q, args...)
	} else {
		q := `
            WITH RECURSIVE subtree AS (
                SELECT id
                FROM folders
                WHERE id = $1
                  AND deleted_at IS NULL
                UNION ALL
                SELECT f.id
                FROM folders f
                JOIN subtree s ON f.parent_id = s.id
                WHERE f.deleted_at IS NULL
            )
        ` + baseSelect + `
            WHERE (
                    (l.resource_type = 'folder' AND l.resource_id IN (SELECT id FROM subtree))
                 OR l.folder_id IN (SELECT id FROM subtree)
                 OR l.from_folder_id IN (SELECT id FROM subtree)
                 OR l.to_folder_id IN (SELECT id FROM subtree)
                 OR ($2 <> '' AND (
                        l.resource_path = $2
                     OR l.from_path = $2
                     OR l.to_path = $2
                     OR l.resource_path LIKE $3
                     OR l.from_path LIKE $3
                     OR l.to_path LIKE $3
                 ))
               )
        `
		args := []any{*folderID, folderPathPrefix, folderPathPrefix + " /%"}
		if actorID != nil {
			q += ` AND l.actor_id = $4`
			args = append(args, *actorID)
		}
		q += `
            ORDER BY l.created_at DESC
            LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2) + `
        `
		args = append(args, limit, offset)
		rows, err = r.db.Query(ctx, q, args...)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list drive activities: %w", err)
	}
	defer rows.Close()

	activities := make([]domain.DriveActivityEntry, 0, limit)
	totalCount := 0
	for rows.Next() {
		var entry domain.DriveActivityEntry
		var rowTotalCount int
		if err := rows.Scan(
			&rowTotalCount,
			&entry.ID,
			&entry.Action,
			&entry.ResourceType,
			&entry.ResourceID,
			&entry.ResourceName,
			&entry.OwnerID,
			&entry.ActorID,
			&entry.ActorFullName,
			&entry.ActorUsername,
			&entry.ActorEmail,
			&entry.FolderID,
			&entry.FromFolderID,
			&entry.ToFolderID,
			&entry.ResourcePath,
			&entry.FromPath,
			&entry.ToPath,
			&entry.Metadata,
			&entry.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan drive activity: %w", err)
		}
		totalCount = rowTotalCount
		activities = append(activities, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate drive activities: %w", err)
	}

	return activities, totalCount, nil
}

func (r *DriveRepository) GetUserNameByID(ctx context.Context, userID int64) (string, error) {
	var firstName, lastName, username string
	q := `SELECT first_name, last_name, username FROM users WHERE id = $1`
	err := r.db.QueryRow(ctx, q, userID).Scan(&firstName, &lastName, &username)
	if err != nil {
		return "", err
	}
	fullName := strings.TrimSpace(firstName + " " + lastName)
	if fullName == "" {
		return username, nil
	}
	return fullName, nil
}
