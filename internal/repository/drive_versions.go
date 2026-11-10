package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

var ErrDriveVersionConflict = errors.New("file changed; refresh before trying again")

func (r *DriveRepository) ListFileVersions(ctx context.Context, id uuid.UUID, limit, offset int) ([]domain.DriveFileVersion, error) {
	rows, err := r.db.Query(ctx, `SELECT v.id,v.file_id,v.version,v.object_name,v.size,v.created_by,
 COALESCE(NULLIF(BTRIM(CONCAT_WS(' ',u.first_name,u.last_name)),''),u.username,''),v.created_at,v.version=f.version
 FROM file_versions v JOIN files f ON f.id=v.file_id LEFT JOIN users u ON u.id=v.created_by
 WHERE v.file_id=$1 AND f.deleted_at IS NULL ORDER BY v.version DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]domain.DriveFileVersion, 0)
	for rows.Next() {
		var v domain.DriveFileVersion
		if err := rows.Scan(&v.ID, &v.FileID, &v.Version, &v.ObjectName, &v.Size, &v.CreatedBy, &v.CreatedByName, &v.CreatedAt, &v.IsCurrent); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (r *DriveRepository) GetFileVersion(ctx context.Context, id uuid.UUID, version int) (*domain.DriveFileVersion, error) {
	var v domain.DriveFileVersion
	err := r.db.QueryRow(ctx, `SELECT id,file_id,version,object_name,size,created_by,created_at FROM file_versions WHERE file_id=$1 AND version=$2`, id, version).Scan(&v.ID, &v.FileID, &v.Version, &v.ObjectName, &v.Size, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

// AppendFileVersion locks the current file via UPDATE and inserts the revision in
// the same transaction. The expected version prevents stale clients overwriting edits.
func (r *DriveRepository) AppendFileVersion(ctx context.Context, id uuid.UUID, object string, size int64, editorID int64, expected int) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var version int
	err = tx.QueryRow(ctx, `UPDATE files SET object_name=$2,size=$3,content_hash='',version=version+1,
 onlyoffice_key=id::text||'-v'||(version+1)::text,updated_at=NOW()
 WHERE id=$1 AND deleted_at IS NULL AND ($4=0 OR version=$4) RETURNING version`, id, object, size, expected).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrDriveVersionConflict
	}
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO file_versions(id,file_id,version,object_name,size,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,NOW())`, uuid.New(), id, version, object, size, editorID)
	if err != nil {
		return 0, fmt.Errorf("save revision: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return version, nil
}
