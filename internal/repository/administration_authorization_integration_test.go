package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestAdministrationAuthorizationMatrix(t *testing.T) {
	ctx, db, a, b := newAdministrationStorageTestDB(t)
	for _, file := range []string{"20261012000200_company_user_blocks.sql", "20261012000300_company_modules.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	r := NewOrganizationRepository(db, zap.NewNop())
	reads := map[string]func(context.Context, int64, *uuid.UUID) error{
		"modules": func(ctx context.Context, actor int64, org *uuid.UUID) error {
			_, err := r.ListAdministrationModules(ctx, actor, org)
			return err
		},
		"storage": func(ctx context.Context, actor int64, org *uuid.UUID) error {
			_, err := r.AdministrationStorage(ctx, actor, org)
			return err
		},
		"audit": func(ctx context.Context, actor int64, org *uuid.UUID) error {
			_, err := r.ListAdministrationAudit(ctx, actor, org, org != nil, domain.AdministrationAuditFilter{Limit: 10})
			return err
		},
	}
	for _, tc := range []struct {
		name    string
		actor   int64
		org     *uuid.UUID
		allowed bool
	}{
		{"platform without company can select A", 1, &a, true},
		{"platform without company can select B", 1, &b, true},
		{"director A current", 2, nil, true},
		{"director B current", 4, nil, true},
		{"employee A current", 3, nil, false},
		{"employee B current", 5, nil, false},
		{"director cannot use platform API for own company", 2, &a, false},
		{"director cannot use platform API for foreign company", 2, &b, false},
		{"unknown actor", 999, &a, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, read := range reads {
				t.Run(name, func(t *testing.T) {
					err := read(ctx, tc.actor, tc.org)
					if tc.allowed {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
						require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
					}
				})
			}
		})
	}
	yes, rev := true, int64(1)
	// Failed authorization must leave both resource state and audit unchanged.
	for _, actor := range []int64{2, 3, 4, 5, 999} {
		err := r.SetAdministrationModule(ctx, actor, &a, "iform", domain.AdministrationModuleRequest{Enabled: &yes, Revision: &rev})
		require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	}
	var auditCount, revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit`).Scan(&auditCount))
	require.Zero(t, auditCount)
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_modules WHERE organization_id=$1 AND application='iform'`, a).Scan(&revision))
	require.Equal(t, int64(1), revision)
	// Owner changes take effect immediately for already authenticated actor IDs.
	_, err := db.Exec(ctx, `UPDATE organizations SET owner_id=3 WHERE id=$1`, a)
	require.NoError(t, err)
	for _, read := range reads {
		require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(read(ctx, 2, nil)).Code)
		require.NoError(t, read(ctx, 3, nil))
	}
	_, err = db.Exec(ctx, `UPDATE users SET role='user' WHERE id=1`)
	require.NoError(t, err)
	for _, read := range reads {
		require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(read(ctx, 1, &a)).Code)
	}
	for _, state := range []string{"is_active=false", "is_active=true,deleted_at=now()"} {
		_, err = db.Exec(ctx, `UPDATE users SET `+state+` WHERE id=3`)
		require.NoError(t, err)
		for _, read := range reads {
			require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(read(ctx, 3, nil)).Code)
		}
	}
}
