package auth

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type EgovLoginResult struct {
	User      *domain.User
	Profile   *domain.EgovProfile
	IsNewUser bool
}

func (s *Service) LoginOrRegisterFromEgov(ctx context.Context, basic *domain.IDPUserBasicResponse, phone, email string) (*EgovLoginResult, error) {
	if s == nil {
		return nil, apperrors.Internal("egov auth service is not configured")
	}
	profile, err := normalizeEgovProfile(basic, phone, email)
	if err != nil {
		return nil, err
	}
	if profile.UserType == string(domain.IDPUserTypeLegal) {
		return nil, apperrors.New("EGOV_LEGAL_NOT_SUPPORTED", "egov legal profiles are not supported", 400).
			WithUserMessage("Вход для юридических лиц пока не поддерживается.")
	}

	var (
		user      *domain.User
		isNewUser bool
	)

	user, err = s.userRepo.GetByIIN(ctx, profile.IIN)
	if err == nil && user != nil {
		if err := s.syncUserFromEgovProfile(ctx, user, profile); err != nil {
			return nil, err
		}
		return s.finalizeEgovLogin(ctx, user, profile, false)
	}

	if profile.Email != "" {
		existingByEmail, emailErr := s.userRepo.GetByEmail(ctx, profile.Email)
		if emailErr == nil && existingByEmail != nil {
			if existingByEmail.IIN != nil && strings.TrimSpace(*existingByEmail.IIN) != "" && strings.TrimSpace(*existingByEmail.IIN) != profile.IIN {
				return nil, apperrors.New("EGOV_USER_CONFLICT", "existing user email belongs to another IIN", 409).
					WithUserMessage("Этот email уже привязан к другому пользователю.")
			}
			user = existingByEmail
			if err := s.syncUserFromEgovProfile(ctx, user, profile); err != nil {
				return nil, err
			}
			return s.finalizeEgovLogin(ctx, user, profile, false)
		}
	}

	if s.egovProfileRepo != nil && profile.Phone != "" {
		existingEgovProfile, profileErr := s.egovProfileRepo.GetByPhone(ctx, profile.Phone)
		if profileErr == nil && existingEgovProfile != nil && existingEgovProfile.IIN != profile.IIN {
			conflictingUser, userErr := s.userRepo.GetByID(ctx, existingEgovProfile.UserID)
			if userErr == nil && conflictingUser != nil {
				return nil, apperrors.New("EGOV_USER_CONFLICT", "existing user phone belongs to another IIN", 409).
					WithDetail("user_id", conflictingUser.ID).
					WithUserMessage("Этот телефон уже привязан к другому пользователю.")
			}
			return nil, apperrors.New("EGOV_USER_CONFLICT", "existing user phone belongs to another IIN", 409).
				WithUserMessage("Этот телефон уже привязан к другому пользователю.")
		}
	}

	creator, ok := s.userRepo.(interface {
		Create(ctx context.Context, user *domain.User) error
	})
	if !ok {
		return nil, apperrors.Internal("local user repository does not support egov user creation")
	}

	user = buildLocalUserFromEgovProfile(profile)
	if err := creator.Create(ctx, user); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate key") || strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return nil, apperrors.New("EGOV_USER_CONFLICT", "user already exists with provided egov identifiers", 409).
				WithUserMessage("Пользователь с такими данными уже существует.")
		}
		return nil, apperrors.InternalWithErr("failed to create local user from egov profile", err)
	}
	isNewUser = true

	return s.finalizeEgovLogin(ctx, user, profile, isNewUser)
}

func (s *Service) finalizeEgovLogin(ctx context.Context, user *domain.User, profile *domain.EgovProfile, isNewUser bool) (*EgovLoginResult, error) {
	if s.egovProfileRepo != nil {
		lastLoginAt := time.Now()
		profile.UserID = user.ID
		profile.LastLoginAt = &lastLoginAt
		if err := s.egovProfileRepo.Upsert(ctx, profile); err != nil {
			return nil, apperrors.InternalWithErr("failed to persist egov profile", err)
		}
	}

	_ = s.userRepo.UpdateLastLogin(ctx, user.ID, time.Now())
	event := buildLoginEventFromUser(ctx, user.ID, user.Email, user.Username, strings.TrimSpace(user.FirstName+" "+user.LastName), "egov_idp")
	if strings.TrimSpace(event.Source) == "" {
		event.Source = "egov_idp"
	}
	if s.loginMonitor != nil {
		_ = s.loginMonitor.NotifyLogin(ctx, event)
	}

	return &EgovLoginResult{
		User:      user,
		Profile:   profile,
		IsNewUser: isNewUser,
	}, nil
}

