package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
	"time"
)

func testDriveACL(t *testing.T, ctx context.Context, s *DriveService, root, nested *domain.Folder, file *domain.DriveFile) {
	t.Helper()
	// Sharing a single file never grants access to its parent or siblings.
	require.NoError(t, s.GrantPermissionsBulk(ctx, 2, domain.ResourceFile, file.ID, []int64{5}, domain.RoleEditor))
	_, err := s.ListFolderChildren(ctx, 5, &nested.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.CreateFolder(ctx, 5, &nested.ID, "Escalation")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	role, err := s.GetMyRole(ctx, 5, domain.ResourceFolder, nested.ID)
	require.NoError(t, err)
	require.Empty(t, role)
	_, err = s.CreatePublicLink(ctx, 5, "file", file.ID, "owner", time.Hour)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	err = s.GrantPermissionsBulk(ctx, 5, domain.ResourceFile, file.ID, []int64{6}, domain.RoleOwner)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.RevokePermissionsBulk(ctx, 2, domain.ResourceFile, file.ID, []int64{5}))
	// Delegated manager inherits down and can manage ACL without taking ownership.
	require.NoError(t, s.GrantPermissionsBulk(ctx, 1, domain.ResourceFolder, root.ID, []int64{4}, "manager"))
	role, err = s.GetMyRole(ctx, 4, domain.ResourceFile, file.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleOwner, role)
	require.NoError(t, s.GrantPermission(ctx, 4, domain.ResourceFile, file.ID, 3, domain.RoleEditor))
	role, err = s.GetMyRole(ctx, 3, domain.ResourceFile, file.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleEditor, role)
	require.NoError(t, s.RevokePermission(ctx, 4, domain.ResourceFile, file.ID, 3))
	role, err = s.GetMyRole(ctx, 3, domain.ResourceFile, file.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleViewer, role, "parent grant remains after direct revoke")
	err = s.TransferOwnership(ctx, 4, domain.ResourceFolder, root.ID, 6)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	err = s.RevokePermission(ctx, 4, domain.ResourceFolder, root.ID, 1)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	err = s.GrantPermission(ctx, 4, domain.ResourceFolder, root.ID, 999, domain.RoleViewer)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	// Public visibility grants only inherited viewing, never management.
	require.NoError(t, s.SetFolderPublicStatus(ctx, 4, root.ID, true))
	role, err = s.GetMyRole(ctx, 5, domain.ResourceFile, file.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleViewer, role)
	err = s.GrantPermission(ctx, 5, domain.ResourceFile, file.ID, 6, domain.RoleViewer)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.SetFolderPublicStatus(ctx, 4, root.ID, false))
	role, err = s.GetMyRole(ctx, 5, domain.ResourceFile, file.ID)
	require.NoError(t, err)
	require.Empty(t, role)
	// Accepting a weaker link preserves an existing stronger direct grant.
	link, err := s.CreatePublicLink(ctx, 4, "file", file.ID, "viewer", time.Hour)
	require.NoError(t, err)
	require.NoError(t, s.GrantPermission(ctx, 4, domain.ResourceFile, file.ID, 6, domain.RoleEditor))
	_, err = s.AcceptPublicLink(ctx, 6, link.Token)
	require.NoError(t, err)
	role, err = s.GetMyRole(ctx, 6, domain.ResourceFile, file.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleEditor, role)
	err = s.RevokePublicLink(ctx, 3, link.Token)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.RevokePublicLink(ctx, 4, link.Token))
	_, err = s.GetPublicItemByToken(ctx, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	link, err = s.CreatePublicLink(ctx, 4, "file", file.ID, "viewer", time.Hour)
	require.NoError(t, err)
	require.NoError(t, s.RevokePermissionsBulk(ctx, 1, domain.ResourceFolder, root.ID, []int64{4}))
	_, err = s.GetPublicItemByToken(ctx, link.Token)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	err = s.GrantPermission(ctx, 4, domain.ResourceFile, file.ID, 5, domain.RoleViewer)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.RevokePublicLink(ctx, 2, link.Token))
	require.NoError(t, s.RevokePermission(ctx, 2, domain.ResourceFile, file.ID, 6))
	err = s.GrantPermissionsBulk(ctx, 1, domain.ResourceFolder, root.ID, []int64{5}, "invalid")
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
}
