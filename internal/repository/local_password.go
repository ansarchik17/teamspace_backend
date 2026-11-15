package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// ChangeLocalPassword is called under WithPasswordUser's credential lock.
// The hash, proof revocation and session cutoff commit together, or not at all.
func (r *RecoveryRepository) ChangeLocalPassword(ctx context.Context, c *pgxpool.Conn, verified *domain.User, hash string) (time.Time, error) {
	tx, err := c.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)
	if err := recoveryPending(ctx, tx, verified.ID); err != nil {
		return time.Time{}, err
	}
	var currentHash, keycloakID string
	var active bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(u.password_hash,''),COALESCE(u.keycloak_id::text,''),`+recoveryAccountActive+` FROM users u WHERE u.id=$1 FOR UPDATE OF u`, verified.ID).Scan(&currentHash, &keycloakID, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, apperrors.RecoveryInvalid()
	}
	if err != nil {
		return time.Time{}, err
	}
	if !active || keycloakID != "" || verified.KeycloakID != nil && *verified.KeycloakID != "" || currentHash == "" || currentHash != verified.PasswordHash {
		return time.Time{}, apperrors.RecoveryInvalid()
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,is_first_login=false,updated_at=now() WHERE id=$1`, verified.ID, hash); err != nil {
		return time.Time{}, err
	}
	var cutoff time.Time
	err = tx.QueryRow(ctx, `INSERT INTO user_credential_state(user_id,invalid_before)
VALUES($1,date_trunc('second',clock_timestamp())+interval '1 second')
ON CONFLICT(user_id) DO UPDATE SET invalid_before=GREATEST(user_credential_state.invalid_before,EXCLUDED.invalid_before)
RETURNING invalid_before`, verified.ID).Scan(&cutoff)
	if err != nil {
		return time.Time{}, err
	}
	for _, query := range []string{
		`UPDATE password_recovery_requests SET state='revoked' WHERE user_id=$1 AND state IN ('pending','processing','password_set')`,
		`UPDATE recovery_email_challenges SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`,
		`DELETE FROM password_recovery_mail_jobs WHERE user_id=$1`,
		`UPDATE quick_pin_credential SET active=false,encrypted_refresh_token=NULL,updated_at=now() WHERE user_id=$1`,
		`UPDATE web_pairing_sessions SET status='revoked',updated_at=now() WHERE user_id=$1 AND status IN ('approved','synced')`,
		`INSERT INTO account_security_audit(user_id,event) VALUES($1,'password_changed')`,
	} {
		if _, err := tx.Exec(ctx, query, verified.ID); err != nil {
			return time.Time{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return cutoff, nil
}