func (s *Service) syncUserFromEgovProfile(ctx context.Context, user *domain.User, profile *domain.EgovProfile) error {
	if user == nil || profile == nil {
		return apperrors.Internal("egov profile sync requires user and profile")
	}

	updated := false
	if user.IIN == nil || strings.TrimSpace(*user.IIN) == "" {
		iin := profile.IIN
		user.IIN = &iin
		updated = true
	} else if strings.TrimSpace(*user.IIN) != profile.IIN {
		return apperrors.New("EGOV_USER_CONFLICT", "existing user belongs to another IIN", 409).
			WithUserMessage("Пользователь уже привязан к другому ИИН.")
	}

	if profile.Email != "" && strings.TrimSpace(user.Email) != profile.Email {
		user.Email = profile.Email
		updated = true
	}
	if strings.TrimSpace(user.FirstName) != profileFirstName(profile) {
		user.FirstName = profileFirstName(profile)
		updated = true
	}
	if strings.TrimSpace(user.LastName) != profileLastName(profile) {
		user.LastName = profileLastName(profile)
		updated = true
	}

	if updated {
		if err := s.userRepo.Update(ctx, user); err != nil {
			return apperrors.InternalWithErr("failed to update local user from egov profile", err)
		}
	}
	return nil
}

func buildLocalUserFromEgovProfile(profile *domain.EgovProfile) *domain.User {
	iin := profile.IIN
	username := profile.IIN
	email := profile.Email
	if email == "" {
		email = profile.IIN + "@egov.local"
	}

	return &domain.User{
		IIN:          &iin,
		Email:        email,
		Username:     username,
		FirstName:    profileFirstName(profile),
		LastName:     profileLastName(profile),
		IsActive:     true,
		IsFirstLogin: true,
		Role:         domain.RoleUser,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

func normalizeEgovProfile(basic *domain.IDPUserBasicResponse, phone, email string) (*domain.EgovProfile, error) {
	if basic == nil {
		return nil, apperrors.New("EGOV_PROFILE_FETCH_FAILED", "egov basic profile is required", 502)
	}
	if !basic.Level.Valid() {
		return nil, apperrors.New("EGOV_PROFILE_FETCH_FAILED", "egov auth level is invalid", 502)
	}
	if !basic.Type.Valid() {
		return nil, apperrors.New("EGOV_PROFILE_FETCH_FAILED", "egov user type is invalid", 502)
	}

	iin := normalizeDigits(basic.Person.IIN, 12)
	if iin == "" {
		return nil, apperrors.New("EGOV_IIN_MISSING", "egov person.iin is missing or invalid", 400).
			WithUserMessage("Не удалось определить ИИН пользователя eGov.")
	}

	var (
		bin    string
		nameRu string
		nameKk string
	)
	if basic.Type == domain.IDPUserTypeLegal {
		if basic.Organization != nil {
			bin = normalizeDigits(basic.Organization.BIN, 12)
			nameRu = normalizePersonName(basic.Organization.NameRu)
			nameKk = normalizePersonName(basic.Organization.NameKk)
		}
		if bin == "" {
			return nil, apperrors.New("EGOV_PROFILE_FETCH_FAILED", "legal egov response is missing organization.bin", 502)
		}
	}

	return &domain.EgovProfile{
		IIN:          iin,
		Phone:        normalizePhone(phone),
		Email:        strings.ToLower(strings.TrimSpace(email)),
		FirstName:    normalizePersonName(basic.Person.Name),
		LastName:     normalizePersonName(basic.Person.Surname),
		Patronymic:   normalizePersonName(basic.Person.Patronymic),
		AuthLevel:    string(basic.Level),
		UserType:     string(basic.Type),
		BIN:          bin,
		NameRu:       nameRu,
		NameKk:       nameKk,
		AuthProvider: domain.AuthProviderEgovIDP,
	}, nil
}

func profileFirstName(profile *domain.EgovProfile) string {
	if profile == nil {
		return ""
	}
	return normalizePersonName(profile.FirstName)
}

func profileLastName(profile *domain.EgovProfile) string {
	if profile == nil {
		return ""
	}
	return normalizePersonName(profile.LastName)
}

func normalizePersonName(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if normalized == "" {
		return ""
	}
	if !shouldNormalizeNameCase(normalized) {
		return normalized
	}
	return titleCasePersonName(normalized)
}

func shouldNormalizeNameCase(value string) bool {
	hasLetter := false
	hasUpper := false
	hasLower := false
	for _, r := range value {
		if !unicode.IsLetter(r) {
			continue
		}
		hasLetter = true
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
		if hasUpper && hasLower {
			return false
		}
	}
	return hasLetter && (hasUpper || hasLower)
}

func titleCasePersonName(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))

	makeUpper := true
	for _, r := range value {
		if unicode.IsLetter(r) {
			if makeUpper {
				r = unicode.ToUpper(r)
				makeUpper = false
			} else {
				r = unicode.ToLower(r)
			}
			builder.WriteRune(r)
			continue
		}
		builder.WriteRune(r)
		makeUpper = isPersonNameSeparator(r)
	}

	return strings.TrimSpace(builder.String())
}

func isPersonNameSeparator(r rune) bool {
	switch r {
	case ' ', '-', '\'', '’':
		return true
	default:
		return false
	}
}

func normalizePhone(value string) string {
	value = strings.TrimSpace(value)
	var digits []rune
	for _, r := range value {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
		}
	}
	if len(digits) == 0 {
		return ""
	}
	return string(digits)
}

func normalizeDigits(value string, expectedLen int) string {
	var digits []rune
	for _, r := range value {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
		}
	}
	if expectedLen > 0 && len(digits) != expectedLen {
		return ""
	}
	return string(digits)
}
