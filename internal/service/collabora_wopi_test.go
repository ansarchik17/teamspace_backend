package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestWOPIPublicUserNameDoesNotReadWorkspaceProfile(t *testing.T) {
	// A public-link session must never expose a Workspace account name, even
	// if claims also contain an account ID. No repository is needed for guests.
	s := &DriveService{}
	name, err := s.GetWOPIUserName(context.Background(), WOPIAccessClaims{
		UserID: 9, PublicToken: uuid.NewString(),
	})
	require.NoError(t, err)
	require.Equal(t, "Гость", name)
}

func TestWOPIWorkspaceUserNamesIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	_, err := db.Exec(ctx, `UPDATE users SET first_name='Айгерим', last_name='Омарова', username='aigerim' WHERE id=1;
UPDATE users SET first_name='Султан', last_name='Ахметов', username='sultan' WHERE id=2;`)
	require.NoError(t, err)
	file := &domain.DriveFile{
		ID: uuid.New(), OwnerID: 1, RealName: "shared.docx", ObjectName: "wopi/" + uuid.NewString(),
		ContentHash: uuid.NewString(), Size: 1, MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Version: 1, Scope: "personal", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, file, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 2, domain.RoleEditor))

	for _, tc := range []struct {
		userID int64
		name   string
	}{
		{1, "Айгерим Омарова"},
		{2, "Султан Ахметов"},
	} {
		session, err := s.BuildWOPISession(ctx, tc.userID, file.ID, "edit", "test-secret")
		require.NoError(t, err)
		require.True(t, session.CanWrite)
		require.Equal(t, tc.name, session.UserName)
		claims, err := ValidateWOPIAccessToken(session.AccessToken, "test-secret")
		require.NoError(t, err)
		name, err := s.GetWOPIUserName(ctx, *claims)
		require.NoError(t, err)
		require.Equal(t, tc.name, name)
	}

	// Existing signed tokens contain only the user ID. Resolve the latest
	// profile for them too, without forcing users to sign in again.
	token, err := GenerateWOPIAccessToken(file.ID, 2, "", true, "test-secret")
	require.NoError(t, err)
	claims, err := ValidateWOPIAccessToken(token, "test-secret")
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET first_name='Данияр', last_name='Жумаев' WHERE id=2`)
	require.NoError(t, err)
	name, err := s.GetWOPIUserName(ctx, *claims)
	require.NoError(t, err)
	require.Equal(t, "Данияр Жумаев", name)

	_, err = db.Exec(ctx, `UPDATE users SET first_name=' ', last_name='', username=' sultan ' WHERE id=2`)
	require.NoError(t, err)
	name, err = s.GetWOPIUserName(ctx, *claims)
	require.NoError(t, err)
	require.Equal(t, "sultan", name)

	_, err = db.Exec(ctx, `UPDATE users SET username='' WHERE id=2`)
	require.NoError(t, err)
	name, err = s.GetWOPIUserName(ctx, *claims)
	require.NoError(t, err)
	require.Equal(t, "Пользователь", name)

	_, err = s.GetWOPIUserName(ctx, WOPIAccessClaims{UserID: 999})
	require.Error(t, err)
	require.Equal(t, 500, apperrors.ToAppError(err).StatusCode)
}
