package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"golang.org/x/crypto/bcrypt"
)

const localTestOldPassword = "LocalOldPassword2026!"
const localTestNewPassword = "LocalNewPassword2026!"

type localPasswordUserRepo struct {
	changePasswordUserRepoStub
	db *pgxpool.Pool
}

func (r *localPasswordUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	var u domain.User
	err := r.db.QueryRow(ctx, `SELECT id,username,email,COALESCE(password_hash,''),keycloak_id,is_active,is_first_login FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.KeycloakID, &u.IsActive, &u.IsFirstLogin)
	return &u, err
}
func (r *localPasswordUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	u, err := r.GetByID(ctx, 7)
	if err != nil || u.Email == email {
		return u, err
	}
	return nil, nil
}
func (r *localPasswordUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	u, err := r.GetByID(ctx, 7)
	if err != nil || u.Username == username {
		return u, err
	}
	return nil, nil
}

func localPasswordFixture(t *testing.T) (context.Context, *Service, *pgxpool.Pool, *atomic.Int64) {
	t.Helper()
	ctx, recovery, db, _ := recoveryServiceFixture(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(localTestOldPassword), bcrypt.MinCost)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `ALTER TABLE users ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now()`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET keycloak_id=NULL,password_hash=$1 WHERE id=7`, string(hash))
	require.NoError(t, err)
	calls := &atomic.Int64{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(provider.Close)
	s := &Service{recovery: recovery, tokenManager: recovery.tokens, userRepo: &localPasswordUserRepo{db: db}, kc: keycloak.New(provider.URL, "test", "web", "")}
	return ctx, s, db, calls
}

func TestLocalPasswordChangeAndLoginIntegration(t *testing.T) {
	ctx, s, db, providerCalls := localPasswordFixture(t)
	old, err := s.Login(ctx, &LoginRequest{Email: "work@example.invalid", Password: localTestOldPassword})
	require.NoError(t, err)
	staleIdentity, err := s.userRepo.GetByID(ctx, 7)
	require.NoError(t, err)
	otherUserTokens, err := s.tokenManager.GenerateTokenPair(ctx, 8, "", "unrelated@example.invalid", "unrelated", "", "", false, nil)
	require.NoError(t, err)
	proof := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO password_recovery_requests(id,user_id,email,email_version,keycloak_id,token_hash) VALUES($1,7,'work@example.invalid',$2,'former-identity','test-proof')`, proof, uuid.New())
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO recovery_email_challenges(id,user_id,email,purpose,code_hash) VALUES(gen_random_uuid(),7,'backup@example.invalid','bind','test-code');
INSERT INTO password_recovery_mail_jobs(user_id,locale) VALUES(7,'ru');
INSERT INTO quick_pin_credential(user_id,active,encrypted_refresh_token) VALUES(7,true,'old-refresh');
INSERT INTO web_pairing_sessions(user_id,status) VALUES(7,'approved')`)
	require.NoError(t, err)
	before := providerCalls.Load()
	closed := int64(0)
	s.recovery.SetSessionCloser(func(id int64) { closed = id })
	result, err := s.ChangePasswordWithResult(ctx, 7, localTestOldPassword, localTestNewPassword, nil)
	require.NoError(t, err)
	require.True(t, result.ReauthenticationRequired)
	require.Equal(t, int64(7), closed)
	require.Equal(t, before, providerCalls.Load(), "changing a local password must not contact Keycloak")
	u, err := s.userRepo.GetByID(ctx, 7)
	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(localTestNewPassword)))
	require.Error(t, bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(localTestOldPassword)))
	require.False(t, u.IsFirstLogin)
	_, err = s.tokenManager.ValidateToken(ctx, old.AccessToken)
	require.ErrorIs(t, err, ErrCredentialsRevoked)
	_, err = s.tokenManager.RefreshAccessToken(ctx, old.RefreshToken)
	require.ErrorIs(t, err, ErrCredentialsRevoked)
	_, err = s.tokenManager.ValidateToken(ctx, otherUserTokens.AccessToken)
	require.NoError(t, err, "another account's sessions must remain valid")
	_, err = s.Login(ctx, &LoginRequest{Email: "work@example.invalid", Password: localTestOldPassword})
	require.Error(t, err)
	_, err = s.localPasswordTokens(ctx, staleIdentity, localTestOldPassword)
	require.Error(t, err, "an in-flight login cannot use a stale password snapshot after the change")
	fresh, err := s.Login(ctx, &LoginRequest{Email: "work@example.invalid", Password: localTestNewPassword})
	require.NoError(t, err, "the first new-password login after completion must work")
	_, err = s.tokenManager.ValidateToken(ctx, fresh.AccessToken)
	require.NoError(t, err)
	var valid bool
	require.NoError(t, db.QueryRow(ctx, `SELECT
 (SELECT state='revoked' FROM password_recovery_requests WHERE id=$1)
 AND NOT EXISTS(SELECT 1 FROM recovery_email_challenges WHERE user_id=7 AND consumed_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM password_recovery_mail_jobs WHERE user_id=7)
 AND NOT EXISTS(SELECT 1 FROM quick_pin_credential WHERE user_id=7 AND (active OR encrypted_refresh_token IS NOT NULL))
 AND NOT EXISTS(SELECT 1 FROM web_pairing_sessions WHERE user_id=7 AND status IN ('approved','synced'))
 AND (SELECT count(*)=1 FROM account_security_audit WHERE user_id=7 AND event='password_changed')`, proof).Scan(&valid))
	require.True(t, valid)
	// Email-link recovery is still Keycloak-only; it cannot dereference a nil identity.
	require.Error(t, s.recovery.repo.WithUser(ctx, 7, func(*pgxpool.Conn, *domain.User) error {
		t.Fatal("local identity admitted to Keycloak recovery")
		return nil
	}))
}

