package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
)

type createUserRollbackRepo struct {
	UserRepository
	err      error
	cancel   context.CancelFunc
	checkErr error
	created  *domain.User
	upserts  int
}

func (r *createUserRollbackRepo) UpsertFromKeycloak(_ context.Context, u *domain.User) error {
	r.upserts++
	return errors.New("employee creation must not use identity linking")
}

func (r *createUserRollbackRepo) CheckNewUserIdentity(context.Context, string, string, string) error {
	return r.checkErr
}

func (r *createUserRollbackRepo) CreateNewUser(_ context.Context, u *domain.User) error {
	if r.cancel != nil {
		r.cancel()
	}
	u.ID = 24
	r.created = u
	return r.err
}

func TestCreateUserRollsBackOnlyNewSSOIdentityOnFailure(t *testing.T) {
	localErr := errors.New("local storage allocation failed")
	for _, tc := range []struct {
		name                          string
		existing, cancel, deleteFails bool
		failStage                     string
		wantDelete                    bool
	}{
		{name: "local persistence fails", failStage: "local", wantDelete: true},
		{name: "request cancelled during persistence", failStage: "local", cancel: true, wantDelete: true},
		{name: "password fails", failStage: "password", wantDelete: true},
		{name: "role fails", failStage: "role", wantDelete: true},
		{name: "success preserves identity"},
		{name: "existing identity is never changed or deleted", existing: true, failStage: "conflict"},
		{name: "concurrent local identity conflict rolls back new SSO only", failStage: "identity_conflict", wantDelete: true},
		{name: "cleanup failure is surfaced", failStage: "local", deleteFails: true, wantDelete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deletes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.existing && r.URL.Path != "/realms/master/protocol/openid-connect/token" && !(r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test/users") {
					t.Errorf("conflict touched an existing identity: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/realms/master/protocol/openid-connect/token":
					_, _ = w.Write([]byte(`{"access_token":"test-admin","expires_in":300}`))
				case r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test/users":
					if tc.existing {
						w.WriteHeader(http.StatusConflict)
						return
					}
					w.Header().Set("Location", "/admin/realms/test/users/test-identity")
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test/users":
					_, _ = w.Write([]byte(`[{"id":"test-identity","username":"test-user","email":"test@example.invalid"}]`))
				case r.Method == http.MethodDelete && r.URL.Path == "/admin/realms/test/users/test-identity":
					deletes++
					if tc.deleteFails {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				case strings.HasSuffix(r.URL.Path, "/reset-password"):
					if tc.failStage == "password" {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test/roles/user":
					_, _ = w.Write([]byte(`{"id":"role-user","name":"user"}`))
				case strings.HasSuffix(r.URL.Path, "/role-mappings/realm"):
					if tc.failStage == "role" {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/test/users/test-identity":
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected Keycloak call: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repo := &createUserRollbackRepo{}
			if tc.failStage == "local" {
				repo.err = localErr
			}
			if tc.failStage == "identity_conflict" {
				repo.err = repository.ErrUserIdentityConflict
			}
			if tc.cancel {
				repo.cancel = cancel
			}
			svc := NewKeycloakService(repo, nil, nil, nil, nil, keycloak.NewAdmin(server.URL, "master", "admin", "admin"), "test")
			company, team := uuid.New(), uuid.New()
			user, err := svc.CreateUser(ctx, 9, &CreateUserRequest{Username: "test-user", Email: "test@example.invalid", Password: "synthetic-test-password", IIN: "000000000000", OrganizationID: &company, TeamID: &team})
			if tc.failStage == "" {
				if err != nil || user == nil {
					t.Fatalf("expected success, got %v", err)
				}
				if repo.created == nil || repo.created.OrganizationID == nil || *repo.created.OrganizationID != company || repo.created.TeamID == nil || *repo.created.TeamID != team {
					t.Fatal("new employee lost the requested company or subdivision")
				}
			} else if err == nil {
				t.Fatal("expected provisioning failure")
			}
			if tc.failStage == "local" && !errors.Is(err, localErr) {
				t.Fatalf("lost original persistence error: %v", err)
			}
			if tc.failStage == "conflict" || tc.failStage == "identity_conflict" {
				var appErr *apperrors.AppError
				if !errors.As(err, &appErr) || appErr.Code != apperrors.ErrCodeUserIdentityConflict || appErr.StatusCode != http.StatusConflict {
					t.Fatalf("expected a safe identity conflict, got %v", err)
				}
			}
			if repo.upserts != 0 {
				t.Fatal("employee creation invoked identity linking")
			}
			wantDeletes := 0
			if tc.wantDelete {
				wantDeletes = 1
			}
			if deletes != wantDeletes {
				t.Fatalf("identity deletions = %d, want %d", deletes, wantDeletes)
			}
			if tc.deleteFails && !strings.Contains(err.Error(), "rollback") {
				t.Fatalf("cleanup failure missing from error: %v", err)
			}
		})
	}
}

func TestCreateUserChecksLocalIdentityBeforeSSO(t *testing.T) {
	for _, checkErr := range []error{repository.ErrUserIdentityConflict, errors.New("database unavailable")} {
		repo := &createUserRollbackRepo{checkErr: checkErr}
		// No Keycloak client: a collision or an unavailable identity check must
		// return before any SSO account, password, or role is touched.
		svc := &Service{userRepo: repo}
		user, err := svc.CreateUser(context.Background(), 9, &CreateUserRequest{Email: "test@example.invalid", Username: "test-user", IIN: "000000000000"})
		var appErr *apperrors.AppError
		if user != nil || !errors.As(err, &appErr) || repo.created != nil || repo.upserts != 0 {
			t.Fatalf("identity check did not stop creation: %v", err)
		}
		if errors.Is(checkErr, repository.ErrUserIdentityConflict) && appErr.StatusCode != http.StatusConflict {
			t.Fatalf("collision did not return HTTP 409: %v", err)
		}
		if !errors.Is(checkErr, repository.ErrUserIdentityConflict) && (appErr.StatusCode != http.StatusInternalServerError || !errors.Is(err, checkErr)) {
			t.Fatalf("database failure was not preserved: %v", err)
		}
	}
}
