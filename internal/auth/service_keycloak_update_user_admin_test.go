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

type updateUserAdminRepoStub struct {
	user              *domain.User
	users             map[int64]*domain.User
	updated           *domain.User
	setIsFirstLoginID int64
	setIsFirstLogin   *bool
}

func (s *updateUserAdminRepoStub) GetByEmail(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if s.users != nil {
		if user, ok := s.users[id]; ok {
			return user, nil
		}
		return nil, nil
	}
	return s.user, nil
}

func (s *updateUserAdminRepoStub) GetByIIN(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) GetByKeycloakID(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) UpsertFromKeycloak(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *updateUserAdminRepoStub) HasRegisteredMobileDevice(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

func (s *updateUserAdminRepoStub) UpdateLastLogin(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *updateUserAdminRepoStub) CompleteOnboarding(_ context.Context, _ int64) error {
	return nil
}

func (s *updateUserAdminRepoStub) SetIsFirstLogin(_ context.Context, userID int64, isFirst bool) error {
	s.setIsFirstLoginID = userID
	s.setIsFirstLogin = &isFirst
	return nil
}

func (s *updateUserAdminRepoStub) Update(_ context.Context, user *domain.User) error {
	clone := *user
	s.updated = &clone
	return nil
}

func (s *updateUserAdminRepoStub) GetAll(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) GetPaginated(_ context.Context, _ string, _, _ int, _ ...domain.UserFilters) ([]*domain.User, int, error) {
	return nil, 0, nil
}

func (s *updateUserAdminRepoStub) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) GetFullDetailsByID(_ context.Context, _ int64) (*domain.UserDetailResponse, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) UpdateStatus(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *updateUserAdminRepoStub) GetByKeycloakIDs(_ context.Context, _ []string) ([]*domain.User, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) GetNeverLoggedIn(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *updateUserAdminRepoStub) ExportMinistryUsers(_ context.Context, _ int64, _ []int64, _ []int64, _ []int64) ([]domain.MinistryUserExportRow, error) {
	return nil, nil
}
func (s *updateUserAdminRepoStub) GetMinistryName(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (s *updateUserAdminRepoStub) SoftDelete(_ context.Context, _ int64) error {
	return nil
}
func (s *updateUserAdminRepoStub) UpdateRole(_ context.Context, _ int64, _ domain.Role) error {
	return nil
}
func (s *updateUserAdminRepoStub) UpdateMinistryID(_ context.Context, _, _ int64) error {
	return nil
}

func TestUpdateUserAdminSyncsNamesToKeycloak(t *testing.T) {
	t.Parallel()

	var gotUpdate keycloak.UserUpdateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/master/protocol/openid-connect/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "admin-token",
				"expires_in":   300,
				"token_type":   "Bearer",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/test/users/kc-user-1":
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
	repo := &updateUserAdminRepoStub{
		user: &domain.User{
			ID:         77,
			KeycloakID: &kcID,
			Email:      "user@example.com",
			Username:   "old.username",
			FirstName:  "Old",
			LastName:   "Name",
			IsActive:   true,
			Role:       domain.RoleUser,
		},
	}

	svc := NewKeycloakService(
		repo,
		nil,
		nil,
		nil,
		keycloak.NewAdmin(server.URL, "master", "admin", "admin"),
		"test",
	)

	firstName := "New"
	lastName := "Surname"
	username := "new.username"
	resp, err := svc.UpdateUserAdmin(context.Background(), 0, 77, &UpdateUserRequest{
		Username:  &username,
		FirstName: &firstName,
		LastName:  &lastName,
	})
	if err != nil {
		t.Fatalf("UpdateUserAdmin returned error: %v", err)
	}

	if gotUpdate.Username == nil || *gotUpdate.Username != username {
		t.Fatalf("keycloak username = %#v, want %q", gotUpdate.Username, username)
	}
	if gotUpdate.FirstName == nil || *gotUpdate.FirstName != firstName {
		t.Fatalf("keycloak firstName = %#v, want %q", gotUpdate.FirstName, firstName)
	}
	if gotUpdate.LastName == nil || *gotUpdate.LastName != lastName {
		t.Fatalf("keycloak lastName = %#v, want %q", gotUpdate.LastName, lastName)
	}
	if repo.updated == nil {
		t.Fatal("expected local user update")
	}
	if repo.updated.FirstName != firstName || repo.updated.LastName != lastName {
		t.Fatalf("local user names = %q %q, want %q %q", repo.updated.FirstName, repo.updated.LastName, firstName, lastName)
	}
	if resp == nil || resp.FirstName != firstName || resp.LastName != lastName {
		t.Fatal("expected updated response")
	}
}

func TestResetUserPasswordAdminMarksFirstLoginAndLogsOutSessions(t *testing.T) {
	t.Parallel()

	var resetCalled bool
	var logoutCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/master/protocol/openid-connect/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "admin-token",
				"expires_in":   300,
				"token_type":   "Bearer",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/test/users/kc-user-1/reset-password":
			resetCalled = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test/users/kc-user-1/logout":
			logoutCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	ministryID := int64(15)
	kcID := "kc-user-1"
	target := &domain.User{
		ID:         77,
		KeycloakID: &kcID,
		Email:      "user@example.com",
		Username:   "user77",
		FirstName:  "Test",
		LastName:   "User",
		IsActive:   true,
		Role:       domain.RoleUser,
		MinistryID: &ministryID,
	}
	requester := &domain.User{
		ID:         78,
		Email:      "admin@example.com",
		Username:   "admin78",
		FirstName:  "Admin",
		LastName:   "User",
		IsActive:   true,
		Role:       domain.RoleUser,
		MinistryID: &ministryID,
	}
	repo := &updateUserAdminRepoStub{
		users: map[int64]*domain.User{
			77: target,
			78: requester,
		},
	}

	svc := NewKeycloakService(
		repo,
		nil,
		nil,
		nil,
		keycloak.NewAdmin(server.URL, "master", "admin", "admin"),
		"test",
	)

	err := svc.ResetUserPasswordAdmin(context.Background(), 78, 77, "NewPassw0rd!", true)
	if err != nil {
		t.Fatalf("ResetUserPasswordAdmin returned error: %v", err)
	}
	if !resetCalled {
		t.Fatal("expected reset-password endpoint to be called")
	}
	if !logoutCalled {
		t.Fatal("expected logout endpoint to be called")
	}
	if repo.setIsFirstLogin == nil || !*repo.setIsFirstLogin || repo.setIsFirstLoginID != 77 {
		t.Fatal("expected target user to be marked for first login password change")
	}
}
