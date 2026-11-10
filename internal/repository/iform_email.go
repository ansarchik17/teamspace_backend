package repository

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// Return only the free candidate, never identities belonging to a used address.
// Deleted/inactive identities still count: their address may remain in SSO.
func (r *OrganizationRepository) FirstAvailableIFormEmail(ctx context.Context, candidates []string) (string, error) {
	var email string
	err := r.db.QueryRow(ctx, `SELECT candidate FROM unnest($1::text[]) WITH ORDINALITY AS c(candidate,ord)
 WHERE NOT EXISTS(SELECT 1 FROM users WHERE lower(email)=c.candidate)
 AND NOT EXISTS(SELECT 1 FROM user_mailboxes WHERE lower(mailbox_login)=c.candidate)
 ORDER BY ord LIMIT 1`, candidates).Scan(&email)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return email, err
}
