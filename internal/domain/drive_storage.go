package domain

// DriveStorage reports logical allocation, including recoverable trash and
// historical content. It is not the physical MinIO bucket size.
type DriveStorage struct {
	CompanyQuotaBytes  *int64           `json:"company_quota_bytes"`
	CompanyUsedBytes   *int64           `json:"company_used_bytes"`
	CompanyFreeBytes   *int64           `json:"company_free_bytes"`
	QuotaBytes         int64            `json:"quota_bytes"`
	UsedBytes          int64            `json:"used_bytes"`
	FreeBytes          int64            `json:"free_bytes"`
	ActiveBytes        int64            `json:"active_bytes"`
	VersionBytes       int64            `json:"version_bytes"`
	TrashBytes         int64            `json:"trash_bytes"`
	MaxUploadBytes     int64            `json:"max_upload_bytes"`
	MaxRequestBytes    int64            `json:"max_request_bytes"`
	MaxFilesPerRequest int              `json:"max_files_per_request"`
	UploadLimits       map[string]int64 `json:"upload_limits"`
	QuotaExceeded      bool             `json:"quota_exceeded"`
	CanUpload          bool             `json:"can_upload"`
}
