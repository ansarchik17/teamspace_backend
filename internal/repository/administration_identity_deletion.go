package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// The local deletion and outbox insert commit together. A failed external
// deletion remains retryable; no provider response or credentials are stored.
func (r *OrganizationRepository) ProcessIdentityDeletion(ctx context.Context, remove func(context.Context, string) error) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id int64
	var identity string
	err = tx.QueryRow(ctx, `SELECT q.user_id,q.keycloak_id FROM administrative_identity_deletions q JOIN users u ON u.id=q.user_id
 WHERE q.completed_at IS NULL AND q.retry_at<=now() AND u.deleted_at IS NOT NULL AND u.keycloak_id=q.keycloak_id
 ORDER BY q.created_at,q.user_id LIMIT 1 FOR UPDATE OF q SKIP LOCKED`).Scan(&id, &identity)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = remove(ctx, identity); err != nil {
		if _, updateErr := tx.Exec(ctx, `UPDATE administrative_identity_deletions SET retry_at=now()+interval '1 minute' WHERE user_id=$1`, id); updateErr != nil {
			return false, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return false, commitErr
		}
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE administrative_identity_deletions SET completed_at=now() WHERE user_id=$1`, id); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
