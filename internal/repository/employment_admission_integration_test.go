package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestEmploymentAdmissionDatesTimezoneCancellationAndCreationRace(t *testing.T) {
	users, db, company, other := userCreationFixture(t)
	ctx := context.Background()
	for _, file := range []string{"20261012000100_company_administration_audit.sql", "20261019000100_iform_operation_receipts.sql", "20261022120000_employment_admission.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	orgs := NewOrganizationRepository(db, zap.NewNop())
	request := uuid.New()
	start := "2026-12-01"
	user := newUserCreationCandidate(company)
	user.Username = "onb." + strings.ReplaceAll(request.String(), "-", "")
	user.EmploymentAdmission = &domain.EmploymentAdmission{OrganizationID: company, RequestID: request, ActorID: 1, StartDate: &start}
	require.NoError(t, users.CreateNewUser(ctx, user))
	require.NotNil(t, user.EmploymentAdmission.EmployeeID)
	require.Equal(t, user.ID, *user.EmploymentAdmission.EmployeeID)
	for _, tc := range []struct {
		zone, instant string
		allowed       bool
	}{
		{"Asia/Almaty", "2026-11-30T18:59:59Z", false},
		{"Asia/Almaty", "2026-11-30T19:00:00Z", true},
		{"America/New_York", "2026-12-01T04:59:59Z", false},
		{"America/New_York", "2026-12-01T05:00:00Z", true},
		{"Pacific/Kiritimati", "2026-11-30T10:00:00Z", true},
	} {
		_, err := db.Exec(ctx, `INSERT INTO organization_employment_settings(organization_id,timezone) VALUES($1,$2) ON CONFLICT(organization_id) DO UPDATE SET timezone=EXCLUDED.timezone`, company, tc.zone)
		require.NoError(t, err)
		var allowed bool
		require.NoError(t, db.QueryRow(ctx, `SELECT workspace_user_can_login_at($1,$2::timestamptz)`, user.ID, tc.instant).Scan(&allowed))
		require.Equal(t, tc.allowed, allowed, tc.zone+" "+tc.instant)
	}
	if _, err := orgs.IFormEmploymentAccess(ctx, 2, company, request, "get", "", 0); err == nil {
		t.Fatal("foreign HR read admission")
	}
	if _, err := orgs.IFormEmploymentAccess(ctx, 1, other, request, "get", "", 0); err == nil {
		t.Fatal("foreign company read admission")
	}
	tomorrow := time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02")
	updated, err := orgs.IFormEmploymentAccess(ctx, 1, company, request, "update", tomorrow, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Revision)
	allowed, err := users.IsUserActive(ctx, user.ID)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = orgs.IFormEmploymentAccess(ctx, 1, company, request, "update", tomorrow, 1)
	require.NoError(t, err, "lost update response is replayable")
	_, err = orgs.IFormEmploymentAccess(ctx, 1, company, request, "update", "2026-12-02", 1)
	require.Error(t, err, "stale change overwrote date")
	_, err = orgs.IFormEmploymentAccess(ctx, 1, company, request, "update", "2026-02-30", 2)
	require.Error(t, err)
	_, err = orgs.IFormEmploymentAccess(ctx, 1, company, request, "update", "2020-01-01", 2)
	require.NoError(t, err)
	allowed, err = users.IsUserActive(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, allowed)
	cancelled, err := orgs.IFormEmploymentAccess(ctx, 1, company, request, "cancel", "", 0)
	require.NoError(t, err)
	require.True(t, cancelled.Cancelled)
	allowed, err = users.IsUserActive(ctx, user.ID)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = orgs.IFormEmploymentAccess(ctx, 1, company, request, "update", "2020-01-01", cancelled.Revision)
	require.Error(t, err, "cancelled admission reopened")
	// Cancellation wins even if account provisioning was already in flight.
	race := uuid.New()
	_, err = orgs.IFormEmploymentAccess(ctx, 1, company, race, "cancel", "", 0)
	require.NoError(t, err)
	late := newUserCreationCandidate(company)
	*late.IIN = "444444444444"
	late.Username = "onb." + strings.ReplaceAll(race.String(), "-", "")
	late.EmploymentAdmission = &domain.EmploymentAdmission{OrganizationID: company, RequestID: race, ActorID: 1, StartDate: &start}
	require.NoError(t, users.CreateNewUser(ctx, late))
	require.True(t, late.EmploymentAdmission.Cancelled)
	allowed, err = users.IsUserActive(ctx, late.ID)
	require.NoError(t, err)
	require.False(t, allowed)
	// A normal existing user keeps access. An old onboarding account without a
	// verified date cannot silently bypass the migration.
	allowed, err = users.IsUserActive(ctx, 1)
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = db.Exec(ctx, `UPDATE users SET username='onb.legacy' WHERE id=2`)
	require.NoError(t, err)
	allowed, err = users.IsUserActive(ctx, 2)
	require.NoError(t, err)
	require.False(t, allowed)
	legacy := uuid.New()
	_, err = db.Exec(ctx, `UPDATE users SET username='onb.'||replace($1::text,'-','') WHERE id=2`, legacy)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO iform_operation_receipts VALUES($1,$2,'employee',1,2,'workspace.employee.create','synthetic','{"state":"SUCCEEDED","employee_id":2}',now())`, other, legacy)
	require.NoError(t, err)
	old, err := orgs.IFormEmploymentAccess(ctx, 2, other, legacy, "get", "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), old.Revision)
	require.Nil(t, old.StartDate)
	require.False(t, old.Allowed)
	_, err = orgs.IFormEmploymentAccess(ctx, 2, other, legacy, "update", tomorrow, 0)
	require.NoError(t, err)
	allowed, err = users.IsUserActive(ctx, 2)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestEmploymentTimezoneSettingsRequireDirectorAndRevision(t *testing.T) {
	ctx, db, company, other := newAccessPositionsTestDB(t)
	repo := NewOrganizationRepository(db, zap.NewNop())
	settings, err := repo.EmploymentTimezone(ctx, 2, nil, nil, 0)
	require.NoError(t, err)
	require.Equal(t, "Asia/Almaty", settings["timezone"])
	ny := "America/New_York"
	settings, err = repo.EmploymentTimezone(ctx, 2, nil, &ny, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), settings["revision"])
	_, err = repo.EmploymentTimezone(ctx, 3, nil, &ny, 1)
	require.Error(t, err, "ordinary member changed company timezone")
	_, err = repo.EmploymentTimezone(ctx, 2, &other, &ny, 1)
	require.Error(t, err, "company director changed another company")
	utc := "UTC"
	_, err = repo.EmploymentTimezone(ctx, 1, &company, &utc, 1)
	require.NoError(t, err)
	_, err = repo.EmploymentTimezone(ctx, 2, nil, &ny, 1)
	require.Error(t, err, "stale timezone revision accepted")
	for _, invalid := range []string{"Local", "", "../../etc/passwd", "Unknown/Zone"} {
		_, err = repo.EmploymentTimezone(ctx, 2, nil, &invalid, 2)
		require.Error(t, err)
	}
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE action='employment.timezone.update'`).Scan(&count))
	require.Equal(t, 2, count)
}

