package repository

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) ListAdministrationModules(ctx context.Context, actor int64, org *uuid.UUID) ([]domain.AdministrationModule, error) {
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT catalog.application,COALESCE(m.available,false),COALESCE(m.enabled,false),COALESCE(m.available AND m.enabled,false) AND o.status='active',COALESCE(m.revision,0) FROM organizations o CROSS JOIN unnest($2::text[]) catalog(application) LEFT JOIN organization_modules m ON m.organization_id=o.id AND m.application=catalog.application WHERE o.id=$1 ORDER BY catalog.application`, scope.ID, domain.AdministrationModules())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AdministrationModule{}
	for rows.Next() {
		var m domain.AdministrationModule
		if err := rows.Scan(&m.Application, &m.Available, &m.Enabled, &m.Effective, &m.Revision); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *OrganizationRepository) SetAdministrationModule(ctx context.Context, actor int64, org *uuid.UUID, app string, req domain.AdministrationModuleRequest) error {
	if !domain.IsAdministrationModule(app) || req.Revision == nil || *req.Revision < 0 || (req.Available == nil && req.Enabled == nil) {
		return apperrors.Validation("invalid module request")
	}
	// Availability is writable only through an explicitly platform-scoped route.
	if req.Available != nil && org == nil {
		return apperrors.Forbidden("platform administrator required")
	}
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, actor, scope.ID, org != nil, "module.update", "module", app, func(inner *OrganizationRepository) error {
		var available, enabled bool
		var revision int64
		if err := inner.db.QueryRow(ctx, `SELECT COALESCE((SELECT available FROM organization_modules WHERE organization_id=$1 AND application=$2),false),COALESCE((SELECT enabled FROM organization_modules WHERE organization_id=$1 AND application=$2),false),COALESCE((SELECT revision FROM organization_modules WHERE organization_id=$1 AND application=$2),0)`, scope.ID, app).Scan(&available, &enabled, &revision); err != nil {
			return err
		}
		if revision != *req.Revision {
			return apperrors.Conflict("module revision changed")
		}
		if req.Available != nil {
			available = *req.Available
		}
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		if req.Enabled != nil && *req.Enabled && !available {
			return apperrors.Forbidden("module is unavailable")
		}
		_, err := inner.db.Exec(ctx, `INSERT INTO organization_modules(organization_id,application,available,enabled,revision) VALUES($1,$2,$3,$4,1) ON CONFLICT(organization_id,application) DO UPDATE SET available=EXCLUDED.available,enabled=EXCLUDED.enabled,revision=organization_modules.revision+1`, scope.ID, app, available, enabled)
		return err
	})
}

// Missing configuration denies access, including for newly created companies.
func requireOrganizationModule(ctx context.Context, db administrationQuerier, org uuid.UUID, app string) error {
	var allowed bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_modules m JOIN organizations o ON o.id=m.organization_id WHERE m.organization_id=$1 AND m.application=$2 AND m.available AND m.enabled AND o.status='active')`, org, app).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return apperrors.Forbidden("module is unavailable")
	}
	return nil
}

func (r *OrganizationRepository) RequireUserModule(ctx context.Context, actor int64, app string) error {
	if !domain.IsAdministrationModule(app) {
		return apperrors.Validation("unknown module")
	}
	scope, err := r.currentScope(ctx, actor)
	if err != nil {
		return err
	}
	return requireOrganizationModule(ctx, r.db, scope.ID, app)
}
