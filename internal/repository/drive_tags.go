package repository

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func (r *DriveRepository) GetDriveFileTags(ctx context.Context, id uuid.UUID) ([]string, error) {
	tags := []string{}
	err := r.db.QueryRow(ctx, `SELECT tags FROM files WHERE id=$1 AND scope='drive' AND deleted_at IS NULL`, id).Scan(&tags)
	return tags, err
}

func (r *DriveRepository) SetDriveFileTags(ctx context.Context, id uuid.UUID, tags []string) error {
	result, err := r.db.Exec(ctx, `UPDATE files SET tags=$2, updated_at=NOW() WHERE id=$1 AND scope='drive' AND deleted_at IS NULL`, id, tags)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return apperrors.NotFound("file not found")
	}
	return nil
}
