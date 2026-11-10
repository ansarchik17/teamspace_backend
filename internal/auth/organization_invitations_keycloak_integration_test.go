package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type invitationTestMail struct {
	text string
	fail bool
}

func (m *invitationTestMail) SendEmail(_ context.Context, _ string, _ string, _ string, text string) error {
	if m.fail {
		return errors.New("synthetic delivery failure")
	}
	m.text = text
	return nil
}

type invitationFailingAdmin struct {
	invitationIdentityAdmin
	failPassword, failEnable bool
	passwordCalls            int
}

func (a *invitationFailingAdmin) SetPassword(ctx context.Context, realm, id, password string, temporary bool) error {
	a.passwordCalls++
	if a.failPassword {
		a.failPassword = false
		return errors.New("synthetic transport failure")
	}
	return a.invitationIdentityAdmin.SetPassword(ctx, realm, id, password, temporary)
}
func (a *invitationFailingAdmin) UpdateUser(ctx context.Context, realm, id string, req keycloak.UserUpdateRequest) error {
	if req.Enabled != nil && *req.Enabled && a.failEnable {
		a.failEnable = false
		return errors.New("synthetic activation failure")
	}
	return a.invitationIdentityAdmin.UpdateUser(ctx, realm, id, req)
}

// Runs against an explicitly supplied disposable Keycloak and PostgreSQL, never a production realm.
func TestInvitationRealKeycloakLifecycle(t *testing.T) {
	base := os.Getenv("TEST_INVITATION_KEYCLOAK_URL")
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if base == "" || dsn == "" {
		t.Skip("set disposable TEST_INVITATION_KEYCLOAK_URL and TEST_MESSENGER_DATABASE_URL")
	}
	require.Contains(t, base, "127.0.0.1:", "this test only permits a loopback Keycloak")
	ctx := context.Background()
	admin := keycloak.NewAdmin(base, "master", os.Getenv("TEST_INVITATION_KEYCLOAK_ADMIN"), os.Getenv("TEST_INVITATION_KEYCLOAK_PASSWORD"))
	realm := "invitation-test-" + uuid.NewString()
	kcRequest := func(method, path string, payload any) []byte {
		token, err := admin.AdminToken(ctx)
		require.NoError(t, err)
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(data))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Less(t, resp.StatusCode, 300, "Keycloak admin %s %s: %s", method, path, string(body))
		return body
	}
	kcRequest("POST", "/admin/realms", map[string]any{"realm": realm, "enabled": true, "loginWithEmailAllowed": true, "sslRequired": "none"})
	t.Cleanup(func() { kcRequest("DELETE", "/admin/realms/"+realm, nil) })
	kcRequest("POST", "/admin/realms/"+realm+"/roles", map[string]any{"name": "user"})
	kcRequest("POST", "/admin/realms/"+realm+"/clients", map[string]any{"clientId": "invitation-login", "enabled": true, "publicClient": true, "directAccessGrantsEnabled": true})
	// Exercise registration with the realm's default unmanaged-attribute policy.
	// The service must install its own narrowly managed attributes rather than opening all attributes.
	root, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer root.Close()
	schema := "invitation_kc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = root.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,name TEXT,slug TEXT,status TEXT DEFAULT 'active',owner_id BIGINT,updated_at TIMESTAMPTZ DEFAULT now());
    CREATE TABLE users(id BIGSERIAL PRIMARY KEY,keycloak_id TEXT UNIQUE,iin TEXT UNIQUE,email TEXT UNIQUE,username TEXT,first_name TEXT,last_name TEXT,avatar_url TEXT,bio TEXT,position TEXT,is_active BOOLEAN,is_first_login BOOLEAN,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ,role TEXT,availability_status TEXT,full_name_local TEXT,can_view_full_drive_activity BOOLEAN,organization_id UUID REFERENCES organizations(id),team_id UUID,manager_id BIGINT,language TEXT DEFAULT 'ru');
    CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID,deleted_at TIMESTAMPTZ);
    CREATE TABLE organization_user_blocks(user_id BIGINT PRIMARY KEY);
    INSERT INTO users(id,email,username,role,is_active) VALUES(1,'owner@example.invalid','owner','user',true);
    SELECT setval(pg_get_serial_sequence('users','id'),1);`)
	require.NoError(t, err)
	company := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_id)VALUES($1,'Test company','test',1)`, company)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=1`, company)
	require.NoError(t, err)
	for _, file := range []string{"20261012000100_company_administration_audit.sql", "20261025000100_organization_invitations.sql"} {
		data, e := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, e)
	}
	repo := repository.NewOrganizationRepository(db, zap.NewNop())
	mail := &invitationTestMail{}
	s := NewInvitationService(repo, &Service{kcAdmin: admin, realm: realm}, mail, "https://workspace.invalid", zap.NewNop())
	flaky := &invitationFailingAdmin{invitationIdentityAdmin: admin}
	s.admin = flaky
	prepare := func(email string) (string, string, *domain.OrganizationInvitation) {
		issued, e := s.Issue(ctx, 1, nil, nil, domain.CreateInvitationRequest{Email: email})
		require.NoError(t, e)
		link, e := url.Parse(issued.URL)
		require.NoError(t, e)
		values, e := url.ParseQuery(link.Fragment)
		require.NoError(t, e)
		token := values.Get("token")
		require.NoError(t, s.SendCode(ctx, token))
		code := regexp.MustCompile(`Код подтверждения: ([0-9]{6})`).FindStringSubmatch(mail.text)
		require.Len(t, code, 2)
		return token, code[1], issued.Invitation
	}
	token, code, invite := prepare("person@example.invalid")
	req := InvitationRegistrationRequest{Token: token, Code: code, Username: "person", Password: "StrongPassword123!", FirstName: "Test", LastName: "Person", IIN: "333333333333"}
	flaky.failPassword = true
	_, err = s.Register(ctx, req)
	require.Error(t, err)
	identities, err := admin.GetUsersByEmail(ctx, realm, "person@example.invalid")
	require.NoError(t, err)
	require.Len(t, identities, 1)
	require.False(t, identities[0].Enabled)
	firstID := identities[0].ID
	require.Equal(t, []string{invite.ID.String()}, identities[0].Attributes["workspace_invitation_id"])
	// Revoking a failed invitation and issuing a fresh one must not strand its disabled account.
	require.NoError(t, s.Revoke(ctx, 1, nil, invite.ID))
	token, code, _ = prepare("person@example.invalid")
	req.Token = token
	req.Code = code
	req.Username = "person"
	result, err := s.Register(ctx, req)
	require.NoError(t, err)
	require.True(t, result.Registered)
	identities, err = admin.GetUsersByEmail(ctx, realm, "person@example.invalid")
	require.NoError(t, err)
	require.Len(t, identities, 1)
	require.Equal(t, firstID, identities[0].ID)
	require.True(t, identities[0].Enabled)
	require.Equal(t, req.Username, identities[0].Username)
	count := flaky.passwordCalls
	req.Password = "ReplayPassword999!"
	again, err := s.Register(ctx, req)
	require.NoError(t, err)
	require.Equal(t, result.UserID, again.UserID)
	require.Equal(t, count, flaky.passwordCalls, "a replay changed the accepted account's password")
	login := func(password string) int {
		form := url.Values{"grant_type": {"password"}, "client_id": {"invitation-login"}, "username": {req.Username}, "password": {password}}
		resp, e := http.PostForm(base+"/realms/"+realm+"/protocol/openid-connect/token", form)
		require.NoError(t, e)
		defer resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, 200, login("StrongPassword123!"))
	require.Equal(t, 400, login("ReplayPassword999!"))
	roles := kcRequest("GET", fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/realm", realm, firstID), nil)
	require.Contains(t, string(roles), `"name":"user"`)
	require.NotContains(t, string(roles), `"name":"admin"`)
	token, code, _ = prepare("activation@example.invalid")
	req.Token = token
	req.Code = code
	req.Username = "activation"
	req.IIN = "444444444444"
	flaky.failEnable = true
	_, err = s.Register(ctx, req)
	require.Error(t, err)
	pending, err := repo.PendingInvitationActivations(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	for hash, id := range pending {
		require.NoError(t, s.activate(ctx, hash, id))
	}
	pending, err = repo.PendingInvitationActivations(ctx)
	require.NoError(t, err)
	require.Empty(t, pending)
	// An unrelated SSO account, even with the invited email, is never reset or adopted by registration.
	_, err = admin.CreateUser(ctx, realm, keycloak.CreateUserRequest{Username: "existing", Email: "existing@example.invalid", Enabled: true, FirstName: "Existing", LastName: "User"})
	require.NoError(t, err)
	token, code, _ = prepare("existing@example.invalid")
	req.Token = token
	req.Code = code
	req.Username = "existing"
	req.IIN = "555555555555"
	count = flaky.passwordCalls
	_, err = s.Register(ctx, req)
	require.Error(t, err)
	require.Equal(t, count, flaky.passwordCalls)
}
