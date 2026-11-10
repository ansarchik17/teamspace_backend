package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestMSG007GroupRemovalPolicy(t *testing.T) {
	tests := []struct {
		name       string
		actorID    int64
		targetID   int64
		actorRole  string
		targetRole string
		wantCode   apperrors.ErrorCode
	}{
		{name: "owner removes member", actorID: 1, targetID: 2, actorRole: "owner", targetRole: "member"},
		{name: "admin removes member", actorID: 2, targetID: 3, actorRole: "admin", targetRole: "member"},
		{name: "cannot remove self", actorID: 2, targetID: 2, actorRole: "admin", targetRole: "admin", wantCode: apperrors.ErrCodeForbidden},
		{name: "cannot remove owner", actorID: 2, targetID: 1, actorRole: "admin", targetRole: "owner", wantCode: apperrors.ErrCodeForbidden},
		{name: "admin cannot remove peer admin", actorID: 2, targetID: 3, actorRole: "admin", targetRole: "admin", wantCode: apperrors.ErrCodeForbidden},
		{name: "owner removes admin", actorID: 1, targetID: 2, actorRole: "owner", targetRole: "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGroupMemberRemoval(tt.actorID, tt.targetID, tt.actorRole, tt.targetRole)
			if tt.wantCode == "" {
				require.NoError(t, err)
				return
			}
			appErr := apperrors.GetAppError(err)
			require.NotNil(t, appErr)
			require.Equal(t, tt.wantCode, appErr.Code)
		})
	}
}

func TestMSG007ChannelManagementPolicy(t *testing.T) {
	require.NoError(t, validateChannelMemberRemoval(1, 2, domain.ChannelRoleOwner, domain.ChannelRoleAdmin))
	require.NoError(t, validateChannelMemberRemoval(2, 3, domain.ChannelRoleAdmin, domain.ChannelRoleSubscriber))

	for _, tc := range []struct {
		actorID, targetID int64
		actorRole         domain.ChannelRole
		targetRole        domain.ChannelRole
	}{
		{actorID: 2, targetID: 2, actorRole: domain.ChannelRoleAdmin, targetRole: domain.ChannelRoleAdmin},
		{actorID: 2, targetID: 1, actorRole: domain.ChannelRoleAdmin, targetRole: domain.ChannelRoleOwner},
		{actorID: 2, targetID: 3, actorRole: domain.ChannelRoleAdmin, targetRole: domain.ChannelRoleAdmin},
	} {
		err := validateChannelMemberRemoval(tc.actorID, tc.targetID, tc.actorRole, tc.targetRole)
		appErr := apperrors.GetAppError(err)
		require.NotNil(t, appErr)
		require.Equal(t, apperrors.ErrCodeForbidden, appErr.Code)
	}

	require.NoError(t, validateChannelRoleChange(1, 2, domain.ChannelRoleSubscriber))
	err := validateChannelRoleChange(1, 1, domain.ChannelRoleOwner)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.GetAppError(err).Code)
}

func TestMSG007GroupRolePolicyProtectsOwner(t *testing.T) {
	require.NoError(t, validateGroupRoleChange(1, 2, "member"))
	for _, tc := range []struct {
		actorID, targetID int64
		targetRole        string
	}{
		{actorID: 1, targetID: 1, targetRole: "owner"},
		{actorID: 1, targetID: 2, targetRole: "owner"},
	} {
		err := validateGroupRoleChange(tc.actorID, tc.targetID, tc.targetRole)
		appErr := apperrors.GetAppError(err)
		require.NotNil(t, appErr)
		require.Equal(t, apperrors.ErrCodeForbidden, appErr.Code)
	}
}
