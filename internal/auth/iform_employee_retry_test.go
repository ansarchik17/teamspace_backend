package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type iformProvisionRetryRepo struct {
	iformOperationUserRepo
	creates int
}

func (r *iformProvisionRetryRepo) CheckNewUserIdentity(context.Context, string, string, string) error {
	return nil
}
func (r *iformProvisionRetryRepo) CreateNewUser(_ context.Context, user *domain.User) error {
	r.creates++
	user.EmploymentAdmission.EmployeeID = &user.ID
	user.ID = 24
	r.existing = user
	r.users[24] = user
	return nil
}

func TestIFormProvisioningReusesCommittedEmployeeAfterLostOperationReceipt(t *testing.T) {
	company, team, requestID := uuid.New(), uuid.New(), uuid.New()
	manager := int64(2)
	repo := &iformProvisionRetryRepo{iformOperationUserRepo: iformOperationUserRepo{users: map[int64]*domain.User{1: {ID: 1, IsActive: true, OrganizationID: &company}, 2: {ID: 2, IsActive: true, OrganizationID: &company}}}}
	creates, passwords := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/realms/master/protocol/openid-connect/token":
			_, _ = w.Write([]byte(`{"access_token":"test-admin","expires_in":300}`))
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test/users":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test/users":
			creates++
			w.Header().Set("Location", "/admin/realms/test/users/test-new-hire")
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/reset-password"):
			passwords++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test/roles/user":
			_, _ = w.Write([]byte(`{"id":"role-user","name":"user"}`))
		case strings.HasSuffix(r.URL.Path, "/role-mappings/realm"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected SSO mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc := NewKeycloakService(repo, nil, nil, nil, keycloak.NewAdmin(server.URL, "master", "admin", "admin"), "test")
	start := "2026-12-01"
	for i := 0; i < 3; i++ {
		// Simulate a lost Workspace receipt/response: the next call must recover
		// the committed local employee without creating SSO or resetting access.
		employee, err := svc.ProvisionIFormEmployee(context.Background(), 1, company, requestID, &CreateUserRequest{StartDate: &start, Email: "hire@example.invalid", FirstName: "Synthetic", LastName: "Hire", IIN: "000000000000", Position: "Engineer", TeamID: &team, ManagerID: &manager})
		if err != nil || employee == nil || employee.ID != 24 {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if creates != 1 || passwords != 1 || repo.creates != 1 {
		t.Fatalf("duplicate effects: SSO=%d passwords=%d local=%d", creates, passwords, repo.creates)
	}
	if err := svc.ValidateIFormOnboardingEmployee(context.Background(), 1, 24, company, requestID); err != nil {
		t.Fatal(err)
	}
	u := repo.existing
	if u.OrganizationID == nil || *u.OrganizationID != company || u.TeamID == nil || *u.TeamID != team || u.ManagerID == nil || *u.ManagerID != manager || stringValue(u.Position) != "Engineer" {
		t.Fatal("employee lost its company or organizational assignment")
	}
}
