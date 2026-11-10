package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestAdministrationDeletionIsolationAndTombstones(t *testing.T) {
	ctx, db, a, b := newAdministrationStorageTestDB(t)
	_, err := db.Exec(ctx, `ALTER TABLE users ADD COLUMN team_id uuid, ADD COLUMN manager_id bigint, ADD COLUMN keycloak_id text, ADD COLUMN email text, ADD COLUMN position text, ADD COLUMN iin text;
ALTER TABLE organizations ADD COLUMN created_at timestamptz DEFAULT now();
CREATE TABLE teams(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),organization_id uuid,manager_id bigint,deleted_at timestamptz,updated_at timestamptz);
CREATE UNIQUE INDEX ON users(keycloak_id);
UPDATE users SET manager_id=3 WHERE id=2;
UPDATE users SET keycloak_id='deleted-identity' WHERE id=3;`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO teams(organization_id,manager_id) VALUES($1,3)`, a)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261022130000_administrative_deletion.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	migration, err = os.ReadFile("../../db/migrations/20261027000200_retired_user_identity_reuse.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	r := NewOrganizationRepository(db, zap.NewNop())
	for _, tc := range []struct {
		actor, target int64
		org           *uuid.UUID
	}{
		{3, 2, nil}, {2, 5, nil}, {2, 3, &a}, {1, 1, &a}, {1, 2, &a}, {2, 2, nil},
	} {
		require.Error(t, r.DeleteAdministrationUser(ctx, tc.actor, tc.target, tc.org))
	}
	require.Error(t, r.DeleteUser(ctx, 2, 5), "legacy route must reject foreign company")
	require.NoError(t, r.DeleteUser(ctx, 2, 3))
	var deleted, active bool
	require.NoError(t, db.QueryRow(ctx, `SELECT deleted_at IS NOT NULL,is_active FROM users WHERE id=3`).Scan(&deleted, &active))
	require.True(t, deleted)
	require.False(t, active)
	var manager *int64
	require.NoError(t, db.QueryRow(ctx, `SELECT manager_id FROM users WHERE id=2`).Scan(&manager))
	require.Nil(t, manager)
	require.NoError(t, db.QueryRow(ctx, `SELECT manager_id FROM teams WHERE organization_id=$1`, a).Scan(&manager))
	require.Nil(t, manager)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true,deleted_at=NULL WHERE id=3`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT deleted_at IS NOT NULL,is_active FROM users WHERE id=3`).Scan(&deleted, &active))
	require.True(t, deleted)
	require.False(t, active)
	kc := "deleted-identity"
	require.ErrorIs(t, NewUserRepository(db, zap.NewNop()).UpsertFromKeycloak(ctx, &domain.User{KeycloakID: &kc}), ErrKeycloakUserNotLinked)
	processed, err := r.ProcessIdentityDeletion(ctx, func(context.Context, string) error { return errors.New("provider temporarily unavailable") })
	require.Error(t, err)
	require.False(t, processed)
	var pending bool
	require.NoError(t, db.QueryRow(ctx, `SELECT completed_at IS NULL AND retry_at>now() FROM administrative_identity_deletions WHERE user_id=3`).Scan(&pending))
	require.True(t, pending)
	_, err = db.Exec(ctx, `UPDATE administrative_identity_deletions SET retry_at=now() WHERE user_id=3`)
	require.NoError(t, err)
	processed, err = r.ProcessIdentityDeletion(ctx, func(_ context.Context, identity string) error { require.Equal(t, kc, identity); return nil })
	require.NoError(t, err)
	require.True(t, processed)
	processed, err = r.ProcessIdentityDeletion(ctx, func(context.Context, string) error { t.Fatal("completed cleanup must not repeat"); return nil })
	require.NoError(t, err)
	require.False(t, processed)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE action='user.delete' AND organization_id=$1 AND after_value->>'deleted'='true'`, a).Scan(&count))
	require.Equal(t, 1, count)
	_, err = r.DeletePlatformOrganization(ctx, 2, a)
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=1`)
	require.NoError(t, err)
	require.Error(t, r.DeleteAdministrationUser(ctx, 1, 5, &b), "revoked administrator")
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=1`)
	require.NoError(t, err)
	require.NoError(t, r.DeleteAdministrationUser(ctx, 1, 5, &b), "platform administrator can delete a company employee")
	_, err = db.Exec(ctx, `UPDATE users SET keycloak_id='company-owner-identity' WHERE id=4`)
	require.NoError(t, err)
	_, err = r.DeletePlatformOrganization(ctx, 1, b)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM administrative_identity_deletions WHERE user_id=4 AND completed_at IS NULL`).Scan(&count))
	require.Equal(t, 1, count, "company deletion must queue the director identity too")
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1`, b).Scan(&status))
	require.Equal(t, "deleted", status)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM users WHERE organization_id=$1 AND deleted_at IS NULL`, b).Scan(&count))
	require.Zero(t, count)
	_, err = r.GetPlatformOrganization(ctx, 1, b)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	_, err = r.platformOrganizationScope(ctx, 1, b)
	require.Error(t, err)
	list, err := r.ListPlatformOrganizations(ctx, 1, domain.PlatformOrganizationFilter{Limit: 25})
	require.NoError(t, err)
	require.Equal(t, 1, list.Total)
	restored := "restored"
	_, err = r.UpdatePlatformOrganization(ctx, 1, b, domain.UpdatePlatformOrganizationRequest{Name: &restored})
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE organizations SET status='active' WHERE id=$1`, b)
	require.Error(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id) VALUES(7,$1)`, b)
	require.Error(t, err)
	_, err = r.DeletePlatformOrganization(ctx, 1, b)
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=1`, a)
	require.NoError(t, err)
	_, err = r.DeletePlatformOrganization(ctx, 1, a)
	require.Error(t, err, "cannot cascade-delete platform admin")
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1`, a).Scan(&status))
	require.Equal(t, "active", status)
}
