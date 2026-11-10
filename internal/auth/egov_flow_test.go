package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/idp"
	"go.uber.org/zap"
)

type egovFlowClientStub struct {
	authorizeURLFn func(clientID, redirectURI, state string, scopes []string, lang string) (string, error)
	exchangeFn     func(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error)
	basicFn        func(ctx context.Context, accessToken string) (*domain.IDPUserBasicResponse, error)
	phoneFn        func(ctx context.Context, accessToken string) (*domain.IDPUserPhoneResponse, error)
	emailFn        func(ctx context.Context, accessToken string) (*domain.IDPUserEmailResponse, error)
}

func (s *egovFlowClientStub) AuthorizeURL(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
	return s.authorizeURLFn(clientID, redirectURI, state, scopes, lang)
}
func (s *egovFlowClientStub) ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
	return s.exchangeFn(ctx, code, redirectURI)
}
func (s *egovFlowClientStub) GetUserBasic(ctx context.Context, accessToken string) (*domain.IDPUserBasicResponse, error) {
	return s.basicFn(ctx, accessToken)
}
func (s *egovFlowClientStub) GetUserPhone(ctx context.Context, accessToken string) (*domain.IDPUserPhoneResponse, error) {
	return s.phoneFn(ctx, accessToken)
}
func (s *egovFlowClientStub) GetUserEmail(ctx context.Context, accessToken string) (*domain.IDPUserEmailResponse, error) {
	return s.emailFn(ctx, accessToken)
}

type egovUserRepoStub struct {
	byID    map[int64]*domain.User
	byIIN   map[string]*domain.User
	byEmail map[string]*domain.User
	nextID  int64
}

func newEgovUserRepoStub() *egovUserRepoStub {
	return &egovUserRepoStub{
		byID:    make(map[int64]*domain.User),
		byIIN:   make(map[string]*domain.User),
		byEmail: make(map[string]*domain.User),
		nextID:  100,
	}
}

