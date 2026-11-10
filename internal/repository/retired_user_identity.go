package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// CleanupRetiredUserIdentities may remove only the exact SSO IDs already queued
// by administrative deletion. It never resolves or deletes an SSO user by email.
// Each queue row commits separately, like the background cleanup worker.
func (r *UserRepository) CleanupRetiredUserIdentities(ctx context.Context, email, username, iin string, remove func(context.Context, string) error) error {
	for range 10 {
		processed, err := r.cleanupRetiredUserIdentity(ctx, email, username, iin, remove)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

func (r *UserRepository) cleanupRetiredUserIdentity(ctx context.Context, email, username, iin string, remove func(context.Context, string) error) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var user int64
	var identity string
	err = tx.QueryRow(ctx, `SELECT q.user_id,q.keycloak_id
 FROM administrative_identity_deletions q JOIN users u ON u.id=q.user_id
 JOIN retired_user_identities h ON h.user_id=u.id
 WHERE q.completed_at IS NULL AND u.deleted_at IS NOT NULL AND u.keycloak_id=q.keycloak_id
 AND NOT EXISTS (SELECT 1 FROM users live WHERE live.keycloak_id=q.keycloak_id AND live.deleted_at IS NULL)
 AND (($1<>'' AND lower(btrim(h.email))=$1)
 OR ($2<>'' AND lower(regexp_replace(btrim(h.username),'\s+','','g'))=$2)
 OR ($3<>'' AND h.iin=$3))
 ORDER BY q.created_at,q.user_id LIMIT 1 FOR UPDATE OF q,u`, normalizeKeycloakEmail(email), normalizeKeycloakUsername(username), normalizeKeycloakIIN(iin)).Scan(&user, &identity)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = remove(ctx, identity); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE administrative_identity_deletions SET completed_at=now() WHERE user_id=$1`, user); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
