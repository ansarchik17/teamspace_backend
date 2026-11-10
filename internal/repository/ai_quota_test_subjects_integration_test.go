package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIQuotaTestSubjectIntegration(t *testing.T) {
	ctx, db, admin, org, otherOrg, now := newAIEnforcementTestDB(t)
	clock := func() time.Time { return *now }
	user, colleague := int64(3), int64(2)
	subjects := domain.AIQuotaTestSubjects{{UserID: user, OrganizationID: org}}
	repo := NewAIReservationRepository(db, clock, WithAIQuotaTestSubjects(subjects))
	admin.SetAIQuotaTestSubjects(subjects)
	// Zero explicitly disables access for ordinary users; no quotas are raised.
	_, err := db.Exec(ctx, `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits)
 VALUES($1,0,0),($2,0,0)`, org, otherOrg)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits)
 VALUES($1,$2,0,0),($1,$3,0,0)`, org, user, colleague)
	require.NoError(t, err)

	reservation, err := repo.Reserve(ctx, testReserveReq(org, &user, "exempt-user", 10))
	require.NoError(t, err)
	require.True(t, reservation.Created)
	settled, err := repo.SettleSuccess(ctx, testSettleReq(org, reservation.AttemptID, 10))
	require.NoError(t, err)
	require.True(t, settled.Applied)
	var charged int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, reservation.FiveHourPeriodID).Scan(&charged))
	require.Positive(t, charged, "exemption must retain actual accounting")
	_, err = repo.Reserve(ctx, testReserveReq(org, nil, "exempt-service", 10))
	require.NoError(t, err)

	limits, err := admin.GetUserAILimitsWithClockAndMode(ctx, user, clock, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Equal(t, domain.AIEnforcementModeObserve, limits.EnforcementMode)
	require.True(t, limits.Access.CanStartRequest)
	colleagueLimits, err := admin.GetUserAILimitsWithClockAndMode(ctx, colleague, clock, domain.AIEnforcementModeEnforced)
	require.NoError(t, err)
	require.Equal(t, domain.AIEnforcementModeEnforced, colleagueLimits.EnforcementMode)
	require.False(t, colleagueLimits.Access.CanStartRequest)
	_, err = repo.Reserve(ctx, testReserveReq(org, &colleague, "colleague-disabled", 10))
	require.Error(t, err)
	_, err = repo.Reserve(ctx, testReserveReq(otherOrg, nil, "other-company-disabled", 10))
	require.Error(t, err)
	// A caller cannot borrow the exempt user's ID in another tenant.
	_, err = repo.Reserve(ctx, testReserveReq(otherOrg, &user, "foreign-identity", 10))
	require.Error(t, err)
	// Actual account suspension remains enforced.
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, user)
	require.NoError(t, err)
	_, err = repo.Reserve(ctx, testReserveReq(org, &user, "inactive-exempt", 10))
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=$1`, user)
	require.NoError(t, err)
	// Removing the operator setting reinstates the original zero quota.
	ordinary := NewAIReservationRepository(db, clock)
	_, err = ordinary.Reserve(ctx, testReserveReq(org, &user, "exemption-removed", 10))
	require.Error(t, err)
	repo.SetAccountingEnabled(false)
	_, err = repo.Reserve(ctx, testReserveReq(org, &user, "accounting-disabled", 10))
	require.ErrorIs(t, err, domain.ErrAIAccountingDisabled)
}
