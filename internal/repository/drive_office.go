package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *DriveRepository) OfficeSaveApplied(ctx context.Context, id uuid.UUID, key, event string) (bool, error) {
	var found bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM drive_office_save_receipts WHERE file_id=$1 AND document_key=$2 AND event_hash=$3)`, id, key, event).Scan(&found)
	return found, err
}

// A force-save advances Drive history while preserving the collaborative key.
// Final save rotates it. Receipt and revision commit together, including quota
// triggers, so retried callbacks cannot append another version.
func (r *DriveRepository) AppendOfficeVersion(ctx context.Context, id uuid.UUID, object string, size, editorID int64, expected int, key, event string, final bool) (version int, applied bool, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	var current int
	var currentKey string
	err = tx.QueryRow(ctx, `SELECT version,COALESCE(NULLIF(onlyoffice_key,''),id::text||'-v'||version::text) FROM files WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&current, &currentKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrDriveVersionConflict
	}
	if err != nil {
		return 0, false, err
	}
	err = tx.QueryRow(ctx, `SELECT version FROM drive_office_save_receipts WHERE file_id=$1 AND document_key=$2 AND event_hash=$3`, id, key, event).Scan(&version)
	if err == nil {
		return version, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	if current != expected || currentKey != key {
		return 0, false, ErrDriveVersionConflict
	}
	version = current + 1
	nextKey := key
	if final {
		nextKey = fmt.Sprintf("%s-v%d", id, version)
	}
	_, err = tx.Exec(ctx, `UPDATE files SET object_name=$2,size=$3,content_hash='',version=$4,onlyoffice_key=$5,updated_at=NOW() WHERE id=$1`, id, object, size, version, nextKey)
	if err != nil {
		return 0, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO file_versions(id,file_id,version,object_name,size,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,NOW())`, uuid.New(), id, version, object, size, editorID)
	if err != nil {
		return 0, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO drive_office_save_receipts(file_id,document_key,event_hash,version) VALUES($1,$2,$3,$4)`, id, key, event, version)
	if err != nil {
		return 0, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return version, true, nil
}