func TestLocalPasswordRejectedChangesLeaveCredentialsIntactIntegration(t *testing.T) {
	for _, tc := range []struct{ name, old, new string }{
		{"incorrect old password", "WrongPassword!", localTestNewPassword},
		{"too short", localTestOldPassword, "short"},
		{"bcrypt byte limit", localTestOldPassword, strings.Repeat("a", 73)},
		{"unicode byte limit", localTestOldPassword, strings.Repeat("я", 37)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, s, db, _ := localPasswordFixture(t)
			u, err := s.userRepo.GetByID(ctx, 7)
			require.NoError(t, err)
			_, err = s.ChangePasswordWithResult(ctx, 7, tc.old, tc.new, nil)
			require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
			after, err := s.userRepo.GetByID(ctx, 7)
			require.NoError(t, err)
			require.Equal(t, u.PasswordHash, after.PasswordHash)
			var count int
			require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM user_credential_state`).Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestLocalPasswordTransactionRollbackAndIdentityGuardIntegration(t *testing.T) {
	ctx, s, db, _ := localPasswordFixture(t)
	old, err := s.userRepo.GetByID(ctx, 7)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE FUNCTION reject_password_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER reject_password_audit BEFORE INSERT ON account_security_audit FOR EACH ROW EXECUTE FUNCTION reject_password_audit()`)
	require.NoError(t, err)
	require.Error(t, s.ChangePassword(ctx, 7, localTestOldPassword, localTestNewPassword, nil))
	after, err := s.userRepo.GetByID(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, old.PasswordHash, after.PasswordHash)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM user_credential_state`).Scan(&count))
	require.Zero(t, count, "password and revocation must roll back together")
	_, err = db.Exec(ctx, `DROP TRIGGER reject_password_audit ON account_security_audit`)
	require.NoError(t, err)
	err = s.recovery.repo.WithPasswordUser(ctx, 7, func(c *pgxpool.Conn, verified *domain.User) error {
		_, err := c.Exec(ctx, `UPDATE users SET keycloak_id='new-provider-identity' WHERE id=7`)
		require.NoError(t, err)
		_, err = s.recovery.repo.ChangeLocalPassword(ctx, c, verified, "must-not-be-written")
		return err
	})
	require.Error(t, err, "proof for a local identity must not overwrite a newly linked provider")
	after, err = s.userRepo.GetByID(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, old.PasswordHash, after.PasswordHash)
}

func TestLocalPasswordHonorsPendingResetIntegration(t *testing.T) {
	ctx, s, db, _ := localPasswordFixture(t)
	_, err := db.Exec(ctx, `INSERT INTO user_credential_state(user_id,reset_pending) VALUES(7,true)`)
	require.NoError(t, err)
	err = s.ChangePassword(ctx, 7, localTestOldPassword, localTestNewPassword, nil)
	require.Error(t, err)
	require.Equal(t, "RECOVERY_PENDING", string(apperrors.ToAppError(err).Code))
	u, err := s.userRepo.GetByID(ctx, 7)
	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(localTestOldPassword)))
	// Cancellation before mutation also leaves the password unchanged.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.Error(t, s.ChangePassword(cancelled, 7, localTestOldPassword, localTestNewPassword, nil))
}
