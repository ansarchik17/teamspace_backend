package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// This token stays between Workspace and ODO. It retains the human actor's
// current identity and grants no administrator role or refresh credentials.
func (s *Service) IFormODOToken(ctx context.Context, actor int64, company uuid.UUID) (string, error) {
	u, err := s.userRepo.GetByID(ctx, actor)
	if err != nil || u == nil || !u.IsActive || u.OrganizationID == nil || *u.OrganizationID != company {
		return "", apperrors.Forbidden("Доступ сотрудника отозван")
	}
	if s.tokenManager == nil {
		return "", apperrors.Internal("ODO token issuer unavailable")
	}
	tm := s.tokenManager
	now := time.Now()
	expiresAt := now.Add(time.Minute)
	tokenID := uuid.NewString()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": tm.cfg.Issuer, "aud": tm.cfg.Audience, "sub": strconv.FormatInt(actor, 10), "user_id": actor, "username": u.Username, "first_name": u.FirstName, "last_name": u.LastName, "email": u.Email, "token_type": "access", "workspace_iform_operations": true,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": expiresAt.Unix(), "jti": tokenID, "token_id": tokenID,
	}).SignedString(tm.secret)
	if err != nil {
		return "", err
	}
	// ODO resolves current company permissions through Workspace using this
	// token, so it must participate in the normal validation/revocation ledger.
	if tm.tokenStorage != nil {
		if err := tm.tokenStorage.StoreToken(ctx, tokenID, actor, expiresAt); err != nil {
			return "", fmt.Errorf("store iForm ODO token: %w", err)
		}
	}
	return token, nil
}

// An opaque generated credential is never returned to iForm or stored in its
// form data. HR uses existing password setup before handing access to the hire.
func (s *Service) ProvisionIFormEmployee(ctx context.Context, actor int64, company uuid.UUID, requestID uuid.UUID, req *CreateUserRequest) (*domain.UserResponse, error) {
	if req == nil || company == uuid.Nil || requestID == uuid.Nil {
		return nil, apperrors.Validation("Некорректные параметры приёма")
	}
	req.OrganizationID = &company
	if req.StartDate == nil || !domain.ValidEmploymentDate(*req.StartDate) {
		return nil, apperrors.New("EMPLOYMENT_DATE_INVALID", "employment start date is required", 400)
	}
	req.employmentAdmission = &domain.EmploymentAdmission{OrganizationID: company, RequestID: requestID, ActorID: actor, StartDate: req.StartDate}
	req.Role = string(domain.RoleUser)
	req.Username = "onb." + strings.ReplaceAll(requestID.String(), "-", "")
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	req.Password = "Aa1!" + base64.RawURLEncoding.EncodeToString(secret)
	validate := validator.New()
	validate.SetTagName("binding")
	if validate.Struct(req) != nil || len(req.Position) > 255 || req.ManagerID == nil || *req.ManagerID <= 0 {
		return nil, apperrors.Validation("Проверьте ФИО, email, ИИН, должность и руководителя сотрудника")
	}
	for _, ch := range req.IIN {
		if ch < '0' || ch > '9' {
			return nil, apperrors.Validation("ИИН должен содержать 12 цифр")
		}
	}
	if err := s.ValidateScopedUserCreate(ctx, actor, req); err != nil {
		return nil, err
	}
	if req.ManagerID != nil {
		manager, err := s.userRepo.GetByID(ctx, *req.ManagerID)
		if err != nil || manager == nil || !manager.IsActive || manager.OrganizationID == nil || *manager.OrganizationID != company {
			return nil, apperrors.Validation("Руководитель не найден в компании")
		}
	}
	if existing, err := s.userRepo.GetByUsername(ctx, req.Username); err == nil && existing != nil {
		if !existing.IsActive || existing.Role != domain.RoleUser || existing.Username != req.Username || existing.OrganizationID == nil || *existing.OrganizationID != company || existing.Email != req.Email || existing.IIN == nil || *existing.IIN != req.IIN || existing.FirstName != req.FirstName || existing.LastName != req.LastName || stringValue(existing.Position) != req.Position || !sameIFormUUID(existing.TeamID, req.TeamID) || !sameIFormManager(existing.ManagerID, req.ManagerID) {
			return nil, apperrors.Conflict("Профиль с таким ключом приёма имеет другие параметры")
		}
		reader, ok := s.userRepo.(employmentAdmissionReader)
		if !ok {
			return nil, apperrors.Internal("Employment admission storage unavailable")
		}
		admission, err := reader.GetEmploymentAdmission(ctx, company, requestID)
		if err != nil {
			return nil, err
		}
		if admission.EmployeeID == nil || *admission.EmployeeID != existing.ID || admission.ActorID != actor || admission.Cancelled {
			return nil, apperrors.New("EMPLOYMENT_ACCESS_DENIED", "admission cancelled or unrelated", 409)
		}
		existing.EmploymentAdmission = admission
		return existing.ToResponse(), nil
	}
	// A previously existing SSO account must not be claimed by an onboarding run.
	if existing, err := s.findExistingKeycloakHRUser(ctx, req.Email, req.Username); err != nil {
		return nil, apperrors.InternalWithErr("Failed to check SSO identity", err)
	} else if existing != nil {
		return nil, apperrors.Conflict("Учётная запись SSO уже существует; требуется проверка HR")
	}
	return s.CreateUser(ctx, actor, req)
}