func TestEmploymentAdmissionAlsoDeniesODOAndSDServiceGrants(t *testing.T) {
	ctx, db, company, _ := newAccessPositionsTestDB(t)
	repo := NewOrganizationRepository(db, zap.NewNop())
	_, err := db.Exec(ctx, `INSERT INTO organization_access_apps(organization_id,application,enabled) VALUES($1,'odo',true),($1,'sd',true) ON CONFLICT(organization_id,application) DO UPDATE SET enabled=true`, company)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_modules(organization_id,application,available,enabled) VALUES($1,'odo',true,true),($1,'sd',true,true) ON CONFLICT(organization_id,application) DO UPDATE SET available=true,enabled=true`, company)
	require.NoError(t, err)
	request := uuid.New()
	_, err = db.Exec(ctx, `UPDATE users SET username='onb.'||replace($1::text,'-','') WHERE id=3`, request)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,employee_id,start_date) VALUES($1,$2,2,3,CURRENT_DATE+2)`, company, request)
	require.NoError(t, err)
	var external uuid.UUID
	require.NoError(t, db.QueryRow(ctx, `SELECT external_company_id FROM organization_app_links WHERE organization_id=$1 AND application='sd'`, company).Scan(&external))
	for _, state := range []string{"future", "allowed", "cancelled"} {
		if state == "allowed" {
			_, err = db.Exec(ctx, `UPDATE employee_admissions SET start_date=CURRENT_DATE-2`)
		} else if state == "cancelled" {
			_, err = db.Exec(ctx, `UPDATE employee_admissions SET cancelled_at=now()`)
		}
		require.NoError(t, err)
		for _, app := range []string{"odo", "sd"} {
			grant, err := repo.ResolveOwnAccess(ctx, 3, app)
			require.NoError(t, err)
			require.True(t, grant.Linked)
			require.Equal(t, state == "allowed", grant.Active, state+" "+app)
			if state != "allowed" {
				require.Empty(t, grant.Permissions)
			}
		}
		externalGrant, err := repo.ResolveSDExternalAccess(ctx, external, "workspace-user:3")
		require.NoError(t, err)
		require.True(t, externalGrant.Linked)
		require.Equal(t, state == "allowed", externalGrant.Active, state)
	}
}
