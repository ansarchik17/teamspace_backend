package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	appservice "github.com/west6ide/workspace-backend/internal/service"
)

type changePasswordUserRepoStub struct {
	user *domain.User
}

func (s *changePasswordUserRepoStub) GetByEmail(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) GetByID(_ context.Context, _ int64) (*domain.User, error) {
	return s.user, nil
}

func (s *changePasswordUserRepoStub) GetByIIN(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) GetByKeycloakID(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) UpsertFromKeycloak(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *changePasswordUserRepoStub) HasRegisteredMobileDevice(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

func (s *changePasswordUserRepoStub) UpdateLastLogin(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *changePasswordUserRepoStub) CompleteOnboarding(_ context.Context, _ int64) error {
	return nil
}

func (s *changePasswordUserRepoStub) SetIsFirstLogin(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (s *changePasswordUserRepoStub) Update(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *changePasswordUserRepoStub) GetAll(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) GetPaginated(_ context.Context, _ string, _, _ int, _ ...domain.UserFilters) ([]*domain.User, int, error) {
	return nil, 0, nil
}

func (s *changePasswordUserRepoStub) GetAllVisibleTo(_ context.Context, _ int64) ([]*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) GetFullDetailsByID(_ context.Context, _ int64) (*domain.UserDetailResponse, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) UpdateStatus(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *changePasswordUserRepoStub) GetByKeycloakIDs(_ context.Context, _ []string) ([]*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) GetNeverLoggedIn(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *changePasswordUserRepoStub) ExportMinistryUsers(_ context.Context, _ int64, _ []int64, _ []int64, _ []int64) ([]domain.MinistryUserExportRow, error) {
	return nil, nil
}
func (s *changePasswordUserRepoStub) GetMinistryName(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (s *changePasswordUserRepoStub) SoftDelete(_ context.Context, _ int64) error {
	return nil
}
func (s *changePasswordUserRepoStub) UpdateRole(_ context.Context, _ int64, _ domain.Role) error {
	return nil
}
func (s *changePasswordUserRepoStub) UpdateMinistryID(_ context.Context, _, _ int64) error {
	return nil
}

type failingKeyBackupService struct {
	cancel context.CancelFunc
}

func (s *failingKeyBackupService) SaveKeyBackup(_ context.Context, _ int64, _ *appservice.SaveKeyBackupRequest) error {
	s.cancel()
	return errors.New("db unavailable")
}

func TestChangePasswordRollsBackWithFreshContextWhenBackupSaveCancelsRequest(t *testing.T) {
	t.Parallel()

	var passwordUpdates []string
	kcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/test-realm/protocol/openid-connect/token":
			require.NoError(t, r.ParseForm())
			if r.Form.Get("client_id") == "admin-cli" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"admin-token","token_type":"Bearer"}`))
				return
			}
			if r.Form.Get("username") == "user@example.com" && r.Form.Get("password") == "old-password" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"user-token","refresh_token":"refresh-token","token_type":"Bearer"}`))
				return
			}
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/reset-password"):
			var body struct {
				Value string `json:"value"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			passwordUpdates = append(passwordUpdates, body.Value)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(kcServer.Close)

	keycloakID := "kc-user-77"
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewKeycloakService(
		&changePasswordUserRepoStub{user: &domain.User{
			ID:         77,
			Email:      "user@example.com",
			KeycloakID: &keycloakID,
		}},
		nil,
		nil,
		keycloak.New(kcServer.URL, "test-realm", "test-client", ""),
		keycloak.NewAdmin(kcServer.URL, "test-realm", "admin", "admin"),
		"test-realm",
		WithKeyBackupService(&failingKeyBackupService{cancel: cancel}),
	)

	err := svc.ChangePassword(ctx, 77, "old-password", "new-password", validChangePasswordBackupRequestForTest())
	require.Error(t, err)
	require.Contains(t, err.Error(), "password change was rolled back")
	require.Equal(t, []string{"new-password", "old-password"}, passwordUpdates)
}

func validChangePasswordBackupRequestForTest() *appservice.SaveKeyBackupRequest {
	cipherTagEmbedded := true
	return &appservice.SaveKeyBackupRequest{
		EncryptedPrivateKey: "Y2lwaGVydGV4dA==",
		Salt:                "MDEyMzQ1Njc4OWFiY2RlZg==",
		IV:                  "MDEyMzQ1Njc4OWFi",
		KDFAlgorithm:        "pbkdf2-sha256",
		KDFParams: &domain.KeyDerivationParams{
			Iterations: intPtr(310000),
			Hash:       stringPtr("sha256"),
			KeyLength:  intPtr(32),
		},
		Version:             1,
		BackupKeyType:       "x25519",
		BackupKeyFormat:     "pkcs8-der-base64",
		BackupKeyScope:      "device_identity",
		BackupFormatVersion: 1,
		CipherAlgorithm:     "aes-256-gcm",
		CipherTagEmbedded:   &cipherTagEmbedded,
		AADMode:             "none",
		PasswordProcessing:  "utf8-raw",
	}
}

func intPtr(value int) *int {
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func TestChangePasswordDoesNotRequireChatEncryptionBackup(t *testing.T) {
	changed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token"):
			require.NoError(t, r.ParseForm())
			if r.Form.Get("client_id") != "admin-cli" {
				require.Equal(t, "employee", r.Form.Get("username"))
				require.Equal(t, "OldPassword123!", r.Form.Get("password"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/reset-password"):
			var payload struct {
				Value string `json:"value"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "NewPassword123!", payload.Value)
			changed = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	id := "kc-user"
	s := &Service{userRepo: &changePasswordUserRepoStub{user: &domain.User{ID: 7, Username: "employee", Email: "work@example.invalid", KeycloakID: &id}}, kc: keycloak.New(server.URL, "test", "web", ""), kcAdmin: keycloak.NewAdmin(server.URL, "master", "admin", "test"), realm: "test"}
	require.NoError(t, s.ChangePassword(context.Background(), 7, "OldPassword123!", "NewPassword123!", nil))
	require.True(t, changed)
}

func TestChangePasswordRejectsIdentityChangedAfterReauthentication(t *testing.T) {
	original, other := "original-identity", "other-identity"
	s := &Service{userRepo: &changePasswordUserRepoStub{user: &domain.User{ID: 7, KeycloakID: &other}}}
	err := s.changePassword(context.Background(), 7, "OldPassword123!", "NewPassword123!", nil, &domain.User{ID: 7, KeycloakID: &original})
	require.Error(t, err, "proof for the previous identity must not authorize changing another identity's password")
}
