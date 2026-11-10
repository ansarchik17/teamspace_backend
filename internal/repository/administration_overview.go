package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const administrationRecentAuditLimit = 5

func administrationUserStatistics(ctx context.Context, db administrationQuerier, org *uuid.UUID) (domain.AdministrationUserStatistics, error) {
	var out domain.AdministrationUserStatistics
	err := db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE u.is_active),count(*) FILTER(WHERE NOT u.is_active),count(*) FILTER(WHERE EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id)) FROM users u JOIN organizations o ON o.id=u.organization_id WHERE o.status<>'deleted' AND u.deleted_at IS NULL AND ($1::uuid IS NULL OR u.organization_id=$1)`, org).Scan(&out.Total, &out.Active, &out.Inactive, &out.Blocked)
	return out, err
}

func administrationIntegrationStatistics(ctx context.Context, db administrationQuerier, org *uuid.UUID) (domain.AdministrationIntegrationStatistics, error) {
	var out domain.AdministrationIntegrationStatistics
	err := db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE p.enabled),count(*) FILTER(WHERE p.status='pending'),count(*) FILTER(WHERE p.status='error'),count(*) FILTER(WHERE p.status='disabled') FROM organization_identity_providers p JOIN organizations o ON o.id=p.organization_id WHERE o.status<>'deleted' AND ($1::uuid IS NULL OR p.organization_id=$1)`, org).Scan(&out.Total, &out.Enabled, &out.Pending, &out.Error, &out.Disabled)
	return out, err
}

func (r *OrganizationRepository) AdministrationOverview(ctx context.Context, actor int64, org *uuid.UUID) (*domain.AdministrationOverview, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger, capacity: r.capacity}
	scope, err := inner.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	out := &domain.AdministrationOverview{}
	err = tx.QueryRow(ctx, `SELECT id,name,slug,status,owner_id FROM organizations WHERE id=$1`, scope.ID).Scan(&out.Organization.ID, &out.Organization.Name, &out.Organization.Slug, &out.Organization.Status, &out.Organization.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, err
	}
	out.Users, err = administrationUserStatistics(ctx, tx, &scope.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM teams WHERE organization_id=$1 AND deleted_at IS NULL`, scope.ID).Scan(&out.UnitCount); err != nil {
		return nil, err
	}
	out.Modules, err = inner.ListAdministrationModules(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	out.Storage, err = inner.AdministrationStorage(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	out.Integrations, err = administrationIntegrationStatistics(ctx, tx, &scope.ID)
	if err != nil {
		return nil, err
	}
	out.RecentAudit, err = inner.ListAdministrationAudit(ctx, actor, org, org != nil, domain.AdministrationAuditFilter{Limit: administrationRecentAuditLimit})
	if err != nil {
		return nil, err
	}
	out.Capabilities = domain.CompanyAdministrationCapabilities(org != nil, out.Organization.Status == "active", out.Storage.QuotaBytes != nil)
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *OrganizationRepository) PlatformAdministrationOverview(ctx context.Context, actor int64) (*domain.PlatformAdministrationOverview, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger, capacity: r.capacity}
	if err := inner.ensurePlatformAdmin(ctx, actor); err != nil {
		return nil, err
	}
	out := &domain.PlatformAdministrationOverview{Capabilities: domain.PlatformAdministrationCapabilities{CreateCompanies: true, ManageCompanyLifecycle: true, AssignOwners: true, ReadGlobalAudit: true}}
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='active'),count(*) FILTER(WHERE status='suspended'),count(*) FILTER(WHERE owner_id IS NULL) FROM organizations WHERE status<>'deleted'`).Scan(&out.Companies.Total, &out.Companies.Active, &out.Companies.Suspended, &out.Companies.WithoutOwner)
	if err != nil {
		return nil, err
	}
	out.Users, err = administrationUserStatistics(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM teams t JOIN organizations o ON o.id=t.organization_id WHERE o.status<>'deleted' AND t.deleted_at IS NULL`).Scan(&out.UnitCount)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_storage_accounts a JOIN organizations o ON o.id=a.organization_id WHERE o.status<>'deleted' AND quota_bytes IS NOT NULL`).Scan(&out.CompaniesWithStorageBudget)
	if err != nil {
		return nil, err
	}
	out.Integrations, err = administrationIntegrationStatistics(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	out.RecentAudit, err = inner.ListAdministrationAudit(ctx, actor, nil, true, domain.AdministrationAuditFilter{Limit: administrationRecentAuditLimit})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
