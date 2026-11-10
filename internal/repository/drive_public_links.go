package repository

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *DriveRepository) ListSharedLinks(ctx context.Context, resourceType domain.ResourceType, resourceID uuid.UUID, limit, offset int) ([]domain.DriveSharedLink, error) {
	rows, err := r.db.Query(ctx, `SELECT token, resource_type, resource_id, created_by, role, expires_at, created_at
 FROM drive_shared_links WHERE resource_type=$1 AND resource_id=$2
 ORDER BY created_at DESC, token DESC LIMIT $3 OFFSET $4`, resourceType, resourceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := make([]domain.DriveSharedLink, 0)
	for rows.Next() {
		var l domain.DriveSharedLink
		if err := rows.Scan(&l.Token, &l.ResourceType, &l.ResourceID, &l.CreatedBy, &l.Role, &l.ExpiresAt, &l.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}
