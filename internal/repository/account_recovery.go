package repository

import (
	"context"
	"crypto/subtle"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type RecoveryRepository struct{ db *pgxpool.Pool }

func NewRecoveryRepository(db *pgxpool.Pool) *RecoveryRepository { return &RecoveryRepository{db: db} }

const recoveryAccountActive = `u.is_active AND u.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id) AND (u.organization_id IS NULL OR EXISTS(SELECT 1 FROM organizations o WHERE o.id=u.organization_id AND o.status='active'))`

// One session lock spans the durable claim, the remote identity mutation, and
// completion. A failed unlock discards the connection instead of leaking a lock.
func (r *RecoveryRepository) WithUser(ctx context.Context, id int64, fn func(*pgxpool.Conn, *domain.User) error) error {
	c, err := r.db.Acquire(ctx)
	if err != nil {
		return err
	}
	locked := false
	defer func() {
		if locked {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var ok bool
			if e := c.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "account-credentials:"+strconv.FormatInt(id, 10)).Scan(&ok); e != nil || !ok {
				_ = c.Hijack().Close(cleanup)
				return
			}
		}
		c.Release()
	}()
	if err = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "account-credentials:"+strconv.FormatInt(id, 10)).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return apperrors.RecoveryPending()
	}
	var u domain.User
	var kc *string
	err = c.QueryRow(ctx, `SELECT u.id,u.username,u.email,u.keycloak_id FROM users u WHERE u.id=$1 AND `+recoveryAccountActive, id).Scan(&u.ID, &u.Username, &u.Email, &kc)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.RecoveryInvalid()
	}
	if err != nil {
		return err
	}
	if kc == nil || *kc == "" {
		return apperrors.RecoveryInvalid()
	}
	u.KeycloakID = kc
	return fn(c, &u)
}

