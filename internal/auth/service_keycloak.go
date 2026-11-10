package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	appservice "github.com/west6ide/workspace-backend/internal/service"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/singleflight"
)

type ChatService interface {
	CreateOrGetConversation(ctx context.Context, user1ID, user2ID int64) (*domain.Conversation, error)
	BroadcastUserStatus(ctx context.Context, userID int64, status string)
}

type ReviewRepository interface {
	GetMonthlyStats(ctx context.Context, userID int64) (*domain.UserReviewStats, error)
	HasReviewedThisMonth(ctx context.Context, reviewerID, targetID int64) (bool, error)
}

type visibleUserSearcher interface {
	SearchVisibleTo(ctx context.Context, requesterID int64, q string, limit, offset int) ([]*domain.User, error)
}

type directoryUserSearcher interface {
	GetPaginatedVisibleTo(ctx context.Context, requesterID int64, q string, limit, offset int, filters ...domain.UserFilters) ([]*domain.User, int, error)
}

type UserRepository interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	GetByIIN(ctx context.Context, iin string) (*domain.User, error)
	GetByKeycloakID(ctx context.Context, kcID string) (*domain.User, error)
	UpsertFromKeycloak(ctx context.Context, u *domain.User) error
	HasRegisteredMobileDevice(ctx context.Context, userID int64) (bool, error)
	UpdateLastLogin(ctx context.Context, userID int64, loginAt time.Time) error
	CompleteOnboarding(ctx context.Context, userID int64) error
	SetIsFirstLogin(ctx context.Context, userID int64, isFirst bool) error
	Update(ctx context.Context, user *domain.User) error
	GetAll(ctx context.Context) ([]*domain.User, error)
	GetPaginated(ctx context.Context, q string, limit, offset int, filters ...domain.UserFilters) ([]*domain.User, int, error)
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	GetFullDetailsByID(ctx context.Context, id int64) (*domain.UserDetailResponse, error)
	UpdateStatus(ctx context.Context, userID int64, status string) error
	GetByKeycloakIDs(ctx context.Context, kcIDs []string) ([]*domain.User, error)
	GetNeverLoggedIn(ctx context.Context) ([]*domain.User, error)
	ShareConversation(ctx context.Context, user1ID, user2ID int64) (bool, error)
	ExportMinistryUsers(ctx context.Context, ministryID int64, excludeStateBodyIDs []int64, excludeDepartmentIDs []int64, excludeDivisionIDs []int64) ([]domain.MinistryUserExportRow, error)
	GetMinistryName(ctx context.Context, ministryID int64) (string, error)
	SoftDelete(ctx context.Context, id int64) error
	UpdateRole(ctx context.Context, id int64, role domain.Role) error
	UpdateMinistryID(ctx context.Context, id int64, ministryID int64) error
}

// organizationTeamValidator is implemented by the SQL-backed user repository.
// It is kept optional so authentication service test doubles and non-tenant
// user repositories do not need to know about corporate structure tables.
type organizationTeamValidator interface {
	ValidateOrganizationTeam(ctx context.Context, organizationID, teamID uuid.UUID) error
}

type HRProfileRepository interface {
	GetByPersonCode(ctx context.Context, personCode string) (*domain.UserHRProfile, error)
	GetByIIN(ctx context.Context, iin string) (*domain.UserHRProfile, error)
	GetByUserID(ctx context.Context, userID int64) (*domain.UserHRProfile, error)
	List(ctx context.Context, filter repository.HRProfileFilter, limit, offset int) ([]*domain.UserHRProfileSummaryResponse, int, error)
	GetUniqueValues(ctx context.Context, field string, filter repository.HRProfileFilter) ([]string, error)
	GetAllFilterValues(ctx context.Context, filter repository.HRProfileFilter) (map[string][]string, error)
	Upsert(ctx context.Context, profile *domain.UserHRProfile) error
	GetManagementVacations(ctx context.Context) ([]*domain.ManagementVacationResponse, error)
}

type HRPersonProvider interface {
	GetPersonByIIN(ctx context.Context, iin string) (*domain.HRPerson, json.RawMessage, error)
}

type EgovProfileRepository interface {
	GetByUserID(ctx context.Context, userID int64) (*domain.EgovProfile, error)
	GetByPhone(ctx context.Context, phone string) (*domain.EgovProfile, error)
	Upsert(ctx context.Context, profile *domain.EgovProfile) error
}

type PublicationRepository interface {
	Create(ctx context.Context, pub *domain.Publication, attachments []domain.PublicationAttachment) error
	FindByAuthorTitleContent(ctx context.Context, authorID int64, title, content string) (*domain.Publication, error)
}

type Service struct {
	recovery        *RecoveryService
	deleteUser      func(context.Context, int64, int64) error
	userRepo        UserRepository
	mailboxRepo     MailboxRepository
	reviewRepo      ReviewRepository
	hrProfileRepo   HRProfileRepository
	publicationRepo PublicationRepository
	egovProfileRepo EgovProfileRepository
	tokenManager    *TokenManager

	hrPersonProvider                         HRPersonProvider
	cache                                    *appservice.AICache
	loginLogRepo                             *repository.LoginLogRepository
	chatService                              ChatService
	keyBackupService                         KeyBackupService
	kc                                       *keycloak.Client
	kcClients                                map[string]*keycloak.Client
	kcAdmin                                  *keycloak.AdminClient
	realm                                    string
	mobileClientID                           string
	requireQRForWebLoginWhenMobileRegistered bool
	loginMonitor                             LoginMonitor
	mailboxProvisioner                       MailboxProvisioner
	hrFilterLoadGroup                        singleflight.Group
}

type ServiceOption func(*Service)

const passwordRollbackTimeout = 5 * time.Second

type KeyBackupService interface {
	SaveKeyBackup(ctx context.Context, userID int64, req *appservice.SaveKeyBackupRequest) error
}

func WithRequireQRForWebLoginWhenMobileRegistered(require bool) ServiceOption {
	return func(s *Service) {
		s.requireQRForWebLoginWhenMobileRegistered = require
	}
}

func WithTokenManager(tokenManager *TokenManager) ServiceOption {
	return func(s *Service) {
		s.tokenManager = tokenManager
	}
}

func WithKeyBackupService(keyBackupService KeyBackupService) ServiceOption {
	return func(s *Service) {
		s.keyBackupService = keyBackupService
	}
}

func WithLoginMonitor(loginMonitor LoginMonitor) ServiceOption {
	return func(s *Service) {
		s.loginMonitor = loginMonitor
	}
}

func WithMobileClientID(clientID string) ServiceOption {
	return func(s *Service) {
		s.mobileClientID = strings.TrimSpace(clientID)
	}
}

func WithKeycloakClients(clients map[string]*keycloak.Client) ServiceOption {
	return func(s *Service) {
		s.kcClients = clients
	}
}

func WithHRProfileRepository(hrProfileRepo HRProfileRepository) ServiceOption {
	return func(s *Service) {
		s.hrProfileRepo = hrProfileRepo
	}
}

func WithPublicationRepository(publicationRepo PublicationRepository) ServiceOption {
	return func(s *Service) {
		s.publicationRepo = publicationRepo
	}
}

func WithHRPersonProvider(hrPersonProvider HRPersonProvider) ServiceOption {
	return func(s *Service) {
		s.hrPersonProvider = hrPersonProvider
	}
}

func WithEgovProfileRepository(egovProfileRepo EgovProfileRepository) ServiceOption {
	return func(s *Service) {
		s.egovProfileRepo = egovProfileRepo
	}
}

func WithAICache(cache *appservice.AICache) ServiceOption {
	return func(s *Service) {
		s.cache = cache
	}
}

func WithMailboxRepository(mailboxRepo MailboxRepository) ServiceOption {
	return func(s *Service) {
		s.mailboxRepo = mailboxRepo
	}
}

func WithMailboxProvisioner(mailboxProvisioner MailboxProvisioner) ServiceOption {
	return func(s *Service) {
		s.mailboxProvisioner = mailboxProvisioner
	}
}

