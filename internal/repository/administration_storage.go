package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

var errStorageCapacityUnavailable = errors.New("storage capacity metrics unavailable")

func (r *OrganizationRepository) AdministrationStorage(ctx context.Context, actor int64, org *uuid.UUID) (*domain.AdministrationStorage, error) {
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	out := &domain.AdministrationStorage{}
	err = r.db.QueryRow(ctx, `SELECT a.quota_bytes,COALESCE(a.revision,0),s.allocated_bytes,s.used_bytes FROM organization_storage_usage($1) s LEFT JOIN organization_storage_accounts a ON a.organization_id=$1`, scope.ID).Scan(&out.QuotaBytes, &out.Revision, &out.AllocatedBytes, &out.UsedBytes)
	if err != nil {
		return nil, err
	}
	if out.QuotaBytes != nil {
		free, unallocated := storageFreeBytes(*out.QuotaBytes, out.UsedBytes), storageFreeBytes(*out.QuotaBytes, out.AllocatedBytes)
		out.FreeBytes = &free
		out.UnallocatedBytes = &unallocated
	}
	return out, nil
}

func (r *OrganizationRepository) SetAdministrationStorage(ctx context.Context, actor int64, org *uuid.UUID, req domain.AdministrationStorageRequest) error {
	if !req.Valid() {
		return apperrors.Validation("invalid storage quota")
	}
	if org == nil {
		return apperrors.Forbidden("platform administrator required")
	}
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, actor, scope.ID, true, "storage.quota.update", "storage_quota", scope.ID.String(), func(inner *OrganizationRepository) error {
		var revision int64
		// Keep the configuration revision distinct from upload accounting updates.
		_, err := inner.db.Exec(ctx, `INSERT INTO organization_storage_accounts(organization_id) VALUES($1) ON CONFLICT DO NOTHING`, scope.ID)
		if err != nil {
			return err
		}
		if err := inner.db.QueryRow(ctx, `SELECT revision FROM organization_storage_accounts WHERE organization_id=$1 FOR UPDATE`, scope.ID).Scan(&revision); err != nil {
			return err
		}
		if revision != *req.Revision {
			return apperrors.Conflict("storage revision changed")
		}
		var allocated, used int64
		if err := inner.db.QueryRow(ctx, `SELECT allocated_bytes,used_bytes FROM organization_storage_usage($1)`, scope.ID).Scan(&allocated, &used); err != nil {
			return err
		}
		if *req.QuotaBytes < allocated || *req.QuotaBytes < used {
			return apperrors.New("COMPANY_BUDGET_BELOW_USAGE", "company quota is below allocations or usage", 409)
		}
		// Serialize budget grants across companies and API instances.
		if _, err := inner.db.Exec(ctx, `SELECT pg_advisory_xact_lock(1700601)`); err != nil {
			return err
		}
		capacity, err := inner.companyStorageCapacity(ctx, scope.ID)
		// Capacity metrics are best-effort: an unavailable metrics endpoint must
		// not prevent administrators from configuring logical company quotas.
		if err != nil {
			if !errors.Is(err, errStorageCapacityUnavailable) {
				return err
			}
			inner.logger.Warn("company budget capacity check unavailable")
		}
		if err == nil && *req.QuotaBytes > capacity.MaxQuotaBytes {
			return apperrors.New("COMPANY_BUDGET_CAPACITY_EXCEEDED", "insufficient storage capacity for company budget", 409)
		}
		_, err = inner.db.Exec(ctx, `UPDATE organization_storage_accounts SET quota_bytes=$2,revision=revision+1,accounting_revision=accounting_revision+1 WHERE organization_id=$1`, scope.ID, *req.QuotaBytes)
		return err
	})
}

