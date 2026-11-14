package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type mailboxSyncRepoStub struct {
	user          *domain.User
	mailboxLogin  string
	updatedUser   *domain.User
	upsertedLogin string
}

func (s *mailboxSyncRepoStub) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	if s.user != nil && s.user.Email == email {
		return s.user, nil
	}
	return nil, nil
}

func (s *mailboxSyncRepoStub) GetByID(_ context.Context, _ int64) (*domain.User, error) {
	return s.user, nil
}

func (s *mailboxSyncRepoStub) GetByIIN(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) GetByKeycloakID(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) UpsertFromKeycloak(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *mailboxSyncRepoStub) HasRegisteredMobileDevice(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

func (s *mailboxSyncRepoStub) UpdateLastLogin(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *mailboxSyncRepoStub) CompleteOnboarding(_ context.Context, _ int64) error {
	return nil
}

func (s *mailboxSyncRepoStub) SetIsFirstLogin(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (s *mailboxSyncRepoStub) Update(_ context.Context, user *domain.User) error {
	clone := *user
	s.updatedUser = &clone
	s.user = &clone
	return nil
}

func (s *mailboxSyncRepoStub) GetAll(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) GetPaginated(_ context.Context, _ string, _, _ int, _ ...domain.UserFilters) ([]*domain.User, int, error) {
	return nil, 0, nil
}

func (s *mailboxSyncRepoStub) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) GetFullDetailsByID(_ context.Context, _ int64) (*domain.UserDetailResponse, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) UpdateStatus(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *mailboxSyncRepoStub) GetByKeycloakIDs(_ context.Context, _ []string) ([]*domain.User, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) GetNeverLoggedIn(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) ExportMinistryUsers(_ context.Context, _ int64, _ []int64, _ []int64, _ []int64) ([]domain.MinistryUserExportRow, error) {
	return nil, nil
}

func (s *mailboxSyncRepoStub) GetMinistryName(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (s *mailboxSyncRepoStub) SoftDelete(_ context.Context, _ int64) error {
	return nil
}
func (s *mailboxSyncRepoStub) UpdateRole(_ context.Context, _ int64, _ domain.Role) error {
	return nil
}
func (s *mailboxSyncRepoStub) UpdateMinistryID(_ context.Context, _, _ int64) error {
	return nil
}

func (s *mailboxSyncRepoStub) GetPrimaryMailboxLogin(_ context.Context, _ int64) (string, error) {
	return s.mailboxLogin, nil
}

func (s *mailboxSyncRepoStub) UpsertPrimaryMailboxLogin(_ context.Context, _ int64, mailboxLogin string) error {
	s.upsertedLogin = mailboxLogin
	s.mailboxLogin = mailboxLogin
	return nil
}

type mailboxProvisionerStub struct {
	login string
}

func (s *mailboxProvisionerStub) ProvisionPrimaryMailbox(_ context.Context, _ *domain.UserResponse) (string, error) {
	return s.login, nil
}

func TestResolveMailOAuthMailboxLoginSyncsUserEmailToProvisionedMailbox(t *testing.T) {
	t.Parallel()

	var gotUpdate keycloak.UserUpdateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/GovApps/protocol/openid-connect/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "admin-token",
				"expires_in":   300,
				"token_type":   "Bearer",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/GovApps/users/kc-user-1":
			if err := json.NewDecoder(r.Body).Decode(&gotUpdate); err != nil {
				t.Fatalf("decode keycloak update request: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	kcID := "kc-user-1"
	repo := &mailboxSyncRepoStub{
		user: &domain.User{
			ID:         15,
			KeycloakID: &kcID,
			Email:      "user@old.example",
			Username:   "old.username",
			FirstName:  "Test",
			LastName:   "User",
			IsActive:   true,
			Role:       domain.RoleUser,
		},
	}

	svc := NewKeycloakService(
		repo,
		nil,
		nil,
		nil,
		keycloak.NewAdminWithClientCredentials(server.URL, "GovApps", "gov-workspace-admin", "secret-1"),
		"GovApps",
		WithMailboxRepository(repo),
		WithMailboxProvisioner(&mailboxProvisionerStub{login: "user@cdn-kindy.kz"}),
	)

	respUser := &domain.UserResponse{
		ID:         15,
		KeycloakID: &kcID,
		Email:      "user@old.example",
		Username:   "old.username",
		FirstName:  "Test",
		LastName:   "User",
		IsActive:   true,
	}
	mailboxLogin, err := svc.ResolveMailOAuthMailboxLogin(context.Background(), respUser)
	if err != nil {
		t.Fatalf("ResolveMailOAuthMailboxLogin returned error: %v", err)
	}

	if mailboxLogin != "user@cdn-kindy.kz" {
		t.Fatalf("mailboxLogin = %q, want user@cdn-kindy.kz", mailboxLogin)
	}
	if repo.upsertedLogin != "user@cdn-kindy.kz" {
		t.Fatalf("upserted mailbox login = %q, want user@cdn-kindy.kz", repo.upsertedLogin)
	}
	if repo.updatedUser == nil || repo.updatedUser.Email != "user@cdn-kindy.kz" {
		t.Fatalf("updated local user email = %#v, want user@cdn-kindy.kz", repo.updatedUser)
	}
	if respUser.Email != "user@cdn-kindy.kz" {
		t.Fatalf("response user email = %q, want user@cdn-kindy.kz", respUser.Email)
	}
	if gotUpdate.Email == nil || *gotUpdate.Email != "user@cdn-kindy.kz" {
		t.Fatalf("keycloak email = %#v, want user@cdn-kindy.kz", gotUpdate.Email)
	}
	if gotUpdate.Username == nil || *gotUpdate.Username != "old.username" {
		t.Fatalf("keycloak username = %#v, want old.username", gotUpdate.Username)
	}
	if gotUpdate.EmailVerified == nil || !*gotUpdate.EmailVerified {
		t.Fatalf("keycloak emailVerified = %#v, want true", gotUpdate.EmailVerified)
	}
}
