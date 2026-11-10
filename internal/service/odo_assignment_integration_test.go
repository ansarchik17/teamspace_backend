package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
)

func newODOFixture(t *testing.T, beforeMigration ...bool) (context.Context, *pgxpool.Pool, *KanbanService, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx, db, s, _, _, _, _ := newKanbanAuditFixture(t)
	_, err := db.Exec(ctx, `ALTER TABLE users ADD COLUMN keycloak_id TEXT,ADD COLUMN organization_id UUID,
 ADD COLUMN iin TEXT,ADD COLUMN email TEXT NOT NULL DEFAULT '',ADD COLUMN password_hash TEXT,ADD COLUMN bio TEXT,ADD COLUMN position TEXT,
 ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT true,ADD COLUMN is_first_login BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN last_login_at TIMESTAMPTZ,ADD COLUMN last_seen_at TIMESTAMPTZ,ADD COLUMN created_at TIMESTAMPTZ DEFAULT now(),ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now(),ADD COLUMN deleted_at TIMESTAMPTZ,
 ADD COLUMN role TEXT NOT NULL DEFAULT 'user',ADD COLUMN availability_status TEXT,ADD COLUMN full_name_local TEXT,ADD COLUMN can_view_full_drive_activity BOOLEAN DEFAULT false,ADD COLUMN language TEXT DEFAULT 'ru',ADD COLUMN team_id UUID,ADD COLUMN manager_id BIGINT;
 ALTER TABLE boards ADD COLUMN system_key TEXT;
 CREATE UNIQUE INDEX test_system_board_key ON boards(system_key) WHERE deleted_at IS NULL;
 ALTER TABLE tasks ADD COLUMN source_document_id TEXT,ADD COLUMN source_url TEXT,ADD COLUMN source_author_sub TEXT,ADD COLUMN source_department_id TEXT;
 CREATE UNIQUE INDEX ux_tasks_documentolog_assignment ON tasks(source_assignment_id) WHERE source_system='documentolog' AND deleted_at IS NULL;
 CREATE TABLE organizations(id UUID PRIMARY KEY,status TEXT NOT NULL,owner_id BIGINT);
 CREATE TABLE organization_modules(organization_id UUID,application TEXT,available BOOLEAN,enabled BOOLEAN);
 CREATE TABLE organization_access_apps(organization_id UUID,application TEXT,enabled BOOLEAN);
 CREATE TABLE organization_access_assignments(organization_id UUID,user_id BIGINT,position_id UUID);
 CREATE TABLE organization_position_permissions(organization_id UUID,position_id UUID,application TEXT,permissions TEXT[]);
 UPDATE users SET keycloak_id='subject-'||id,first_name='',last_name='';`)
	require.NoError(t, err)
	// Use the production department table definition, rather than a synthetic
	// table that can hide a mismatch between repository SQL and deployed schema.
	structure, err := os.ReadFile("../../db/migrations/20260912000000_create_corporate_structure.sql")
	require.NoError(t, err)
	_, teamDDL, found := strings.Cut(string(structure), "CREATE TABLE IF NOT EXISTS teams (")
	require.True(t, found)
	teamDDL, _, found = strings.Cut(teamDDL, ");")
	require.True(t, found)
	_, err = db.Exec(ctx, "CREATE TABLE teams ("+teamDDL+");")
	require.NoError(t, err)
	a, b := uuid.New(), uuid.New()
	for _, org := range []uuid.UUID{a, b} {
		_, err = db.Exec(ctx, `INSERT INTO organizations(id,status) VALUES($1,'active')`, org)
		require.NoError(t, err)
		_, err = db.Exec(ctx, `INSERT INTO organization_modules VALUES($1,'odo',true,true)`, org)
		require.NoError(t, err)
	}
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=CASE WHEN id IN (5,6) THEN $2::uuid ELSE $1::uuid END`, a, b)
	require.NoError(t, err)
	s.userRepo = repository.NewUserRepository(db, zap.NewNop())
	if len(beforeMigration) > 0 && beforeMigration[0] {
		return ctx, db, s, a, b
	}
	migration, err := os.ReadFile("../../db/migrations/20261014000300_odo_assignment_sync_v1.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	return ctx, db, s, a, b
}

func TestODOServiceCompanyIsolationIntegration(t *testing.T) {
	ctx, db, s, a, b := newODOFixture(t)
	req := DocumentologAssignmentUpsertRequest{OrganizationID: a.String(), SourceSystem: "documentolog", AssignmentID: "asg-company-a", DocumentID: "doc-a", AuthorSub: "subject-2", AssigneeSub: "subject-3", Title: "Scoped assignment", Priority: "medium", Status: "assigned"}
	task, board, err := s.UpsertODOIntegrationAssignment(ctx, 1, a, req)
	require.NoError(t, err)
	require.Equal(t, "documentolog-assignments:"+a.String(), board.SystemKey)
	items, err := s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, task.ID, items[0].ID)
	// Managed ODO access must respect assignment.read, not just user membership.
	_, err = db.Exec(ctx, `INSERT INTO organization_access_apps VALUES($1,'odo',true)`, a)
	require.NoError(t, err)
	_, err = s.ListMyDocumentologAssignments(ctx, 3)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE organizations SET owner_id=2 WHERE id=$1`, a)
	require.NoError(t, err)
	items, err = s.ListMyDocumentologAssignments(ctx, 2)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode, "director needs assigned work rights")
	_, err = db.Exec(ctx, `UPDATE users SET role='admin' WHERE id=4`)
	require.NoError(t, err)
	items, err = s.ListMyDocumentologAssignments(ctx, 4)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode, "platform admin needs assigned work rights")
	position := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organization_access_assignments VALUES($1,3,$2)`, a, position)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_position_permissions VALUES($1,$2,'odo',ARRAY['document.read','assignment.read'])`, a, position)
	require.NoError(t, err)
	items, err = s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Len(t, items, 1)
	_, err = db.Exec(ctx, `DELETE FROM organization_access_apps`)
	require.NoError(t, err)
	items, err = s.ListMyDocumentologAssignments(ctx, 2)
	require.NoError(t, err)
	require.Empty(t, items, "author is not automatically an assignee")
	other := req
	other.OrganizationID = b.String()
	other.AssignmentID = "asg-company-b"
	other.DocumentID = "doc-b"
	other.AuthorSub = "subject-5"
	other.AssigneeSub = "subject-6"
	_, otherBoard, err := s.UpsertODOIntegrationAssignment(ctx, 6, b, other)
	require.NoError(t, err)
	require.NotEqual(t, board.ID, otherBoard.ID)
	other.AssignmentID = req.AssignmentID
	other.DocumentID = req.DocumentID
	_, _, err = s.UpsertODOIntegrationAssignment(ctx, 6, b, other)
	require.NoError(t, err, "identical source IDs in different companies are independent")
	for _, test := range []struct {
		name   string
		actor  int64
		change func(*DocumentologAssignmentUpsertRequest)
	}{
		{"actor from foreign company", 6, func(*DocumentologAssignmentUpsertRequest) {}},
		{"foreign assignee", 1, func(r *DocumentologAssignmentUpsertRequest) { r.AssigneeSub = "subject-5" }},
		{"foreign author", 1, func(r *DocumentologAssignmentUpsertRequest) { r.AuthorSub = "subject-5" }},
		{"foreign board", 1, func(r *DocumentologAssignmentUpsertRequest) { r.BoardID = otherBoard.ID.String() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := req
			test.change(&copy)
			_, _, err := s.UpsertODOIntegrationAssignment(ctx, test.actor, a, copy)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		})
	}
	dept := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO teams(id,organization_id,name) VALUES($1,$2,'Foreign department')`, dept, b)
	require.NoError(t, err)
	copy := req
	copy.DepartmentID = dept.String()
	_, _, err = s.UpsertODOIntegrationAssignment(ctx, 1, a, copy)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	copy = req
	copy.AssigneeSub = "workspace:3"
	_, _, err = s.UpsertODOIntegrationAssignment(ctx, 1, a, copy)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=3`)
	require.NoError(t, err)
	_, _, err = s.UpsertODOIntegrationAssignment(ctx, 1, a, req)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=3`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=false WHERE organization_id=$1`, a)
	require.NoError(t, err)
	_, _, err = s.UpsertODOIntegrationAssignment(ctx, 1, a, req)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.ListMyDocumentologAssignments(ctx, 3)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	// Even accidental membership/assignment in a foreign board cannot disclose its tasks.
	_, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=true WHERE organization_id=$1`, a)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO task_assignees(task_id,user_id) SELECT id,3 FROM tasks WHERE board_id=$1`, otherBoard.ID)
	require.NoError(t, err)
	items, err = s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, task.ID, items[0].ID)
}
