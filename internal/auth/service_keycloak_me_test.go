package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

type meUserRepoStub struct {
	user    *domain.User
	details *domain.UserDetailResponse
	err     error
}

func (s *meUserRepoStub) GetByEmail(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *meUserRepoStub) GetByID(_ context.Context, _ int64) (*domain.User, error) {
	return s.user, s.err
}

func (s *meUserRepoStub) GetByIIN(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *meUserRepoStub) GetByKeycloakID(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *meUserRepoStub) UpsertFromKeycloak(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *meUserRepoStub) HasRegisteredMobileDevice(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

func (s *meUserRepoStub) UpdateLastLogin(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *meUserRepoStub) CompleteOnboarding(_ context.Context, _ int64) error {
	return nil
}

func (s *meUserRepoStub) SetIsFirstLogin(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (s *meUserRepoStub) Update(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *meUserRepoStub) GetAll(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *meUserRepoStub) GetPaginated(_ context.Context, _ string, _, _ int, _ ...domain.UserFilters) ([]*domain.User, int, error) {
	return nil, 0, nil
}

func (s *meUserRepoStub) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}

func (s *meUserRepoStub) GetFullDetailsByID(_ context.Context, _ int64) (*domain.UserDetailResponse, error) {
	return s.details, s.err
}

func (s *meUserRepoStub) UpdateStatus(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *meUserRepoStub) GetByKeycloakIDs(_ context.Context, _ []string) ([]*domain.User, error) {
	return nil, nil
}

func (s *meUserRepoStub) GetNeverLoggedIn(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *meUserRepoStub) ExportMinistryUsers(_ context.Context, _ int64, _ []int64, _ []int64, _ []int64) ([]domain.MinistryUserExportRow, error) {
	return nil, nil
}
func (s *meUserRepoStub) GetMinistryName(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (s *meUserRepoStub) SoftDelete(_ context.Context, _ int64) error {
	return nil
}
func (s *meUserRepoStub) UpdateRole(_ context.Context, _ int64, _ domain.Role) error {
	return nil
}
func (s *meUserRepoStub) UpdateMinistryID(_ context.Context, _, _ int64) error {
	return nil
}

type meHRProfileRepoStub struct {
	profile *domain.UserHRProfile
	err     error
}

func (s *meHRProfileRepoStub) GetByPersonCode(_ context.Context, _ string) (*domain.UserHRProfile, error) {
	return nil, nil
}

func (s *meHRProfileRepoStub) GetByIIN(_ context.Context, _ string) (*domain.UserHRProfile, error) {
	return nil, nil
}

func (s *meHRProfileRepoStub) GetByUserID(_ context.Context, _ int64) (*domain.UserHRProfile, error) {
	return s.profile, s.err
}

func (s *meHRProfileRepoStub) List(_ context.Context, _ repository.HRProfileFilter, _, _ int) ([]*domain.UserHRProfileSummaryResponse, int, error) {
	return nil, 0, nil
}

func (s *meHRProfileRepoStub) GetUniqueValues(_ context.Context, _ string, _ repository.HRProfileFilter) ([]string, error) {
	return nil, nil
}

func (s *meHRProfileRepoStub) GetAllFilterValues(_ context.Context, _ repository.HRProfileFilter) (map[string][]string, error) {
	return map[string][]string{}, nil
}

func (s *meHRProfileRepoStub) Upsert(_ context.Context, _ *domain.UserHRProfile) error {
	return nil
}

func (s *meHRProfileRepoStub) GetManagementVacations(_ context.Context) ([]*domain.ManagementVacationResponse, error) {
	return nil, nil
}

func TestGetCurrentUserIncludesHRDisplayFieldsFromProfile(t *testing.T) {
	t.Parallel()

	svc := NewKeycloakService(
		&meUserRepoStub{
			user: &domain.User{
				ID:        42,
				Email:     "user@example.com",
				Username:  "user",
				FirstName: "Test",
				LastName:  "User",
				Position:  strPtr("Base Position"),
			},
			details: &domain.UserDetailResponse{
				UserResponse: domain.UserResponse{
					ID:           42,
					Email:        "user@example.com",
					Username:     "user",
					FirstName:    "Test",
					LastName:     "User",
					FullName:     "Test User",
					Position:     strPtr("Base Position"),
					PositionName: "Base Position",
					CreatedAt:    time.Now(),
					IsActive:     true,
				},
				MinistryName:  strPtr("Base Ministry"),
				StateBodyName: strPtr("Base State Body"),
			},
		},
		nil,
		nil,
		nil,
		nil,
		"",
		WithHRProfileRepository(&meHRProfileRepoStub{
			profile: &domain.UserHRProfile{
				IIN:             "123456789012",
				DocType:         "appointment-order",
				JobName:         "Chief Specialist",
				SubName:         "Digital Office",
				NameGO:          "Ministry of Finance",
				PersonStatus:    "active",
				AbsenceStatus:   "",
				LastOrderNumber: "003",
				LastOrderDate:   timePtr(time.Date(2025, time.December, 22, 0, 0, 0, 0, time.UTC)),
			},
		}),
	)

	resp, err := svc.GetCurrentUser(context.Background(), 42)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.IIN)
	require.Equal(t, "123456789012", *resp.IIN)
	require.Equal(t, "appointment-order", resp.DocType)
	require.Equal(t, "Chief Specialist", resp.JobName)
	require.Equal(t, "Digital Office", resp.SubName)
	require.Equal(t, "Chief Specialist", resp.PositionName)
	require.Equal(t, "Digital Office", resp.DepartmentName)
	require.Equal(t, "Ministry of Finance", resp.OrganizationName)
	require.Equal(t, "active", resp.PersonStatus)
	require.Equal(t, "", resp.AbsenceStatus)
	require.Equal(t, "003", resp.LastOrderNumber)
	require.NotNil(t, resp.LastOrderDate)
	require.Equal(t, 2025, resp.LastOrderDate.Year())
	require.Equal(t, time.December, resp.LastOrderDate.Month())
	require.Equal(t, 22, resp.LastOrderDate.Day())
}

func TestGetCurrentUserFallsBackToUserHierarchyWhenHRProfileMissing(t *testing.T) {
	t.Parallel()

	iin := "999999999999"
	svc := NewKeycloakService(
		&meUserRepoStub{
			user: &domain.User{
				ID:        7,
				Email:     "user@example.com",
				Username:  "user",
				FirstName: "Test",
				LastName:  "User",
				IIN:       &iin,
				Position:  strPtr("Backend Developer"),
			},
			details: &domain.UserDetailResponse{
				UserResponse: domain.UserResponse{
					ID:           7,
					Email:        "user@example.com",
					Username:     "user",
					FirstName:    "Test",
					LastName:     "User",
					FullName:     "Test User",
					IIN:          &iin,
					Position:     strPtr("Backend Developer"),
					PositionName: "Backend Developer",
					CreatedAt:    time.Now(),
					IsActive:     true,
				},
				MinistryName:  strPtr("Digital Development Ministry"),
				StateBodyName: strPtr("Public Services Committee"),
			},
		},
		nil,
		nil,
		nil,
		nil,
		"",
		WithHRProfileRepository(&meHRProfileRepoStub{err: errors.New("hr profile not found")}),
	)

	resp, err := svc.GetCurrentUser(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.IIN)
	require.Equal(t, iin, *resp.IIN)
	require.Equal(t, "Backend Developer", resp.PositionName)
	require.Equal(t, "Public Services Committee", resp.DepartmentName)
	require.Equal(t, "Digital Development Ministry", resp.OrganizationName)
	require.Empty(t, resp.DocType)
	require.Nil(t, resp.LastOrderDate)
}

func strPtr(v string) *string {
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}