func (r *OrganizationRepository) AdministrationStorageUsers(ctx context.Context, actor int64, org *uuid.UUID, limit, offset int) (*domain.AdministrationStorageUsers, error) {
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, apperrors.Validation("invalid storage pagination")
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out := &domain.AdministrationStorageUsers{Items: []domain.AdministrationStorageUser{}, Limit: limit, Offset: offset}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE organization_id=$1 AND deleted_at IS NULL`, scope.ID).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT u.id,COALESCE(NULLIF(u.full_name_local,''),NULLIF(trim(concat_ws(' ',u.first_name,u.last_name)),''),u.username,''),COALESCE(a.quota_bytes,p.quota_bytes),s.active_bytes+s.version_bytes+s.trash_bytes,COALESCE(a.revision,0) FROM users u CROSS JOIN drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=u.id CROSS JOIN LATERAL drive_storage_usage(u.id) s WHERE u.organization_id=$1 AND u.deleted_at IS NULL ORDER BY u.id LIMIT $2 OFFSET $3`, scope.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item domain.AdministrationStorageUser
		if err := rows.Scan(&item.UserID, &item.FullName, &item.QuotaBytes, &item.UsedBytes, &item.Revision); err != nil {
			rows.Close()
			return nil, err
		}
		out.Items = append(out.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *OrganizationRepository) SetAdministrationUserStorage(ctx context.Context, actor, target int64, org *uuid.UUID, req domain.AdministrationStorageRequest) error {
	if !req.Valid() || target <= 0 {
		return apperrors.Validation("invalid storage quota")
	}
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, actor, scope.ID, org != nil, "storage.user_quota.update", "user_storage_quota", fmt.Sprint(target), func(inner *OrganizationRepository) error {
		var member int64
		err := inner.db.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR SHARE`, target, scope.ID).Scan(&member)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperrors.NotFound("employee")
		}
		if err != nil {
			return err
		}
		_, err = inner.db.Exec(ctx, `INSERT INTO organization_storage_accounts(organization_id) VALUES($1) ON CONFLICT DO NOTHING`, scope.ID)
		if err != nil {
			return err
		}
		var companyQuota *int64
		if err := inner.db.QueryRow(ctx, `SELECT quota_bytes FROM organization_storage_accounts WHERE organization_id=$1 FOR UPDATE`, scope.ID).Scan(&companyQuota); err != nil {
			return err
		}
		if companyQuota == nil && org == nil {
			return apperrors.Conflict("platform must configure company storage budget")
		}
		var revision, oldQuota int64
		if err := inner.db.QueryRow(ctx, `SELECT COALESCE(a.revision,0),COALESCE(a.quota_bytes,p.quota_bytes) FROM drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=$1`, target).Scan(&revision, &oldQuota); err != nil {
			return err
		}
		if revision != *req.Revision {
			return apperrors.Conflict("user storage revision changed")
		}
		if companyQuota != nil {
			var allocated int64
			if err := inner.db.QueryRow(ctx, `SELECT allocated_bytes FROM organization_storage_usage($1)`, scope.ID).Scan(&allocated); err != nil {
				return err
			}
			if *req.QuotaBytes > *companyQuota-(allocated-oldQuota) {
				return apperrors.Conflict("company storage budget exceeded")
			}
		}
		_, err = inner.db.Exec(ctx, `INSERT INTO drive_storage_accounts(user_id,quota_bytes,revision) VALUES($1,$2,1) ON CONFLICT(user_id) DO UPDATE SET quota_bytes=EXCLUDED.quota_bytes,revision=drive_storage_accounts.revision+1`, target, *req.QuotaBytes)
		return err
	})
}

func storageFreeBytes(quota, used int64) int64 {
	if used >= quota {
		return 0
	}
	return quota - used
}

func (r *OrganizationRepository) companyStorageCapacity(ctx context.Context, org uuid.UUID) (*domain.CompanyStorageCapacity, error) {
	if r.capacity == nil {
		return nil, errors.Join(errStorageCapacityUnavailable, apperrors.Conflict("storage capacity unavailable"))
	}
	c, err := r.capacity.Capacity(ctx)
	if err != nil || c.TotalBytes <= 0 || c.FreeBytes < 0 || c.FreeBytes > c.TotalBytes || c.ReserveBytes < 0 || c.ReserveBytes > c.TotalBytes {
		return nil, errors.Join(errStorageCapacityUnavailable, apperrors.Conflict("storage capacity unavailable"))
	}
	var used, committed, remaining int64
	err = r.db.QueryRow(ctx, `SELECT
 (SELECT used_bytes FROM organization_storage_usage($1)),
 COALESCE(SUM(GREATEST(COALESCE(a.quota_bytes,s.allocated_bytes),s.used_bytes)),0)::bigint,
 COALESCE(SUM(GREATEST(COALESCE(a.quota_bytes,s.allocated_bytes)-s.used_bytes,0)),0)::bigint
 FROM organizations o LEFT JOIN organization_storage_accounts a ON a.organization_id=o.id
 CROSS JOIN LATERAL organization_storage_usage(o.id) s WHERE o.id<>$1`, org).Scan(&used, &committed, &remaining)
	if err != nil {
		return nil, err
	}
	return &domain.CompanyStorageCapacity{StorageCapacity: c, OtherCommittedBytes: committed, OtherRemainingBytes: remaining, MaxQuotaBytes: domain.MaximumCompanyBudget(c, used, committed, remaining)}, nil
}
func (r *OrganizationRepository) AdministrationStorageCapacity(ctx context.Context, actor int64, org uuid.UUID) (*domain.CompanyStorageCapacity, error) {
	if _, err := r.accessScope(ctx, actor, &org); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger, capacity: r.capacity}
	out, err := inner.companyStorageCapacity(ctx, org)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
