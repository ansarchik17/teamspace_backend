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

func (r *DriveRepository) CreateDeleteRequest(ctx context.Context, req *domain.DriveDeleteRequest) error {
	q := `
        INSERT INTO drive_delete_requests (
            id, resource_type, resource_id, resource_name, parent_folder_id,
            owner_id, requested_by, recursive, status, created_at
        )
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    `
	_, err := r.db.Exec(ctx, q,
		req.ID, req.ResourceType, req.ResourceID, req.ResourceName, req.ParentFolderID,
		req.OwnerID, req.RequestedBy, req.Recursive, req.Status, req.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create drive delete request: %w", err)
	}
	return nil
}

func (r *DriveRepository) GetDeleteRequestByID(ctx context.Context, id uuid.UUID) (*domain.DriveDeleteRequest, error) {
	q := `
        SELECT id, resource_type, resource_id, resource_name, parent_folder_id,
               owner_id, requested_by, resolved_by, recursive, status,
               rejection_reason, created_at, resolved_at, executed_at
        FROM drive_delete_requests
        WHERE id = $1
    `
	var req domain.DriveDeleteRequest
	err := r.db.QueryRow(ctx, q, id).Scan(
		&req.ID, &req.ResourceType, &req.ResourceID, &req.ResourceName, &req.ParentFolderID,
		&req.OwnerID, &req.RequestedBy, &req.ResolvedBy, &req.Recursive, &req.Status,
		&req.RejectionReason, &req.CreatedAt, &req.ResolvedAt, &req.ExecutedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get drive delete request by id: %w", err)
	}
	return &req, nil
}

func (r *DriveRepository) FindPendingDeleteRequest(ctx context.Context, resourceType domain.ResourceType, resourceID uuid.UUID, requestedBy int64) (*domain.DriveDeleteRequest, error) {
	q := `
        SELECT id, resource_type, resource_id, resource_name, parent_folder_id,
               owner_id, requested_by, resolved_by, recursive, status,
               rejection_reason, created_at, resolved_at, executed_at
        FROM drive_delete_requests
        WHERE resource_type = $1
          AND resource_id = $2
          AND requested_by = $3
          AND status = 'pending'
        ORDER BY created_at DESC
        LIMIT 1
    `
	var req domain.DriveDeleteRequest
	err := r.db.QueryRow(ctx, q, resourceType, resourceID, requestedBy).Scan(
		&req.ID, &req.ResourceType, &req.ResourceID, &req.ResourceName, &req.ParentFolderID,
		&req.OwnerID, &req.RequestedBy, &req.ResolvedBy, &req.Recursive, &req.Status,
		&req.RejectionReason, &req.CreatedAt, &req.ResolvedAt, &req.ExecutedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find pending drive delete request: %w", err)
	}
	return &req, nil
}

func (r *DriveRepository) ResolveDeleteRequest(ctx context.Context, id uuid.UUID, status domain.DriveDeleteRequestStatus, resolvedBy int64, rejectionReason *string, resolvedAt time.Time, executedAt *time.Time) error {
	q := `
        UPDATE drive_delete_requests
        SET status = $2,
            resolved_by = $3,
            rejection_reason = $4,
            resolved_at = $5,
            executed_at = $6
        WHERE id = $1
    `
	res, err := r.db.Exec(ctx, q, id, status, resolvedBy, rejectionReason, resolvedAt, executedAt)
	if err != nil {
		return fmt.Errorf("resolve drive delete request: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("drive delete request not found")
	}
	return nil
}

func (r *DriveRepository) DeleteDeleteRequest(ctx context.Context, id uuid.UUID) error {
	q := `DELETE FROM drive_delete_requests WHERE id = $1`
	res, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete drive delete request: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("drive delete request not found")
	}
	return nil
}
