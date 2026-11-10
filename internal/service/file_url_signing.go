package service

import "github.com/west6ide/workspace-backend/internal/fileurl"

func signLegacyFileDownloadURL(rawURL *string) *string {
	return fileurl.SignLegacyDownloadURL(rawURL)
}
