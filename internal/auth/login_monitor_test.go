package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type loginMonitorTestUserRepo struct {
	user *domain.User
}

func (s *loginMonitorTestUserRepo) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	if s.user != nil && strings.EqualFold(s.user.Email, email) {
		return s.user, nil
	}
	return nil, nil
}

func (s *loginMonitorTestUserRepo) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if s.user != nil && s.user.ID == id {
		return s.user, nil
	}
	return nil, nil
}

func (s *loginMonitorTestUserRepo) GetByIIN(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *loginMonitorTestUserRepo) GetByKeycloakID(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *loginMonitorTestUserRepo) UpsertFromKeycloak(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *loginMonitorTestUserRepo) HasRegisteredMobileDevice(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

func (s *loginMonitorTestUserRepo) UpdateLastLogin(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *loginMonitorTestUserRepo) CompleteOnboarding(_ context.Context, _ int64) error {
	return nil
}

func (s *loginMonitorTestUserRepo) SetIsFirstLogin(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (s *loginMonitorTestUserRepo) Update(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *loginMonitorTestUserRepo) GetAll(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *loginMonitorTestUserRepo) GetPaginated(_ context.Context, _ string, _, _ int, _ ...domain.UserFilters) ([]*domain.User, int, error) {
	return nil, 0, nil
}

func (s *loginMonitorTestUserRepo) GetByUsername(_ context.Context, username string) (*domain.User, error) {
	if s.user != nil && s.user.Username == username {
		return s.user, nil
	}
	return nil, nil
}

func (s *loginMonitorTestUserRepo) GetFullDetailsByID(_ context.Context, _ int64) (*domain.UserDetailResponse, error) {
	return nil, nil
}

func (s *loginMonitorTestUserRepo) UpdateStatus(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *loginMonitorTestUserRepo) GetByKeycloakIDs(_ context.Context, _ []string) ([]*domain.User, error) {
	return nil, nil
}

func (s *loginMonitorTestUserRepo) GetNeverLoggedIn(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *loginMonitorTestUserRepo) ExportMinistryUsers(_ context.Context, _ int64, _ []int64, _ []int64, _ []int64) ([]domain.MinistryUserExportRow, error) {
	return nil, nil
}
func (s *loginMonitorTestUserRepo) GetMinistryName(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (s *loginMonitorTestUserRepo) SoftDelete(_ context.Context, _ int64) error {
	return nil
}
func (s *loginMonitorTestUserRepo) UpdateRole(_ context.Context, _ int64, _ domain.Role) error {
	return nil
}
func (s *loginMonitorTestUserRepo) UpdateMinistryID(_ context.Context, _, _ int64) error {
	return nil
}

type loginMonitorTestReviewRepo struct{}

func (loginMonitorTestReviewRepo) GetMonthlyStats(_ context.Context, _ int64) (*domain.UserReviewStats, error) {
	return nil, nil
}

func (loginMonitorTestReviewRepo) HasReviewedThisMonth(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}

type loginMonitorStub struct {
	blocked bool
	events  []LoginEvent
	mu      sync.Mutex
	ch      chan struct{}
}

func (s *loginMonitorStub) CheckBlocked(_ context.Context, _ int64, _ string) (bool, error) {
	return s.blocked, nil
}

func (s *loginMonitorStub) NotifyLogin(_ context.Context, event LoginEvent) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	if s.ch != nil {
		select {
		case s.ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func TestLoginBlockedByMonitor(t *testing.T) {
	keycloakSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realms/test-realm/protocol/openid-connect/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if r.Form.Get("grant_type") != "password" {
			t.Fatalf("unexpected grant_type = %q", r.Form.Get("grant_type"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"token_type":    "Bearer",
			"expires_in":    300,
		})
	}))
	defer keycloakSrv.Close()

	parsedURL, err := url.Parse(keycloakSrv.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	monitor := &loginMonitorStub{blocked: true, ch: make(chan struct{}, 1)}
	user := &domain.User{
		ID:        42,
		Email:     "user@example.com",
		Username:  "user77",
		FirstName: "Ivan",
		LastName:  "Petrov",
		IsActive:  true,
	}

	svc := NewKeycloakService(
		&loginMonitorTestUserRepo{user: user},
		loginMonitorTestReviewRepo{},
		nil,
		keycloak.New(parsedURL.Scheme+"://"+parsedURL.Host, "test-realm", "client-id", "client-secret"),
		nil,
		"test-realm",
		WithLoginMonitor(monitor),
	)

	_, err = svc.Login(WithLoginRequestMeta(context.Background(), LoginRequestMeta{
		Source:    "web",
		IP:        "127.0.0.1",
		UserAgent: "test-agent",
	}), &LoginRequest{
		Email:    "user@example.com",
		Password: "correct-password",
	})
	if err == nil {
		t.Fatalf("expected error")
	}

	appErr := apperrors.ToAppError(err)
	if appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", appErr.StatusCode)
	}
	select {
	case <-monitor.ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected blocked login to be notified")
	}

	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if len(monitor.events) == 0 {
		t.Fatalf("expected blocked login to be notified")
	}
}
