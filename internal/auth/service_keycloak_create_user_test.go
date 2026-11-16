package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type createUserTeamRepository struct {
	*updateUserAdminRepoStub
	teamError          error
	validatedCompany   uuid.UUID
	validatedTeam      uuid.UUID
	teamValidationRuns int
	created            *domain.User
}

func (r *createUserTeamRepository) ValidateOrganizationTeam(_ context.Context, companyID, teamID uuid.UUID) error {
	r.teamValidationRuns++
	r.validatedCompany, r.validatedTeam = companyID, teamID
	return r.teamError
}

func (r *createUserTeamRepository) CheckNewUserIdentity(context.Context, string, string, string) error {
	return nil
}

func (r *createUserTeamRepository) CreateNewUser(_ context.Context, user *domain.User) error {
	user.ID = 2
	copy := *user
	r.created = &copy
	return nil
}

func TestScopedUserCreateValidatesOptionalOrganizationTeam(t *testing.T) {
	companyID, teamID, otherCompanyID := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name       string
		global     bool
		company    *uuid.UUID
		team       *uuid.UUID
		teamError  error
		wantStatus int
	}{
		{name: "director without team"},
		{name: "director with zero team", team: &uuid.Nil, wantStatus: http.StatusBadRequest},
		{name: "director with valid team", team: &teamID},
		{name: "foreign team", team: &teamID, teamError: errors.New("team belongs to another company"), wantStatus: http.StatusBadRequest},
		{name: "archived team", team: &teamID, teamError: errors.New("team not found"), wantStatus: http.StatusBadRequest},
		{name: "foreign company", company: &otherCompanyID, team: &teamID, wantStatus: http.StatusForbidden},
		{name: "foreign company without team", company: &otherCompanyID, wantStatus: http.StatusForbidden},
		{name: "platform account without company", global: true},
		{name: "platform creates employee without team", global: true, company: &companyID},
		{name: "platform creates employee with team", global: true, company: &companyID, team: &teamID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := &domain.User{ID: 1, Role: domain.RoleUser, OrganizationID: &companyID}
			if tc.global {
				actor.Role, actor.OrganizationID = domain.RoleSuperAdmin, nil
			}
			repo := &createUserTeamRepository{
				updateUserAdminRepoStub: &updateUserAdminRepoStub{user: actor},
				teamError:               tc.teamError,
			}
			service := NewKeycloakService(repo, nil, nil, nil, nil, "test")
			request := &CreateUserRequest{OrganizationID: tc.company, TeamID: tc.team}
			err := service.ValidateScopedUserCreate(context.Background(), actor.ID, request)
			if tc.wantStatus != 0 {
				require.Error(t, err)
				require.Equal(t, tc.wantStatus, apperrors.ToAppError(err).StatusCode)
				return
			}
			require.NoError(t, err)
			if request.OrganizationID != nil {
				require.Equal(t, companyID, *request.OrganizationID)
			}
			if request.TeamID != nil {
				require.Equal(t, companyID, repo.validatedCompany)
				require.Equal(t, teamID, repo.validatedTeam)
				require.Equal(t, 1, repo.teamValidationRuns)
			} else {
				require.Zero(t, repo.teamValidationRuns)
			}
		})
	}
}

func TestCreateUserRejectsInvalidOrganizationAssignmentBeforeKeycloak(t *testing.T) {
	companyID, teamID := uuid.New(), uuid.New()
	for _, body := range []string{
		`{"organization_id":"` + companyID.String() + `","team_id":"` + uuid.Nil.String() + `"}`,
		`{"organization_id":"` + uuid.Nil.String() + `","team_id":"` + teamID.String() + `"}`,
		`{"team_id":"` + teamID.String() + `"}`,
	} {
		t.Run(body, func(t *testing.T) {
			var request CreateUserRequest
			require.NoError(t, json.Unmarshal([]byte(body), &request))
			// No Keycloak client is installed: rejection must precede any SSO I/O.
			service := &Service{}
			_, err := service.CreateUser(context.Background(), 1, &request)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, apperrors.ToAppError(err).StatusCode)
		})
	}
	service := &Service{}
	_, err := service.CreateUser(context.Background(), 1, nil)
	require.Error(t, err)
}

func TestCreateUserRejectsUnavailableTeamBeforeKeycloak(t *testing.T) {
	companyID, teamID := uuid.New(), uuid.New()
	repo := &createUserTeamRepository{teamError: errors.New("team not found")}
	service := NewKeycloakService(repo, nil, nil, nil, nil, "test")
	_, err := service.CreateUser(context.Background(), 1, &CreateUserRequest{OrganizationID: &companyID, TeamID: &teamID})
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 1, repo.teamValidationRuns)
	require.Nil(t, repo.created)
}

func TestCreateUserPreservesOptionalTeam(t *testing.T) {
	for _, assignment := range []string{"omitted", "null", "selected"} {
		t.Run(assignment, func(t *testing.T) {
			companyID, teamID := uuid.New(), uuid.New()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/realms/master/protocol/openid-connect/token":
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-admin", "expires_in": 300})
				case r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test/users":
					var identity keycloak.CreateUserRequest
					require.NoError(t, json.NewDecoder(r.Body).Decode(&identity))
					if assignment == "selected" {
						require.Equal(t, []string{teamID.String()}, identity.Attributes["team_id"])
					} else {
						require.NotContains(t, identity.Attributes, "team_id")
					}
					w.Header().Set("Location", "/admin/realms/test/users/new-employee")
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/reset-password"):
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test/roles/user":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "user-role", "name": "user"})
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/role-mappings/realm"):
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected Keycloak request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			repo := &createUserTeamRepository{}
			service := NewKeycloakService(repo, nil, nil, nil, keycloak.NewAdmin(server.URL, "master", "test", "test"), "test")
			data := map[string]any{"email": "employee@example.invalid", "username": "new.employee", "password": "synthetic-password", "iin": "123456789012", "first_name": "New", "last_name": "Employee", "organization_id": companyID.String()}
			if assignment == "selected" {
				data["team_id"] = teamID.String()
			} else if assignment == "null" {
				data["team_id"] = nil
			}
			encoded, err := json.Marshal(data)
			require.NoError(t, err)
			var request CreateUserRequest
			require.NoError(t, json.Unmarshal(encoded, &request))
			result, err := service.CreateUser(context.Background(), 1, &request)
			require.NoError(t, err)
			require.NotNil(t, repo.created)
			require.Equal(t, companyID, *repo.created.OrganizationID)
			if assignment == "selected" {
				require.Equal(t, teamID, *repo.created.TeamID)
				require.Equal(t, teamID, *result.TeamID)
				require.Equal(t, 1, repo.teamValidationRuns)
			} else {
				require.Nil(t, repo.created.TeamID)
				require.Nil(t, result.TeamID)
				require.Zero(t, repo.teamValidationRuns)
			}
			require.Equal(t, domain.RoleUser, result.Role)
		})
	}
}
