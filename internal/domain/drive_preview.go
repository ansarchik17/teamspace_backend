package domain

import "github.com/google/uuid"

// DrivePreview describes how a client can display a file without changing it.
type DrivePreview struct {
	FileID       uuid.UUID `json:"file_id"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	Version      int       `json:"version"`
	Kind         string    `json:"kind"` // pdf, image, audio, video, office, unsupported
	MIMEType     string    `json:"mime_type"`
	Supported    bool      `json:"supported"`
	PreviewURL   string    `json:"preview_url,omitempty"`
	DownloadURL  string    `json:"download_url"`
	AuthRequired bool      `json:"auth_required"`
	Reason       string    `json:"reason,omitempty"`
}