func (r *egovUserRepoStub) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	if user, ok := r.byEmail[email]; ok {
		return cloneUser(user), nil
	}
	return nil, errors.New("user not found")
}
func (r *egovUserRepoStub) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if user, ok := r.byID[id]; ok {
		return cloneUser(user), nil
	}
	return nil, errors.New("user not found")
}
func (r *egovUserRepoStub) GetByIIN(_ context.Context, iin string) (*domain.User, error) {
	if user, ok := r.byIIN[iin]; ok {
		return cloneUser(user), nil
	}
	return nil, errors.New("user not found")
}
func (r *egovUserRepoStub) GetByKeycloakID(_ context.Context, _ string) (*domain.User, error) {
	return nil, errors.New("user not found")
}
func (r *egovUserRepoStub) UpsertFromKeycloak(_ context.Context, _ *domain.User) error { return nil }
func (r *egovUserRepoStub) HasRegisteredMobileDevice(_ context.Context, _ int64) (bool, error) {
	return false, nil
}
func (r *egovUserRepoStub) UpdateLastLogin(_ context.Context, userID int64, loginAt time.Time) error {
	if user, ok := r.byID[userID]; ok {
		user.LastLoginAt = &loginAt
	}
	return nil
}
func (r *egovUserRepoStub) CompleteOnboarding(_ context.Context, _ int64) error { return nil }
func (r *egovUserRepoStub) SetIsFirstLogin(_ context.Context, userID int64, isFirst bool) error {
	if user, ok := r.byID[userID]; ok {
		user.IsFirstLogin = isFirst
	}
	return nil
}
func (r *egovUserRepoStub) Update(_ context.Context, user *domain.User) error {
	r.store(user)
	return nil
}
func (r *egovUserRepoStub) GetAll(_ context.Context) ([]*domain.User, error) { return nil, nil }
func (r *egovUserRepoStub) GetPaginated(_ context.Context, _ string, _, _ int, _ ...domain.UserFilters) ([]*domain.User, int, error) {
	return nil, 0, nil
}
func (r *egovUserRepoStub) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, errors.New("user not found")
}
func (r *egovUserRepoStub) GetFullDetailsByID(_ context.Context, _ int64) (*domain.UserDetailResponse, error) {
	return nil, nil
}
func (r *egovUserRepoStub) UpdateStatus(_ context.Context, _ int64, _ string) error { return nil }
func (r *egovUserRepoStub) GetByKeycloakIDs(_ context.Context, _ []string) ([]*domain.User, error) {
	return nil, nil
}
func (r *egovUserRepoStub) GetNeverLoggedIn(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}
func (r *egovUserRepoStub) ShareConversation(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *egovUserRepoStub) ExportMinistryUsers(_ context.Context, _ int64, _ []int64, _ []int64, _ []int64) ([]domain.MinistryUserExportRow, error) {
	return nil, nil
}
func (r *egovUserRepoStub) GetMinistryName(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (r *egovUserRepoStub) SoftDelete(_ context.Context, _ int64) error {
	return nil
}
func (r *egovUserRepoStub) UpdateRole(_ context.Context, _ int64, _ domain.Role) error {
	return nil
}
func (r *egovUserRepoStub) UpdateMinistryID(_ context.Context, _, _ int64) error {
	return nil
}
func (r *egovUserRepoStub) Create(_ context.Context, user *domain.User) error {
	user.ID = r.nextID
	r.nextID++
	r.store(user)
	return nil
}
func (r *egovUserRepoStub) store(user *domain.User) {
	copied := cloneUser(user)
	r.byID[copied.ID] = copied
	if copied.IIN != nil {
		r.byIIN[*copied.IIN] = copied
	}
	r.byEmail[copied.Email] = copied
}

type egovProfileRepoStub struct {
	byUserID map[int64]*domain.EgovProfile
	byPhone  map[string]*domain.EgovProfile
}

func newEgovProfileRepoStub() *egovProfileRepoStub {
	return &egovProfileRepoStub{
		byUserID: make(map[int64]*domain.EgovProfile),
		byPhone:  make(map[string]*domain.EgovProfile),
	}
}

func (r *egovProfileRepoStub) GetByUserID(_ context.Context, userID int64) (*domain.EgovProfile, error) {
	if profile, ok := r.byUserID[userID]; ok {
		copyValue := *profile
		return &copyValue, nil
	}
	return nil, errors.New("egov profile not found")
}
func (r *egovProfileRepoStub) GetByPhone(_ context.Context, phone string) (*domain.EgovProfile, error) {
	if profile, ok := r.byPhone[phone]; ok {
		copyValue := *profile
		return &copyValue, nil
	}
	return nil, errors.New("egov profile not found")
}
func (r *egovProfileRepoStub) Upsert(_ context.Context, profile *domain.EgovProfile) error {
	copyValue := *profile
	r.byUserID[profile.UserID] = &copyValue
	if profile.Phone != "" {
		r.byPhone[profile.Phone] = &copyValue
	}
	return nil
}

func TestEgovFlowStartAndCallbackSuccess(t *testing.T) {
	t.Parallel()

	userRepo := newEgovUserRepoStub()
	profileRepo := newEgovProfileRepoStub()
	authService := &Service{
		userRepo:        userRepo,
		egovProfileRepo: profileRepo,
	}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  15 * time.Minute,
		RefreshTokenExpiry: 24 * time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)

	client := &egovFlowClientStub{
		authorizeURLFn: func(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state + "&redirect_uri=" + url.QueryEscape(redirectURI), nil
		},
		exchangeFn: func(_ context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
			if redirectURI != "https://api.example.kz/api/v1/auth/egov/callback" {
				t.Fatalf("unexpected redirect uri: %s", redirectURI)
			}
			return &domain.IDPTokenResponse{
				AccessToken:  "idp-access",
				RefreshToken: "idp-refresh",
				TokenType:    "Bearer",
				Scope:        "user:basic:read user:phone:read user:email:read",
			}, nil
		},
		basicFn: func(_ context.Context, _ string) (*domain.IDPUserBasicResponse, error) {
			return &domain.IDPUserBasicResponse{
				Person: domain.IDPPerson{
					IIN:        "990101300000",
					Surname:    "Иванов",
					Name:       "Иван",
					Patronymic: "Иванович",
				},
				Level: domain.IDPAuthLevelPassword,
				Type:  domain.IDPUserTypeIndividual,
			}, nil
		},
		phoneFn: func(_ context.Context, _ string) (*domain.IDPUserPhoneResponse, error) {
			return &domain.IDPUserPhoneResponse{Phone: "77070000000"}, nil
		},
		emailFn: func(_ context.Context, _ string) (*domain.IDPUserEmailResponse, error) {
			return &domain.IDPUserEmailResponse{Email: "test@test.kz"}, nil
		},
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic, egovScopePhone, egovScopeEmail},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, err := flow.Start(context.Background(), "ru", "/web/")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	parsedURL, err := url.Parse(startResp.RedirectURL)
	if err != nil {
		t.Fatalf("parse redirect url: %v", err)
	}
	state := parsedURL.Query().Get("state")
	if state == "" {
		t.Fatal("expected state in redirect url")
	}

	callbackResp, redirectTarget, err := flow.Complete(context.Background(), "auth-code", state, "", "")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if redirectTarget != "/web/" {
		t.Fatalf("unexpected redirect target: %s", redirectTarget)
	}
	if callbackResp.AccessToken == "" || callbackResp.RefreshToken == "" {
		t.Fatal("expected local tokens")
	}
	if callbackResp.AccessToken == "idp-access" || callbackResp.RefreshToken == "idp-refresh" {
		t.Fatal("expected callback response to expose only local app tokens")
	}
	if !callbackResp.IsNewUser {
		t.Fatal("expected new user")
	}
	if callbackResp.User == nil || callbackResp.User.IIN != "990101300000" {
		t.Fatalf("unexpected user: %#v", callbackResp.User)
	}
	if _, err := userRepo.GetByIIN(context.Background(), "990101300000"); err != nil {
		t.Fatalf("expected user stored by iin: %v", err)
	}
}

