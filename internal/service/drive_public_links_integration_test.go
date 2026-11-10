package service

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func testDrivePublicLinks(t *testing.T, ctx context.Context, db *pgxpool.Pool, s *DriveService) {
	t.Helper()
	folder, err := s.CreateFolder(ctx, 1, nil, "public-link-lifecycle")
	require.NoError(t, err)
	links, err := s.ListPublicLinks(ctx, 1, domain.ResourceFolder, folder.ID, 50, 0)
	require.NoError(t, err)
	require.NotNil(t, links)
	require.Empty(t, links)
	link, err := s.CreatePublicLink(ctx, 1, "folder", folder.ID, "manager", time.Hour)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(4), link.Token.Version())
	require.Equal(t, "owner", link.Role)
	require.WithinDuration(t, time.Now().Add(time.Hour), *link.ExpiresAt, time.Second)
	other, err := s.CreatePublicLink(ctx, 1, "folder", folder.ID, "viewer", 0)
	require.NoError(t, err)
	require.Nil(t, other.ExpiresAt)
	require.NotEqual(t, link.Token, other.Token)
	links, err = s.ListPublicLinks(ctx, 1, domain.ResourceFolder, folder.ID, 1, 0)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, other.Token, links[0].Token)
	links, err = s.ListPublicLinks(ctx, 1, domain.ResourceFolder, folder.ID, 1, 1)
	require.NoError(t, err)
	require.Equal(t, link.Token, links[0].Token)
	require.WithinDuration(t, *link.ExpiresAt, *links[0].ExpiresAt, time.Microsecond)
	_, err = s.ListPublicLinks(ctx, 3, domain.ResourceFolder, folder.ID, 50, 0)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	item, err := s.GetPublicItemByToken(ctx, link.Token)
	require.NoError(t, err)
	require.Equal(t, folder.ID, item.Folder.ID)
	// Accepting a valid link must not leave a permanent ACL behind.
	_, err = s.AcceptPublicLink(ctx, 5, link.Token)
	require.NoError(t, err)
	roleBeforeExpiry, err := s.GetMyRole(ctx, 5, domain.ResourceFolder, folder.ID)
	require.NoError(t, err)
	require.Empty(t, roleBeforeExpiry)
	nested, err := s.CreateFolder(ctx, 1, &folder.ID, "nested")
	require.NoError(t, err)
	outside, err := s.CreateFolder(ctx, 1, nil, "outside-link")
	require.NoError(t, err)
	item, err = s.GetPublicItemByToken(ctx, link.Token, PublicLinkTarget{Type: domain.ResourceFolder, ID: nested.ID})
	require.NoError(t, err)
	require.Equal(t, nested.ID, item.Folder.ID)
	require.Equal(t, "viewer", item.AccessRole)
	_, err = s.GetPublicItemByToken(ctx, link.Token, PublicLinkTarget{Type: domain.ResourceFolder, ID: outside.ID})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	file := &domain.DriveFile{ID: uuid.New(), OwnerID: 1, FolderID: &nested.ID,
		RealName: "shared.txt", ObjectName: "drv004/" + uuid.NewString(), ContentHash: uuid.NewString(),
		Size: 1, MimeType: "text/plain", Version: 1, Scope: "personal", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, file, 1))
	item, err = s.GetPublicItemByToken(ctx, link.Token, PublicLinkTarget{Type: domain.ResourceFile, ID: file.ID})
	require.NoError(t, err)
	require.Equal(t, file.ID, item.File.ID)
	require.Contains(t, item.File.DownloadURL, link.Token.String())
	fileLink, err := s.CreatePublicLink(ctx, 1, "file", file.ID, "viewer", time.Hour)
	require.NoError(t, err)
	_, err = s.GetPublicItemByToken(ctx, fileLink.Token, PublicLinkTarget{Type: domain.ResourceFolder, ID: nested.ID})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.RevokePublicLink(ctx, 1, fileLink.Token))
	// A moved child is no longer in the scope of the link.
	require.NoError(t, s.UpdateFolder(ctx, 1, nested.ID, nil, &outside.ID))
	_, err = s.GetPublicItemByToken(ctx, link.Token, PublicLinkTarget{Type: domain.ResourceFolder, ID: nested.ID})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, _, _, _, err = s.DownloadPublicFile(ctx, link.Token, PublicLinkTarget{Type: domain.ResourceFile, ID: file.ID})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE drive_shared_links SET expires_at=(NOW() AT TIME ZONE 'UTC')-INTERVAL '1 second' WHERE token=$1`, link.Token)
	require.NoError(t, err)
	_, err = s.GetPublicItemByToken(ctx, link.Token)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, _, _, _, err = s.DownloadPublicFile(ctx, link.Token)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.AcceptPublicLink(ctx, 5, link.Token)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	role, err := s.GetMyRole(ctx, 5, domain.ResourceFolder, folder.ID)
	require.NoError(t, err)
	require.Empty(t, role)
	require.NoError(t, s.RevokePublicLink(ctx, 1, link.Token))
	_, err = s.GetPublicItemByToken(ctx, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.AcceptPublicLink(ctx, 5, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, _, _, _, err = s.DownloadPublicFile(ctx, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	links, err = s.ListPublicLinks(ctx, 1, domain.ResourceFolder, folder.ID, 50, 0)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, other.Token, links[0].Token)
	require.NoError(t, s.RevokePublicLink(ctx, 1, other.Token))
}

// This lifecycle suite needs only PostgreSQL; denied downloads must never touch storage.
func TestDrivePublicLinksIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	testDrivePublicLinks(t, ctx, db, s)
}

func newDrivePostgresTest(t *testing.T) (context.Context, *pgxpool.Pool, *DriveService) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "drive_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY); CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,username TEXT,first_name TEXT,last_name TEXT,email TEXT,avatar_url TEXT,deleted_at TIMESTAMPTZ); INSERT INTO users(id) VALUES(1),(2),(3),(4),(5),(6);`)
	require.NoError(t, err)
	for _, name := range []string{"014_create_cloud_disk.sql", "024_alter_table_files.sql", "20260504143300_add_is_public_to_drive.sql", "20260513000200_add_scope_to_files.sql", "20260624110000_add_content_hash_to_drive_files.sql", "20260728193000_add_external_source_to_drive_files.sql", "20260123001000_create_drive_links.sql", "20260626000100_create_drive_activity_logs.sql", "20260927120000_drive_version_history_index.sql", "20260927140000_drive_storage_quotas.sql", "20260928120000_drive_tags_download_audit.sql", "20260929140000_office_save_receipts.sql", "20260929160000_office_presence.sql", "20260930120000_office_save_failures.sql", "20261012000400_company_storage_quotas.sql", "20261013000100_company_shared_storage.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err, name)
	}
	_, err = db.Exec(ctx, `CREATE TABLE user_recent_items(user_id BIGINT,resource_type TEXT,resource_id UUID,last_accessed_at TIMESTAMPTZ,UNIQUE(user_id,resource_type,resource_id))`)
	require.NoError(t, err)

	repo := repository.NewDriveRepository(db, zap.NewNop())
	s := NewDriveService(repo, repository.NewRecentRepository(db, zap.NewNop()), nil, "http://localhost", nil, nil, zap.NewNop())
	return ctx, db, s
}
