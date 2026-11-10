package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type retiredCreationRepo struct {
	createUserRollbackRepo
	err   error
	calls int
}

func (r *retiredCreationRepo) CleanupRetiredUserIdentities(ctx context.Context, email, username, iin string, remove func(context.Context, string) error) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	return remove(ctx, "retired-id")
}

func TestEmployeeCreationRetriesOnlyAfterRetiredIdentityCleanup(t *testing.T) {
	for _, scenario := range []string{"recreated", "cleanup_failed", "unrelated_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			creates, deletes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/realms/master/protocol/openid-connect/token":
					_, _ = w.Write([]byte(`{"access_token":"test","expires_in":300}`))
				case r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test/users":
					creates++
					if creates == 1 || scenario == "unrelated_conflict" {
						w.WriteHeader(http.StatusConflict)
						return
					}
					w.Header().Set("Location", "/admin/realms/test/users/new-id")
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodDelete && r.URL.Path == "/admin/realms/test/users/retired-id":
					deletes++
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected SSO operation: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			repo := &retiredCreationRepo{}
			if scenario == "cleanup_failed" {
				repo.err = errors.New("cleanup unavailable")
			}
			s := NewKeycloakService(repo, nil, nil, nil, nil, keycloak.NewAdmin(server.URL, "master", "admin", "test"), "test")
			id, err := s.createEmployeeKeycloakIdentity(context.Background(), keycloak.CreateUserRequest{Email: "test@example.invalid", Username: "test"}, "000000000000")
			require.Equal(t, 1, repo.calls)
			if scenario == "recreated" {
				require.NoError(t, err)
				require.Equal(t, "new-id", id)
			} else {
				require.Error(t, err)
			}
			if scenario == "cleanup_failed" {
				require.Equal(t, 1, creates)
				require.Zero(t, deletes)
			} else {
				require.Equal(t, 2, creates, "retry is bounded even for an unrelated conflict")
				require.Equal(t, 1, deletes)
			}
		})
	}
}
