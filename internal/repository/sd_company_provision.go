package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) SDCompanyProvisionDetails(ctx context.Context, actor int64, org uuid.UUID) (domain.SDCompanyProvision, error) {
	if err := r.AuthorizeAccessPlatform(ctx, actor, org); err != nil {
		return domain.SDCompanyProvision{}, err
	}
	return r.sdCompanyProvisionDetails(ctx, org)
}

// Only the actor's current active company can be provisioned through SSO.
// No organization, owner, slug or company ID is accepted from the browser.
func (r *OrganizationRepository) SDCompanyProvisionDetailsForUser(ctx context.Context, actor int64) (domain.SDCompanyProvision, error) {
	var org uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT o.id FROM users u JOIN organizations o ON o.id=u.organization_id
 WHERE u.id=$1 AND u.is_active AND u.deleted_at IS NULL AND o.status='active'`, actor).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SDCompanyProvision{}, apperrors.Forbidden("Нет активной компании Workspace").WithUserMessage("Учётная запись или компания Workspace недоступна. Обратитесь к администратору.")
	}
	if err != nil {
		return domain.SDCompanyProvision{}, err
	}
	return r.sdCompanyProvisionDetails(ctx, org)
}

func (r *OrganizationRepository) sdCompanyProvisionDetails(ctx context.Context, org uuid.UUID) (domain.SDCompanyProvision, error) {
	var out domain.SDCompanyProvision
	// An existing link takes precedence, including a manually linked SD company.
	var linked domain.AccessSDCompany
	err := r.db.QueryRow(ctx, `SELECT l.external_company_id,o.name,l.is_tenant_root
 FROM organization_app_links l JOIN organizations o ON o.id=l.organization_id
 WHERE l.organization_id=$1 AND l.application='sd' AND o.status='active'`, org).Scan(&linked.ID, &linked.Name, &linked.IsTenantRoot)
	if err == nil {
		out.OrganizationID, out.LinkedCompany = org, &linked
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	err = r.db.QueryRow(ctx, `SELECT o.id,o.name,o.slug,u.id,COALESCE(u.keycloak_id::text,''),
 COALESCE(u.username,''),COALESCE(u.email,''),COALESCE(u.first_name,''),COALESCE(u.last_name,'')
 FROM organizations o JOIN users u ON u.id=o.owner_id AND u.organization_id=o.id
 WHERE o.id=$1 AND o.status='active' AND u.is_active AND u.deleted_at IS NULL`, org).Scan(
		&out.OrganizationID, &out.Name, &out.Slug, &out.Owner.UserID, &out.Owner.Subject, &out.Owner.Username, &out.Owner.Email, &out.Owner.FirstName, &out.Owner.LastName)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, apperrors.Conflict("Для создания компании СД назначьте активного владельца компании Workspace в разделе «Структура»")
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

// Never overwrite a link saved by another administrator during provisioning.
func (r *OrganizationRepository) LinkProvisionedSDCompany(ctx context.Context, actor int64, profile domain.SDCompanyProvision, company uuid.UUID) error {
	if err := r.AuthorizeAccessPlatform(ctx, actor, profile.OrganizationID); err != nil {
		return err
	}
	return r.linkProvisionedSDCompany(ctx, actor, profile, company, false)
}

func (r *OrganizationRepository) LinkAutomaticallyProvisionedSDCompany(ctx context.Context, actor int64, profile domain.SDCompanyProvision, company uuid.UUID) error {
	return r.linkProvisionedSDCompany(ctx, actor, profile, company, true)
}

func (r *OrganizationRepository) linkProvisionedSDCompany(ctx context.Context, actor int64, profile domain.SDCompanyProvision, company uuid.UUID, automatic bool) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var unchanged bool
	if err = tx.QueryRow(ctx, `SELECT name=$2 AND slug=$3 AND owner_id IS NOT DISTINCT FROM $4::bigint AND status='active' FROM organizations WHERE id=$1 FOR UPDATE`, profile.OrganizationID, profile.Name, profile.Slug, profile.Owner.UserID).Scan(&unchanged); err != nil {
		return err
	}
	if !unchanged {
		return apperrors.Conflict("Компания или владелец изменились во время создания СД. Обновите страницу")
	}
	var activeOwner bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND organization_id=$2 AND is_active AND deleted_at IS NULL)`, profile.Owner.UserID, profile.OrganizationID).Scan(&activeOwner); err != nil {
		return err
	}
	if !activeOwner {
		return apperrors.Conflict("Владелец компании изменился или отключён. Повторите попытку")
	}
	if automatic {
		var activeMember bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND organization_id=$2 AND is_active AND deleted_at IS NULL)`, actor, profile.OrganizationID).Scan(&activeMember); err != nil {
			return err
		}
		if !activeMember {
			return apperrors.Forbidden("Пользователь больше не состоит в компании Workspace")
		}
	}
	result, err := tx.Exec(ctx, `INSERT INTO organization_app_links(organization_id,application,external_company_id,is_tenant_root) VALUES($1,'sd',$2,true) ON CONFLICT DO NOTHING`, profile.OrganizationID, company)
	if err != nil {
		return err
	}
	var actual uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT external_company_id FROM organization_app_links WHERE organization_id=$1 AND application='sd'`, profile.OrganizationID).Scan(&actual); err != nil {
		return err
	}
	if actual != company {
		return apperrors.Conflict("Другой администратор уже связал компанию с СД. Обновите страницу")
	}
	if result.RowsAffected() == 1 {
		details, _ := json.Marshal(map[string]any{"company_id": company, "slug": profile.Slug, "automatic": automatic})
		if _, err = tx.Exec(ctx, `INSERT INTO organization_access_audit(organization_id,actor_id,action,details) VALUES($1,$2,'provision_sd',$3)`, profile.OrganizationID, actor, details); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
