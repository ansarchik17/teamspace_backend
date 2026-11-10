package domain

// API safety ceiling; prevents impractical allocations and arithmetic overflow.
const MaxAdministrationStorageBytes int64 = 1 << 50

type AdministrationStorageRequest struct {
	QuotaBytes *int64 `json:"quota_bytes"`
	Revision   *int64 `json:"revision"`
}

func (r AdministrationStorageRequest) Valid() bool {
	return r.QuotaBytes != nil && *r.QuotaBytes >= 0 && *r.QuotaBytes <= MaxAdministrationStorageBytes && r.Revision != nil && *r.Revision >= 0
}

type AdministrationStorage struct {
	QuotaBytes       *int64 `json:"quota_bytes"`
	AllocatedBytes   int64  `json:"allocated_bytes"`
	UsedBytes        int64  `json:"used_bytes"`
	FreeBytes        *int64 `json:"free_bytes"`
	UnallocatedBytes *int64 `json:"unallocated_bytes"`
	Revision         int64  `json:"revision"`
}

type AdministrationStorageUser struct {
	UserID     int64  `json:"user_id"`
	FullName   string `json:"full_name"`
	QuotaBytes int64  `json:"quota_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	Revision   int64  `json:"revision"`
}

type AdministrationStorageUsers struct {
	Items  []AdministrationStorageUser `json:"items"`
	Limit  int                         `json:"limit"`
	Offset int                         `json:"offset"`
	Total  int                         `json:"total"`
}
