package service

import (
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestOfficeCollaborationIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	_, err := db.Exec(ctx, `UPDATE users SET first_name='Ансар',last_name='Келес',avatar_url='https://example.com/avatar.png' WHERE id=3`)
	require.NoError(t, err)
	file := quotaTestFile(1, 1)
	file.RealName = "Collaboration.docx"
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, file, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 3, domain.RoleEditor))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 4, domain.RoleViewer))
	config := func(user int64) *OnlyOfficeEditorConfig {
		cfg, err := s.BuildOnlyOfficeConfig(ctx, user, file.ID, "edit", "secret", "https://example.com")
		require.NoError(t, err)
		return cfg
	}
	claims := func(cfg *OnlyOfficeEditorConfig) *OnlyOfficeTokenClaims {
		u, err := url.Parse(cfg.CollaborationURL)
		require.NoError(t, err)
		token, err := ValidateOnlyOfficeCollaborationToken(u.Query().Get("token"), "secret")
		require.NoError(t, err)
		return token
	}
	cfg := config(3)
	require.Equal(t, "Ансар Келес", cfg.UserName)
	require.Equal(t, "https://example.com/avatar.png", cfg.UserImage)
	first, second, owner, viewer := claims(cfg), claims(config(3)), claims(config(1)), claims(config(4))
	require.NotEqual(t, first.SessionID, second.SessionID)
	require.Equal(t, owner.DocumentKey, first.DocumentKey)
	require.Equal(t, "view", viewer.Mode)
	require.NoError(t, s.UpdateOfficePresence(ctx, first, "heartbeat"))
	require.NoError(t, s.UpdateOfficePresence(ctx, second, "heartbeat"))
	require.NoError(t, s.UpdateOfficePresence(ctx, owner, "heartbeat"))
	require.Equal(t, 403, apperrors.ToAppError(s.UpdateOfficePresence(ctx, viewer, "heartbeat")).StatusCode)
	active, err := s.OfficeParticipants(ctx, 4, file.ID)
	require.NoError(t, err)
	require.Len(t, active, 2)
	require.Equal(t, "Ансар Келес", active[1].Name)
	_, err = s.OfficeParticipants(ctx, 2, file.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	users, err := s.OfficeUsers(ctx, 4, file.ID, first.DocumentKey, []string{"1", "2", "3", "4"})
	require.NoError(t, err)
	require.Len(t, users, 3)
	require.NoError(t, s.UpdateOfficePresence(ctx, first, "leave"))
	active, err = s.OfficeParticipants(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Len(t, active, 2)
	require.NoError(t, s.UpdateOfficePresence(ctx, second, "leave"))
	active, err = s.OfficeParticipants(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.NoError(t, s.UpdateOfficePresence(ctx, second, "heartbeat"))
	_, err = db.Exec(ctx, `UPDATE drive_office_presence SET last_seen_at=NOW()-INTERVAL '91 seconds' WHERE user_id=3`)
	require.NoError(t, err)
	active, err = s.OfficeParticipants(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.NoError(t, s.UpdateOfficePresence(ctx, second, "heartbeat"))
	require.NoError(t, s.RevokePermission(ctx, 1, domain.ResourceFile, file.ID, 3))
	active, err = s.OfficeParticipants(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, 403, apperrors.ToAppError(s.UpdateOfficePresence(ctx, second, "heartbeat")).StatusCode)
	users, err = s.OfficeUsers(ctx, 1, file.ID, first.DocumentKey, []string{"3"})
	require.NoError(t, err)
	require.Empty(t, users)
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 3, domain.RoleEditor))
	// Force-save keeps editor presence; final/external rotation hides old sessions.
	_, _, err = s.repo.AppendOfficeVersion(ctx, file.ID, "force-save", 1, 1, 1, first.DocumentKey, "event", false)
	require.NoError(t, err)
	require.NoError(t, s.UpdateOfficePresence(ctx, second, "heartbeat"))
	_, err = s.repo.AppendFileVersion(ctx, file.ID, "external", 1, 1, 2)
	require.NoError(t, err)
	active, err = s.OfficeParticipants(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Empty(t, active)
	require.Equal(t, 409, apperrors.ToAppError(s.UpdateOfficePresence(ctx, second, "heartbeat")).StatusCode)
	_, err = s.OfficeUsers(ctx, 1, file.ID, first.DocumentKey, []string{"3"})
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	// A tab's leave cannot remove another user's presence.
	other := *second
	other.UserID = 4
	other.SessionID = owner.SessionID
	require.NoError(t, s.UpdateOfficePresence(ctx, &other, "leave"))
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM drive_office_presence WHERE session_id=$1`, uuid.MustParse(owner.SessionID)).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, s.repo.SoftDeleteFile(ctx, file.ID))
	_, err = s.OfficeParticipants(ctx, 1, file.ID)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
}