func TestEgovFlowStartAllowsTestHTTPBaseURL(t *testing.T) {
	t.Parallel()

	authService := &Service{userRepo: newEgovUserRepoStub()}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  15 * time.Minute,
		RefreshTokenExpiry: 24 * time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)

	client := &egovFlowClientStub{
		authorizeURLFn: func(_ string, redirectURI, state string, scopes []string, lang string) (string, error) {
			if redirectURI != "https://api.example.kz/api/v1/auth/egov/callback" {
				t.Fatalf("unexpected redirect uri: %s", redirectURI)
			}
			if state == "" {
				t.Fatal("expected state")
			}
			return "http://test.idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) { return nil, nil },
		basicFn:    func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn:    func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn:    func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "http://test.idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
		Environment:   "test",
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, err := flow.Start(context.Background(), "ru", "/web/")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.HasPrefix(startResp.RedirectURL, "http://test.idp.egov.kz/idp/oauth/authorize") {
		t.Fatalf("unexpected redirect url: %s", startResp.RedirectURL)
	}
}

func TestEgovFlowStartNormalizesCommaSeparatedScopes(t *testing.T) {
	t.Parallel()

	authService := &Service{userRepo: newEgovUserRepoStub()}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  15 * time.Minute,
		RefreshTokenExpiry: 24 * time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)

	client := &egovFlowClientStub{
		authorizeURLFn: func(_ string, _ string, state string, scopes []string, lang string) (string, error) {
			wantScopes := []string{egovScopeBasic, egovScopePhone, egovScopeEmail}
			if len(scopes) != len(wantScopes) {
				t.Fatalf("unexpected scopes: %#v", scopes)
			}
			for i := range wantScopes {
				if scopes[i] != wantScopes[i] {
					t.Fatalf("unexpected scope at index %d: got %q want %q", i, scopes[i], wantScopes[i])
				}
			}
			values := url.Values{}
			values.Set("state", state)
			values.Set("lang", lang)
			values.Set("scope", strings.Join(scopes, " "))
			return "https://idp.egov.kz/idp/oauth/authorize?" + values.Encode(), nil
		},
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			t.Fatal("exchange should not be called")
			return nil, nil
		},
		basicFn: func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn: func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn: func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{"user:basic:read,user:phone:read,user:email:read"},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, err := flow.Start(context.Background(), "", "/web/")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(startResp.RedirectURL, "scope=user%3Abasic%3Aread") {
		t.Fatalf("expected encoded scope in redirect url, got %s", startResp.RedirectURL)
	}
	if strings.Contains(startResp.RedirectURL, "%2C") {
		t.Fatalf("unexpected comma-separated scope encoding in redirect url: %s", startResp.RedirectURL)
	}
}

