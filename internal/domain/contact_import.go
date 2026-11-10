package domain

import "time"

type ContactImportRow struct {
	Row             int     `json:"row"`
	ExternalID      string  `json:"external_id"`
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	FirstName       string  `json:"first_name"`
	LastName        string  `json:"last_name"`
	Position        *string `json:"position,omitempty"`
	Department      *string `json:"department,omitempty"`
	ManagerUsername *string `json:"manager_username,omitempty"`
	WorkEmail       *string `json:"work_email,omitempty"`
	Phone           *string `json:"phone,omitempty"`
	IsActive        *bool   `json:"is_active,omitempty"`
}

type ContactImportIssue struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ContactImportResult struct {
	DryRun  bool                 `json:"dry_run"`
	Applied bool                 `json:"applied"`
	Total   int                  `json:"total"`
	Created int                  `json:"created"`
	Updated int                  `json:"updated"`
	Issues  []ContactImportIssue `json:"issues"`
}

type DirectorySettings struct {
	Type       string            `json:"type"`
	URL        string            `json:"url"`
	BaseDN     string            `json:"base_dn"`
	BindDN     string            `json:"bind_dn"`
	Filter     string            `json:"filter"`
	Attributes map[string]string `json:"attributes"`
	CACertPEM  string            `json:"ca_cert_pem,omitempty"`
	Enabled    bool              `json:"enabled"`
}

type DirectoryConfigRequest struct {
	DirectorySettings
	BindPassword *string `json:"bind_password"`
}

type DirectoryConfig struct {
	DirectorySettings
	Revision        int64      `json:"revision"`
	HasBindPassword bool       `json:"has_bind_password"`
	LastSyncAt      *time.Time `json:"last_sync_at,omitempty"`
	LastSyncCount   int        `json:"last_sync_count"`
}

type DirectorySyncRequest struct {
	DryRun bool `json:"dry_run"`
}
