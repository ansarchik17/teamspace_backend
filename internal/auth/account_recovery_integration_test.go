package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type recoveryTestMail struct {
	texts    []string
	disabled bool
	fail     bool
}

func (m *recoveryTestMail) Enabled() bool { return !m.disabled }
func (m *recoveryTestMail) SendEmail(_ context.Context, _ string, _ string, _ string, text string) error {
	if m.fail || m.disabled {
		return errors.New("smtp unavailable")
	}
	m.texts = append(m.texts, text)
	return nil
}
func (m *recoveryTestMail) code(t *testing.T) string {
	t.Helper()
	match := regexp.MustCompile(`[0-9]{6}`).FindString(m.texts[len(m.texts)-1])
	require.NotEmpty(t, match)
	return match
}
func (m *recoveryTestMail) token(t *testing.T) string {
	t.Helper()
	for i := len(m.texts) - 1; i >= 0; i-- {
		match := regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(m.texts[i])
		if len(match) == 2 {
			return match[1]
		}
	}
	t.Fatal("no recovery link delivered")
	return ""
}
func recoveryServiceFixture(t *testing.T) (context.Context, *RecoveryService, *pgxpool.Pool, *recoveryTestMail) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set a disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx := context.Background()
	root, e := pgxpool.New(ctx, dsn)
	require.NoError(t, e)
	schema := "recovery_auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, e = root.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, e)
	t.Cleanup(func() { _, _ = root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); root.Close() })
	cfg, e := pgxpool.ParseConfig(dsn)
	require.NoError(t, e)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, e)
	t.Cleanup(db.Close)
	_, e = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,status TEXT);CREATE TABLE users(id BIGINT PRIMARY KEY,username TEXT,email TEXT,keycloak_id TEXT,is_active BOOLEAN DEFAULT true,deleted_at TIMESTAMPTZ,organization_id UUID,is_first_login BOOLEAN DEFAULT true,password_hash TEXT);
 CREATE TABLE organization_user_blocks(user_id BIGINT PRIMARY KEY);
 CREATE TABLE user_mailboxes(user_id BIGINT,mailbox_login TEXT);
 CREATE TABLE user_verified_contacts(user_id BIGINT,email TEXT UNIQUE,source TEXT CHECK(source='organization_invitation'),PRIMARY KEY(user_id,email));
 CREATE TABLE quick_pin_credential(user_id BIGINT,active BOOLEAN,encrypted_refresh_token TEXT,updated_at TIMESTAMPTZ);
 CREATE TABLE web_pairing_sessions(user_id BIGINT,status TEXT,updated_at TIMESTAMPTZ);
 INSERT INTO users(id,username,email,keycloak_id,password_hash)VALUES(7,'employee','work@example.invalid','identity','old-local-hash')`)
	require.NoError(t, e)
	data, e := os.ReadFile("../../db/migrations/20261025000200_account_recovery.sql")
	require.NoError(t, e)
	_, e = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, e)
	mail := &recoveryTestMail{}
	repo := repository.NewRecoveryRepository(db)
	s := NewRecoveryService(repo, nil, mail, "https://workspace.invalid/ignored?untrusted=1", zap.NewNop())
	s.tokens = NewTokenManager(&config.JWTConfig{Secret: "test-secret", Issuer: "issuer", Audience: "audience", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}, newLinkedDeviceTokenStorageStub())
	s.tokens.SetCredentialLookup(repo.TokenAllowed)
	return ctx, s, db, mail
}

type recoveryFakeIdentity struct {
	password                      string
	uncertain, logoutFail, policy bool
	sets, logouts                 int
}

func (f *recoveryFakeIdentity) SetPassword(_ context.Context, _, _, password string, _ bool) error {
	f.sets++
	if f.policy || password == f.password {
		return &keycloak.RequestError{StatusCode: 400}
	}
	f.password = password
	if f.uncertain {
		f.uncertain = false
		return errors.New("response lost after commit")
	}
	return nil
}
func (f *recoveryFakeIdentity) LogoutUser(context.Context, string, string) error {
	f.logouts++
	if f.logoutFail {
		f.logoutFail = false
		return errors.New("temporarily unavailable")
	}
	return nil
}
func (f *recoveryFakeIdentity) Password(_ context.Context, username, password string) (*keycloak.TokenResponse, error) {
	if username != "employee" || password != f.password {
		return nil, &keycloak.RequestError{StatusCode: 400, ErrorCode: "invalid_grant"}
	}
	raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "identity", "iat": time.Now().Unix()}).SignedString([]byte("fake-provider"))
	return &keycloak.TokenResponse{AccessToken: raw}, e
}
func setupRecoveryEmail(t *testing.T, ctx context.Context, s *RecoveryService, mail *recoveryTestMail, password string) {
	t.Helper()
	challenge, e := s.BeginEmail(ctx, 7, "personal@example.invalid", "bind", password, "ru")
	require.NoError(t, e)
	require.NoError(t, s.ConfirmEmail(ctx, 7, challenge.ID, mail.code(t), password, "ru"))
}
func issueRecoveryToken(t *testing.T, ctx context.Context, s *RecoveryService, mail *recoveryTestMail) string {
	t.Helper()
	require.NoError(t, s.RequestPassword(ctx, "employee", "127.0.0.1", "ru"))
	s.work(ctx)
	return mail.token(t)
}
func TestRecoveryServiceUncertainPasswordCommit(t *testing.T) {
	ctx, s, db, mail := recoveryServiceFixture(t)
	identity := &recoveryFakeIdentity{password: "OldPassword123!", uncertain: true}
	s.admin = identity
	s.client = identity
	setupRecoveryEmail(t, ctx, s, mail, identity.password)
	old, e := s.tokens.GenerateTokenPair(ctx, 7, "", "", "", "", "", false, nil)
	require.NoError(t, e)
	token := issueRecoveryToken(t, ctx, s, mail)
	require.Error(t, s.Complete(ctx, token, "NewPassword123!", "127.0.0.1", "ru"))
	require.Equal(t, "NewPassword123!", identity.password)
	_, e = s.tokens.ValidateToken(ctx, old.AccessToken)
	require.ErrorIs(t, e, ErrCredentialsRevoked)
	_, e = s.BeginEmail(ctx, 7, "other@example.invalid", "bind", "NewPassword123!", "ru")
	require.Error(t, e)
	// Retrying the same password hits a password-history rejection but succeeds
	// after trusted password proof; no plaintext secret is stored to resume it.
	require.NoError(t, s.Complete(ctx, token, "NewPassword123!", "127.0.0.1", "ru"))
	require.Equal(t, 2, identity.sets)
	require.Error(t, s.Complete(ctx, token, "OtherPassword123!", "127.0.0.1", "ru"))
	var passwordHash *string
	require.NoError(t, db.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=7`).Scan(&passwordHash))
	require.Nil(t, passwordHash)
	var snapshot string
	require.NoError(t, db.QueryRow(ctx, `SELECT row_to_json(r)::text FROM password_recovery_requests r LIMIT 1`).Scan(&snapshot))
	require.NotContains(t, snapshot, token)
	require.NotContains(t, snapshot, "NewPassword")
	_, e = s.tokens.ValidateToken(ctx, old.RefreshToken)
	require.Error(t, e)
}
func TestRecoveryServicePolicyAndCompletionRetry(t *testing.T) {
	ctx, s, _, mail := recoveryServiceFixture(t)
	identity := &recoveryFakeIdentity{password: "OldPassword123!", policy: true}
	s.admin = identity
	s.client = identity
	setupRecoveryEmail(t, ctx, s, mail, identity.password)
	token := issueRecoveryToken(t, ctx, s, mail)
	for i := 0; i < 2; i++ {
		require.Error(t, s.Complete(ctx, token, "ValidLengthButBadPolicy", "127.0.0.1", "ru"))
		allowed, e := s.repo.TokenAllowed(ctx, 7, time.Now())
		require.NoError(t, e)
		require.True(t, allowed, "known policy rejection must not lock account")
	}
	identity.policy = false
	identity.logoutFail = true
	require.Error(t, s.Complete(ctx, token, "NewPassword123!", "127.0.0.1", "ru"))
	sets := identity.sets
	// Worker finishes after restart without the password, even if SMTP is disabled.
	mail.disabled = true
	s.work(ctx)
	require.Equal(t, sets, identity.sets)
	require.Error(t, s.Preview(ctx, token, "127.0.0.1"))
}
func TestRecoveryRealKeycloakLifecycle(t *testing.T) {
	base := os.Getenv("TEST_INVITATION_KEYCLOAK_URL")
	if base == "" {
		t.Skip("set disposable TEST_INVITATION_KEYCLOAK_URL")
	}
	require.Contains(t, base, "127.0.0.1:")
	ctx, s, db, mail := recoveryServiceFixture(t)
	admin := keycloak.NewAdmin(base, "master", os.Getenv("TEST_INVITATION_KEYCLOAK_ADMIN"), os.Getenv("TEST_INVITATION_KEYCLOAK_PASSWORD"))
	realm := "recovery-test-" + uuid.NewString()
	request := func(method, path string, payload any) {
		raw, e := json.Marshal(payload)
		require.NoError(t, e)
		token, e := admin.AdminToken(ctx)
		require.NoError(t, e)
		req, e := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(raw))
		require.NoError(t, e)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		require.NoError(t, e)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		require.Less(t, resp.StatusCode, 300, string(body))
	}
	request("POST", "/admin/realms", map[string]any{"realm": realm, "enabled": true, "passwordPolicy": "length(8) and passwordHistory(3)"})
	t.Cleanup(func() { request("DELETE", "/admin/realms/"+realm, nil) })
	request("POST", "/admin/realms/"+realm+"/clients", map[string]any{"clientId": "recovery-login", "enabled": true, "publicClient": true, "directAccessGrantsEnabled": true})
	id, e := admin.CreateUser(ctx, realm, keycloak.CreateUserRequest{Username: "employee", Email: "work@example.invalid", Enabled: true, FirstName: "Test", LastName: "User"})
	require.NoError(t, e)
	require.NoError(t, admin.SetPassword(ctx, realm, id, "OldPassword123!", false))
	_, e = db.Exec(ctx, `UPDATE users SET keycloak_id=$1 WHERE id=7`, id)
	require.NoError(t, e)
	client := keycloak.New(base, realm, "recovery-login", "")
	s.admin = admin
	s.client = client
	s.realm = realm
	old, e := client.Password(ctx, "employee", "OldPassword123!")
	require.NoError(t, e)
	setupRecoveryEmail(t, ctx, s, mail, "OldPassword123!")
	token := issueRecoveryToken(t, ctx, s, mail)
	require.NoError(t, s.Complete(ctx, token, "NewPassword123!", "127.0.0.1", "kk"))
	_, e = client.Password(ctx, "employee", "OldPassword123!")
	require.Error(t, e)
	fresh, e := client.Password(ctx, "employee", "NewPassword123!")
	require.NoError(t, e)
	_, e = client.Refresh(ctx, old.RefreshToken)
	require.Error(t, e, "old provider refresh must fail after logout-all")
	require.Error(t, (&Service{tokenManager: s.tokens}).checkProviderCredentialState(ctx, old.AccessToken, 7))
	require.Error(t, s.Preview(ctx, token, "127.0.0.1"))
	// Completion waits for the second-precision cutoff: the first immediate
	// login works, without asking the user to retry after a valid password.
	require.NoError(t, (&Service{tokenManager: s.tokens}).checkProviderCredentialState(ctx, fresh.AccessToken, 7))
}
