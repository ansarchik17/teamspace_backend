package repository

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *DriveRepository) GetDriveStorage(ctx context.Context, userID int64) (*domain.DriveStorage, error) {
	var s domain.DriveStorage
	err := r.db.QueryRow(ctx, `SELECT COALESCE(a.quota_bytes,p.quota_bytes),COALESCE(a.max_upload_bytes,p.max_upload_bytes),u.active_bytes,u.version_bytes,u.trash_bytes,company.quota_bytes,company_usage.used_bytes
 FROM drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=$1
 LEFT JOIN users owner ON owner.id=$1
 LEFT JOIN organization_storage_accounts company ON company.organization_id=owner.organization_id
 LEFT JOIN LATERAL (SELECT used_bytes FROM organization_storage_usage(owner.organization_id) WHERE company.quota_bytes IS NOT NULL) company_usage ON true
 CROSS JOIN drive_storage_usage($1) u`, userID).Scan(&s.QuotaBytes, &s.MaxUploadBytes, &s.ActiveBytes, &s.VersionBytes, &s.TrashBytes, &s.CompanyQuotaBytes, &s.CompanyUsedBytes)
	if err != nil {
		return nil, err
	}
	s.UsedBytes = s.ActiveBytes + s.VersionBytes + s.TrashBytes
	s.FreeBytes = s.QuotaBytes - s.UsedBytes
	if s.FreeBytes < 0 {
		s.FreeBytes = 0
	}
	s.QuotaExceeded = s.UsedBytes > s.QuotaBytes
	if s.CompanyQuotaBytes != nil && s.CompanyUsedBytes != nil {
		free := storageFreeBytes(*s.CompanyQuotaBytes, *s.CompanyUsedBytes)
		s.CompanyFreeBytes = &free
		s.FreeBytes = min(s.FreeBytes, free)
		s.QuotaExceeded = s.QuotaExceeded || *s.CompanyUsedBytes > *s.CompanyQuotaBytes
	}
	s.CanUpload = s.FreeBytes > 0
	return &s, nil
}
