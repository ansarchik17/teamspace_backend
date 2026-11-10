package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func recoveryFixture(t *testing.T) (context.Context, *RecoveryRepository, *pgxpool.Pool) {
	t.Helper()
	ctx, _, db, _, _ := invitationFixture(t)
	for _, file := range []string{"20260603170000_create_user_mailboxes.sql", "20261025000200_account_recovery.sql"} {
		data, e := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, e)
	}
	_, err := db.Exec(ctx, `ALTER TABLE users ADD COLUMN password_hash TEXT;
 CREATE TABLE quick_pin_credential(user_id BIGINT,active BOOLEAN,encrypted_refresh_token TEXT,updated_at TIMESTAMPTZ);
 CREATE TABLE web_pairing_sessions(user_id BIGINT,status TEXT,updated_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	return ctx, NewRecoveryRepository(db), db
}
func bindRecovery(t *testing.T, ctx context.Context, r *RecoveryRepository, id int64, email string) {
	t.Helper()
	challenge := uuid.New()
	require.NoError(t, r.WithUser(ctx, id, func(c *pgxpool.Conn, u *domain.User) error {
		_, err := r.BeginEmail(ctx, c, u, challenge, email, "bind", "correct", func(string) error { return nil })
		if err != nil {
			return err
		}
		return r.ConfirmEmail(ctx, c, u, challenge, "correct", "ru")
	}))
}
func ageRecoveryCodes(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	_, err := db.Exec(ctx, `UPDATE recovery_email_challenges SET created_at=now()-interval '2 hours'`)
	require.NoError(t, err)
}
func TestRecoveryEmailProofAndIdentityBoundaries(t *testing.T) {
	ctx, r, db := recoveryFixture(t)
	var failed uuid.UUID
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		failed = uuid.New()
		_, err := r.BeginEmail(ctx, c, u, failed, "first@example.invalid", "bind", "correct", func(string) error { return errors.New("smtp") })
		return err
	}))
	state, err := r.State(ctx, 1)
	require.NoError(t, err)
	require.Nil(t, state.Pending)
	require.Nil(t, state.Email)
	bindRecovery(t, ctx, r, 1, "first@example.invalid")
	require.NoError(t, db.QueryRow(ctx, `SELECT email FROM users WHERE id=1`).Scan(new(string)))
	var corporate string
	require.NoError(t, db.QueryRow(ctx, `SELECT email FROM users WHERE id=1`).Scan(&corporate))
	require.Equal(t, "director@beles.invalid", corporate)
	require.Error(t, r.WithUser(ctx, 2, func(c *pgxpool.Conn, u *domain.User) error {
		_, e := r.BeginEmail(ctx, c, u, uuid.New(), "first@example.invalid", "bind", "proof", nil)
		return e
	}))
	_, err = db.Exec(ctx, `INSERT INTO user_mailboxes(user_id,mailbox_login,is_primary)VALUES(1,'work@example.invalid',true)`)
	require.NoError(t, err)
	ageRecoveryCodes(t, ctx, db)
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		_, e := r.BeginEmail(ctx, c, u, uuid.New(), "work@example.invalid", "bind", "proof", nil)
		return e
	}))
	challenge := uuid.New()
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		_, e := r.BeginEmail(ctx, c, u, challenge, "second@example.invalid", "bind", "correct", func(string) error { return nil })
		return e
	}))
	state, err = r.State(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "first@example.invalid", *state.Email)
	require.Error(t, r.WithUser(ctx, 2, func(c *pgxpool.Conn, u *domain.User) error {
		return r.ConfirmEmail(ctx, c, u, challenge, "correct", "ru")
	}))
	for n := 0; n < 5; n++ {
		require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
			return r.ConfirmEmail(ctx, c, u, challenge, "wrong", "ru")
		}))
	}
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		return r.ConfirmEmail(ctx, c, u, challenge, "correct", "ru")
	}))
	state, err = r.State(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "first@example.invalid", *state.Email)
	require.Nil(t, state.Pending)
	ageRecoveryCodes(t, ctx, db)
	bindRecovery(t, ctx, r, 1, "second@example.invalid")
	state, err = r.State(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "second@example.invalid", *state.Email)
	ageRecoveryCodes(t, ctx, db)
	challenge = uuid.New()
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		_, e := r.BeginEmail(ctx, c, u, challenge, "ignored", "remove", "remove-proof", func(email string) error { require.Equal(t, "second@example.invalid", email); return nil })
		if e != nil {
			return e
		}
		return r.ConfirmEmail(ctx, c, u, challenge, "remove-proof", "kk")
	}))
	state, err = r.State(ctx, 1)
	require.NoError(t, err)
	require.Nil(t, state.Email)
	// Invitation ownership alone does not opt an account into password recovery.
	_, err = db.Exec(ctx, `INSERT INTO user_verified_contacts(user_id,email,source)VALUES(1,'invited@example.invalid','organization_invitation')`)
	require.NoError(t, err)
	require.NoError(t, r.RequestPassword(ctx, "invited@example.invalid", "ru"))
	ids, err := r.MailJobIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.NoError(t, r.Cleanup(ctx))
}
func TestRecoveryResetDurableGuardAndRevocation(t *testing.T) {
	ctx, r, db := recoveryFixture(t)
	bindRecovery(t, ctx, r, 1, "personal@example.invalid")
	require.NoError(t, r.RequestPassword(ctx, "unknown", "ru"))
	ids, err := r.MailJobIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.NoError(t, r.RequestPassword(ctx, "beles.director", "kk"))
	ids, err = r.MailJobIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, ids)
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		return r.DeliverPassword(ctx, c, u, uuid.New(), "failed", func(string, string) error { return errors.New("smtp") })
	}))
	_, err = r.FindReset(ctx, "failed")
	require.Error(t, err)
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		return r.DeliverPassword(ctx, c, u, uuid.New(), "secret-hash", func(email, locale string) error {
			require.Equal(t, "personal@example.invalid", email)
			require.Equal(t, "kk", locale)
			return nil
		})
	}))
	p, err := r.FindReset(ctx, "secret-hash")
	require.NoError(t, err)
	old := time.Now().Add(-time.Hour)
	allowed, err := r.TokenAllowed(ctx, 1, old)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error { return r.ClaimReset(ctx, c, u, p) }))
	allowed, err = r.TokenAllowed(ctx, 1, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.False(t, allowed, "claim survives a process restart and denies even new tokens")
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error { return r.CheckPending(ctx, c, 1) }))
	_, err = db.Exec(ctx, `UPDATE users SET password_hash='legacy hash' WHERE id=1;INSERT INTO quick_pin_credential(user_id,active,encrypted_refresh_token)VALUES(1,true,'old refresh');INSERT INTO web_pairing_sessions(user_id,status)VALUES(1,'approved')`)
	require.NoError(t, err)
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		if e := r.PasswordSet(ctx, c, p); e != nil {
			return e
		}
		return r.FinishReset(ctx, c, p, "kk")
	}))
	allowed, err = r.TokenAllowed(ctx, 1, old)
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = r.TokenAllowed(ctx, 1, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = r.FindReset(ctx, "secret-hash")
	require.Error(t, err)
	var revoked bool
	require.NoError(t, db.QueryRow(ctx, `SELECT (SELECT password_hash IS NULL FROM users WHERE id=1) AND (SELECT NOT active AND encrypted_refresh_token IS NULL FROM quick_pin_credential WHERE user_id=1) AND (SELECT status='revoked' FROM web_pairing_sessions WHERE user_id=1)`).Scan(&revoked))
	require.True(t, revoked)
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error { return r.FinishReset(ctx, c, p, "ru") }), "finishing twice must not emit duplicate notifications or move the cutoff")
}
func TestRecoveryConcurrentEmailOwnership(t *testing.T) {
	ctx, r, db := recoveryFixture(t)
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	for i, id := range ids {
		require.NoError(t, r.WithUser(ctx, int64(i+1), func(c *pgxpool.Conn, u *domain.User) error {
			_, e := r.BeginEmail(ctx, c, u, id, "shared@example.invalid", "bind", "correct", func(string) error { return nil })
			return e
		}))
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(user int64, id uuid.UUID) {
			defer wg.Done()
			if r.WithUser(ctx, user, func(c *pgxpool.Conn, u *domain.User) error { return r.ConfirmEmail(ctx, c, u, id, "correct", "ru") }) == nil {
				wins.Add(1)
			}
		}(int64(i+1), id)
	}
	wg.Wait()
	require.Equal(t, int32(1), wins.Load())
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM user_recovery_emails WHERE email='shared@example.invalid'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		require.Error(t, r.WithUser(ctx, 1, func(*pgxpool.Conn, *domain.User) error {
			t.Fatal("second process acquired same user's lock")
			return nil
		}))
		return nil
	}))
}
func TestRecoveryAmbiguousLoginAndMigrationRollback(t *testing.T) {
	ctx, r, db := recoveryFixture(t)
	bindRecovery(t, ctx, r, 1, "personal@example.invalid")
	bindRecovery(t, ctx, r, 2, "other@example.invalid")
	_, err := db.Exec(ctx, `UPDATE users SET username='personal@example.invalid' WHERE id=2`)
	require.NoError(t, err)
	require.NoError(t, r.RequestPassword(ctx, "personal@example.invalid", "ru"))
	ids, err := r.MailJobIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids, "ambiguous login must not send multiple account links")
	data, err := os.ReadFile("../../db/migrations/20261025000200_account_recovery.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[1])
	require.NoError(t, err)
}

func TestRecoveryExpiredProofAndOrdinaryPasswordChange(t *testing.T) {
	ctx, r, db := recoveryFixture(t)
	bindRecovery(t, ctx, r, 1, "personal@example.invalid")
	issue := func(hash string) {
		require.NoError(t, r.RequestPassword(ctx, "beles.director", "ru"))
		require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
			return r.DeliverPassword(ctx, c, u, uuid.New(), hash, func(string, string) error { return nil })
		}))
	}
	issue("expired")
	_, err := db.Exec(ctx, `UPDATE password_recovery_requests SET expires_at=now()-interval '1 second',created_at=now()-interval '2 hours'`)
	require.NoError(t, err)
	_, err = r.FindReset(ctx, "expired")
	require.Error(t, err)
	issue("old-link")
	proof, err := r.FindReset(ctx, "old-link")
	require.NoError(t, err)
	require.NoError(t, r.RequestPassword(ctx, "beles.director", "ru"))
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error { return r.RevokeProofs(ctx, c, u.ID) }))
	_, err = r.FindReset(ctx, "old-link")
	require.Error(t, err)
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error { return r.ClaimReset(ctx, c, u, proof) }))
	ids, err := r.MailJobIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=1`)
	require.NoError(t, err)
	require.Error(t, r.WithUser(ctx, 1, func(*pgxpool.Conn, *domain.User) error {
		t.Fatal("inactive account entered credential operation")
		return nil
	}))
	require.NoError(t, r.RequestPassword(ctx, "beles.director", "ru"))
	ids, err = r.MailJobIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestRecoveryLinkRejectsChangedIdentity(t *testing.T) {
	ctx, r, db := recoveryFixture(t)
	bindRecovery(t, ctx, r, 1, "personal@example.invalid")
	require.NoError(t, r.RequestPassword(ctx, "beles.director", "ru"))
	require.NoError(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error {
		return r.DeliverPassword(ctx, c, u, uuid.New(), "original-identity-proof", func(string, string) error { return nil })
	}))
	p, err := r.FindReset(ctx, "original-identity-proof")
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET keycloak_id='different-identity' WHERE id=1`)
	require.NoError(t, err)
	require.Error(t, r.WithUser(ctx, 1, func(c *pgxpool.Conn, u *domain.User) error { return r.ClaimReset(ctx, c, u, p) }))
	allowed, err := r.TokenAllowed(ctx, 1, time.Now())
	require.NoError(t, err)
	require.True(t, allowed, "stale proof must not claim or lock the replacement identity")
}