func TestEgovFlowRejectsReusedState(t *testing.T) {
	t.Parallel()

	store := NewInMemoryEgovStateStore()
	err := store.Save(context.Background(), &EgovAuthState{State: "state-1"}, time.Minute)
	if err != nil {
		t.Fatalf("save state: %v", err)
	}
	if _, err := store.Consume(context.Background(), "state-1"); err != nil {
		t.Fatalf("consume state: %v", err)
	}
	if _, err := store.Consume(context.Background(), "state-1"); err == nil {
		t.Fatal("expected reused state to fail")
	}
}

func TestEgovFlowRejectsExpiredState(t *testing.T) {
	t.Parallel()

	store := NewInMemoryEgovStateStore()
	err := store.Save(context.Background(), &EgovAuthState{State: "state-1"}, time.Nanosecond)
	if err != nil {
		t.Fatalf("save state: %v", err)
	}
	time.Sleep(time.Millisecond)
	if _, err := store.Consume(context.Background(), "state-1"); err == nil {
		t.Fatal("expected expired state to fail")
	}
}

func TestEgovStateStoreClassifiesReuseAndExpiry(t *testing.T) {
	t.Parallel()

	store := NewInMemoryEgovStateStore()
	if err := store.Save(context.Background(), &EgovAuthState{State: "state-1"}, time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if _, err := store.Consume(context.Background(), "state-1"); err != nil {
		t.Fatalf("consume state: %v", err)
	}
	if _, err := store.Consume(context.Background(), "state-1"); !errors.Is(err, ErrEgovStateReused) {
		t.Fatalf("expected reused error, got %#v", err)
	}
}

func TestEgovStateStoreClassifiesNotFound(t *testing.T) {
	t.Parallel()

	store := NewInMemoryEgovStateStore()
	if _, err := store.Consume(context.Background(), "missing"); !errors.Is(err, ErrEgovStateNotFound) {
		t.Fatalf("expected not found error, got %#v", err)
	}
}

func TestEgovFlowLangWhitelist(t *testing.T) {
	t.Parallel()
	if _, err := validateEgovLang("de", "ru"); err == nil {
		t.Fatal("expected invalid lang to fail")
	} else {
		var appErr *apperrors.AppError
		if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_LANG" {
			t.Fatalf("unexpected error: %#v", err)
		}
	}
}

func TestValidateEgovBaseURLProdHTTPSAllowed(t *testing.T) {
	t.Parallel()

	if err := validateEgovBaseURL("https://idp.egov.kz", "prod"); err != nil {
		t.Fatalf("expected prod https base url to pass: %v", err)
	}
}

func TestValidateEgovBaseURLProdHTTPRejected(t *testing.T) {
	t.Parallel()

	if err := validateEgovBaseURL("http://idp.egov.kz", "prod"); err == nil {
		t.Fatal("expected prod http base url to fail")
	}
}

func TestValidateEgovBaseURLTestHTTPAllowed(t *testing.T) {
	t.Parallel()

	if err := validateEgovBaseURL("http://test.idp.egov.kz", "test"); err != nil {
		t.Fatalf("expected test http base url to pass: %v", err)
	}
}

func TestValidateEgovBaseURLTestWithPathRejected(t *testing.T) {
	t.Parallel()

	if err := validateEgovBaseURL("http://test.idp.egov.kz/idp/sign-in", "test"); err == nil {
		t.Fatal("expected test base url with path to fail")
	}
}

func TestValidateEgovBaseURLEvilHostRejected(t *testing.T) {
	t.Parallel()

	if err := validateEgovBaseURL("http://evil.example.kz", "test"); err == nil {
		t.Fatal("expected evil host to fail")
	}
}

