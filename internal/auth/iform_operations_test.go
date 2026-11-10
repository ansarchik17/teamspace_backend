package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type iformOperationUserRepo struct {
	UserRepository
	users     map[int64]*domain.User
	existing  *domain.User
	admission *domain.EmploymentAdmission
}

type iformTokenStoreFailure struct{ TokenStorage }

func (iformTokenStoreFailure) StoreToken(context.Context, string, int64, time.Time) error {
	return errors.New("token store unavailable")
}

func TestIFormCredentialSetupNeverResetsUsedForeignOrUnrelatedProfiles(t *testing.T) {
	company, other, requestID := uuid.New(), uuid.New(), uuid.New()
	target := &domain.User{ID: 24, Role: domain.RoleUser, IsActive: true, OrganizationID: &company, Username: "onb." + strings.ReplaceAll(requestID.String(), "-", "")}
	repo := &iformOperationUserRepo{users: map[int64]*domain.User{1: {ID: 1, IsActive: true, OrganizationID: &company}, 24: target}}
	svc := &Service{userRepo: repo} // No Keycloak client: an already used profile must never reset credentials.
	if err := svc.InitializeIFormEmployeeCredential(context.Background(), 1, 24, company, requestID, "ignored"); err != nil {
		t.Fatal(err)
	}
	if isIFormFirstLogin(target) {
		t.Fatal("used profile still has first-login restriction")
	}
	target.IsFirstLogin = true
	if !isIFormFirstLogin(target) {
		t.Fatal("native new hire can skip changing temporary credential")
	}
	for _, change := range []func(){func() { target.OrganizationID = &other }, func() { target.OrganizationID = &company; target.Username = "unrelated" }, func() {
		target.Username = "onb." + strings.ReplaceAll(requestID.String(), "-", "")
		target.IsActive = false
	}} {
		change()
		if err := svc.InitializeIFormEmployeeCredential(context.Background(), 1, 24, company, requestID, "ignored"); err == nil {
			t.Fatal("unrelated/blocked profile credential setup accepted")
		}
	}
}

func (r *iformOperationUserRepo) GetByID(_ context.Context, id int64) (*domain.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return u, nil
}
func (r *iformOperationUserRepo) GetByUsername(context.Context, string) (*domain.User, error) {
	return r.existing, nil
}

func (r *iformOperationUserRepo) GetEmploymentAdmission(_ context.Context, company, request uuid.UUID) (*domain.EmploymentAdmission, error) {
	if r.admission != nil {
		return r.admission, nil
	}
	id := int64(24)
	date := "2026-12-01"
	return &domain.EmploymentAdmission{OrganizationID: company, RequestID: request, ActorID: 1, EmployeeID: &id, StartDate: &date, Timezone: "Asia/Almaty", Revision: 1}, nil
}

func TestIFormODOTokenKeepsActorAndCannotOutliveOneMinute(t *testing.T) {
	company := uuid.New()
	repo := &iformOperationUserRepo{users: map[int64]*domain.User{9: {ID: 9, Username: "hr", IsActive: true, OrganizationID: &company}}}
	storage := newLinkedDeviceTokenStorageStub()
	tm := NewTokenManager(&config.JWTConfig{Secret: "synthetic-token-key", Issuer: "workspace", Audience: "odo"}, storage)
	svc := &Service{userRepo: repo, tokenManager: tm}
	raw, err := svc.IFormODOToken(context.Background(), 9, company)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return tm.secret, nil })
	if err != nil {
		t.Fatal(err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sub"] != "9" || claims["token_type"] != "access" || claims["workspace_iform_operations"] != true || claims["role"] != nil || claims["iin"] != nil {
		t.Fatalf("incorrect token scope: %#v", claims)
	}
	expiry, _ := claims.GetExpirationTime()
	if left := time.Until(expiry.Time); left > time.Minute || left < 50*time.Second {
		t.Fatalf("token lifetime %s", left)
	}
	validated, err := tm.ValidateToken(context.Background(), raw)
	if err != nil || validated.UserID != 9 || validated.TokenID == "" || claims["jti"] != validated.TokenID {
		t.Fatalf("ODO cannot use token for current Workspace permission lookup: %v", err)
	}
	if err := tm.RevokeAllUserTokens(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.ValidateToken(context.Background(), raw); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("revoked automation token still accepted: %v", err)
	}
	tm.tokenStorage = iformTokenStoreFailure{storage}
	if raw, err := svc.IFormODOToken(context.Background(), 9, company); err == nil || raw != "" {
		t.Fatal("unregistered automation token returned after storage failure")
	}
	if _, err = svc.IFormODOToken(context.Background(), 9, uuid.New()); err == nil {
		t.Fatal("foreign company token issued")
	}
	repo.users[9].IsActive = false
	if _, err = svc.IFormODOToken(context.Background(), 9, company); err == nil {
		t.Fatal("blocked actor token issued")
	}
}

