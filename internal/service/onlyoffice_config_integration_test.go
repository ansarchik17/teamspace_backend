package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestOnlyOfficeConfigIntegration(t *testing.T) {
	t.Setenv("ONLYOFFICE_JWT_SECRET", "test-office-secret")
	t.Setenv("ONLYOFFICE_PUBLIC_URL", "https://office.example.com")
	ctx, db, s := newDrivePostgresTest(t)
	for ext, kind := range map[string]string{"docx": "word", "xlsx": "cell", "pptx": "slide", "zip": ""} {
		file := &domain.DriveFile{ID: uuid.New(), OwnerID: 1, RealName: "test." + ext, ObjectName: uuid.NewString(), ContentHash: uuid.NewString(), Size: 1, MimeType: "application/zip", Scope: "personal", Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, file, 1))
		cfg, err := s.BuildOnlyOfficeConfig(ctx, 1, file.ID, "edit", "secret", "https://api.example.com")
		if kind == "" {
			require.Equal(t, 415, apperrors.ToAppError(err).StatusCode)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, kind, cfg.DocumentType)
		require.True(t, cfg.CanEdit)
		require.NotEmpty(t, cfg.Token)
		require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 3, domain.RoleViewer))
		cfg, err = s.BuildOnlyOfficeConfig(ctx, 3, file.ID, "edit", "secret", "https://api.example.com")
		require.NoError(t, err)
		require.False(t, cfg.CanEdit)
		require.Equal(t, "view", cfg.Mode)
		_, err = s.BuildOnlyOfficeConfig(ctx, 5, file.ID, "edit", "secret", "https://api.example.com")
		require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW() WHERE id=$1`, file.ID)
		require.NoError(t, err)
		_, err = s.BuildOnlyOfficeConfig(ctx, 1, file.ID, "edit", "secret", "https://api.example.com")
		require.Error(t, err)
	}
}