func TestEgovFlowCompleteRequiresState(t *testing.T) {
	t.Parallel()

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, &Service{userRepo: newEgovUserRepoStub()}, NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  time.Minute,
		RefreshTokenExpiry: time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil), zap.NewNop(), NewInMemoryEgovStateStore(), &egovFlowClientStub{
		authorizeURLFn: func(string, string, string, []string, string) (string, error) { return "", nil },
		exchangeFn:     func(context.Context, string, string) (*domain.IDPTokenResponse, error) { return nil, nil },
		basicFn:        func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn:        func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn:        func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	})

	_, _, err := flow.Complete(context.Background(), "test-code", "", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_CALLBACK" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestEgovFlowCompleteRequiresCode(t *testing.T) {
	t.Parallel()

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, &Service{userRepo: newEgovUserRepoStub()}, NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  time.Minute,
		RefreshTokenExpiry: time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil), zap.NewNop(), NewInMemoryEgovStateStore(), &egovFlowClientStub{
		authorizeURLFn: func(string, string, string, []string, string) (string, error) { return "", nil },
		exchangeFn:     func(context.Context, string, string) (*domain.IDPTokenResponse, error) { return nil, nil },
		basicFn:        func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn:        func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn:        func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	})

	_, _, err := flow.Complete(context.Background(), "", "callback-state", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_CALLBACK" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestEgovFlowAccessDeniedSkipsTokenExchange(t *testing.T) {
	t.Parallel()

	var exchangeCalled bool
	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, &Service{userRepo: newEgovUserRepoStub()}, NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  time.Minute,
		RefreshTokenExpiry: time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil), zap.NewNop(), NewInMemoryEgovStateStore(), &egovFlowClientStub{
		authorizeURLFn: func(string, string, string, []string, string) (string, error) { return "", nil },
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			exchangeCalled = true
			return nil, nil
		},
		basicFn: func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn: func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn: func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	})

	_, _, err := flow.Complete(context.Background(), "", "state-1", "access_denied", "User denied access")
	if err == nil {
		t.Fatal("expected error")
	}
	if exchangeCalled {
		t.Fatal("expected token exchange to be skipped")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_ACCESS_DENIED" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestEgovFlowTokenExchangeInvalidGrantMapsToInvalidGrant(t *testing.T) {
	t.Parallel()

	authService := &Service{userRepo: newEgovUserRepoStub()}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  time.Minute,
		RefreshTokenExpiry: time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)

	client := &egovFlowClientStub{
		authorizeURLFn: func(_ string, _ string, state string, _ []string, _ string) (string, error) {
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			return nil, &idp.APIError{
				StatusCode:       http.StatusBadRequest,
				ErrorCode:        "invalid_grant",
				ErrorDescription: "authorization code expired",
			}
		},
		basicFn: func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn: func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn: func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, err := flow.Start(context.Background(), "ru", "/web/")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	stateURL, _ := url.Parse(startResp.RedirectURL)
	state := stateURL.Query().Get("state")

	_, _, err = flow.Complete(context.Background(), "auth-code", state, "", "")
	if err == nil {
		t.Fatal("expected invalid grant error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_GRANT" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestEgovFlowTokenExchangeUnauthorizedMapsToInvalidClient(t *testing.T) {
	t.Parallel()

	authService := &Service{userRepo: newEgovUserRepoStub()}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  time.Minute,
		RefreshTokenExpiry: time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)

	client := &egovFlowClientStub{
		authorizeURLFn: func(_ string, _ string, state string, _ []string, _ string) (string, error) {
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			return nil, &idp.APIError{
				StatusCode:       http.StatusUnauthorized,
				ErrorCode:        "Unauthorized",
				ErrorDescription: "Unauthorized",
			}
		},
		basicFn: func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn: func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn: func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "http://test.idp.egov.kz",
		ClientID:      "alem-egov-idp",
		ClientSecret:  "secret",
		RedirectURL:   "https://alem-workspace.gov.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
		Environment:   "test",
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, err := flow.Start(context.Background(), "ru", "/web/")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	stateURL, _ := url.Parse(startResp.RedirectURL)
	state := stateURL.Query().Get("state")

	_, _, err = flow.Complete(context.Background(), "auth-code", state, "", "")
	if err == nil {
		t.Fatal("expected invalid client error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_CLIENT" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestEgovFlowTokenExchangeRedirectMismatchMapsToSpecificError(t *testing.T) {
	t.Parallel()

	authService := &Service{userRepo: newEgovUserRepoStub()}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  time.Minute,
		RefreshTokenExpiry: time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)

	client := &egovFlowClientStub{
		authorizeURLFn: func(_ string, _ string, state string, _ []string, _ string) (string, error) {
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			return nil, &idp.APIError{
				StatusCode:       http.StatusBadRequest,
				ErrorCode:        "invalid_grant",
				ErrorDescription: "redirect uri mismatch",
			}
		},
		basicFn: func(context.Context, string) (*domain.IDPUserBasicResponse, error) { return nil, nil },
		phoneFn: func(context.Context, string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn: func(context.Context, string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, err := flow.Start(context.Background(), "ru", "/web/")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	stateURL, _ := url.Parse(startResp.RedirectURL)
	state := stateURL.Query().Get("state")

	_, _, err = flow.Complete(context.Background(), "auth-code", state, "", "")
	if err == nil {
		t.Fatal("expected redirect mismatch error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_REDIRECT_MISMATCH" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestEgovFlowTokenExchangeRedirectMatchesAuthorizeRedirect(t *testing.T) {
	t.Parallel()

	authService := &Service{
		userRepo:        newEgovUserRepoStub(),
		egovProfileRepo: newEgovProfileRepoStub(),
	}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  15 * time.Minute,
		RefreshTokenExpiry: 24 * time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)

	var authorizeRedirectURI string
	var exchangeRedirectURI string
	client := &egovFlowClientStub{
		authorizeURLFn: func(_ string, redirectURI, state string, _ []string, _ string) (string, error) {
			authorizeRedirectURI = redirectURI
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state + "&redirect_uri=" + url.QueryEscape(redirectURI), nil
		},
		exchangeFn: func(_ context.Context, _ string, redirectURI string) (*domain.IDPTokenResponse, error) {
			exchangeRedirectURI = redirectURI
			return &domain.IDPTokenResponse{
				AccessToken: "idp-access",
				TokenType:   "Bearer",
				Scope:       egovScopeBasic,
			}, nil
		},
		basicFn: func(_ context.Context, _ string) (*domain.IDPUserBasicResponse, error) {
			return &domain.IDPUserBasicResponse{
				Person: domain.IDPPerson{
					IIN:     "990101300000",
					Surname: "Иванов",
					Name:    "Иван",
				},
				Level: domain.IDPAuthLevelPassword,
				Type:  domain.IDPUserTypeIndividual,
			}, nil
		},
		phoneFn: func(_ context.Context, _ string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn: func(_ context.Context, _ string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "alem-egov-idp",
		ClientSecret:  "secret",
		RedirectURL:   "https://alem-workspace.gov.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, err := flow.Start(context.Background(), "ru", "/web/")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	stateURL, _ := url.Parse(startResp.RedirectURL)
	state := stateURL.Query().Get("state")

	if _, _, err = flow.Complete(context.Background(), "auth-code", state, "", ""); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if authorizeRedirectURI == "" {
		t.Fatal("expected authorize redirect uri to be captured")
	}
	if exchangeRedirectURI == "" {
		t.Fatal("expected exchange redirect uri to be captured")
	}
	if exchangeRedirectURI != authorizeRedirectURI {
		t.Fatalf("redirect uri mismatch: authorize=%s exchange=%s", authorizeRedirectURI, exchangeRedirectURI)
	}
}

func TestEgovFlowMissingIINFails(t *testing.T) {
	t.Parallel()

	_, err := normalizeEgovProfile(&domain.IDPUserBasicResponse{
		Person: domain.IDPPerson{Name: "Иван", Surname: "Иванов"},
		Level:  domain.IDPAuthLevelPassword,
		Type:   domain.IDPUserTypeIndividual,
	}, "", "")
	if err == nil {
		t.Fatal("expected missing iin error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_IIN_MISSING" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestEgovFlowLegalMappingRequiresOrganization(t *testing.T) {
	t.Parallel()

	profile, err := normalizeEgovProfile(&domain.IDPUserBasicResponse{
		Person: domain.IDPPerson{IIN: "990101300000"},
		Organization: &domain.IDPOrganization{
			BIN:    "123456789012",
			NameRu: "Организация",
			NameKk: "Ұйым",
		},
		Level: domain.IDPAuthLevelCertificate,
		Type:  domain.IDPUserTypeLegal,
	}, "", "")
	if err != nil {
		t.Fatalf("normalize profile: %v", err)
	}
	if profile.BIN != "123456789012" || profile.UserType != string(domain.IDPUserTypeLegal) {
		t.Fatalf("unexpected legal profile: %#v", profile)
	}
}

func TestEgovFlowPhoneInsufficientScopeIsPartialSuccess(t *testing.T) {
	t.Parallel()

	userRepo := newEgovUserRepoStub()
	authService := &Service{
		userRepo: userRepo,
	}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  15 * time.Minute,
		RefreshTokenExpiry: 24 * time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)
	client := &egovFlowClientStub{
		authorizeURLFn: func(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
		exchangeFn: func(_ context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
			return &domain.IDPTokenResponse{AccessToken: "idp-access", Scope: "user:basic:read user:phone:read"}, nil
		},
		basicFn: func(_ context.Context, _ string) (*domain.IDPUserBasicResponse, error) {
			return &domain.IDPUserBasicResponse{
				Person: domain.IDPPerson{IIN: "990101300000", Name: "Иван", Surname: "Иванов"},
				Level:  domain.IDPAuthLevelPassword,
				Type:   domain.IDPUserTypeIndividual,
			}, nil
		},
		phoneFn: func(_ context.Context, _ string) (*domain.IDPUserPhoneResponse, error) {
			return nil, &idp.APIError{StatusCode: http.StatusForbidden, ErrorCode: "insufficient_scope", RequiredScope: egovScopePhone}
		},
		emailFn: func(_ context.Context, _ string) (*domain.IDPUserEmailResponse, error) {
			return nil, nil
		},
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic, egovScopePhone},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, _ := flow.Start(context.Background(), "ru", "/web/")
	stateURL, _ := url.Parse(startResp.RedirectURL)
	state := stateURL.Query().Get("state")

	callbackResp, _, err := flow.Complete(context.Background(), "auth-code", state, "", "")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if callbackResp.User == nil {
		t.Fatal("expected user response")
	}
	if callbackResp.User.Phone != "" {
		t.Fatalf("expected empty phone on insufficient scope, got %q", callbackResp.User.Phone)
	}
}

func TestEgovFlowPhoneNotFoundIsPartialSuccess(t *testing.T) {
	t.Parallel()

	userRepo := newEgovUserRepoStub()
	authService := &Service{
		userRepo: userRepo,
	}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  15 * time.Minute,
		RefreshTokenExpiry: 24 * time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)
	client := &egovFlowClientStub{
		authorizeURLFn: func(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
		exchangeFn: func(_ context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
			return &domain.IDPTokenResponse{AccessToken: "idp-access", Scope: "user:basic:read user:phone:read"}, nil
		},
		basicFn: func(_ context.Context, _ string) (*domain.IDPUserBasicResponse, error) {
			return &domain.IDPUserBasicResponse{
				Person: domain.IDPPerson{IIN: "990101300000", Name: "Иван", Surname: "Иванов"},
				Level:  domain.IDPAuthLevelPassword,
				Type:   domain.IDPUserTypeIndividual,
			}, nil
		},
		phoneFn: func(_ context.Context, _ string) (*domain.IDPUserPhoneResponse, error) {
			return nil, &idp.APIError{StatusCode: http.StatusNotFound, ErrorCode: "Not Found", ErrorDescription: "Phone number not found"}
		},
		emailFn: func(_ context.Context, _ string) (*domain.IDPUserEmailResponse, error) {
			return nil, nil
		},
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic, egovScopePhone},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, _ := flow.Start(context.Background(), "ru", "/web/")
	stateURL, _ := url.Parse(startResp.RedirectURL)
	state := stateURL.Query().Get("state")

	callbackResp, _, err := flow.Complete(context.Background(), "auth-code", state, "", "")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if callbackResp.User == nil {
		t.Fatal("expected user response")
	}
	if callbackResp.User.Phone != "" {
		t.Fatalf("expected empty phone on not found, got %q", callbackResp.User.Phone)
	}
}

func TestEgovFlowInvalidTokenFails(t *testing.T) {
	t.Parallel()

	userRepo := newEgovUserRepoStub()
	authService := &Service{userRepo: userRepo}
	tokenManager := NewTokenManager(&config.JWTConfig{
		Secret:             "secret",
		AccessTokenExpiry:  15 * time.Minute,
		RefreshTokenExpiry: 24 * time.Hour,
		Issuer:             "test-issuer",
		Audience:           "test-aud",
	}, nil)
	client := &egovFlowClientStub{
		authorizeURLFn: func(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
		exchangeFn: func(_ context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
			return &domain.IDPTokenResponse{AccessToken: "idp-access", Scope: "user:basic:read"}, nil
		},
		basicFn: func(_ context.Context, _ string) (*domain.IDPUserBasicResponse, error) {
			return nil, &idp.APIError{StatusCode: http.StatusUnauthorized, ErrorCode: "invalid_token"}
		},
		phoneFn: func(_ context.Context, _ string) (*domain.IDPUserPhoneResponse, error) { return nil, nil },
		emailFn: func(_ context.Context, _ string) (*domain.IDPUserEmailResponse, error) { return nil, nil },
	}

	flow := NewEgovFlowServiceWithClient(config.IDPConfig{
		BaseURL:       "https://idp.egov.kz",
		ClientID:      "cid",
		ClientSecret:  "secret",
		RedirectURL:   "https://api.example.kz/api/v1/auth/egov/callback",
		DefaultScopes: []string{egovScopeBasic},
		DefaultLang:   "ru",
		StateTTL:      10 * time.Minute,
	}, authService, tokenManager, zap.NewNop(), NewInMemoryEgovStateStore(), client)

	startResp, _ := flow.Start(context.Background(), "ru", "/web/")
	stateURL, _ := url.Parse(startResp.RedirectURL)
	state := stateURL.Query().Get("state")

	_, _, err := flow.Complete(context.Background(), "auth-code", state, "", "")
	if err == nil {
		t.Fatal("expected invalid token error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_TOKEN" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestLoginOrRegisterFromEgovConflictingEmailFails(t *testing.T) {
	t.Parallel()

	userRepo := newEgovUserRepoStub()
	existingIIN := "111111111111"
	userRepo.store(&domain.User{
		ID:        1,
		IIN:       &existingIIN,
		Email:     "test@test.kz",
		Username:  "existing",
		FirstName: "Old",
		LastName:  "User",
		IsActive:  true,
		Role:      domain.RoleUser,
	})
	service := &Service{
		userRepo: userRepo,
	}

	_, err := service.LoginOrRegisterFromEgov(context.Background(), &domain.IDPUserBasicResponse{
		Person: domain.IDPPerson{IIN: "990101300000", Name: "Иван", Surname: "Иванов"},
		Level:  domain.IDPAuthLevelPassword,
		Type:   domain.IDPUserTypeIndividual,
	}, "", "test@test.kz")
	if err == nil {
		t.Fatal("expected conflict")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_USER_CONFLICT" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestLoginOrRegisterFromEgovLegalNotSupported(t *testing.T) {
	t.Parallel()

	service := &Service{
		userRepo: newEgovUserRepoStub(),
	}

	_, err := service.LoginOrRegisterFromEgov(context.Background(), &domain.IDPUserBasicResponse{
		Person: domain.IDPPerson{IIN: "990101300000"},
		Organization: &domain.IDPOrganization{
			BIN:    "123456789012",
			NameRu: "Организация",
			NameKk: "Ұйым",
		},
		Level: domain.IDPAuthLevelCertificate,
		Type:  domain.IDPUserTypeLegal,
	}, "", "")
	if err == nil {
		t.Fatal("expected legal not supported error")
	}

	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_LEGAL_NOT_SUPPORTED" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestLoginOrRegisterFromEgovNormalizesUppercaseNames(t *testing.T) {
	t.Parallel()

	userRepo := newEgovUserRepoStub()
	service := &Service{
		userRepo: userRepo,
	}

	result, err := service.LoginOrRegisterFromEgov(context.Background(), &domain.IDPUserBasicResponse{
		Person: domain.IDPPerson{
			IIN:        "990101300000",
			Surname:    "АБЫЛХАЙЫРОВ",
			Name:       "АСКАР",
			Patronymic: "СЕРИКУЛЫ",
		},
		Level: domain.IDPAuthLevelPassword,
		Type:  domain.IDPUserTypeIndividual,
	}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.User.LastName != "Абылхайыров" {
		t.Fatalf("last name = %q, want %q", result.User.LastName, "Абылхайыров")
	}
	if result.User.FirstName != "Аскар" {
		t.Fatalf("first name = %q, want %q", result.User.FirstName, "Аскар")
	}
	if result.Profile.Patronymic != "Серикулы" {
		t.Fatalf("patronymic = %q, want %q", result.Profile.Patronymic, "Серикулы")
	}
}

func TestNormalizePersonNamePreservesMixedCaseInput(t *testing.T) {
	t.Parallel()

	got := normalizePersonName("  Абылхайыров-Аскар  ")
	if got != "Абылхайыров-Аскар" {
		t.Fatalf("normalizePersonName() = %q, want %q", got, "Абылхайыров-Аскар")
	}
}

func cloneUser(user *domain.User) *domain.User {
	if user == nil {
		return nil
	}
	copyValue := *user
	if user.IIN != nil {
		iinCopy := *user.IIN
		copyValue.IIN = &iinCopy
	}
	return &copyValue
}