func TestIFormProvisioningReusesOnlyMatchingLocalProfileWithoutResettingCredentials(t *testing.T) {
	company, other, team, requestID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	manager := int64(2)
	position := "Employee"
	iin := "000000000000"
	repo := &iformOperationUserRepo{users: map[int64]*domain.User{1: {ID: 1, Role: domain.RoleUser, IsActive: true, OrganizationID: &company}, 2: {ID: 2, IsActive: true, OrganizationID: &company}}, existing: &domain.User{ID: 24, Username: "onb." + strings.ReplaceAll(requestID.String(), "-", ""), IsActive: true, Role: domain.RoleUser, OrganizationID: &company, TeamID: &team, ManagerID: &manager, IIN: &iin, Position: &position, Email: "hire@example.invalid", FirstName: "Synthetic", LastName: "Hire"}}
	svc := &Service{userRepo: repo} // No Keycloak client: a replay must never contact SSO.
	req := func() *CreateUserRequest {
		start := "2026-12-01"
		return &CreateUserRequest{StartDate: &start, Email: "hire@example.invalid", FirstName: "Synthetic", LastName: "Hire", IIN: iin, Position: position, TeamID: &team, ManagerID: &manager, Role: "admin"}
	}
	input := req()
	got, err := svc.ProvisionIFormEmployee(context.Background(), 1, company, requestID, input)
	if err != nil || got.ID != 24 {
		t.Fatalf("replay failed: %v", err)
	}
	if input.Role != "user" {
		t.Fatal("platform role was not constrained")
	}
	repo.existing.IsActive = false
	if _, err = svc.ProvisionIFormEmployee(context.Background(), 1, company, requestID, req()); err == nil {
		t.Fatal("blocked employee was reused")
	}
	repo.existing.IsActive = true
	repo.existing.OrganizationID = &other
	if _, err = svc.ProvisionIFormEmployee(context.Background(), 1, company, requestID, req()); err == nil {
		t.Fatal("foreign profile claimed")
	}
	repo.existing.OrganizationID = &company
	repo.users[2].OrganizationID = &other
	if _, err = svc.ProvisionIFormEmployee(context.Background(), 1, company, requestID, req()); err == nil {
		t.Fatal("foreign manager accepted")
	}
}

func TestIFormSDHandoffChecksCurrentEmployeeAndRequest(t *testing.T) {
	company, team, requestID := uuid.New(), uuid.New(), uuid.New()
	manager, position := int64(2), "Engineer"
	target := domain.User{ID: 24, IsActive: true, Role: domain.RoleUser, OrganizationID: &company, TeamID: &team, ManagerID: &manager, Position: &position, Username: "onb." + strings.ReplaceAll(requestID.String(), "-", "")}
	repo := &iformOperationUserRepo{users: map[int64]*domain.User{1: {ID: 1, IsActive: true, OrganizationID: &company}, 2: {ID: 2, IsActive: true, OrganizationID: &company}, 24: &target}}
	svc := &Service{userRepo: repo}
	if err := svc.ValidateIFormOnboardingEmployee(context.Background(), 1, 24, company, requestID); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*domain.User){
		func(u *domain.User) { u.IsActive = false },
		func(u *domain.User) { other := uuid.New(); u.OrganizationID = &other },
		func(u *domain.User) { u.Username = "unrelated" },
		func(u *domain.User) { u.Role = domain.RoleSuperAdmin },
		func(u *domain.User) { u.TeamID = nil },
		func(u *domain.User) { u.ManagerID = nil },
		func(u *domain.User) { missing := int64(999); u.ManagerID = &missing },
		func(u *domain.User) { u.Position = nil },
	} {
		changed := target
		mutate(&changed)
		repo.users[24] = &changed
		if err := svc.ValidateIFormOnboardingEmployee(context.Background(), 1, 24, company, requestID); err == nil {
			t.Fatal("invalid employee reached SD handoff")
		}
	}
	repo.users[24] = &target
	repo.users[2].IsActive = false
	if err := svc.ValidateIFormOnboardingEmployee(context.Background(), 1, 24, company, requestID); err == nil {
		t.Fatal("blocked manager accepted for SD handoff")
	}
	repo.users[2].IsActive = true
	repo.users[1].IsActive = false
	if err := svc.ValidateIFormOnboardingEmployee(context.Background(), 1, 24, company, requestID); err == nil {
		t.Fatal("revoked actor handed employee to SD")
	}
}
