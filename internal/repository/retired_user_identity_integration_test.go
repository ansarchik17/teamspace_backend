package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRetiredUserIdentifiersCreateNewAccountWithoutOldHistory(t *testing.T) {
	for _, scenario := range []string{"already_deleted", "future_deletion"} {
		t.Run(scenario, func(t *testing.T) {
			repo, db, _, org := userCreationFixture(t)
			ctx := context.Background()
			apply := func(name string) {
				data, err := os.ReadFile("../../db/migrations/" + name)
				require.NoError(t, err)
				// Execute goose Up, including its PL/pgSQL blocks, in one transaction.
				_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
				require.NoError(t, err)
			}
			apply("20261022130000_administrative_deletion.sql")
			_, err := db.Exec(ctx, `ALTER TABLE users ALTER COLUMN username TYPE varchar(50); ALTER TABLE users ADD UNIQUE(username); CREATE TABLE old_documents(user_id BIGINT REFERENCES users(id),content TEXT)`)
			require.NoError(t, err)
			original := newUserCreationCandidate(org)
			require.NoError(t, repo.CreateNewUser(ctx, original))
			_, err = db.Exec(ctx, `INSERT INTO old_documents VALUES($1,'historical document')`, original.ID)
			require.NoError(t, err)
			if scenario == "future_deletion" {
				apply("20261027000200_retired_user_identity_reuse.sql")
			}
			if scenario == "future_deletion" {
				_, err = db.Exec(ctx, `INSERT INTO administrative_identity_deletions(user_id,keycloak_id) VALUES($1,$2)`, original.ID, *original.KeycloakID)
				require.NoError(t, err)
			}
			_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now(),is_active=false WHERE id=$1`, original.ID)
			require.NoError(t, err)
			if scenario == "already_deleted" {
				apply("20261027000200_retired_user_identity_reuse.sql")
			}
			candidate := *original
			candidate.ID = 0
			newIdentity := uuid.NewString()
			candidate.KeycloakID = &newIdentity
			require.NoError(t, repo.CheckNewUserIdentity(ctx, candidate.Email, candidate.Username, *candidate.IIN))
			require.NoError(t, repo.CreateNewUser(ctx, &candidate))
			require.NotEqual(t, original.ID, candidate.ID)
			var owner, newHistory int64
			require.NoError(t, db.QueryRow(ctx, `SELECT user_id FROM old_documents`).Scan(&owner))
			require.Equal(t, original.ID, owner)
			require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM old_documents WHERE user_id=$1`, candidate.ID).Scan(&newHistory))
			require.Zero(t, newHistory)
			var email, username, iin string
			require.NoError(t, db.QueryRow(ctx, `SELECT email,username,iin FROM retired_user_identities WHERE user_id=$1`, original.ID).Scan(&email, &username, &iin))
			require.Equal(t, original.Email, email)
			require.Equal(t, original.Username, username)
			require.Equal(t, *original.IIN, iin)
			// Old SSO profile sync cannot take the new account's identifiers back.
			_, err = db.Exec(ctx, `UPDATE users SET email=$2,username=$3,iin=$4,is_active=true,deleted_at=NULL WHERE id=$1`, original.ID, email, username, iin)
			require.NoError(t, err)
			var retired bool
			require.NoError(t, db.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND NOT is_active AND iin IS NULL AND length(username)<=50 FROM users WHERE id=$1`, original.ID).Scan(&retired))
			require.True(t, retired)
			staleProfile := *original
			require.ErrorIs(t, repo.UpsertFromKeycloak(ctx, &staleProfile), ErrKeycloakUserNotLinked, "old SSO identity must not link to the replacement")
			require.ErrorIs(t, repo.CheckNewUserIdentity(ctx, email, username, iin), ErrUserIdentityConflict, "new active identity remains protected")
			failure := errors.New("temporary SSO failure")
			require.ErrorIs(t, repo.CleanupRetiredUserIdentities(ctx, email, username, iin, func(_ context.Context, id string) error {
				require.Equal(t, *original.KeycloakID, id)
				return failure
			}), failure)
			var pending bool
			require.NoError(t, db.QueryRow(ctx, `SELECT completed_at IS NULL FROM administrative_identity_deletions WHERE user_id=$1`, original.ID).Scan(&pending))
			require.True(t, pending)
			calls := 0
			require.NoError(t, repo.CleanupRetiredUserIdentities(ctx, email, username, iin, func(_ context.Context, id string) error {
				calls++
				require.Equal(t, *original.KeycloakID, id, "never delete the replacement SSO identity")
				return nil
			}))
			require.Equal(t, 1, calls)
			require.NoError(t, repo.CleanupRetiredUserIdentities(ctx, email, username, iin, func(context.Context, string) error {
				t.Fatal("completed deletion must not repeat")
				return nil
			}))
			_, err = db.Exec(ctx, `DELETE FROM retired_user_identities WHERE user_id=$1`, original.ID)
			require.Error(t, err, "identity history must remain immutable")
		})
	}
}