func sameIFormUUID(a, b *uuid.UUID) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// Check current state again before handing workplace preparation to SD. A
// receipt proves creation; it does not prove that the employee is still active.
func (s *Service) ValidateIFormOnboardingEmployee(ctx context.Context, actor, employee int64, company, requestID uuid.UUID) error {
	requester, target, err := s.EnsureUserInScope(ctx, actor, employee)
	if err != nil {
		return err
	}
	if !requester.IsActive || requester.OrganizationID == nil || *requester.OrganizationID != company || !target.IsActive || target.OrganizationID == nil || *target.OrganizationID != company || target.Role != domain.RoleUser || target.Username != "onb."+strings.ReplaceAll(requestID.String(), "-", "") || target.TeamID == nil || *target.TeamID == uuid.Nil || target.ManagerID == nil || *target.ManagerID <= 0 || strings.TrimSpace(stringValue(target.Position)) == "" {
		return apperrors.New("IFORM_ONBOARDING_EMPLOYEE_INVALID", "employee is inactive or unrelated to this request", 409)
	}
	if err := s.validateUserOrganizationAssignment(ctx, &CreateUserRequest{OrganizationID: &company, TeamID: target.TeamID}); err != nil {
		return apperrors.New("IFORM_ONBOARDING_EMPLOYEE_INVALID", "employee subdivision is no longer available", 409)
	}
	manager, err := s.userRepo.GetByID(ctx, *target.ManagerID)
	if err != nil || manager == nil || !manager.IsActive || manager.OrganizationID == nil || *manager.OrganizationID != company {
		return apperrors.New("IFORM_ONBOARDING_EMPLOYEE_INVALID", "employee manager is no longer available", 409)
	}
	reader, ok := s.userRepo.(employmentAdmissionReader)
	if !ok {
		return apperrors.Internal("Employment admission storage unavailable")
	}
	admission, err := reader.GetEmploymentAdmission(ctx, company, requestID)
	if err != nil {
		return err
	}
	if admission.Cancelled || admission.EmployeeID == nil || *admission.EmployeeID != employee || admission.ActorID != actor {
		return apperrors.New("EMPLOYMENT_ACCESS_DENIED", "admission cancelled or unrelated", 409)
	}
	return nil
}

func (s *Service) InitializeIFormEmployeeCredential(ctx context.Context, actor, employee int64, company, requestID uuid.UUID, password string) error {
	requester, target, err := s.EnsureUserInScope(ctx, actor, employee)
	if err != nil {
		return err
	}
	if !requester.IsActive || requester.OrganizationID == nil || *requester.OrganizationID != company || !target.IsActive || target.OrganizationID == nil || *target.OrganizationID != company || target.Role != domain.RoleUser || target.Username != "onb."+strings.ReplaceAll(requestID.String(), "-", "") {
		return apperrors.Forbidden("Профиль не относится к этому приёму")
	}
	// Successful first sign-in proves that access is ready. Never reset a used account.
	if !target.IsFirstLogin {
		return nil
	}
	reader, ok := s.userRepo.(employmentAdmissionReader)
	if !ok {
		return apperrors.Internal("Employment admission storage unavailable")
	}
	admission, err := reader.GetEmploymentAdmission(ctx, company, requestID)
	if err != nil {
		return err
	}
	if admission.Cancelled || admission.EmployeeID == nil || *admission.EmployeeID != employee || admission.ActorID != actor {
		return apperrors.New("EMPLOYMENT_ACCESS_DENIED", "admission cancelled or unrelated", 409)
	}
	return s.ResetUserPasswordAdmin(ctx, actor, employee, password, true)
}

func isIFormFirstLogin(user *domain.User) bool {
	if user == nil || !user.IsFirstLogin || !strings.HasPrefix(user.Username, "onb.") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(user.Username, "onb."))
	return err == nil
}
func sameIFormManager(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