// The cutoff is checked for both local access/refresh tokens and Keycloak JWTs.
// A reset in progress fails closed even after an API restart or uncertain SSO response.
func (r *RecoveryRepository) TokenAllowed(ctx context.Context, id int64, issuedAt time.Time) (bool, error) {
	var allowed bool
	err := r.db.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM user_credential_state WHERE user_id=$1 AND (reset_pending OR (invalid_before IS NOT NULL AND $2::timestamptz<invalid_before)))`, id, issuedAt).Scan(&allowed)
	return allowed, err
}
func (r *RecoveryRepository) Rate(ctx context.Context, key string, limit int, window time.Duration) error {
	var count int
	err := r.db.QueryRow(ctx, `INSERT INTO account_recovery_rate_limits(key_hash) VALUES($1) ON CONFLICT(key_hash) DO UPDATE SET requests=CASE WHEN account_recovery_rate_limits.started_at<now()-make_interval(secs=>$2) THEN 1 ELSE account_recovery_rate_limits.requests+1 END,started_at=CASE WHEN account_recovery_rate_limits.started_at<now()-make_interval(secs=>$2) THEN now() ELSE account_recovery_rate_limits.started_at END RETURNING requests`, key, window.Seconds()).Scan(&count)
	if err != nil {
		return err
	}
	if count > limit {
		return apperrors.RecoveryLimited()
	}
	return nil
}
func recoveryPending(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) error {
	var pending bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_credential_state WHERE user_id=$1 AND reset_pending)`, id).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return apperrors.RecoveryPending()
	}
	return nil
}
func (r *RecoveryRepository) State(ctx context.Context, id int64) (*domain.RecoveryEmailState, error) {
	out := &domain.RecoveryEmailState{}
	err := r.db.QueryRow(ctx, `SELECT (SELECT email FROM user_recovery_emails WHERE user_id=$1),(SELECT verified_at FROM user_recovery_emails WHERE user_id=$1)`, id).Scan(&out.Email, &out.VerifiedAt)
	if err != nil {
		return nil, err
	}
	var p domain.RecoveryEmailChallenge
	err = r.db.QueryRow(ctx, `SELECT id,email,purpose,expires_at,created_at+interval '60 seconds' FROM recovery_email_challenges WHERE user_id=$1 AND consumed_at IS NULL AND expires_at>now() AND attempts<5 ORDER BY created_at DESC LIMIT 1`, id).Scan(&p.ID, &p.Email, &p.Purpose, &p.ExpiresAt, &p.RetryAt)
	if err == nil {
		out.Pending = &p
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return out, nil
}
func emailAvailable(ctx context.Context, tx pgx.Tx, id int64, email string) error {
	var conflict bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE (lower(btrim(email))=$1 OR lower(btrim(username))=$1) AND id<>$2) OR EXISTS(SELECT 1 FROM user_verified_contacts WHERE email=$1 AND user_id<>$2) OR EXISTS(SELECT 1 FROM user_recovery_emails WHERE email=$1 AND user_id<>$2) OR EXISTS(SELECT 1 FROM user_mailboxes WHERE lower(btrim(mailbox_login))=$1)`, email, id).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return apperrors.RecoveryEmailConflict()
	}
	return nil
}
func (r *RecoveryRepository) BeginEmail(ctx context.Context, c *pgxpool.Conn, u *domain.User, id uuid.UUID, email, purpose, hash string, send func(string) error) (*domain.RecoveryEmailChallenge, error) {
	tx, err := c.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = recoveryPending(ctx, tx, u.ID); err != nil {
		return nil, err
	}
	if purpose == "remove" {
		if err = tx.QueryRow(ctx, `SELECT email FROM user_recovery_emails WHERE user_id=$1`, u.ID).Scan(&email); errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.RecoveryInvalid()
		} else if err != nil {
			return nil, err
		}
	}
	var limited bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recovery_email_challenges WHERE user_id=$1 AND created_at>now()-interval '60 seconds') OR (SELECT count(*)>=5 FROM recovery_email_challenges WHERE user_id=$1 AND created_at>now()-interval '1 hour')`, u.ID).Scan(&limited)
	if err != nil {
		return nil, err
	}
	if limited {
		return nil, apperrors.RecoveryLimited()
	}
	if purpose == "bind" {
		if err = emailAvailable(ctx, tx, u.ID, email); err != nil {
			return nil, err
		}
	}
	out := &domain.RecoveryEmailChallenge{ID: id, Email: email, Purpose: purpose}
	if _, err = tx.Exec(ctx, `UPDATE recovery_email_challenges SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`, u.ID); err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO recovery_email_challenges(id,user_id,email,purpose,code_hash) VALUES($1,$2,$3,$4,$5) RETURNING expires_at,created_at+interval '60 seconds'`, id, u.ID, email, purpose, hash).Scan(&out.ExpiresAt, &out.RetryAt)
	if err != nil {
		return nil, err
	}
	if err = send(email); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func (r *RecoveryRepository) ConfirmEmail(ctx context.Context, c *pgxpool.Conn, u *domain.User, id uuid.UUID, hash, locale string) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = recoveryPending(ctx, tx, u.ID); err != nil {
		return err
	}
	var email, purpose, want string
	var attempts int
	var valid bool
	err = tx.QueryRow(ctx, `SELECT email,purpose,code_hash,attempts,consumed_at IS NULL AND expires_at>now() FROM recovery_email_challenges WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, u.ID).Scan(&email, &purpose, &want, &attempts, &valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!valid || attempts >= 5) {
		return apperrors.RecoveryInvalid()
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) != 1 {
		if _, err = tx.Exec(ctx, `UPDATE recovery_email_challenges SET attempts=attempts+1 WHERE id=$1`, id); err != nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		return apperrors.New("RECOVERY_CODE_INVALID", "Invalid confirmation code", 400)
	}
	var previous *string
	if err = tx.QueryRow(ctx, `SELECT (SELECT email FROM user_recovery_emails WHERE user_id=$1)`, u.ID).Scan(&previous); err != nil {
		return err
	}
	locks := []string{email}
	if previous != nil && *previous != email {
		locks = append(locks, *previous)
	}
	sort.Strings(locks)
	for _, e := range locks {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "new-user:email:"+e); err != nil {
			return err
		}
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT `+recoveryAccountActive+` FROM users u WHERE u.id=$1 FOR UPDATE OF u`, u.ID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return apperrors.RecoveryInvalid()
	}
	event := "recovery_email_changed"
	if purpose == "bind" {
		if err = emailAvailable(ctx, tx, u.ID, email); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO user_verified_contacts(user_id,email,source) VALUES($1,$2,'recovery_email') ON CONFLICT(user_id,email) DO NOTHING`, u.ID, email); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO user_recovery_emails(user_id,email,version) VALUES($1,$2,$3) ON CONFLICT(user_id) DO UPDATE SET email=EXCLUDED.email,version=EXCLUDED.version,verified_at=now()`, u.ID, email, uuid.New()); err != nil {
			return err
		}
	} else {
		if previous == nil || *previous != email {
			return apperrors.RecoveryInvalid()
		}
		if _, err = tx.Exec(ctx, `DELETE FROM user_recovery_emails WHERE user_id=$1`, u.ID); err != nil {
			return err
		}
		event = "recovery_email_removed"
	}
	if previous != nil && (*previous != email || purpose == "remove") {
		if _, err = tx.Exec(ctx, `DELETE FROM user_verified_contacts WHERE user_id=$1 AND email=$2 AND source='recovery_email'`, u.ID, *previous); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO account_security_notifications(user_id,email,event,locale) VALUES($1,$2,$3,$4)`, u.ID, *previous, event, locale); err != nil {
			return err
		}
	}
	if purpose == "bind" {
		if _, err = tx.Exec(ctx, `INSERT INTO account_security_notifications(user_id,email,event,locale) VALUES($1,$2,$3,$4)`, u.ID, email, event, locale); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE recovery_email_challenges SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`, u.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE password_recovery_requests SET state='revoked' WHERE user_id=$1 AND state IN ('pending','processing')`, u.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM password_recovery_mail_jobs WHERE user_id=$1`, u.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_security_audit(user_id,event) VALUES($1,$2)`, u.ID, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Queue only a user ID; tokens are generated in memory by the mail worker and
// only their hashes are committed. The public HTTP request never waits on SMTP.
func (r *RecoveryRepository) RequestPassword(ctx context.Context, login, locale string) error {
	_, err := r.db.Exec(ctx, `WITH candidates AS (SELECT u.id FROM users u JOIN user_recovery_emails e ON e.user_id=u.id WHERE (lower(u.username)=$1 OR lower(btrim(u.email))=$1 OR e.email=$1) AND `+recoveryAccountActive+` AND u.keycloak_id IS NOT NULL), single_account AS (SELECT min(id) AS id FROM candidates HAVING count(*)=1) INSERT INTO password_recovery_mail_jobs(user_id,locale) SELECT id,$2 FROM single_account ON CONFLICT(user_id) DO UPDATE SET attempts=0,available_at=now(),created_at=now(),locale=EXCLUDED.locale WHERE password_recovery_mail_jobs.attempts>=3 AND password_recovery_mail_jobs.available_at<=now()`, login, locale)
	return err
}
func (r *RecoveryRepository) MailJobIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id FROM password_recovery_mail_jobs WHERE attempts<3 AND available_at<=now() ORDER BY available_at LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *RecoveryRepository) DeliverPassword(ctx context.Context, c *pgxpool.Conn, u *domain.User, id uuid.UUID, hash string, send func(email, locale string) error) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locale, email string
	var version uuid.UUID
	err = tx.QueryRow(ctx, `SELECT j.locale,e.email,e.version FROM password_recovery_mail_jobs j JOIN user_recovery_emails e ON e.user_id=j.user_id WHERE j.user_id=$1 AND j.attempts<3 AND j.available_at<=now() FOR UPDATE OF j`, u.ID).Scan(&locale, &email, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `DELETE FROM password_recovery_mail_jobs WHERE user_id=$1`, u.ID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	var limited bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM password_recovery_requests WHERE user_id=$1 AND created_at>now()-interval '60 seconds') OR (SELECT count(*)>=5 FROM password_recovery_requests WHERE user_id=$1 AND created_at>now()-interval '1 hour')`, u.ID).Scan(&limited); err != nil {
		return err
	}
	if !limited {
		if _, err = tx.Exec(ctx, `UPDATE password_recovery_requests SET state='revoked' WHERE user_id=$1 AND state='pending'`, u.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO password_recovery_requests(id,user_id,email,email_version,token_hash,locale,keycloak_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, u.ID, email, version, hash, locale, *u.KeycloakID); err != nil {
			return err
		}
		if err = send(email, locale); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM password_recovery_mail_jobs WHERE user_id=$1`, u.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *RecoveryRepository) FailMailJob(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE password_recovery_mail_jobs SET attempts=attempts+1,available_at=now()+interval '1 minute' WHERE user_id=$1`, id)
	return err
}
func (r *RecoveryRepository) FindReset(ctx context.Context, hash string) (*domain.PasswordRecovery, error) {
	var p domain.PasswordRecovery
	err := r.db.QueryRow(ctx, `SELECT id,user_id,email,email_version,state,attempts,locale,keycloak_id FROM password_recovery_requests WHERE token_hash=$1 AND expires_at>now() AND state IN ('pending','processing','password_set')`, hash).Scan(&p.ID, &p.UserID, &p.Email, &p.EmailVersion, &p.State, &p.Attempts, &p.Locale, &p.KeycloakID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.RecoveryInvalid()
	}
	return &p, err
}
func (r *RecoveryRepository) ClaimReset(ctx context.Context, c *pgxpool.Conn, u *domain.User, p *domain.PasswordRecovery) error {
	if p.Locale != "kk" {
		p.Locale = "ru"
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM password_recovery_requests p JOIN user_recovery_emails e ON e.user_id=p.user_id AND e.email=p.email AND e.version=p.email_version WHERE p.id=$1 AND p.user_id=$2 AND p.keycloak_id=$3 AND p.expires_at>now() AND p.state IN ('pending','processing','password_set'))`, p.ID, u.ID, *u.KeycloakID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return apperrors.RecoveryInvalid()
	}
	if err = tx.QueryRow(ctx, `SELECT state,attempts FROM password_recovery_requests WHERE id=$1 FOR UPDATE`, p.ID).Scan(&p.State, &p.Attempts); err != nil {
		return err
	}
	var otherPending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_credential_state WHERE user_id=$1 AND reset_pending AND reset_request_id<>$2 AND EXISTS(SELECT 1 FROM password_recovery_requests WHERE id=reset_request_id AND state='password_set'))`, u.ID, p.ID).Scan(&otherPending); err != nil {
		return err
	}
	if otherPending {
		return apperrors.RecoveryPending()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_credential_state(user_id,reset_request_id,reset_pending) VALUES($1,$2,true) ON CONFLICT(user_id) DO UPDATE SET reset_request_id=EXCLUDED.reset_request_id,reset_pending=true`, u.ID, p.ID); err != nil {
		return err
	}
	if p.State != "password_set" {
		if _, err = tx.Exec(ctx, `UPDATE password_recovery_requests SET state='processing',attempts=attempts+1,locale=$2 WHERE id=$1`, p.ID, p.Locale); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (r *RecoveryRepository) AbortPolicyFailure(ctx context.Context, c *pgxpool.Conn, p *domain.PasswordRecovery) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE user_credential_state SET reset_pending=false,reset_request_id=NULL WHERE user_id=$1 AND reset_request_id=$2`, p.UserID, p.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE password_recovery_requests SET state='pending',attempts=0 WHERE id=$1 AND state='processing'`, p.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *RecoveryRepository) PasswordSet(ctx context.Context, c *pgxpool.Conn, p *domain.PasswordRecovery) error {
	_, err := c.Exec(ctx, `UPDATE password_recovery_requests SET state='password_set' WHERE id=$1 AND state='processing'`, p.ID)
	return err
}
func (r *RecoveryRepository) FinishReset(ctx context.Context, c *pgxpool.Conn, p *domain.PasswordRecovery, locale string) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `UPDATE user_credential_state SET reset_pending=false,reset_request_id=NULL,invalid_before=date_trunc('second',clock_timestamp())+interval '1 second' WHERE user_id=$1 AND reset_request_id=$2 AND reset_pending AND EXISTS(SELECT 1 FROM password_recovery_requests WHERE id=$2 AND state='password_set') RETURNING invalid_before`, p.UserID, p.ID).Scan(&p.InvalidBefore)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.RecoveryInvalid()
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE password_recovery_requests SET state=CASE WHEN id=$2 THEN 'completed' ELSE 'revoked' END WHERE user_id=$1 AND state IN ('pending','processing','password_set')`, p.UserID, p.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET is_first_login=false,password_hash=NULL WHERE id=$1`, p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE quick_pin_credential SET active=false,encrypted_refresh_token=NULL,updated_at=now() WHERE user_id=$1`, p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE web_pairing_sessions SET status='revoked',updated_at=now() WHERE user_id=$1 AND status IN ('approved','synced')`, p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE recovery_email_challenges SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`, p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM password_recovery_mail_jobs WHERE user_id=$1`, p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_security_audit(user_id,event) VALUES($1,'password_recovered')`, p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_security_notifications(user_id,email,event,locale) VALUES($1,$2,'password_recovered',$3)`, p.UserID, p.Email, locale); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *RecoveryRepository) PendingResets(ctx context.Context) ([]domain.PasswordRecovery, error) {
	rows, err := r.db.Query(ctx, `SELECT p.id,p.user_id,p.email,p.email_version,p.state,p.attempts,p.locale,p.keycloak_id FROM password_recovery_requests p JOIN user_credential_state s ON s.reset_request_id=p.id JOIN users u ON u.id=p.user_id WHERE p.state='password_set' AND s.reset_pending AND p.keycloak_id=u.keycloak_id AND `+recoveryAccountActive+` ORDER BY p.created_at LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PasswordRecovery{}
	for rows.Next() {
		var p domain.PasswordRecovery
		if err = rows.Scan(&p.ID, &p.UserID, &p.Email, &p.EmailVersion, &p.State, &p.Attempts, &p.Locale, &p.KeycloakID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *RecoveryRepository) RevokeProofs(ctx context.Context, c *pgxpool.Conn, id int64) error {
	_, err := c.Exec(ctx, `WITH credentials AS (UPDATE users SET password_hash=NULL WHERE id=$1), jobs AS (DELETE FROM password_recovery_mail_jobs WHERE user_id=$1), links AS (UPDATE password_recovery_requests SET state='revoked' WHERE user_id=$1 AND state='pending') UPDATE recovery_email_challenges SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`, id)
	return err
}
func (r *RecoveryRepository) Notify(ctx context.Context, send func(string, string, string) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	var email, event, locale string
	err = tx.QueryRow(ctx, `SELECT id,email,event,locale FROM account_security_notifications WHERE attempts<5 AND available_at<=now() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &email, &event, &locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if send(email, event, locale) == nil {
		_, err = tx.Exec(ctx, `DELETE FROM account_security_notifications WHERE id=$1`, id)
	} else {
		_, err = tx.Exec(ctx, `UPDATE account_security_notifications SET attempts=attempts+1,available_at=now()+interval '5 minutes' WHERE id=$1`, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *RecoveryRepository) Cleanup(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `DELETE FROM account_recovery_rate_limits WHERE started_at<now()-interval '1 day'; DELETE FROM recovery_email_challenges WHERE created_at<now()-interval '7 days'; DELETE FROM password_recovery_mail_jobs WHERE created_at<now()-interval '1 day'; DELETE FROM account_security_notifications WHERE attempts>=5 AND created_at<now()-interval '7 days'; DELETE FROM password_recovery_requests p WHERE created_at<now()-interval '7 days' AND NOT EXISTS(SELECT 1 FROM user_credential_state WHERE reset_request_id=p.id)`)
	return err
}

func (r *RecoveryRepository) CheckPending(ctx context.Context, c *pgxpool.Conn, id int64) error {
	return recoveryPending(ctx, c, id)
}
