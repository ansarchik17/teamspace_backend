package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// Exercise the new insert together with the actual position trigger, company
// directory, effective permissions and subsequent subdivision assignment.
func TestEmployeeCreationStructureAndPositionAccess(t *testing.T) {
	users, db, beles, sultan := userCreationFixture(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `ALTER TABLE organizations ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now();
	CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID,parent_team_id UUID,name TEXT,unit_type TEXT DEFAULT 'team',
	 description TEXT,manager_id BIGINT,display_order INT DEFAULT 0,deleted_at TIMESTAMPTZ,
	 created_at TIMESTAMPTZ DEFAULT now(),updated_at TIMESTAMPTZ DEFAULT now());`)
	require.NoError(t, err)
	for _, migration := range []string{
		"20261002000100_organization_access.sql",
		"20261003000100_sync_employee_access_position.sql",
		"20261012000100_company_administration_audit.sql",
		"20261012000300_company_modules.sql",
		"20261022120000_employment_admission.sql",
	} {
		data, err := os.ReadFile("../../db/migrations/" + migration)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err, migration)
	}
	orgs := NewOrganizationRepository(db, zap.NewNop())
	sultanTeam, belesTeam := uuid.New(), uuid.New()
	sultanPosition, belesPosition := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO teams(id,organization_id,name) VALUES($1,$2,'Sultan team'),($3,$4,'Beles team');`, sultanTeam, sultan, belesTeam, beles)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_access_positions(id,organization_id,name,position_key)
	 VALUES($1,$2,'Developer','developer'),($3,$4,'Developer','developer')`, sultanPosition, sultan, belesPosition, beles)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_position_permissions(organization_id,position_id,application,permissions)
	 VALUES($1,$2,'odo',ARRAY['document.read']),($3,$4,'odo',ARRAY['document.read_all'])`, sultan, sultanPosition, beles, belesPosition)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_access_apps(organization_id,application,enabled) VALUES($1,'odo',true),($2,'odo',true)`, sultan, beles)
	require.NoError(t, err)

	withoutPosition := newUserCreationCandidate(sultan)
	require.NoError(t, users.CreateNewUser(ctx, withoutPosition))
	access, err := orgs.ResolveOwnAccess(ctx, withoutPosition.ID, "odo")
	require.NoError(t, err)
	require.True(t, access.Managed && access.Active)
	require.Empty(t, access.Permissions, "an employee without a position gained working permissions")
	require.Empty(t, access.DepartmentID)

	knownPosition := newUserCreationCandidate(sultan)
	*knownPosition.IIN = "444444444444"
	title := " Developer "
	knownPosition.Position = &title
	require.NoError(t, users.CreateNewUser(ctx, knownPosition))
	access, err = orgs.ResolveOwnAccess(ctx, knownPosition.ID, "odo")
	require.NoError(t, err)
	require.Equal(t, []string{"document.read"}, access.Permissions, "same job title in Beles leaked its permissions")
	require.Equal(t, []string{"user"}, access.Roles)
	canCreate, err := users.CanCreateUser(ctx, knownPosition.ID)
	require.NoError(t, err)
	require.False(t, canCreate, "job title gave company administration rights")

	unknownPosition := newUserCreationCandidate(sultan)
	*unknownPosition.IIN = "555555555555"
	newTitle := "New position"
	unknownPosition.Position = &newTitle
	require.NoError(t, users.CreateNewUser(ctx, unknownPosition))
	access, err = orgs.ResolveOwnAccess(ctx, unknownPosition.ID, "odo")
	require.NoError(t, err)
	require.Empty(t, access.Permissions, "a new unconfigured position gained permissions")

	members, err := orgs.ListMembers(ctx, 2)
	require.NoError(t, err)
	require.Len(t, members, 4, "new employees without subdivisions disappeared from the company")
	for _, member := range members {
		require.Nil(t, member.UnitID)
		if member.ID == withoutPosition.ID {
			require.Equal(t, withoutPosition.FirstName, member.FirstName)
			require.Equal(t, withoutPosition.LastName, member.LastName)
		}
	}
	otherMembers, err := orgs.ListMembers(ctx, 1)
	require.NoError(t, err)
	require.Len(t, otherMembers, 1, "Sultan creation changed Beles membership")

	require.NoError(t, orgs.AssignUser(ctx, 2, withoutPosition.ID, domain.AssignUserToUnitRequest{UnitID: &sultanTeam}))
	access, err = orgs.ResolveOwnAccess(ctx, withoutPosition.ID, "odo")
	require.NoError(t, err)
	require.Equal(t, sultanTeam.String(), access.DepartmentID)
	require.Empty(t, access.Permissions, "subdivision assignment changed position permissions")
	before := userCreationSnapshot(t, db)
	require.Error(t, orgs.AssignUser(ctx, 2, withoutPosition.ID, domain.AssignUserToUnitRequest{UnitID: &belesTeam}))
	require.Equal(t, before, userCreationSnapshot(t, db))
	require.Error(t, orgs.AssignUser(ctx, withoutPosition.ID, knownPosition.ID, domain.AssignUserToUnitRequest{UnitID: &sultanTeam}))
	require.Equal(t, before, userCreationSnapshot(t, db))

	require.NoError(t, orgs.ChangeOrganizationAccess(ctx, 2, nil, "member", AccessChange{ID: sultanPosition, UserID: withoutPosition.ID}))
	access, err = orgs.ResolveOwnAccess(ctx, withoutPosition.ID, "odo")
	require.NoError(t, err)
	require.Equal(t, []string{"document.read"}, access.Permissions)
	require.Equal(t, sultanTeam.String(), access.DepartmentID)
	require.Error(t, orgs.ChangeOrganizationAccess(ctx, 2, nil, "member", AccessChange{ID: belesPosition, UserID: withoutPosition.ID}))

	require.NoError(t, orgs.AssignUser(ctx, 2, withoutPosition.ID, domain.AssignUserToUnitRequest{}))
	access, err = orgs.ResolveOwnAccess(ctx, withoutPosition.ID, "odo")
	require.NoError(t, err)
	require.Empty(t, access.DepartmentID)
	require.Equal(t, sultan.String(), access.OrganizationID)
	require.Equal(t, []string{"document.read"}, access.Permissions, "removing subdivision changed job title permissions")
	var owners string
	require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(owner_id::text,',' ORDER BY owner_id) FROM organizations`).Scan(&owners))
	require.Equal(t, "1,2", owners)
}
