package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestModuleAccessDeniesMissingConfigurationAndDatabaseFailure(t *testing.T) {
	db := &administrationTestDB{active: true}
	ctx := context.Background()
	org := uuid.New()
	require.NoError(t, requireOrganizationModule(ctx, db, org, "iform"))
	db.active = false
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(requireOrganizationModule(ctx, db, org, "iform")).Code)
	db.err = errors.New("database unavailable")
	require.ErrorIs(t, requireOrganizationModule(ctx, db, org, "iform"), db.err)
}

func TestModuleMutationRejectsInvalidInputAndTenantAvailabilityWrite(t *testing.T) {
	r := &OrganizationRepository{}
	ctx := context.Background()
	yes := true
	zero, negative := int64(0), int64(-1)
	for _, tc := range []struct {
		app string
		req domain.AdministrationModuleRequest
	}{
		{"unknown", domain.AdministrationModuleRequest{Enabled: &yes, Revision: &zero}},
		{"iform", domain.AdministrationModuleRequest{Enabled: &yes}},
		{"iform", domain.AdministrationModuleRequest{Enabled: &yes, Revision: &negative}},
		{"iform", domain.AdministrationModuleRequest{Revision: &zero}},
	} {
		require.Equal(t, apperrors.ErrCodeValidation, apperrors.ToAppError(r.SetAdministrationModule(ctx, 1, nil, tc.app, tc.req)).Code)
	}
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(r.SetAdministrationModule(ctx, 1, nil, "iform", domain.AdministrationModuleRequest{Available: &yes, Revision: &zero})).Code)
}
