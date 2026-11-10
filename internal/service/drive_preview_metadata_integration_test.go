package service

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
	"time"
)

func TestDrivePreviewMetadataIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	folder, err := s.CreateFolder(ctx, 1, nil, "preview-metadata")
	require.NoError(t, err)
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 3, domain.RoleViewer))
	link, err := s.CreatePublicLink(ctx, 1, "folder", folder.ID, "viewer", time.Hour)
	require.NoError(t, err)
	for _, tc := range []struct{ name, mime, kind string }{
		{"report.pdf", "application/pdf", "pdf"}, {"image.png", "image/png", "image"},
		{"song.mp3", "audio/mpeg", "audio"}, {"movie.mp4", "video/mp4", "video"},
		{"report.docx", "application/zip", "office"}, {"table.xlsx", "application/zip", "office"},
		{"slides.pptx", "application/zip", "office"}, {"page.html", "text/html", "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := &domain.DriveFile{ID: uuid.New(), OwnerID: 1, FolderID: &folder.ID, RealName: tc.name,
				ObjectName: uuid.NewString(), ContentHash: uuid.NewString(), Size: 1, MimeType: tc.mime,
				Scope: "personal", Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, file, 1))
			p, err := s.GetFilePreview(ctx, 3, file.ID)
			require.NoError(t, err)
			require.Equal(t, tc.kind, p.Kind)
			_, err = s.GetFilePreview(ctx, 5, file.ID)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			target := PublicLinkTarget{Type: domain.ResourceFile, ID: file.ID}
			p, err = s.GetPublicFilePreview(ctx, link.Token, target)
			require.NoError(t, err)
			require.Equal(t, tc.kind, p.Kind)
			require.False(t, p.AuthRequired)
			require.Contains(t, p.DownloadURL, link.Token.String())
			if tc.kind == "office" {
				cfg, err := s.BuildPublicOfficePreview(ctx, link.Token, target)
				require.NoError(t, err)
				require.False(t, cfg.CanEdit)
				require.Equal(t, "view", cfg.Mode)
				require.Empty(t, cfg.CallbackURL)
				require.Contains(t, cfg.DocumentURL, "/public/drive/files/"+link.Token.String()+"/download")
				require.Contains(t, cfg.DocumentURL, file.ID.String())
				require.Contains(t, cfg.DocumentKey, link.Token.String())
				require.NotContains(t, cfg.DocumentURL, "/onlyoffice/download")
			} else {
				_, err = s.BuildPublicOfficePreview(ctx, link.Token, target)
				require.Equal(t, 415, apperrors.ToAppError(err).StatusCode)
			}
			require.NoError(t, s.RevokePermission(ctx, 1, domain.ResourceFolder, folder.ID, 3))
			_, err = s.GetFilePreview(ctx, 3, file.ID)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 3, domain.RoleViewer))
			_, err = db.Exec(ctx, `UPDATE drive_shared_links SET expires_at=(NOW() AT TIME ZONE 'UTC')-INTERVAL '1 second' WHERE token=$1`, link.Token)
			require.NoError(t, err)
			_, err = s.GetPublicFilePreview(ctx, link.Token, target)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			_, err = s.BuildPublicOfficePreview(ctx, link.Token, target)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			_, err = db.Exec(ctx, `UPDATE drive_shared_links SET expires_at=NULL WHERE token=$1`, link.Token)
			require.NoError(t, err)
		})
	}
	require.NoError(t, s.RevokePublicLink(ctx, 1, link.Token))
	_, err = s.GetPublicFilePreview(ctx, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.BuildPublicOfficePreview(ctx, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
}
