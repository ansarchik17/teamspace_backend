package repository

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// A failure is scoped to the checkpoint and session at which the attempt started.
// A later committed revision makes this record obsolete without a separate write.
func (r *DriveRepository) RecordOfficeSaveFailure(ctx context.Context, id uuid.UUID, key string, version int, kind string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO drive_office_save_failures(file_id,document_key,version,kind)
 SELECT id,$2,$3,$4 FROM files WHERE id=$1 AND deleted_at IS NULL AND version=$3
 AND COALESCE(NULLIF(onlyoffice_key,''),id::text||'-v'||version::text)=$2
 ON CONFLICT(file_id) DO UPDATE SET document_key=EXCLUDED.document_key,version=EXCLUDED.version,kind=EXCLUDED.kind,failed_at=NOW()
 WHERE drive_office_save_failures.version<=EXCLUDED.version`, id, key, version, kind)
	return err
}

func (r *DriveRepository) OfficeSaveState(ctx context.Context, id uuid.UUID) (*domain.OfficeSaveState, error) {
	var state domain.OfficeSaveState
	err := r.db.QueryRow(ctx, `SELECT f.version,v.created_at,e.kind,e.failed_at FROM files f
 JOIN file_versions v ON v.file_id=f.id AND v.version=f.version
 LEFT JOIN drive_office_save_failures e ON e.file_id=f.id AND e.version=f.version
 AND e.document_key=COALESCE(NULLIF(f.onlyoffice_key,''),f.id::text||'-v'||f.version::text)
 WHERE f.id=$1 AND f.deleted_at IS NULL`, id).Scan(&state.Version, &state.SavedAt, &state.FailureKind, &state.FailedAt)
	return &state, err
}
