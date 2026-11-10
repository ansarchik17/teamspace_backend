package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func testDrivePreview(t *testing.T, ctx context.Context, db *pgxpool.Pool, s *DriveService) {
	t.Helper()
	folder, err := s.CreateFolder(ctx, 1, nil, "preview-tests")
	require.NoError(t, err)
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 3, domain.RoleViewer))
	payload := "%PDF-1.7\nDRV-005 range test\n"
	file, err := s.UploadFileToFolder(ctx, 1, &folder.ID, "report.pdf", int64(len(payload)), "application/pdf", strings.NewReader(payload))
	require.NoError(t, err)
	p, err := s.GetFilePreview(ctx, 3, file.ID)
	require.NoError(t, err)
	require.Equal(t, "pdf", p.Kind)
	require.Equal(t, int64(len(payload)), p.Size)
	_, err = s.GetFilePreview(ctx, 5, file.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, _, err = s.OpenFilePreview(ctx, 5, file.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	reader, _, err := s.OpenFilePreview(ctx, 3, file.ID)
	require.NoError(t, err)
	// Real MinIO seeker supports ServeContent's seek-to-end/rewind/range sequence.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Range", "bytes=2-6")
	w := httptest.NewRecorder()
	http.ServeContent(w, req, "report.pdf", time.Time{}, reader)
	require.NoError(t, reader.Close())
	require.Equal(t, 206, w.Code)
	require.Equal(t, payload[2:7], w.Body.String())
	require.NoError(t, s.RevokePermission(ctx, 1, domain.ResourceFolder, folder.ID, 3))
	_, _, err = s.OpenFilePreview(ctx, 3, file.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	link, err := s.CreatePublicLink(ctx, 1, "file", file.ID, "viewer", time.Hour)
	require.NoError(t, err)
	reader, p, err = s.OpenPublicFilePreview(ctx, link.Token)
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, payload, string(data))
	require.False(t, p.AuthRequired)
	_, err = db.Exec(ctx, `UPDATE drive_shared_links SET expires_at=(NOW() AT TIME ZONE 'UTC')-INTERVAL '1 second' WHERE token=$1`, link.Token)
	require.NoError(t, err)
	_, _, err = s.OpenPublicFilePreview(ctx, link.Token)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.RevokePublicLink(ctx, 1, link.Token))
	_, _, err = s.OpenPublicFilePreview(ctx, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW() WHERE id=$1`, file.ID)
	require.NoError(t, err)
	_, _, err = s.OpenFilePreview(ctx, 1, file.ID)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	for _, ext := range []string{"docx", "xlsx", "pptx"} {
		body := "office fixture " + ext
		office, err := s.UploadFileToFolder(ctx, 1, &folder.ID, "report."+ext, int64(len(body)), "application/zip", strings.NewReader(body))
		require.NoError(t, err)
		p, err = s.GetFilePreview(ctx, 1, office.ID)
		require.NoError(t, err)
		require.Equal(t, "office", p.Kind)
		cfg, err := s.BuildOnlyOfficeConfig(ctx, 1, office.ID, "view", "test-secret", "https://example.com")
		require.NoError(t, err)
		require.Equal(t, "view", cfg.Mode)
		require.False(t, cfg.CanEdit)
		require.Equal(t, ext, cfg.FileType)
		require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, office.ID, 3, domain.RoleViewer))
		err = s.SaveOnlyOfficeCallback(ctx, 3, office.ID, OnlyOfficeCallback{Status: 2, URL: "http://must-not-be-fetched.invalid/file"})
		require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		_, _, err = s.OpenFilePreview(ctx, 1, office.ID)
		require.Equal(t, 415, apperrors.ToAppError(err).StatusCode)
	}
	body := "<script>alert(1)</script>"
	unsafe, err := s.UploadFileToFolder(ctx, 1, &folder.ID, "unsafe.html", int64(len(body)), "text/html", strings.NewReader(body))
	require.NoError(t, err)
	p, err = s.GetFilePreview(ctx, 1, unsafe.ID)
	require.NoError(t, err)
	require.False(t, p.Supported)
	_, _, err = s.OpenFilePreview(ctx, 1, unsafe.ID)
	require.Equal(t, 415, apperrors.ToAppError(err).StatusCode)
}
