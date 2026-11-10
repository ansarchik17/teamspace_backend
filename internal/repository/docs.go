package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Use the same organization selection and administration rules as the company builder.
func (r *DriveRepository) DocumentOrganization(ctx context.Context, userID int64, manage bool) (uuid.UUID, error) {
	scope, err := NewOrganizationRepository(r.db, r.logger).currentScope(ctx, userID)
	if errors.Is(err, domain.ErrOrganizationNotFound) {
		return uuid.Nil, apperrors.Forbidden("active organization required")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if manage {
		if err := authorizeAdministration(ctx, r.db, userID, scope.OwnerID, false); err != nil {
			if apperrors.ToAppError(err).StatusCode == 403 {
				return uuid.Nil, apperrors.Forbidden("only organization administrators can manage templates")
			}
			return uuid.Nil, err
		}
	}
	return scope.ID, nil
}

const documentTemplateColumns = `t.id,t.organization_id,t.file_id,t.name,t.format,t.created_by,t.created_at`

// Published templates expose the current source contents to company members, but
// never follow a source transferred outside the publishing organization.
const documentTemplateAvailable = ` FROM document_templates t JOIN files f ON f.id=t.file_id
 JOIN users owner ON owner.id=f.owner_id JOIN organizations o ON o.id=t.organization_id
 WHERE t.organization_id=$1 AND o.status='active' AND f.deleted_at IS NULL AND f.scope='drive'
 AND owner.deleted_at IS NULL AND (owner.organization_id=t.organization_id OR (owner.organization_id IS NULL AND o.owner_id=owner.id))
 AND lower(right(f.real_name,5))='.'||t.format`

func scanDocumentTemplate(row pgx.Row) (*domain.DocumentTemplate, error) {
	var item domain.DocumentTemplate
	err := row.Scan(&item.ID, &item.OrganizationID, &item.FileID, &item.Name, &item.Format, &item.CreatedBy, &item.CreatedAt)
	return &item, err
}

func (r *DriveRepository) ListDocumentTemplates(ctx context.Context, org uuid.UUID, limit, offset int) ([]domain.DocumentTemplate, error) {
	rows, err := r.db.Query(ctx, `SELECT `+documentTemplateColumns+documentTemplateAvailable+` ORDER BY t.created_at DESC,t.id LIMIT $2 OFFSET $3`, org, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.DocumentTemplate, 0)
	for rows.Next() {
		item, err := scanDocumentTemplate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *DriveRepository) PublishDocumentTemplate(ctx context.Context, item *domain.DocumentTemplate) error {
	// Ownership and organization are checked again inside the insert.
	err := r.db.QueryRow(ctx, `INSERT INTO document_templates(organization_id,file_id,name,format,created_by)
 SELECT $1,f.id,$3,$4,$5 FROM files f JOIN users u ON u.id=f.owner_id JOIN organizations o ON o.id=$1
 WHERE f.id=$2 AND f.owner_id=$5 AND f.deleted_at IS NULL AND f.scope='drive'
 AND u.deleted_at IS NULL AND o.status='active' AND (u.organization_id=$1 OR (u.organization_id IS NULL AND o.owner_id=u.id))
 RETURNING id,created_at`, item.OrganizationID, item.FileID, item.Name, item.Format, item.CreatedBy).Scan(&item.ID, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.Forbidden("publish an owned Drive file from your organization")
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apperrors.Conflict("file is already published as a template")
	}
	return err
}

func (r *DriveRepository) DeleteDocumentTemplate(ctx context.Context, org, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM document_templates WHERE organization_id=$1 AND id=$2`, org, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return apperrors.NotFound("template not found")
	}
	return nil
}

// Resolve source bytes and tenant eligibility in one query so source metadata
// cannot be replaced between a catalog lookup and a separate file lookup.
func (r *DriveRepository) DocumentTemplateSource(ctx context.Context, org, id uuid.UUID) (*domain.DriveFile, string, error) {
	var file domain.DriveFile
	var format string
	err := r.db.QueryRow(ctx, `SELECT f.id,f.object_name,f.size,t.format`+documentTemplateAvailable+` AND t.id=$2`, org, id).Scan(&file.ID, &file.ObjectName, &file.Size, &format)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", apperrors.NotFound("template not found")
	}
	return &file, format, err
}
