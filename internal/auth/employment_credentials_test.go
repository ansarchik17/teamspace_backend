package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type admissionPreparationRepo struct{ iformOperationUserRepo }

func (r *admissionPreparationRepo) IsUserActive(context.Context, int64) (bool, error) {
	return false, nil
}
func (r *admissionPreparationRepo) SetIsFirstLogin(_ context.Context, id int64, first bool) error {
	r.users[id].IsFirstLogin = first
	return nil
}
func (r *admissionPreparationRepo) GetByEmail(context.Context, string) (*domain.User, error) {
	return r.users[24], nil
}

func TestHRPreparesCredentialWithoutOpeningEarlyPasswordLogin(t *testing.T) {
	company, request := uuid.New(), uuid.New()
	kcID := "future-hire"
	date := time.Now().Add(30 * 24 * time.Hour).Format("2006-01-02")
	employee := int64(24)
	repo := &admissionPreparationRepo{iformOperationUserRepo{users: map[int64]*domain.User{1: {ID: 1, IsActive: true, OrganizationID: &company}, 24: {ID: 24, IsActive: true, IsFirstLogin: true, Role: domain.RoleUser, OrganizationID: &company, KeycloakID: &kcID, Email: "hire@example.invalid", Username: "onb." + strings.ReplaceAll(request.String(), "-", "")}}, admission: &domain.EmploymentAdmission{OrganizationID: company, RequestID: request, ActorID: 1, EmployeeID: &employee, StartDate: &date, Timezone: "Asia/Almaty", Revision: 1}}}
	resets := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"admin-token","expires_in":300}`))
		case strings.HasSuffix(r.URL.Path, "/reset-password"):
			resets++
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/logout"):
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected provider request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer source.Close()
	svc := NewKeycloakService(repo, nil, nil, nil, nil, keycloak.NewAdmin(source.URL, "master", "admin", "admin"), "test")
	require.NoError(t, svc.InitializeIFormEmployeeCredential(context.Background(), 1, 24, company, request, "Prepared123!"))
	require.Equal(t, 1, resets)
	require.Error(t, svc.checkLoginAccess(context.Background(), 24), "preparation opened early access")
	// No password client is configured: admission must reject before calling it.
	pair, err := svc.Login(context.Background(), &LoginRequest{Email: "hire@example.invalid", Password: "Prepared123!"})
	require.Error(t, err)
	require.Nil(t, pair)
	repo.admission.Cancelled = true
	require.Error(t, svc.InitializeIFormEmployeeCredential(context.Background(), 1, 24, company, request, "Prepared456!"))
	require.Equal(t, 1, resets)
}