func NewKeycloakService(userRepo UserRepository,
	reviewRepo ReviewRepository,
	loginLogRepo *repository.LoginLogRepository,
	chatService ChatService,
	kc *keycloak.Client, kcAdmin *keycloak.AdminClient, realm string, opts ...ServiceOption) *Service {
	s := &Service{
		userRepo:                                 userRepo,
		reviewRepo:                               reviewRepo,
		loginLogRepo:                             loginLogRepo,
		chatService:                              chatService,
		kc:                                       kc,
		kcAdmin:                                  kcAdmin,
		realm:                                    realm,
		requireQRForWebLoginWhenMobileRegistered: envBool("REQUIRE_QR_FOR_WEB_LOGIN_WHEN_MOBILE_REGISTERED", false),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

type RegisterRequest struct {
	Email          string     `json:"email" binding:"required,email"`
	Username       string     `json:"username" binding:"required,min=3,max=50"`
	Password       string     `json:"password" binding:"required,min=8"`
	FirstName      string     `json:"first_name" binding:"required,min=1,max=50"`
	LastName       string     `json:"last_name" binding:"required,min=1,max=50"`
	IIN            string     `json:"iin" binding:"required,len=12"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	TeamID         *uuid.UUID `json:"team_id,omitempty"`
}

type LoginRequest struct {
	Email      string `json:"email"`
	Username   string `json:"username"`
	IIN        string `json:"iin"`
	Identifier string `json:"identifier"`
	Login      string `json:"login"`
	Password   string `json:"password" binding:"required"`
}

type RegisterByHRRequest struct {
	IIN       string           `json:"iin"`
	Email     string           `json:"email"`
	Username  string           `json:"username"`
	Password  string           `json:"password" binding:"required,min=8"`
	AutoLogin bool             `json:"auto_login"`
	Person    *domain.HRPerson `json:"person"`
}

type RegisterByHRResponse struct {
	User                *domain.UserResponse `json:"user"`
	AccountCreated      bool                 `json:"account_created"`
	HRProfileSynced     bool                 `json:"hr_profile_synced"`
	OrgResolved         bool                 `json:"org_resolved"`
	OrgResolutionSource string               `json:"org_resolution_source,omitempty"`
	Tokens              *TokenPair           `json:"tokens,omitempty"`
}

const (
	hrProfilesCacheTTL     = 60 * time.Second
	hrFilterValuesCacheTTL = 10 * time.Minute
)

type cachedHRProfilesPage struct {
	Profiles []*domain.UserHRProfileSummaryResponse `json:"profiles"`
	Total    int                                    `json:"total"`
}

func hrProfilesCacheKey(requesterID int64, filter repository.HRProfileFilter, limit, offset int) string {
	cursorUpdatedAt := ""
	if filter.CursorUpdatedAt != nil {
		cursorUpdatedAt = filter.CursorUpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	cursorID := int64(0)
	if filter.CursorID != nil {
		cursorID = *filter.CursorID
	}
	scopeMinistryID := int64(0)
	if filter.ScopeMinistryID != nil {
		scopeMinistryID = *filter.ScopeMinistryID
	}
	scopeUserID := int64(0)
	if filter.ScopeUserID != nil {
		scopeUserID = *filter.ScopeUserID
	}
	return fmt.Sprintf(
		"hr:profiles:v1:req:%d:q:%s:job:%s:sub:%s:go:%s:status:%s:iin:%s:scope_min:%d:scope_user:%d:cur_t:%s:cur_id:%d:limit:%d:offset:%d",
		requesterID,
		strings.ToLower(strings.TrimSpace(filter.Query)),
		strings.TrimSpace(filter.JobName),
		strings.TrimSpace(filter.SubName),
		strings.TrimSpace(filter.NameGO),
		strings.TrimSpace(filter.PersonStatus),
		strings.TrimSpace(filter.IIN),
		scopeMinistryID,
		scopeUserID,
		cursorUpdatedAt,
		cursorID,
		limit,
		offset,
	)
}

func hrFilterValuesCacheKey(requesterID int64, field string, filter repository.HRProfileFilter) string {
	scopeMinistryID := int64(0)
	if filter.ScopeMinistryID != nil {
		scopeMinistryID = *filter.ScopeMinistryID
	}
	scopeUserID := int64(0)
	if filter.ScopeUserID != nil {
		scopeUserID = *filter.ScopeUserID
	}
	return fmt.Sprintf(
		"hr:filters:v1:req:%d:field:%s:scope_min:%d:scope_user:%d",
		requesterID,
		field,
		scopeMinistryID,
		scopeUserID,
	)
}

var hrFilterFields = []string{"job_name", "sub_name", "name_go", "person_status", "iin"}

func isHRFilterField(field string) bool {
	for _, allowed := range hrFilterFields {
		if field == allowed {
			return true
		}
	}
	return false
}

func (s *Service) Register(ctx context.Context, req *RegisterRequest) (*TokenPair, error) {
	kcID, err := s.kcAdmin.CreateUser(ctx, s.realm, keycloak.CreateUserRequest{
		Username:      req.Username,
		Email:         req.Email,
		Enabled:       true,
		EmailVerified: true,
		FirstName:     req.FirstName,
		LastName:      req.LastName,
		Attributes: map[string][]string{
			"iin": {req.IIN},
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "409") {
			return nil, apperrors.Conflict("User with this email or username already exists in SSO")
		}
		return nil, apperrors.InternalWithErr("Failed to create user in Keycloak", err)
	}

	if err := s.kcAdmin.SetPassword(ctx, s.realm, kcID, req.Password, false); err != nil {
		if strings.Contains(err.Error(), "400") {
			return nil, apperrors.BadRequest("Password does not meet security requirements")
		}
		return nil, apperrors.InternalWithErr("Failed to set user password", err)
	}

	if err := s.kcAdmin.SetUserRealmRoles(ctx, s.realm, kcID, []string{"user"}); err != nil {
		return nil, apperrors.InternalWithErr("Failed to set default user role", err)
	}

	kcIDPtr := kcID
	iinPtr := req.IIN

	u := &domain.User{
		KeycloakID:     &kcIDPtr,
		IIN:            &iinPtr,
		Email:          req.Email,
		Username:       req.Username,
		FirstName:      req.FirstName,
		LastName:       req.LastName,
		IsActive:       true,
		IsFirstLogin:   true,
		Role:           domain.RoleUser,
		OrganizationID: req.OrganizationID,
		TeamID:         req.TeamID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.userRepo.UpsertFromKeycloak(ctx, u); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "duplicate key") || strings.Contains(errMsg, "violates unique constraint") {
			if strings.Contains(errMsg, "iin") {
				return nil, apperrors.Conflict(fmt.Sprintf("User with IIN %s already exists", req.IIN))
			}
			if strings.Contains(errMsg, "email") {
				return nil, apperrors.Conflict(fmt.Sprintf("User with email %s already exists", req.Email))
			}
			return nil, apperrors.Conflict("User already exists with provided unique fields")
		}

		return nil, apperrors.InternalWithErr("Failed to sync local user profile", err)
	}

	tok, err := s.kc.Password(ctx, req.Email, req.Password)
	if err != nil {
		return nil, apperrors.Unauthorized("Registration successful, but failed to auto-login. Please try logging in manually.")
	}

	return &TokenPair{
		AccessToken:        tok.AccessToken,
		RefreshToken:       tok.RefreshToken,
		IDToken:            tok.IDToken,
		TokenType:          tok.TokenType,
		ExpiresIn:          tok.ExpiresIn,
		ExpiresAt:          time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		MustChangePassword: u.IsFirstLogin,
	}, nil
}

func (s *Service) RegisterByHR(ctx context.Context, req *RegisterByHRRequest) (*RegisterByHRResponse, error) {
	if req == nil {
		return nil, apperrors.Validation("request is required")
	}
	if strings.TrimSpace(req.Password) == "" {
		return nil, apperrors.Validation("password is required")
	}

	person, rawPayload, err := s.resolveHRPerson(ctx, req)
	if err != nil {
		return nil, err
	}

	existingUser, err := s.findExistingUserForHR(ctx, person)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to lookup existing user", err)
	}

	if existingUser != nil {
		if err := s.checkNotOffboarded(ctx, existingUser.ID); err != nil {
			return nil, err
		}
	}
	email := resolveHREmail(req.Email, existingUser, person)
	username := resolveHRUsername(req.Username, existingUser, person)
	accountCreated := existingUser == nil
	passwordApplied := false
	var kcID string

	if existingUser != nil && existingUser.KeycloakID != nil && *existingUser.KeycloakID != "" {
		kcID = *existingUser.KeycloakID
		attrs := map[string][]string{
			"iin":         {person.IIN},
			"person_code": {person.PersonCode},
		}
		if strings.TrimSpace(person.JobName) != "" {
			attrs["position"] = []string{person.JobName}
		}
		if err := s.kcAdmin.UpdateUser(ctx, s.realm, kcID, keycloak.UserUpdateRequest{
			Username:   &username,
			FirstName:  refString(person.FirstName),
			LastName:   refString(person.LastName),
			Email:      refString(email),
			Enabled:    refBool(resolveHRIsActive(existingUser, person)),
			Attributes: attrs,
		}); err != nil {
			return nil, apperrors.InternalWithErr("Failed to update user in Keycloak", err)
		}
	} else {
		createAttrs := map[string][]string{
			"iin":         {person.IIN},
			"person_code": {person.PersonCode},
		}
		if strings.TrimSpace(person.JobName) != "" {
			createAttrs["position"] = []string{person.JobName}
		}
		kcID, err = s.kcAdmin.CreateUser(ctx, s.realm, keycloak.CreateUserRequest{
			Username:      username,
			Email:         email,
			Enabled:       resolveHRIsActive(existingUser, person),
			EmailVerified: true,
			FirstName:     person.FirstName,
			LastName:      person.LastName,
			Attributes:    createAttrs,
		})
		if err != nil {
			if strings.Contains(err.Error(), "409") {
				kcUser, resolveErr := s.findExistingKeycloakHRUser(ctx, email, username)
				if resolveErr != nil {
					return nil, apperrors.InternalWithErr("Failed to resolve existing user in Keycloak after conflict", resolveErr)
				}
				if kcUser == nil {
					return nil, apperrors.Conflict("User already exists in SSO")
				}
				kcID = kcUser.ID
				accountCreated = false
				conflictAttrs := map[string][]string{
					"iin":         {person.IIN},
					"person_code": {person.PersonCode},
				}
				if strings.TrimSpace(person.JobName) != "" {
					conflictAttrs["position"] = []string{person.JobName}
				}
				if err := s.kcAdmin.UpdateUser(ctx, s.realm, kcID, keycloak.UserUpdateRequest{
					Username:   &username,
					FirstName:  refString(person.FirstName),
					LastName:   refString(person.LastName),
					Email:      refString(email),
					Enabled:    refBool(resolveHRIsActive(existingUser, person)),
					Attributes: conflictAttrs,
				}); err != nil {
					return nil, apperrors.InternalWithErr("Failed to update existing conflicted user in Keycloak", err)
				}
			} else {
				return nil, apperrors.InternalWithErr("Failed to create user in Keycloak", err)
			}
		}
		if accountCreated {
			if err := s.kcAdmin.SetPassword(ctx, s.realm, kcID, req.Password, false); err != nil {
				if strings.Contains(err.Error(), "400") {
					return nil, apperrors.BadRequest("Password does not meet security requirements")
				}
				return nil, apperrors.InternalWithErr("Failed to set user password", err)
			}
			if err := s.kcAdmin.SetUserRealmRoles(ctx, s.realm, kcID, []string{"user"}); err != nil {
				return nil, apperrors.InternalWithErr("Failed to set default user role", err)
			}
			passwordApplied = true
		}
	}

	var assignment *domain.HierarchyAssignment
	var resolutionSource string

	assignment, err = s.normalizeHierarchyAssignment(ctx, assignment)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to normalize HR hierarchy assignment", err)
	}

	user := mergeHRUser(existingUser, person, email, username, kcID, assignment)
	if err := s.userRepo.UpsertFromKeycloak(ctx, user); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "duplicate key") || strings.Contains(errMsg, "violates unique constraint") {
			if strings.Contains(errMsg, "iin") {
				return nil, apperrors.Conflict(fmt.Sprintf("User with IIN %s already exists", person.IIN))
			}
			if strings.Contains(errMsg, "email") {
				return nil, apperrors.Conflict(fmt.Sprintf("User with email %s already exists", email))
			}
			return nil, apperrors.Conflict("User already exists with provided unique fields")
		}
		return nil, apperrors.InternalWithErr("Failed to sync local HR user profile", err)
	}

	hrProfileSynced := false
	if s.hrProfileRepo != nil {
		previousProfile, prevErr := s.hrProfileRepo.GetByUserID(ctx, user.ID)
		if prevErr != nil && !strings.Contains(strings.ToLower(prevErr.Error()), "not found") {
			return nil, apperrors.InternalWithErr("Failed to load previous HR snapshot", prevErr)
		}
		profile := buildUserHRProfile(user.ID, person, rawPayload)
		if err := s.hrProfileRepo.Upsert(ctx, profile); err != nil {
			return nil, apperrors.InternalWithErr("Failed to sync local HR snapshot", err)
		}
		if s.publicationRepo != nil {
			if err := s.publishManagementVacationNews(ctx, user, previousProfile, profile); err != nil {
				return nil, apperrors.InternalWithErr("Failed to publish management vacation news", err)
			}
		}
		hrProfileSynced = true
	}

	resp := &RegisterByHRResponse{
		User:                user.ToResponse(),
		AccountCreated:      accountCreated,
		HRProfileSynced:     hrProfileSynced,
		OrgResolved:         assignment != nil,
		OrgResolutionSource: resolutionSource,
	}

	if req.AutoLogin && passwordApplied {
		tok, err := s.kc.Password(ctx, email, req.Password)
		if err != nil {
			return nil, apperrors.Unauthorized("Registration succeeded, but auto-login failed")
		}
		resp.Tokens = &TokenPair{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			IDToken:      tok.IDToken,
			TokenType:    tok.TokenType,
			ExpiresIn:    tok.ExpiresIn,
			ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		}
	}

	return resp, nil
}

func (s *Service) Login(ctx context.Context, req *LoginRequest) (*TokenPair, error) {
	loginIdentifier, err := resolveLoginRequestIdentifier(req)
	if err != nil {
		return nil, err
	}

	u, keycloakLogin, err := s.resolvePasswordLoginUser(ctx, loginIdentifier)
	if err != nil {
		return nil, err
	}
	if u != nil {
		if err := s.checkLoginAccess(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	//if err == nil && u != nil {
	//	if u.KeycloakID != nil && *u.KeycloakID != "" {
	//		_ = s.kcAdmin.LogoutUser(ctx, s.realm, *u.KeycloakID)
	//	}
	//}

	tok, err := s.kc.Password(ctx, keycloakLogin, req.Password)
	if err != nil {
		if u != nil && (u.KeycloakID == nil || *u.KeycloakID == "") && u.PasswordHash != "" && s.tokenManager != nil {
			if bcryptErr := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); bcryptErr == nil {
				localTokens, genErr := s.tokenManager.GenerateTokenPair(ctx, u.ID, derefStringValue(u.IIN), u.Email, u.Username, u.FirstName, u.LastName, false, nil)
				if genErr == nil {
					tok = &keycloak.TokenResponse{
						AccessToken:  localTokens.AccessToken,
						RefreshToken: localTokens.RefreshToken,
						IDToken:      localTokens.IDToken,
						TokenType:    localTokens.TokenType,
						ExpiresIn:    localTokens.ExpiresIn,
					}
					err = nil
				}
			}
		}
	}
	if err != nil {
		return nil, apperrors.InvalidCredentials()
	}

	if u != nil && s.tokenManager != nil {
		if err := s.checkProviderCredentialState(ctx, tok.AccessToken, u.ID); err != nil {
			return nil, apperrors.ReauthRequired("Please sign in again")
		}
	}
	if u != nil && IsWebLoginClient(ctx) && s.requireQRForWebLoginWhenMobileRegistered {
		hasRegisteredMobileDevice, deviceErr := s.userRepo.HasRegisteredMobileDevice(ctx, u.ID)
		if deviceErr != nil {
			return nil, apperrors.InternalWithErr("failed to evaluate web login policy", deviceErr)
		}
		if hasRegisteredMobileDevice {
			if tok.RefreshToken != "" {
				_ = s.kc.Logout(ctx, tok.RefreshToken)
			}
			return nil, apperrors.QRLoginRequired()
		}
	}

	if u != nil {
		if err := s.checkLoginAccess(ctx, u.ID); err != nil {
			return nil, err
		}
		if s.loginMonitor != nil {
			meta := LoginRequestMetaFromContext(ctx)
			blocked, blockErr := s.loginMonitor.CheckBlocked(ctx, u.ID, meta.DeviceID)
			if blockErr == nil && blocked {
				go s.notifyLoginEvent(context.Background(), buildLoginEventFromUser(ctx, u.ID, u.Email, u.Username, strings.TrimSpace(u.FirstName+" "+u.LastName), "password"))
				if tok.RefreshToken != "" {
					_ = s.kc.Logout(ctx, tok.RefreshToken)
				}
				return nil, apperrors.Forbidden("device is blocked")
			}
		}

		if u.ID != domain.SupportUserID {
			_, _ = s.chatService.CreateOrGetConversation(ctx, u.ID, domain.SupportUserID)
		}

		_ = s.userRepo.UpdateLastLogin(ctx, u.ID, time.Now())

		// Log login event for analytics
		go func(uid int64, email string) {
			// IP and UserAgent are not available here in Service layer easily without context value extraction
			// For now, we log user_id and email. IP handling would require passing it from Handler.
			if s.loginLogRepo != nil {
				_ = s.loginLogRepo.Create(context.Background(), uid, email)
			}
		}(u.ID, u.Email)

		go s.notifyLoginEvent(context.Background(), buildLoginEventFromUser(ctx, u.ID, u.Email, u.Username, strings.TrimSpace(u.FirstName+" "+u.LastName), "password"))
	}

	cannotSkip := false
	mustChangePassword := false
	if u != nil && (IsWebLoginClient(ctx) || isIFormFirstLogin(u)) {
		if req.Password == "Qwerty12!" || isIFormFirstLogin(u) {
			u.IsFirstLogin = true
			_ = s.userRepo.SetIsFirstLogin(ctx, u.ID, true)
			cannotSkip = true
			mustChangePassword = true
		} else {
			if u.IsFirstLogin {
				u.IsFirstLogin = false
				_ = s.userRepo.CompleteOnboarding(ctx, u.ID)
			}
		}
	}

	return &TokenPair{
		AccessToken:              tok.AccessToken,
		RefreshToken:             tok.RefreshToken,
		IDToken:                  tok.IDToken,
		TokenType:                tok.TokenType,
		ExpiresIn:                tok.ExpiresIn,
		ExpiresAt:                time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		MustChangePassword:       mustChangePassword,
		CannotSkipPasswordChange: cannotSkip,
	}, nil
}

func (s *Service) notifyLoginEvent(ctx context.Context, event LoginEvent) {
	if s.loginMonitor == nil {
		return
	}
	if err := s.loginMonitor.NotifyLogin(ctx, event); err != nil {
		fmt.Printf("failed to notify login event: %v\n", err)
	}
}

func (s *Service) KeycloakAuthCodeURL(redirectURI, state string, loginHints ...string) string {
	return s.kc.AuthCodeURL(redirectURI, state, loginHints...)
}

func (s *Service) KeycloakAuthCodeURLForIdentityProvider(redirectURI, state, providerAlias string, loginHints ...string) string {
	return s.kc.AuthCodeURLWithIdentityProvider(redirectURI, state, providerAlias, loginHints...)
}

func (s *Service) KeycloakAuthCodeURLForClient(clientID, redirectURI, state string, loginHints ...string) (string, error) {
	client := s.keycloakClientByID(clientID)
	if client == nil {
		return "", apperrors.Internal("keycloak client is not configured")
	}
	return client.AuthCodeURL(redirectURI, state, loginHints...), nil
}

// KeycloakSilentAuthCodeURL builds an authorization URL with prompt=none for silent SSO.
// Keycloak will return a code without showing any UI if a valid session exists.
func (s *Service) KeycloakSilentAuthCodeURL(redirectURI, state string) string {
	return s.kc.AuthCodeURLSilent(redirectURI, state)
}

func (s *Service) KeycloakMobileAuthCodeURL(redirectURI, state, codeChallenge, codeChallengeMethod string) (string, error) {
	client := s.mobileKeycloakClient()
	if client == nil {
		return "", apperrors.Internal("mobile keycloak client is not configured")
	}
	return client.MobileAuthCodeURL(redirectURI, state, codeChallenge, codeChallengeMethod), nil
}

// KeycloakLogoutURL builds the Keycloak end-session URL for browser-redirect logout.
// The browser must navigate to this URL so Keycloak can clear KEYCLOAK_IDENTITY and KEYCLOAK_SESSION cookies.
func (s *Service) KeycloakLogoutURL(postLogoutRedirectURI string) string {
	return s.kc.LogoutURL(postLogoutRedirectURI)
}

func (s *Service) KeycloakLogoutURLForClient(clientID, postLogoutRedirectURI string) (string, error) {
	client := s.keycloakClientByID(clientID)
	if client == nil {
		return "", apperrors.Internal("keycloak client is not configured")
	}
	return client.LogoutURL(postLogoutRedirectURI), nil
}

func (s *Service) LoginWithKeycloakAuthCode(ctx context.Context, code, redirectURI string) (*TokenPair, error) {
	tok, err := s.kc.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		return nil, apperrors.Unauthorized(fmt.Sprintf("Failed to complete Keycloak login: %v", err))
	}

	return s.buildTokenPairFromKeycloakTokens(ctx, tok, true)
}

func (s *Service) LoginWithKeycloakAuthCodeForClient(ctx context.Context, clientID, code, redirectURI string) (*TokenPair, error) {
	client := s.keycloakClientByID(clientID)
	if client == nil {
		return nil, apperrors.Internal("keycloak client is not configured")
	}

	tok, err := client.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		return nil, apperrors.Unauthorized("Failed to complete Keycloak login")
	}

	return s.buildTokenPairFromKeycloakTokens(ctx, tok, true)
}

func (s *Service) keycloakClientByID(clientID string) *keycloak.Client {
	normalizedClientID := strings.TrimSpace(clientID)
	if normalizedClientID == "" {
		return s.kc
	}
	if client := s.kcClients[normalizedClientID]; client != nil {
		return client
	}
	return nil
}

func (s *Service) LoginWithMobileKeycloakAuthCode(ctx context.Context, code, redirectURI, codeVerifier string) (*TokenPair, error) {
	client := s.mobileKeycloakClient()
	if client == nil {
		return nil, apperrors.Internal("mobile keycloak client is not configured")
	}

	tok, err := client.ExchangeCodePKCE(ctx, code, redirectURI, codeVerifier)
	if err != nil {
		return nil, apperrors.Unauthorized("Failed to complete mobile Keycloak login")
	}

	return s.buildTokenPairFromKeycloakTokens(ctx, tok, false)
}

func (s *Service) buildTokenPairFromKeycloakTokens(ctx context.Context, tok *keycloak.TokenResponse, allowMustChangePassword bool) (*TokenPair, error) {
	if tok == nil {
		return nil, apperrors.Unauthorized("Failed to complete Keycloak login")
	}

	var mustChangePassword bool
	var cannotSkip bool
	var localTokens *TokenPair
	if tok.AccessToken != "" {
		if err := s.syncUserFromKeycloakAccessToken(ctx, tok.AccessToken); err != nil {
			return nil, apperrors.InternalWithErr("failed to sync local user from Keycloak", err)
		}
		if err := s.checkIssuedKeycloakAccess(ctx, tok.AccessToken); err != nil {
			return nil, err
		}

		claims := jwt.MapClaims{}
		if _, _, err := jwt.NewParser().ParseUnverified(tok.AccessToken, claims); err == nil {
			if kcID, ok := claims["sub"].(string); ok {
				if u, err := s.userRepo.GetByKeycloakID(ctx, kcID); err == nil && u != nil {
					if s.tokenManager != nil {
						if err := s.checkProviderCredentialState(ctx, tok.AccessToken, u.ID); err != nil {
							return nil, apperrors.ReauthRequired("Please sign in again")
						}
						if genTokens, genErr := s.tokenManager.GenerateTokenPair(providerAuthenticationContext(ctx, tok.AccessToken), u.ID, derefStringValue(u.IIN), u.Email, u.Username, u.FirstName, u.LastName, false, nil); genErr == nil {
							localTokens = genTokens
						} else {
							return nil, apperrors.ReauthRequired("Please sign in again")
						}
					}

					if s.loginMonitor != nil {
						meta := LoginRequestMetaFromContext(ctx)
						blocked, blockErr := s.loginMonitor.CheckBlocked(ctx, u.ID, meta.DeviceID)
						if blockErr == nil && blocked {
							go s.notifyLoginEvent(context.Background(), buildLoginEventFromUser(ctx, u.ID, u.Email, u.Username, strings.TrimSpace(u.FirstName+" "+u.LastName), "keycloak"))
							if tok.RefreshToken != "" && s.kc != nil {
								_ = s.kc.Logout(ctx, tok.RefreshToken)
							}
							return nil, apperrors.Forbidden("device is blocked")
						}
					}

					go s.notifyLoginEvent(context.Background(), buildLoginEventFromUser(ctx, u.ID, u.Email, u.Username, strings.TrimSpace(u.FirstName+" "+u.LastName), "keycloak"))

					mustChangePassword, cannotSkip = s.firstLoginPasswordRequirement(ctx, u, allowMustChangePassword)
				}
			}
		}
	}

	if localTokens != nil {
		localTokens.MustChangePassword = mustChangePassword
		localTokens.CannotSkipPasswordChange = cannotSkip
		return localTokens, nil
	}

	return &TokenPair{
		AccessToken:              tok.AccessToken,
		RefreshToken:             tok.RefreshToken,
		IDToken:                  tok.IDToken,
		TokenType:                tok.TokenType,
		ExpiresIn:                tok.ExpiresIn,
		ExpiresAt:                time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		MustChangePassword:       mustChangePassword,
		CannotSkipPasswordChange: cannotSkip,
	}, nil
}

func (s *Service) CurrentUserFromAccessToken(ctx context.Context, accessToken string) (*domain.UserResponse, error) {
	kcID := extractSubFromJWT(accessToken)
	if kcID == "" {
		return nil, apperrors.Unauthorized("invalid access token subject")
	}
	return s.GetCurrentUserByKeycloakID(ctx, kcID)
}

func (s *Service) SyncUserFromKeycloakAccessToken(ctx context.Context, accessToken string) error {
	return s.syncUserFromKeycloakAccessToken(ctx, accessToken)
}

func (s *Service) mobileKeycloakClient() *keycloak.Client {
	if s.mobileClientID != "" && s.kcClients != nil {
		if client := s.kcClients[s.mobileClientID]; client != nil {
			return client
		}
	}
	return nil
}

func (s *Service) syncUserFromKeycloakAccessToken(ctx context.Context, accessToken string) error {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(accessToken, claims); err != nil {
		return err
	}

	kcID, _ := claims["sub"].(string)
	if strings.TrimSpace(kcID) == "" {
		return fmt.Errorf("missing keycloak subject")
	}

	if s.tokenManager != nil {
		if existing, err := s.userRepo.GetByKeycloakID(ctx, kcID); err == nil && existing != nil {
			if err := s.tokenManager.CheckCredentialClaims(ctx, existing.ID, claims); err != nil {
				return err
			}
		}
	}
	email, _ := claims["email"].(string)
	username, _ := claims["preferred_username"].(string)
	firstName, _ := claims["given_name"].(string)
	lastName, _ := claims["family_name"].(string)
	iin, _ := claims["iin"].(string)
	role := mapKeycloakClaimsToDomainRole(claims)
	var organizationID *uuid.UUID
	if rawOrganizationID, ok := claims["organization_id"].(string); ok {
		if parsed, parseErr := uuid.Parse(strings.TrimSpace(rawOrganizationID)); parseErr == nil {
			organizationID = &parsed
		}
	}

	kcIDCopy := strings.TrimSpace(kcID)
	var iinPtr *string
	if strings.TrimSpace(iin) != "" {
		iinCopy := strings.TrimSpace(iin)
		iinPtr = &iinCopy
	}

	user := &domain.User{
		KeycloakID:     &kcIDCopy,
		IIN:            iinPtr,
		Email:          strings.ToLower(strings.TrimSpace(email)),
		Username:       strings.TrimSpace(username),
		FirstName:      strings.TrimSpace(firstName),
		LastName:       strings.TrimSpace(lastName),
		IsActive:       true,
		Role:           role,
		OrganizationID: organizationID,
	}

	if err := s.userRepo.UpsertFromKeycloak(ctx, user); err != nil {
		return err
	}
	if user.ID > 0 {
		_ = s.userRepo.UpdateLastLogin(ctx, user.ID, time.Now())
	}
	return nil
}

// mapKeycloakClaimsToDomainRole maps only realm roles from the trusted
// Keycloak access token to the application's local role model. Client roles
// are intentionally ignored so an unrelated client cannot grant platform
// administrator access.
func mapKeycloakClaimsToDomainRole(claims jwt.MapClaims) domain.Role {
	realmAccess, ok := claims["realm_access"].(map[string]interface{})
	if !ok {
		return domain.RoleUser
	}

	rawRoles, ok := realmAccess["roles"].([]interface{})
	if !ok {
		return domain.RoleUser
	}

	roles := make([]string, 0, len(rawRoles))
	for _, rawRole := range rawRoles {
		if role, ok := rawRole.(string); ok {
			roles = append(roles, role)
		}
	}
	return mapKCToDomainRoleFromRoles(roles)
}

func mapKCToDomainRoleFromRoles(roles []string) domain.Role {
	hasRole := func(wanted string) bool {
		for _, role := range roles {
			if strings.EqualFold(strings.TrimSpace(role), wanted) {
				return true
			}
		}
		return false
	}

	switch {
	case hasRole("admin") || hasRole("external_user_admin"):
		return domain.RoleSuperAdmin
	case hasRole("manager") || hasRole("leader"):
		return domain.RoleManager
	default:
		return domain.RoleUser
	}
}

func resolveLoginRequestIdentifier(req *LoginRequest) (string, error) {
	if req == nil {
		return "", apperrors.Validation("login request is required")
	}

	identifiers := []string{
		strings.TrimSpace(req.Identifier),
		strings.TrimSpace(req.Login),
		strings.TrimSpace(req.Email),
		strings.TrimSpace(req.Username),
		strings.TrimSpace(req.IIN),
	}

	var selected string
	for _, identifier := range identifiers {
		if identifier == "" {
			continue
		}
		if selected != "" && identifier != selected {
			return "", apperrors.Validation("provide only one login identifier")
		}
		selected = identifier
	}
	if selected == "" {
		return "", apperrors.Validation("email, username or iin is required")
	}

	return selected, nil
}

func (s *Service) resolvePasswordLoginUser(ctx context.Context, identifier string) (*domain.User, string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, "", apperrors.Validation("email, username or iin is required")
	}

	if strings.Contains(identifier, "@") {
		email := strings.ToLower(identifier)
		user, err := s.userRepo.GetByEmail(ctx, email)
		if err != nil && !isUserLookupNotFound(err) {
			return nil, "", apperrors.InternalWithErr("failed to lookup user by email", err)
		}
		if user != nil {
			return user, keycloakLoginForUser(user, email), nil
		}
		return nil, email, nil
	}

	if isIINLoginIdentifier(identifier) {
		user, err := s.userRepo.GetByIIN(ctx, identifier)
		if err != nil && !isUserLookupNotFound(err) {
			return nil, "", apperrors.InternalWithErr("failed to lookup user by iin", err)
		}
		if user != nil {
			return user, keycloakLoginForUser(user, identifier), nil
		}
		return nil, "", apperrors.InvalidCredentials()
	}

	user, err := s.userRepo.GetByUsername(ctx, identifier)
	if err != nil && !isUserLookupNotFound(err) {
		return nil, "", apperrors.InternalWithErr("failed to lookup user by username", err)
	}
	if user != nil {
		return user, keycloakLoginForUser(user, identifier), nil
	}

	return nil, identifier, nil
}

func keycloakLoginForUser(user *domain.User, fallback string) string {
	if user == nil {
		return fallback
	}
	if email := strings.TrimSpace(user.Email); email != "" {
		return strings.ToLower(email)
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		return username
	}
	return fallback
}

func isIINLoginIdentifier(identifier string) bool {
	if len(identifier) != 12 {
		return false
	}
	for _, r := range identifier {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isUserLookupNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "user not found") ||
		strings.Contains(msg, "no rows") ||
		strings.Contains(msg, "record not found") ||
		strings.Contains(msg, "not found")
}

func (s *Service) resolveHRPerson(ctx context.Context, req *RegisterByHRRequest) (*domain.HRPerson, json.RawMessage, error) {
	if req.Person != nil {
		person := normalizeHRPerson(req.Person)
		rawPayload, err := json.Marshal(person)
		if err != nil {
			return nil, nil, apperrors.Validation("failed to serialize HR payload")
		}
		return person, rawPayload, nil
	}

	iin := strings.TrimSpace(req.IIN)
	if iin == "" {
		return nil, nil, apperrors.Validation("iin or person payload is required")
	}
	if s.hrPersonProvider == nil {
		return nil, nil, apperrors.BadRequest("HR provider is not configured; pass person payload or configure provider")
	}

	person, rawPayload, err := s.hrPersonProvider.GetPersonByIIN(ctx, iin)
	if err != nil {
		return nil, nil, apperrors.InternalWithErr("Failed to fetch user from HR integration", err)
	}
	if person == nil {
		return nil, nil, apperrors.NotFound("Employee was not found in HR integration")
	}
	return normalizeHRPerson(person), rawPayload, nil
}

func (s *Service) findExistingUserForHR(ctx context.Context, person *domain.HRPerson) (*domain.User, error) {
	if person == nil {
		return nil, nil
	}

	if person.IIN != "" {
		user, err := s.userRepo.GetByIIN(ctx, person.IIN)
		if err == nil && user != nil {
			return user, nil
		}
	}

	if s.hrProfileRepo != nil && person.PersonCode != "" {
		profile, err := s.hrProfileRepo.GetByPersonCode(ctx, person.PersonCode)
		if err == nil && profile != nil {
			user, getErr := s.userRepo.GetByID(ctx, profile.UserID)
			if getErr == nil && user != nil {
				return user, nil
			}
		}
	}

	if s.hrProfileRepo != nil && person.IIN != "" {
		profile, err := s.hrProfileRepo.GetByIIN(ctx, person.IIN)
		if err == nil && profile != nil {
			user, getErr := s.userRepo.GetByID(ctx, profile.UserID)
			if getErr == nil && user != nil {
				return user, nil
			}
		}
	}

	if person.IIN != "" {
		user, err := s.userRepo.GetByUsername(ctx, person.IIN)
		if err == nil && user != nil {
			return user, nil
		}
	}

	return nil, nil
}

func (s *Service) findExistingKeycloakHRUser(ctx context.Context, email, username string) (*keycloak.UserRepresentation, error) {
	if email = strings.TrimSpace(strings.ToLower(email)); email != "" {
		users, err := s.kcAdmin.GetUsersByEmail(ctx, s.realm, email)
		if err != nil {
			return nil, err
		}
		if len(users) > 0 {
			return &users[0], nil
		}
	}

	if username = strings.TrimSpace(username); username != "" {
		users, err := s.kcAdmin.GetUsersByUsername(ctx, s.realm, username)
		if err != nil {
			return nil, err
		}
		if len(users) > 0 {
			return &users[0], nil
		}
	}

	return nil, nil
}

func normalizeHRPerson(person *domain.HRPerson) *domain.HRPerson {
	if person == nil {
		return nil
	}

	cloned := *person
	cloned.PersonCode = strings.TrimSpace(cloned.PersonCode)
	cloned.BadgeCode = strings.TrimSpace(cloned.BadgeCode)
	cloned.IIN = strings.TrimSpace(cloned.IIN)
	cloned.LastName = strings.TrimSpace(cloned.LastName)
	cloned.FirstName = strings.TrimSpace(cloned.FirstName)
	cloned.MiddleName = strings.TrimSpace(cloned.MiddleName)
	cloned.Photo = strings.TrimSpace(cloned.Photo)
	cloned.DocType = strings.TrimSpace(cloned.DocType)
	cloned.PersonStatus = strings.TrimSpace(cloned.PersonStatus)
	cloned.LastOrderNumber = strings.TrimSpace(cloned.LastOrderNumber)
	cloned.LastOrderDate = strings.TrimSpace(cloned.LastOrderDate)
	cloned.LastBeginDate = strings.TrimSpace(cloned.LastBeginDate)
	cloned.FirstBeginDate = strings.TrimSpace(cloned.FirstBeginDate)
	cloned.AbsenceStatus = strings.TrimSpace(cloned.AbsenceStatus)
	cloned.AbsenceOrderNumber = strings.TrimSpace(cloned.AbsenceOrderNumber)
	cloned.AbsenceOrderDate = strings.TrimSpace(cloned.AbsenceOrderDate)
	cloned.BeginAbsenceDate = strings.TrimSpace(cloned.BeginAbsenceDate)
	cloned.EndAbsenceDate = strings.TrimSpace(cloned.EndAbsenceDate)
	cloned.CardType = strings.TrimSpace(cloned.CardType)
	cloned.BIN = strings.TrimSpace(cloned.BIN)
	cloned.NameGO = strings.TrimSpace(cloned.NameGO)
	cloned.JobName = strings.TrimSpace(cloned.JobName)
	cloned.JobUID = strings.TrimSpace(cloned.JobUID)
	cloned.SubName = strings.TrimSpace(cloned.SubName)
	cloned.SubUID = strings.TrimSpace(cloned.SubUID)
	return &cloned
}

func mergeHRUser(existing *domain.User, person *domain.HRPerson, email, username, kcID string, assignment *domain.HierarchyAssignment) *domain.User {
	var user domain.User
	if existing != nil {
		user = *existing
	} else {
		user = domain.User{
			Role:      domain.RoleUser,
			CreatedAt: time.Now(),
		}
	}

	iin := person.IIN
	kcIDCopy := kcID
	user.KeycloakID = &kcIDCopy
	user.IIN = &iin
	user.Email = email
	user.Username = username
	user.FirstName = person.FirstName
	user.LastName = person.LastName
	user.IsActive = resolveHRIsActive(existing, person)
	user.UpdatedAt = time.Now()

	if strings.TrimSpace(person.JobName) != "" {
		position := person.JobName
		user.Position = &position
	}

	if assignment != nil {
		applyHierarchyAssignment(&user, assignment)
	}

	return &user
}

func applyHierarchyAssignment(user *domain.User, assignment *domain.HierarchyAssignment) {
	if user == nil || assignment == nil {
		return
	}

	user.PrimeMinisterID = nil
	user.DeputyPrimeMinisterID = nil
	user.MinistryID = assignment.MinistryID
	user.StateBodyID = assignment.StateBodyID
	user.DepartmentID = assignment.DepartmentID
	user.DivisionID = assignment.DivisionID
}

func (s *Service) normalizeHierarchyAssignment(ctx context.Context, assignment *domain.HierarchyAssignment) (*domain.HierarchyAssignment, error) {
	if assignment == nil {
		return &domain.HierarchyAssignment{}, nil
	}
	return assignment, nil
}

func (s *Service) resolveHierarchyAssignmentFallback(ctx context.Context, person *domain.HRPerson) (*domain.HierarchyAssignment, string, error) {
	return nil, "", nil
}
func (s *Service) resolveWithinMinistry(ctx context.Context, ministry any, subName, source string) (*domain.HierarchyAssignment, string, error) {
	return nil, source, nil
}

func (s *Service) resolveWithinStateBody(ctx context.Context, stateBody any, subName, source string) (*domain.HierarchyAssignment, string, error) {
	return nil, source, nil
}

func hierarchyMatchScore(subName, unitName string) int {
	switch {
	case subName == unitName:
		return 10000 + len(unitName)
	case strings.Contains(subName, unitName):
		return 5000 + len(unitName)
	case strings.Contains(unitName, subName):
		return 1000 + len(subName)
	default:
		return 0
	}
}

func hierarchyNameCandidates(name string) []string {
	base := normalizedHierarchyName(name)
	if base == "" {
		return nil
	}

	candidates := []string{base}
	if strings.HasSuffix(base, " рк") {
		candidates = append(candidates, strings.TrimSuffix(base, " рк")+" республики казахстан")
	}
	if strings.HasSuffix(base, " республика казахстан") {
		candidates = append(candidates, strings.TrimSuffix(base, " республика казахстан")+" рк")
	}

	seen := make(map[string]struct{}, len(candidates))
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}

	return unique
}

func normalizedHierarchyName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	replacements := []struct {
		old string
		new string
	}{
		{"ё", "е"},
		{"\"", " "},
		{"'", " "},
		{"«", " "},
		{"»", " "},
		{"(", " "},
		{")", " "},
		{",", " "},
		{".", " "},
		{"-", " "},
		{"\t", " "},
		{"\n", " "},
		{"\r", " "},
	}
	for _, replacement := range replacements {
		value = strings.ReplaceAll(value, replacement.old, replacement.new)
	}

	return strings.Join(strings.Fields(value), " ")
}

func buildUserHRProfile(userID int64, person *domain.HRPerson, rawPayload json.RawMessage) *domain.UserHRProfile {
	if len(rawPayload) == 0 {
		rawPayload = json.RawMessage(`{}`)
	}

	return &domain.UserHRProfile{
		UserID:             userID,
		PersonCode:         person.PersonCode,
		BadgeCode:          person.BadgeCode,
		IIN:                person.IIN,
		LastName:           person.LastName,
		FirstName:          person.FirstName,
		MiddleName:         person.MiddleName,
		Photo:              person.Photo,
		DocType:            person.DocType,
		PersonStatus:       person.PersonStatus,
		LastOrderNumber:    person.LastOrderNumber,
		LastOrderDate:      parseHRDate(person.LastOrderDate),
		LastBeginDate:      parseHRDate(person.LastBeginDate),
		FirstBeginDate:     parseHRDate(person.FirstBeginDate),
		AbsenceStatus:      person.AbsenceStatus,
		AbsenceOrderNumber: person.AbsenceOrderNumber,
		AbsenceOrderDate:   parseHRDate(person.AbsenceOrderDate),
		BeginAbsenceDate:   parseHRDate(person.BeginAbsenceDate),
		EndAbsenceDate:     parseHRDate(person.EndAbsenceDate),
		CardType:           person.CardType,
		BIN:                person.BIN,
		NameGO:             person.NameGO,
		JobName:            person.JobName,
		JobUID:             person.JobUID,
		SubName:            person.SubName,
		SubUID:             person.SubUID,
		RawPayload:         rawPayload,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
}

func parseHRDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil
	}
	return &parsed
}

func mapHRPersonStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "работающий", "active", "working", "employed":
		return true
	default:
		return false
	}
}

func resolveHRIsActive(existing *domain.User, person *domain.HRPerson) bool {
	if strings.TrimSpace(person.PersonStatus) == "" {
		if existing != nil {
			return existing.IsActive
		}
		return true
	}
	return mapHRPersonStatus(person.PersonStatus)
}

func (s *Service) publishManagementVacationNews(ctx context.Context, user *domain.User, previousProfile, currentProfile *domain.UserHRProfile) error {
	if user == nil || currentProfile == nil {
		return nil
	}
	if !isVacationProfile(currentProfile) || !isManagementUser(user, currentProfile) || sameVacationPeriod(previousProfile, currentProfile) {
		return nil
	}

	title := strings.TrimSpace(user.FirstName + " " + user.LastName + " находится в отпуске")
	content := buildVacationPublicationContent(currentProfile)

	existing, err := s.publicationRepo.FindByAuthorTitleContent(ctx, user.ID, title, content)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}

	now := time.Now()
	pub := &domain.Publication{
		ID:                      uuid.New(),
		AuthorID:                user.ID,
		ApprovedByID:            &user.ID,
		SourceType:              "user",
		MinistryID:              user.MinistryID,
		DepartmentID:            user.DepartmentID,
		Type:                    "news",
		Title:                   title,
		Content:                 &content,
		TargetAudience:          "all",
		Importance:              "normal",
		IsPinned:                false,
		AllowComments:           true,
		RequiresAcknowledgement: false,
		PublishedAt:             now,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	return s.publicationRepo.Create(ctx, pub, nil)
}

func isVacationProfile(profile *domain.UserHRProfile) bool {
	if profile == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(profile.AbsenceStatus), "vacation")
}

func sameVacationPeriod(previousProfile, currentProfile *domain.UserHRProfile) bool {
	if !isVacationProfile(previousProfile) || !isVacationProfile(currentProfile) {
		return false
	}
	return sameDate(previousProfile.BeginAbsenceDate, currentProfile.BeginAbsenceDate) &&
		sameDate(previousProfile.EndAbsenceDate, currentProfile.EndAbsenceDate)
}

func sameDate(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func isManagementUser(user *domain.User, profile *domain.UserHRProfile) bool {
	if user == nil {
		return false
	}

	switch user.Role {
	case domain.RoleSuperAdmin, domain.RoleManager:
		return true
	}

	var candidates []string
	if user.Position != nil {
		candidates = append(candidates, *user.Position)
	}
	if profile != nil {
		candidates = append(candidates, profile.JobName)
	}

	for _, candidate := range candidates {
		if containsManagementKeyword(candidate) {
			return true
		}
	}

	return false
}

func containsManagementKeyword(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return false
	}

	keywords := []string{
		"министр",
		"председатель",
		"директор",
		"руководитель",
		"начальник",
		"заместитель",
		"vice-minister",
		"deputy minister",
		"head of staff",
	}
	for _, keyword := range keywords {
		if strings.Contains(normalized, keyword) {
			return true
		}
	}
	return false
}

func buildVacationPublicationContent(profile *domain.UserHRProfile) string {
	if profile == nil {
		return ""
	}
	if profile.BeginAbsenceDate == nil || profile.EndAbsenceDate == nil {
		return "Сотрудник временно находится в отпуске."
	}

	days := int(profile.EndAbsenceDate.Sub(*profile.BeginAbsenceDate).Hours()/24) + 1
	if days < 1 {
		days = 1
	}

	return fmt.Sprintf(
		"Период отпуска: %s - %s\nДлительность: %d календарных дней",
		profile.BeginAbsenceDate.Format("02.01.2006"),
		profile.EndAbsenceDate.Format("02.01.2006"),
		days,
	)
}

func resolveHREmail(requestEmail string, existing *domain.User, person *domain.HRPerson) string {
	if email := strings.ToLower(strings.TrimSpace(requestEmail)); email != "" {
		return email
	}
	if existing != nil && strings.TrimSpace(existing.Email) != "" {
		return strings.ToLower(strings.TrimSpace(existing.Email))
	}
	return strings.ToLower(fmt.Sprintf("%s@hr.local", person.IIN))
}

func resolveHRUsername(requestUsername string, existing *domain.User, person *domain.HRPerson) string {
	if username := strings.TrimSpace(requestUsername); username != "" {
		return username
	}
	if existing != nil && strings.TrimSpace(existing.Username) != "" {
		return existing.Username
	}
	if person.IIN != "" {
		return "hr-" + person.IIN
	}
	return "hr-" + strings.ReplaceAll(strings.ToLower(person.PersonCode), ".", "-")
}

func refString(value string) *string {
	return &value
}

func refBool(value bool) *bool {
	return &value
}

func envBool(key string, defaultValue bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}

func (s *Service) CompleteOnboarding(ctx context.Context, userID int64) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return apperrors.NotFound("User not found")
	}
	if isIFormFirstLogin(user) {
		return apperrors.Forbidden("Сначала смените временный пароль")
	}
	return s.userRepo.CompleteOnboarding(ctx, userID)
}

func (s *Service) RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error) {
	if s.tokenManager != nil {
		if existing, err := s.userRepo.GetByKeycloakID(ctx, extractSubFromJWT(refreshToken)); err == nil && existing != nil {
			if err := s.checkProviderCredentialState(ctx, refreshToken, existing.ID); err != nil {
				return nil, apperrors.ReauthRequired("Please sign in again")
			}
		}
	}
	kcClient := resolveKeycloakClientForToken(refreshToken, s.kc, s.kcClients)
	tok, err := kcClient.Refresh(ctx, refreshToken)
	if err != nil {
		var kcErr *keycloak.RequestError
		if errors.As(err, &kcErr) {
			if kcErr.IsInvalidGrant() {
				appErr := apperrors.ReauthRequired("Session expired. Please sign in again.").
					WithDetail("keycloak_status", kcErr.StatusCode).
					WithDetail("keycloak_error", kcErr.ErrorCode)
				if kcErr.IsOfflineSessionNotFound() {
					appErr = appErr.WithDetail("reason", "offline_session_not_found")
				} else {
					appErr = appErr.WithDetail("reason", "invalid_grant")
				}
				return nil, appErr.WithError(err)
			}
			return nil, apperrors.RefreshFailed("Failed to refresh session").
				WithDetail("keycloak_status", kcErr.StatusCode).
				WithDetail("keycloak_error", kcErr.ErrorCode).
				WithError(err)
		}
		return nil, apperrors.RefreshFailed(fmt.Sprintf("Failed to refresh session: %v", err))
	}

	var mustChangePassword bool
	var cannotSkip bool
	var localTokens *TokenPair
	if err := s.checkIssuedKeycloakAccess(ctx, tok.AccessToken); err != nil {
		return nil, err
	}
	if tok.AccessToken != "" {
		kcID := extractSubFromJWT(tok.AccessToken)
		if kcID != "" {
			if user, err := s.userRepo.GetByKeycloakID(ctx, kcID); err == nil && user != nil {
				if s.tokenManager != nil {
					if err := s.checkProviderCredentialState(ctx, tok.AccessToken, user.ID); err != nil {
						return nil, apperrors.ReauthRequired("Please sign in again")
					}
					if genTokens, genErr := s.tokenManager.GenerateTokenPair(providerAuthenticationContext(ctx, tok.AccessToken), user.ID, derefStringValue(user.IIN), user.Email, user.Username, user.FirstName, user.LastName, false, nil); genErr == nil {
						localTokens = genTokens
					} else {
						return nil, apperrors.ReauthRequired("Please sign in again")
					}
				}

				if s.loginLogRepo != nil {
					_ = s.loginLogRepo.Create(ctx, user.ID, user.Email)
				}

				mustChangePassword, cannotSkip = s.firstLoginPasswordRequirement(ctx, user, true)
			}
		}
	}

	if localTokens != nil {
		localTokens.MustChangePassword = mustChangePassword
		localTokens.CannotSkipPasswordChange = cannotSkip
		return localTokens, nil
	}

	return &TokenPair{
		AccessToken:              tok.AccessToken,
		RefreshToken:             tok.RefreshToken,
		IDToken:                  tok.IDToken,
		TokenType:                tok.TokenType,
		ExpiresIn:                tok.ExpiresIn,
		ExpiresAt:                time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		MustChangePassword:       mustChangePassword,
		CannotSkipPasswordChange: cannotSkip,
	}, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return apperrors.BadRequest("refresh_token is required")
	}
	kcClient := resolveKeycloakClientForToken(refreshToken, s.kc, s.kcClients)
	if err := kcClient.Logout(ctx, refreshToken); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "status=400") || strings.Contains(msg, "status=401") {
			return apperrors.Unauthorized("Invalid or expired refresh token")
		}
		return apperrors.InternalWithErr("Failed to logout in Keycloak provider", err)
	}
	return nil
}

func (s *Service) verifyPasswordKeycloak(ctx context.Context, userEmail, password string) error {
	_, err := s.kc.Password(ctx, userEmail, password)
	if err != nil {
		return apperrors.Unauthorized("Old password is incorrect")
	}
	return nil
}

func (s *Service) validateNewPassword(newPassword string) error {
	if len(newPassword) < 8 {
		return apperrors.BadRequest("New password is too weak (min 8 characters)")
	}
	return nil
}

func (s *Service) setPasswordKeycloak(ctx context.Context, keycloakID, newPassword string) error {
	if err := s.kcAdmin.SetPassword(ctx, s.realm, keycloakID, newPassword, false); err != nil {
		var provider *keycloak.RequestError
		if errors.As(err, &provider) && provider.StatusCode == 400 {
			return apperrors.New("RECOVERY_PASSWORD_POLICY", "Password does not meet organization policy", 400)
		}
		return apperrors.InternalWithErr("Failed to change password in system", err)
	}
	return nil
}

type UpdateUserRequest struct {
	Email                    *string    `json:"email"`
	Username                 *string    `json:"username"`
	Password                 *string    `json:"password"`
	FirstName                *string    `json:"first_name"`
	LastName                 *string    `json:"last_name"`
	IIN                      *string    `json:"iin"`
	Role                     *string    `json:"role"`
	Position                 *string    `json:"position"`
	OrganizationID           *uuid.UUID `json:"organization_id,omitempty"`
	TeamID                   *uuid.UUID `json:"team_id,omitempty"`
	ManagerID                *int64     `json:"manager_id,omitempty"`
	IsActive                 *bool      `json:"is_active"`
	FullNameLocal            *string    `json:"full_name_local"`
	CanViewFullDriveActivity *bool      `json:"can_view_full_drive_activity"`
}

type CreateUserRequest struct {
	StartDate           *string `json:"-"` // Populated only by the typed onboarding bridge.
	employmentAdmission *domain.EmploymentAdmission
	Email               string     `json:"email" binding:"required,email"`
	Username            string     `json:"username" binding:"required,min=3,max=50"`
	Password            string     `json:"password" binding:"required,min=8"`
	FirstName           string     `json:"first_name" binding:"required,min=1,max=50"`
	LastName            string     `json:"last_name" binding:"required,min=1,max=50"`
	IIN                 string     `json:"iin" binding:"required,len=12"`
	Role                string     `json:"role"`
	Position            string     `json:"position"`
	OrganizationID      *uuid.UUID `json:"organization_id,omitempty"`
	// Required when creating an employee in an organization.
	TeamID                   *uuid.UUID `json:"team_id,omitempty"`
	ManagerID                *int64     `json:"manager_id,omitempty"`
	CanViewFullDriveActivity bool       `json:"can_view_full_drive_activity"`
}

type userAccountCreator interface {
	CheckNewUserIdentity(context.Context, string, string, string) error
	CreateNewUser(context.Context, *domain.User) error
}

func (s *Service) CreateUser(ctx context.Context, requesterID int64, req *CreateUserRequest) (_ *domain.UserResponse, resultErr error) {
	if req == nil {
		return nil, apperrors.Validation("request is required")
	}
	if err := s.validateUserOrganizationAssignment(ctx, req); err != nil {
		return nil, err
	}
	creator, ok := s.userRepo.(userAccountCreator)
	if !ok {
		return nil, apperrors.Internal("User account creation is unavailable")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Username = strings.TrimSpace(req.Username)
	req.IIN = strings.TrimSpace(req.IIN)
	if err := creator.CheckNewUserIdentity(ctx, req.Email, req.Username, req.IIN); err != nil {
		if errors.Is(err, repository.ErrUserIdentityConflict) {
			return nil, apperrors.UserIdentityConflict()
		}
		return nil, apperrors.InternalWithErr("Failed to check new user identity", err)
	}

	kcRole := req.Role
	if kcRole == "" {
		kcRole = "user"
	}

	kcID, err := s.createEmployeeKeycloakIdentity(ctx, keycloak.CreateUserRequest{
		Username:      req.Username,
		Email:         req.Email,
		Enabled:       true,
		EmailVerified: true,
		FirstName:     req.FirstName,
		LastName:      req.LastName,
		Attributes:    s.makeKeycloakAttributesFromCreateReq(ctx, req),
	}, req.IIN)
	createdInKeycloak := err == nil
	defer func() {
		if !createdInKeycloak || resultErr == nil {
			return
		}
		// Only compensate the new identity created by this call.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), passwordRollbackTimeout)
		defer cancel()
		if rollbackErr := s.kcAdmin.DeleteUser(rollbackCtx, s.realm, kcID); rollbackErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("new Keycloak user rollback failed: %w", rollbackErr))
		}
	}()
	if err != nil {
		if strings.Contains(err.Error(), "409") {
			return nil, apperrors.UserIdentityConflict()
		}
		return nil, apperrors.InternalWithErr("Failed to create user in Keycloak", err)
	}

	if err := s.kcAdmin.SetPassword(ctx, s.realm, kcID, req.Password, false); err != nil {
		return nil, apperrors.Internal("Failed to set password")
	}

	if err := s.kcAdmin.SetUserRealmRoles(ctx, s.realm, kcID, []string{kcRole}); err != nil {
		return nil, apperrors.Internal("Failed to set role")
	}

	kcIDPtr := kcID
	iinPtr := req.IIN

	var posPtr *string
	if req.Position != "" {
		p := req.Position
		posPtr = &p
	}

	u := &domain.User{
		KeycloakID:               &kcIDPtr,
		EmploymentAdmission:      req.employmentAdmission,
		IIN:                      &iinPtr,
		Email:                    req.Email,
		Username:                 req.Username,
		FirstName:                req.FirstName,
		LastName:                 req.LastName,
		IsActive:                 true,
		IsFirstLogin:             true,
		Role:                     domain.Role(kcRole),
		Position:                 posPtr,
		OrganizationID:           req.OrganizationID,
		TeamID:                   req.TeamID,
		ManagerID:                req.ManagerID,
		CreatedAt:                time.Now(),
		UpdatedAt:                time.Now(),
		CanViewFullDriveActivity: req.CanViewFullDriveActivity,
	}

	if err := creator.CreateNewUser(ctx, u); err != nil {
		if errors.Is(err, repository.ErrUserIdentityConflict) {
			return nil, apperrors.UserIdentityConflict()
		}
		return nil, apperrors.InternalWithErr("Failed to create local user", err)
	}
	return u.ToResponse(), nil
}

func (s *Service) UpdateUserAdmin(ctx context.Context, requesterID int64, userID int64, req *UpdateUserRequest) (*domain.UserResponse, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, apperrors.NotFound("User not found")
	}

	if err := s.checkNotOffboarded(ctx, userID); err != nil {
		return nil, err
	}

	if req.Role != nil {
		kcRole := *req.Role
		if kcRole == "" {
			kcRole = "user"
		}
		if u.KeycloakID != nil && *u.KeycloakID != "" {
			if err := s.kcAdmin.SetUserRealmRoles(ctx, s.realm, *u.KeycloakID, []string{kcRole}); err != nil {
				return nil, apperrors.Internal("Failed to update Keycloak roles")
			}
		}
		u.Role = mapKCToDomainRole(kcRole)
	}

	if req.IsActive != nil {
		u.IsActive = *req.IsActive
	}

	if req.Password != nil && *req.Password != "" {
		if u.KeycloakID != nil && *u.KeycloakID != "" {
			if err := s.kcAdmin.SetPassword(ctx, s.realm, *u.KeycloakID, *req.Password, false); err != nil {
				return nil, apperrors.Internal("Failed to update Keycloak password")
			}
		}
	}

	if req.Email != nil {
		u.Email = *req.Email
	}
	if req.Username != nil {
		u.Username = *req.Username
	}
	if req.FirstName != nil {
		u.FirstName = *req.FirstName
	}
	if req.LastName != nil {
		u.LastName = *req.LastName
	}

	if req.Position != nil {
		if *req.Position != "" {
			u.Position = req.Position
		} else {
			u.Position = nil
		}
	}

	if req.IIN != nil {
		u.IIN = req.IIN
	}

	if req.OrganizationID != nil {
		u.OrganizationID = req.OrganizationID
	}
	if req.TeamID != nil {
		u.TeamID = req.TeamID
	}
	if req.ManagerID != nil {
		u.ManagerID = req.ManagerID
	}
	if req.FullNameLocal != nil {
		u.FullNameLocal = req.FullNameLocal
	}
	if req.CanViewFullDriveActivity != nil {
		u.CanViewFullDriveActivity = *req.CanViewFullDriveActivity
	}

	if u.KeycloakID != nil && *u.KeycloakID != "" {
		username := u.Username
		firstName := u.FirstName
		lastName := u.LastName
		email := u.Email
		emailVerified := true
		enabled := u.IsActive

		kcReq := keycloak.UserUpdateRequest{
			Username:      &username,
			FirstName:     &firstName,
			LastName:      &lastName,
			Email:         &email,
			EmailVerified: &emailVerified,
			Enabled:       &enabled,
			Attributes:    s.makeKeycloakAttributes(ctx, u),
		}
		if err := s.kcAdmin.UpdateUser(ctx, s.realm, *u.KeycloakID, kcReq); err != nil {
			return nil, apperrors.InternalWithErr("Failed to update user profile in Keycloak", err)
		}
	}

	if err := s.userRepo.Update(ctx, u); err != nil {
		return nil, apperrors.Internal("Failed to update local user")
	}
	return u.ToResponse(), nil
}

func (s *Service) DeleteUser(ctx context.Context, requesterID int64, targetUserID int64) error {
	if s.deleteUser == nil {
		return apperrors.Internal("User deletion authorization is unavailable")
	}
	if err := s.checkNotOffboarded(ctx, targetUserID); err != nil {
		return err
	}
	return s.deleteUser(ctx, requesterID, targetUserID)
}

func (s *Service) AssignMinistryAdmin(ctx context.Context, requesterID int64, ministryID int64, targetUserID int64) error {
	return apperrors.BadRequest("Ministry feature is deprecated")
}

func (s *Service) RevokeMinistryAdmin(ctx context.Context, requesterID int64, ministryID int64) error {
	return apperrors.BadRequest("Ministry feature is deprecated")
}

func (s *Service) ListMinistryAdmins(ctx context.Context) ([]any, error) {
	return nil, nil
}

func mapKCToDomainRole(kcRole string) domain.Role {
	switch kcRole {
	case "admin", "external_user_admin":
		return domain.RoleSuperAdmin
	case "manager", "leader":
		return domain.RoleManager
	default:
		return domain.RoleUser
	}
}

func (s *Service) LogoutAll(ctx context.Context, userID int64) error {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return apperrors.NotFound("User not found")
	}

	if u.KeycloakID != nil && *u.KeycloakID != "" {
		kcID := *u.KeycloakID
		if err := s.kcAdmin.LogoutUser(ctx, s.realm, kcID); err != nil {
			return apperrors.InternalWithErr("Failed to logout user from Keycloak", err)
		}
	}
	return nil
}

func (s *Service) GetCurrentUserByKeycloakID(ctx context.Context, kcID string) (*domain.UserResponse, error) {
	user, err := s.userRepo.GetByKeycloakID(ctx, kcID)
	if err != nil {
		return nil, apperrors.ToAppError(err)
	}
	if user == nil {
		return nil, apperrors.NotFound("user not found")
	}
	return user.ToResponse(), nil
}

func (s *Service) Me(ctx context.Context, userID int64) (*domain.UserResponse, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || u == nil {
		return nil, apperrors.NotFound("User not found")
	}
	return u.ToResponse(), nil
}

func (s *Service) GetCurrentUser(ctx context.Context, userID int64) (*domain.UserResponse, error) {
	resp, err := s.getCurrentUserBaseResponse(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.hrProfileRepo != nil {
		profile, err := s.hrProfileRepo.GetByUserID(ctx, userID)
		if err == nil && profile != nil {
			resp.ApplyHRProfile(profile)
		}
	}

	return resp, nil
}

func (s *Service) ResolveMailOAuthMailboxLogin(ctx context.Context, user *domain.UserResponse) (string, error) {
	if user == nil {
		return "", apperrors.Internal("user is required for mailbox resolution")
	}

	fallback := strings.ToLower(strings.TrimSpace(user.Email))
	if s.mailboxRepo != nil {
		mailboxLogin, err := s.mailboxRepo.GetPrimaryMailboxLogin(ctx, user.ID)
		if err != nil {
			return "", apperrors.InternalWithErr("failed to load mailbox mapping", err)
		}
		if mailboxLogin != "" {
			return strings.ToLower(strings.TrimSpace(mailboxLogin)), nil
		}
	}

	if s.mailboxRepo == nil || s.mailboxProvisioner == nil {
		return fallback, nil
	}

	mailboxLogin, err := s.mailboxProvisioner.ProvisionPrimaryMailbox(ctx, user)
	if err != nil {
		return "", err
	}

	mailboxLogin = strings.ToLower(strings.TrimSpace(mailboxLogin))
	if mailboxLogin == "" {
		return "", apperrors.Internal("mailbox provisioning returned an empty mailbox login")
	}

	if err := s.mailboxRepo.UpsertPrimaryMailboxLogin(ctx, user.ID, mailboxLogin); err != nil {
		return "", apperrors.InternalWithErr("failed to persist mailbox mapping", err)
	}
	if err := s.syncMailboxEmail(ctx, user, mailboxLogin); err != nil {
		return "", err
	}

	return mailboxLogin, nil
}

func (s *Service) syncMailboxEmail(ctx context.Context, user *domain.UserResponse, mailboxLogin string) error {
	if user == nil {
		return nil
	}
	mailboxLogin = strings.ToLower(strings.TrimSpace(mailboxLogin))
	currentEmail := strings.ToLower(strings.TrimSpace(user.Email))
	if mailboxLogin == "" || mailboxLogin == currentEmail {
		return nil
	}

	updated, err := s.UpdateEmail(ctx, user.ID, mailboxLogin)
	if err != nil {
		return err
	}
	user.Email = updated.Email
	return nil
}

func (s *Service) getCurrentUserBaseResponse(ctx context.Context, userID int64) (*domain.UserResponse, error) {
	details, err := s.userRepo.GetFullDetailsByID(ctx, userID)
	if err == nil && details != nil {
		resp := &details.UserResponse
		if resp.PositionName == "" {
			resp.PositionName = stringValue(resp.Position)
		}
		resp.CanViewFullDriveActivityByPosition = domain.PositionGrantsFullDriveActivity(resp.PositionName)
		resp.CanViewFullDriveActivityEffective = resp.CanViewFullDriveActivity || resp.CanViewFullDriveActivityByPosition
		if resp.DepartmentName == "" {
			resp.DepartmentName = firstNonEmptyString(details.StateBodyName)
		}
		if resp.OrganizationName == "" {
			resp.OrganizationName = firstNonEmptyString(details.MinistryName)
		}
		return resp, nil
	}

	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || u == nil {
		return nil, apperrors.NotFound("User not found")
	}

	resp := u.ToResponse()
	if resp.PositionName == "" {
		resp.PositionName = stringValue(resp.Position)
	}
	resp.CanViewFullDriveActivityByPosition = domain.PositionGrantsFullDriveActivity(resp.PositionName)
	resp.CanViewFullDriveActivityEffective = resp.CanViewFullDriveActivity || resp.CanViewFullDriveActivityByPosition
	return resp, nil
}

func firstNonEmptyString(values ...*string) string {
	for _, value := range values {
		if value != nil && *value != "" {
			return *value
		}
	}
	return ""
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) GetUserFullDetails(ctx context.Context, targetUserID int64, requesterID int64) (*domain.UserDetailResponse, error) {
	if requesterID != 0 {
		if _, err := s.userRepo.GetByID(ctx, requesterID); err != nil {
			return nil, apperrors.NotFound("Requester not found")
		}
	}

	u, err := s.userRepo.GetFullDetailsByID(ctx, targetUserID)
	if err != nil {
		return nil, apperrors.NotFound("User not found")
	}

	stats, err := s.reviewRepo.GetMonthlyStats(ctx, targetUserID)
	if err == nil {
		u.ReviewStats = stats
	}

	if requesterID != 0 && requesterID != targetUserID {
		hasReviewed, err := s.reviewRepo.HasReviewedThisMonth(ctx, requesterID, targetUserID)
		if err == nil {
			u.HasReviewedThisMonth = hasReviewed
		}
	}

	return u, nil
}

func (s *Service) UpdateAvatar(ctx context.Context, userID int64, avatarURL string) (*domain.UserResponse, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || u == nil {
		return nil, apperrors.NotFound("User not found")
	}
	u.AvatarURL = &avatarURL
	u.UpdatedAt = time.Now()

	if err := s.userRepo.Update(ctx, u); err != nil {
		return nil, apperrors.Internal("Failed to update user")
	}
	return u.ToResponse(), nil
}

func (s *Service) UpdateEmail(ctx context.Context, userID int64, newEmail string) (*domain.UserResponse, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || u == nil {
		return nil, apperrors.NotFound("User not found")
	}

	// Check if new email is already registered by another user
	existing, err := s.userRepo.GetByEmail(ctx, newEmail)
	if err == nil && existing != nil && existing.ID != userID {
		return nil, apperrors.Conflict("Этот email адрес уже зарегистрирован другим пользователем.")
	}

	// Update email and username in Keycloak if user has Keycloak ID
	if u.KeycloakID != nil && *u.KeycloakID != "" {
		emailVerified := true
		currentUsername := strings.TrimSpace(u.Username)
		err := s.kcAdmin.UpdateUser(ctx, s.realm, *u.KeycloakID, keycloak.UserUpdateRequest{
			Email:         &newEmail,
			EmailVerified: &emailVerified,
			Username:      &currentUsername,
		})
		if err != nil {
			return nil, apperrors.InternalWithErr("Failed to update email in Keycloak", err)
		}
	}

	// Update locally in database
	u.Email = newEmail
	u.UpdatedAt = time.Now()

	if err := s.userRepo.Update(ctx, u); err != nil {
		return nil, apperrors.InternalWithErr("Failed to update user in database", err)
	}

	return u.ToResponse(), nil
}

func (s *Service) GetAllUsers(ctx context.Context, query string, filters domain.UserFilters, limit, offset int) ([]*domain.UserResponse, int, error) {
	users, total, err := s.userRepo.GetPaginated(ctx, query, limit, offset, filters)
	if err != nil {
		return nil, 0, apperrors.Internal("Failed to load users")
	}

	res := make([]*domain.UserResponse, 0, len(users))
	for _, u := range users {
		res = append(res, u.ToResponse())
	}
	return res, total, nil
}

// GetDirectoryUsers returns the user directory with tenant isolation. Global
// administrators can see users from every organization; regular users only
// see users belonging to their own organization.
func (s *Service) GetDirectoryUsers(ctx context.Context, requesterID int64, query string, filters domain.UserFilters, limit, offset int) ([]*domain.UserResponse, int, error) {
	requester, err := s.userRepo.GetByID(ctx, requesterID)
	if err != nil {
		return nil, 0, apperrors.InternalWithErr("Failed to load directory requester", err)
	}
	if requester == nil {
		return nil, 0, apperrors.NotFound("Requester")
	}

	if domain.HasGlobalUserScope(requester) {
		return s.GetAllUsers(ctx, query, filters, limit, offset)
	}

	searcher, ok := s.userRepo.(directoryUserSearcher)
	if !ok {
		return nil, 0, apperrors.Internal("Organization-scoped user directory is not available")
	}

	users, total, err := searcher.GetPaginatedVisibleTo(ctx, requesterID, query, limit, offset, filters)
	if err != nil {
		return nil, 0, apperrors.InternalWithErr("Failed to load organization user directory", err)
	}

	res := make([]*domain.UserResponse, 0, len(users))
	for _, user := range users {
		res = append(res, user.ToResponse())
	}
	return res, total, nil
}

func (s *Service) ListScopedUsers(ctx context.Context, requesterID int64, query string, limit, offset int) ([]*domain.UserResponse, error) {
	searcher, ok := s.userRepo.(visibleUserSearcher)
	if !ok {
		return nil, apperrors.Internal("Scoped user search is not available")
	}
	users, err := searcher.SearchVisibleTo(ctx, requesterID, query, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to load scoped users", err)
	}
	out := make([]*domain.UserResponse, 0, len(users))
	for _, user := range users {
		out = append(out, user.ToResponse())
	}
	return out, nil
}

func (s *Service) ListExternalAdminUsers(ctx context.Context, requesterID int64, query string, limit, offset int) ([]*domain.UserResponse, error) {
	type allUserSearcher interface {
		SearchAllUsers(ctx context.Context, q string, limit, offset int) ([]*domain.User, error)
	}
	searcher, ok := s.userRepo.(allUserSearcher)
	if !ok {
		return s.ListScopedUsers(ctx, requesterID, query, limit, offset)
	}
	users, err := searcher.SearchAllUsers(ctx, query, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to load users for external admin", err)
	}
	out := make([]*domain.UserResponse, 0, len(users))
	for _, user := range users {
		out = append(out, user.ToResponse())
	}
	return out, nil
}

func (s *Service) EnsureExternalAdminUserInScope(ctx context.Context, requesterID, targetUserID int64) (*domain.User, *domain.User, error) {
	requester, err := s.userRepo.GetByID(ctx, requesterID)
	if err != nil {
		return nil, nil, apperrors.InternalWithErr("Failed to load requester", err)
	}
	if requester == nil {
		return nil, nil, apperrors.NotFound("Requester not found")
	}

	target, err := s.userRepo.GetByID(ctx, targetUserID)
	if err != nil {
		return nil, nil, apperrors.InternalWithErr("Failed to load user", err)
	}
	if target == nil {
		return nil, nil, apperrors.NotFound("User not found")
	}
	return requester, target, nil
}

func (s *Service) ValidateExternalAdminUserCreate(ctx context.Context, requesterID int64, req *CreateUserRequest) error {
	requester, err := s.userRepo.GetByID(ctx, requesterID)
	if err != nil {
		return apperrors.InternalWithErr("Failed to load requester", err)
	}
	if requester == nil {
		return apperrors.NotFound("Requester not found")
	}
	if req == nil {
		return apperrors.Validation("request is required")
	}

	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		req.Role = string(domain.RoleUser)
	}
	if req.Role != string(domain.RoleUser) {
		return apperrors.Forbidden("External admin can only create users with role user")
	}
	return nil
}

func (s *Service) ResetUserPasswordExternalAdmin(ctx context.Context, requesterID, targetUserID int64, newPassword string, logoutAll bool) error {
	_, target, err := s.EnsureExternalAdminUserInScope(ctx, requesterID, targetUserID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(newPassword) == "" {
		return apperrors.Validation("password is required")
	}
	if err := s.validateNewPassword(newPassword); err != nil {
		return err
	}
	if target.KeycloakID == nil || *target.KeycloakID == "" {
		return apperrors.Internal("User has no Keycloak ID associated")
	}
	if err := s.kcAdmin.SetPassword(ctx, s.realm, *target.KeycloakID, newPassword, false); err != nil {
		if strings.Contains(err.Error(), "400") {
			return apperrors.BadRequest("Password does not meet security requirements")
		}
		return apperrors.InternalWithErr("Failed to update Keycloak password", err)
	}
	if err := s.userRepo.SetIsFirstLogin(ctx, targetUserID, true); err != nil {
		return apperrors.InternalWithErr("Failed to mark user for password change", err)
	}
	if logoutAll {
		if err := s.LogoutAll(ctx, targetUserID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) EnsureUserInScope(ctx context.Context, requesterID, targetUserID int64) (*domain.User, *domain.User, error) {
	requester, err := s.userRepo.GetByID(ctx, requesterID)
	if err != nil {
		return nil, nil, apperrors.InternalWithErr("Failed to load requester", err)
	}
	if requester == nil {
		return nil, nil, apperrors.NotFound("Requester not found")
	}

	target, err := s.userRepo.GetByID(ctx, targetUserID)
	if err != nil {
		return nil, nil, apperrors.InternalWithErr("Failed to load user", err)
	}
	if target == nil {
		return nil, nil, apperrors.NotFound("User not found")
	}
	if !domain.CanAccessUserByScope(requester, target) {
		return nil, nil, apperrors.Forbidden("Access denied")
	}
	return requester, target, nil
}

func (s *Service) ValidateScopedUserCreate(ctx context.Context, requesterID int64, req *CreateUserRequest) error {
	requester, err := s.userRepo.GetByID(ctx, requesterID)
	if err != nil {
		return apperrors.InternalWithErr("Failed to load requester", err)
	}
	if requester == nil {
		return apperrors.NotFound("Requester not found")
	}
	if req == nil {
		return apperrors.Validation("request is required")
	}

	if domain.HasGlobalUserScope(requester) {
		return s.validateUserOrganizationAssignment(ctx, req)
	}

	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		req.Role = string(domain.RoleUser)
	}
	if req.Role != string(domain.RoleUser) {
		return apperrors.Forbidden("Scoped administrator can only create users with role user")
	}
	if requester.OrganizationID == nil {
		return apperrors.Forbidden("Requester has no organization scope assigned")
	}
	if req.OrganizationID != nil && *req.OrganizationID != *requester.OrganizationID {
		return apperrors.Forbidden("Cannot create users outside requester's organization scope")
	}
	req.OrganizationID = requester.OrganizationID
	return s.validateUserOrganizationAssignment(ctx, req)
}

func (s *Service) validateUserOrganizationAssignment(ctx context.Context, req *CreateUserRequest) error {
	if req == nil {
		return apperrors.Validation("request is required")
	}
	if req.TeamID != nil && *req.TeamID == uuid.Nil {
		return apperrors.Validation("team_id must be a non-zero UUID")
	}
	if req.OrganizationID == nil {
		if req.TeamID != nil {
			return apperrors.Validation("organization_id is required when team_id is set")
		}
		return nil
	}
	if *req.OrganizationID == uuid.Nil {
		return apperrors.Validation("organization_id must be a non-zero UUID")
	}
	if req.TeamID == nil {
		return apperrors.Validation("team_id is required when creating an employee in an organization")
	}
	validator, ok := s.userRepo.(organizationTeamValidator)
	if !ok {
		return nil
	}
	if err := validator.ValidateOrganizationTeam(ctx, *req.OrganizationID, *req.TeamID); err != nil {
		return apperrors.Validation("selected subdivision does not belong to the selected organization")
	}
	return nil
}

func (s *Service) ResetUserPasswordAdmin(ctx context.Context, requesterID, targetUserID int64, newPassword string, logoutAll bool) error {
	_, target, err := s.EnsureUserInScope(ctx, requesterID, targetUserID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(newPassword) == "" {
		return apperrors.Validation("password is required")
	}
	if err := s.validateNewPassword(newPassword); err != nil {
		return err
	}
	if target.KeycloakID == nil || *target.KeycloakID == "" {
		return apperrors.Internal("User has no Keycloak ID associated")
	}
	if err := s.kcAdmin.SetPassword(ctx, s.realm, *target.KeycloakID, newPassword, false); err != nil {
		if strings.Contains(err.Error(), "400") {
			return apperrors.BadRequest("Password does not meet security requirements")
		}
		return apperrors.InternalWithErr("Failed to update Keycloak password", err)
	}
	if err := s.userRepo.SetIsFirstLogin(ctx, targetUserID, true); err != nil {
		return apperrors.InternalWithErr("Failed to mark user for password change", err)
	}
	if logoutAll {
		if err := s.LogoutAll(ctx, targetUserID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ChangePassword(ctx context.Context, userID int64, oldPassword, newPassword string, keyBackup *appservice.SaveKeyBackupRequest) error {
	if s.recovery != nil {
		return s.recovery.repo.WithUser(ctx, userID, func(c *pgxpool.Conn, u *domain.User) error {
			if err := s.recovery.repo.CheckPending(ctx, c, userID); err != nil {
				return err
			}
			if err := s.recovery.verify(ctx, u, oldPassword); err != nil {
				return err
			}
			if err := s.recovery.repo.RevokeProofs(ctx, c, userID); err != nil {
				return err
			}
			return s.changePassword(ctx, userID, oldPassword, newPassword, keyBackup, u)
		})
	}
	return s.changePassword(ctx, userID, oldPassword, newPassword, keyBackup, nil)
}
func (s *Service) changePassword(ctx context.Context, userID int64, oldPassword, newPassword string, keyBackup *appservice.SaveKeyBackupRequest, verified *domain.User) error {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || u == nil {
		return apperrors.NotFound("User not found")
	}

	if u.KeycloakID == nil || *u.KeycloakID == "" {
		return apperrors.Internal("User has no Keycloak ID associated")
	}

	if verified != nil && (verified.KeycloakID == nil || *verified.KeycloakID != *u.KeycloakID) {
		return apperrors.RecoveryInvalid()
	}
	if keyBackup == nil {
		if verified == nil {
			login := u.Username
			if login == "" {
				login = u.Email
			}
			if err := s.verifyPasswordKeycloak(ctx, login, oldPassword); err != nil {
				return err
			}
		}
		if err := recoveryPassword(newPassword); err != nil {
			return err
		}
		if err := s.setPasswordKeycloak(ctx, *u.KeycloakID, newPassword); err != nil {
			return err
		}
		_ = s.userRepo.CompleteOnboarding(ctx, userID)
		return nil
	}
	if s.keyBackupService == nil {
		return apperrors.Internal("E2EE key backup service is not configured")
	}

	keycloakID := *u.KeycloakID
	if verified == nil {
		if err := s.verifyPasswordKeycloak(ctx, u.Email, oldPassword); err != nil {
			return err
		}
	}
	if err := s.validateNewPassword(newPassword); err != nil {
		return err
	}
	if _, err := appservice.BuildStrictValidatedKeyBackup(userID, keyBackup); err != nil {
		return err
	}
	if err := s.setPasswordKeycloak(ctx, keycloakID, newPassword); err != nil {
		return err
	}
	if err := s.keyBackupService.SaveKeyBackup(ctx, userID, keyBackup); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), passwordRollbackTimeout)
		defer cancel()

		rollbackErr := s.kcAdmin.SetPassword(rollbackCtx, s.realm, keycloakID, oldPassword, false)
		if rollbackErr != nil {
			return apperrors.InternalWithErr(
				"Failed to save E2EE key backup after password change; password rollback also failed",
				errors.Join(err, rollbackErr),
			)
		}
		return apperrors.InternalWithErr("Failed to save E2EE key backup; password change was rolled back", err)
	}

	if userID > 0 {
		_ = s.userRepo.CompleteOnboarding(ctx, userID)
	}

	return nil
}

func (s *Service) ChangeAvailabilityStatus(ctx context.Context, userID int64, status string) error {
	switch status {
	case domain.StatusOnline, domain.StatusOffline, domain.StatusSick, domain.StatusVacation:
	default:
		return apperrors.BadRequest("Invalid status")
	}

	if err := s.userRepo.UpdateStatus(ctx, userID, status); err != nil {
		return apperrors.InternalWithErr("Failed to update status", err)
	}

	s.chatService.BroadcastUserStatus(ctx, userID, status)

	return nil
}

func (s *Service) GetOnlineUsers(ctx context.Context, keycloakClientID string) ([]*domain.UserResponse, error) {
	clientUUID, err := s.kcAdmin.GetClientUUIDByClientID(ctx, s.realm, keycloakClientID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to resolve Keycloak client UUID", err)
	}

	sessions, err := s.kcAdmin.ListClientUserSessions(ctx, s.realm, clientUUID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to fetch active sessions from Keycloak", err)
	}

	uniq := make(map[string]struct{}, len(sessions))
	kcIDs := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		if sess.UserID == "" {
			continue
		}
		if _, ok := uniq[sess.UserID]; ok {
			continue
		}
		uniq[sess.UserID] = struct{}{}
		kcIDs = append(kcIDs, sess.UserID)
	}

	users, err := s.userRepo.GetByKeycloakIDs(ctx, kcIDs)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to load local users by keycloak ids", err)
	}

	out := make([]*domain.UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, u.ToResponse())
	}
	return out, nil
}

func (s *Service) GetNeverLoggedInUsers(ctx context.Context) ([]*domain.UserResponse, error) {
	users, err := s.userRepo.GetNeverLoggedIn(ctx)
	if err != nil {
		return nil, apperrors.Internal("Failed to fetch users who never logged in")
	}

	res := make([]*domain.UserResponse, 0, len(users))
	for _, u := range users {
		res = append(res, u.ToResponse())
	}
	return res, nil
}

func (s *Service) ListHRProfiles(ctx context.Context, requesterID int64, filter repository.HRProfileFilter, limit, offset int) ([]*domain.UserHRProfileSummaryResponse, int, error) {
	users, total, err := s.userRepo.GetPaginated(ctx, filter.Query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	res := make([]*domain.UserHRProfileSummaryResponse, 0, len(users))
	for _, u := range users {
		pos := ""
		if u.Position != nil {
			pos = *u.Position
		}
		res = append(res, &domain.UserHRProfileSummaryResponse{
			User: u.ToResponse(),
			HRProfile: &domain.UserHRProfileSummary{
				ID:        u.ID,
				UserID:    u.ID,
				FirstName: u.FirstName,
				LastName:  u.LastName,
				JobName:   pos,
			},
		})
	}
	return res, total, nil
}

func (s *Service) GetManagementVacations(ctx context.Context) ([]*domain.ManagementVacationResponse, error) {
	if s.hrProfileRepo == nil {
		return []*domain.ManagementVacationResponse{}, nil
	}
	return s.hrProfileRepo.GetManagementVacations(ctx)
}

func (s *Service) GetHRFilterValues(ctx context.Context, requesterID int64, field string) ([]string, error) {
	return []string{}, nil
}

func (s *Service) GetHRProfileByUserID(ctx context.Context, requesterID, userID int64) (*domain.UserHRProfileResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, apperrors.NotFound("User not found")
	}

	pos := ""
	if user.Position != nil {
		pos = *user.Position
	}

	return &domain.UserHRProfileResponse{
		User: user.ToResponse(),
		HRProfile: &domain.UserHRProfile{
			ID:        user.ID,
			UserID:    user.ID,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			JobName:   pos,
		},
	}, nil
}

func (s *Service) applyHRProfileScope(ctx context.Context, requesterID int64, filter *repository.HRProfileFilter) error {
	if requesterID == 0 {
		return nil
	}

	requester, err := s.userRepo.GetByID(ctx, requesterID)
	if err != nil {
		return apperrors.InternalWithErr("Failed to load requester", err)
	}
	if requester == nil {
		return apperrors.NotFound("Requester not found")
	}
	if domain.HasGlobalUserScope(requester) {
		return nil
	}

	if requester.MinistryID != nil {
		filter.ScopeMinistryID = requester.MinistryID
		return nil
	}

	filter.ScopeUserID = &requester.ID
	return nil
}

// extractSubFromJWT parses the JWT payload without signature verification
// and returns the "sub" claim (Keycloak user UUID). Returns "" on any error.
func extractSubFromJWT(tokenStr string) string {
	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	sub, _ := claims["sub"].(string)
	return sub
}

// SyncUsersWithKeycloak synchronizes all users from DB to Keycloak.
// Missing users will be created with default parameters (including password "Qwerty12!").
func (s *Service) SyncUsersWithKeycloak(ctx context.Context) error {
	users, err := s.userRepo.GetAll(ctx)
	if err != nil {
		return err
	}

	for _, u := range users {
		if u == nil {
			continue
		}

		kcRole := string(u.Role)
		if kcRole == "" {
			kcRole = "user"
		}

		var kcID string

		if u.KeycloakID != nil && strings.TrimSpace(*u.KeycloakID) != "" {
			kcID = strings.TrimSpace(*u.KeycloakID)
			if s.kcAdmin != nil {
				rep, getErr := s.kcAdmin.GetUserByID(ctx, s.realm, kcID)
				if getErr == nil && rep != nil {
					var iinPtr *string
					if rep.Attributes != nil {
						if iins, ok := rep.Attributes["iin"]; ok && len(iins) > 0 && strings.TrimSpace(iins[0]) != "" {
							val := strings.TrimSpace(iins[0])
							iinPtr = &val
						}
					}
					kcIDCopy := kcID
					kcUser := &domain.User{
						KeycloakID: &kcIDCopy,
						IIN:        iinPtr,
						Email:      strings.ToLower(strings.TrimSpace(rep.Email)),
						Username:   strings.TrimSpace(rep.Username),
						FirstName:  strings.TrimSpace(rep.FirstName),
						LastName:   strings.TrimSpace(rep.LastName),
						IsActive:   u.IsActive,
						Role:       u.Role,
					}
					_ = s.userRepo.UpsertFromKeycloak(ctx, kcUser)
					continue
				}
			}
		}

		if s.kcAdmin == nil {
			continue
		}

		createdKcID, err := s.kcAdmin.CreateUser(ctx, s.realm, keycloak.CreateUserRequest{
			Username:      u.Username,
			Email:         u.Email,
			Enabled:       true,
			EmailVerified: true,
			FirstName:     u.FirstName,
			LastName:      u.LastName,
			Attributes:    s.makeKeycloakAttributes(ctx, u),
		})

		if err != nil {
			if strings.Contains(err.Error(), "409") {
				var reps []keycloak.UserRepresentation
				var getErr error
				if u.Email != "" {
					reps, getErr = s.kcAdmin.GetUsersByEmail(ctx, s.realm, u.Email)
				}
				if (getErr != nil || len(reps) == 0) && u.Username != "" {
					reps, getErr = s.kcAdmin.GetUsersByUsername(ctx, s.realm, u.Username)
				}
				if getErr != nil || len(reps) == 0 {
					continue
				}
				kcID = reps[0].ID
				var iinPtr *string
				if reps[0].Attributes != nil {
					if iins, ok := reps[0].Attributes["iin"]; ok && len(iins) > 0 && strings.TrimSpace(iins[0]) != "" {
						val := strings.TrimSpace(iins[0])
						iinPtr = &val
					}
				}
				kcIDCopy := kcID
				kcUser := &domain.User{
					KeycloakID: &kcIDCopy,
					IIN:        iinPtr,
					Email:      strings.ToLower(strings.TrimSpace(reps[0].Email)),
					Username:   strings.TrimSpace(reps[0].Username),
					FirstName:  strings.TrimSpace(reps[0].FirstName),
					LastName:   strings.TrimSpace(reps[0].LastName),
					IsActive:   u.IsActive,
					Role:       u.Role,
				}
				_ = s.userRepo.UpsertFromKeycloak(ctx, kcUser)

				_ = s.kcAdmin.UpdateUser(ctx, s.realm, kcID, keycloak.UserUpdateRequest{
					Username:   &u.Username,
					Attributes: s.makeKeycloakAttributes(ctx, u),
				})
			} else {
				continue
			}
		} else {
			kcID = createdKcID
		}

		if err := s.kcAdmin.SetPassword(ctx, s.realm, kcID, "Qwerty12!", false); err != nil {
			fmt.Printf("failed to set password for user %s: %v\n", u.Email, err)
		}

		if err := s.kcAdmin.SetUserRealmRoles(ctx, s.realm, kcID, []string{kcRole}); err != nil {
			fmt.Printf("failed to set roles for user %s: %v\n", u.Email, err)
		}

		if u.KeycloakID == nil || *u.KeycloakID != kcID {
			u.KeycloakID = &kcID
			_ = s.userRepo.Update(ctx, u)
		}
	}
	return nil
}

func (s *Service) FlagUsersWithDefaultPassword(ctx context.Context, rdb *redis.Client) error {
	users, err := s.userRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to get all users: %w", err)
	}

	for _, u := range users {
		if u == nil || u.Email == "" || u.IsFirstLogin {
			// Skip nil, empty email, or already flagged users
			continue
		}

		redisKey := fmt.Sprintf("default_password_checked:%d", u.ID)
		checked, err := rdb.Get(ctx, redisKey).Result()
		if err == nil && checked == "true" {
			// Already checked previously, skip
			continue
		}

		// Try logging into Keycloak with user's email and Qwerty12!
		_, verifyErr := s.kc.Password(ctx, u.Email, "Qwerty12!")
		if verifyErr == nil {
			// Password is Qwerty12!, flag user in DB
			u.IsFirstLogin = true
			if dbErr := s.userRepo.SetIsFirstLogin(ctx, u.ID, true); dbErr != nil {
				fmt.Printf("failed to update user default password flag in db for user %d: %v\n", u.ID, dbErr)
			}
			// Mark checked in Redis
			if rdbErr := rdb.Set(ctx, redisKey, "true", 0).Err(); rdbErr != nil {
				fmt.Printf("failed to set checked state in Redis for user %d: %v\n", u.ID, rdbErr)
			}
		} else {
			// If verification failed because of invalid password (unauthorized / invalid_grant / 401 / 400),
			// it means they successfully changed their password. We mark them as checked in Redis.
			// But if it's a network/connection error (like Keycloak being down), we do NOT mark them as checked
			// so we try again on the next startup.
			errStr := strings.ToLower(verifyErr.Error())
			isWrongPassword := strings.Contains(errStr, "invalid_grant") ||
				strings.Contains(errStr, "401") ||
				strings.Contains(errStr, "400") ||
				strings.Contains(errStr, "unauthorized")

			if isWrongPassword {
				if rdbErr := rdb.Set(ctx, redisKey, "true", 0).Err(); rdbErr != nil {
					fmt.Printf("failed to set checked state in Redis for user %d: %v\n", u.ID, rdbErr)
				}
			} else {
				fmt.Printf("keycloak connection error checking password for user %s: %v\n", u.Email, verifyErr)
			}
		}

		// Throttle to avoid overloading Keycloak
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	return nil
}

func (s *Service) makeKeycloakAttributes(ctx context.Context, u *domain.User) map[string][]string {
	attrs := make(map[string][]string)
	if u.IIN != nil && *u.IIN != "" {
		attrs["iin"] = []string{*u.IIN}
	}
	if u.TeamID != nil {
		attrs["team_id"] = []string{u.TeamID.String()}
	}

	// Prefer HR job_name (real position name from HR system) over u.Position (internal code).
	// This matches the SQL: COALESCE(hp.job_name, u.position, '')
	var positionName string
	if s.hrProfileRepo != nil {
		if hr, err := s.hrProfileRepo.GetByUserID(ctx, u.ID); err == nil && hr != nil && strings.TrimSpace(hr.JobName) != "" {
			positionName = strings.TrimSpace(hr.JobName)
		}
	}
	if positionName == "" && u.Position != nil && strings.TrimSpace(*u.Position) != "" {
		positionName = strings.TrimSpace(*u.Position)
	}
	if positionName != "" {
		attrs["position"] = []string{positionName}
	}

	// Fetch the optional company identifier from the corporate HR profile.
	var bin string
	if s.hrProfileRepo != nil {
		if hr, err := s.hrProfileRepo.GetByUserID(ctx, u.ID); err == nil && hr != nil && hr.BIN != "" {
			bin = hr.BIN
		}
	}
	if bin != "" {
		attrs["bin"] = []string{bin}
	}
	return attrs
}

func (s *Service) makeKeycloakAttributesFromCreateReq(ctx context.Context, req *CreateUserRequest) map[string][]string {
	attrs := make(map[string][]string)
	if req.IIN != "" {
		attrs["iin"] = []string{req.IIN}
	}
	if req.Position != "" {
		attrs["position"] = []string{req.Position}
	}
	if req.TeamID != nil {
		attrs["team_id"] = []string{req.TeamID.String()}
	}
	return attrs
}

// General profile administration can mutate platform roles; company position assignment uses its isolated endpoint.
func (s *Service) RequirePlatformUserAdministration(ctx context.Context, actor int64) error {
	user, err := s.userRepo.GetByID(ctx, actor)
	if err != nil || user == nil || !user.IsActive || user.Role != domain.RoleSuperAdmin {
		return apperrors.Forbidden("Только системный администратор может изменять учетные записи и роли платформы")
	}
	return nil
}
