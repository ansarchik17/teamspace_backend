package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestOrganizationTreeIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "cnt003_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,status TEXT,owner_id BIGINT,updated_at TIMESTAMPTZ);
 CREATE TABLE teams(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),organization_id UUID,parent_team_id UUID,name TEXT,unit_type TEXT,kind TEXT,description TEXT,manager_id BIGINT,display_order INT DEFAULT 0,deleted_at TIMESTAMPTZ,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,role TEXT,deleted_at TIMESTAMPTZ,is_active BOOLEAN DEFAULT TRUE,full_name_local TEXT,first_name TEXT,last_name TEXT,email TEXT,username TEXT,position TEXT,avatar_url TEXT,availability_status TEXT,team_id UUID,manager_id BIGINT,updated_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	org, other := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1,'active',1,now()),($2,'active',9,now());INSERT INTO users(id,organization_id,role,full_name_local,email,username) VALUES (1,$1,'user','Владелец','owner@example.com','owner'),(2,$1,'user','Сотрудник','user@example.com','user'),(3,$1,'user','Неактивный','inactive@example.com','inactive'),(4,$1,'user','Удалённый','deleted@example.com','deleted'),(9,$2,'user','Чужой','foreign@example.com','foreign');UPDATE users SET is_active=FALSE WHERE id=3;UPDATE users SET deleted_at=NOW() WHERE id=4`, org, other)
	require.NoError(t, err)
	priorMigration, err := os.ReadFile("../../db/migrations/20261001120000_employee_contacts.sql")
	require.NoError(t, err)
	priorParts := strings.Split(string(priorMigration), "-- +goose Down")
	_, err = db.Exec(ctx, priorParts[0])
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261001140000_organization_tree_guards.sql")
	require.NoError(t, err)
	parts := strings.Split(string(migration), "-- +goose Down")
	_, err = db.Exec(ctx, parts[0])
	require.NoError(t, err)
	repo := repository.NewOrganizationRepository(db, zap.NewNop())
	svc := NewOrganizationService(repo, zap.NewNop())
	tree, err := svc.OrganizationTree(ctx, 2)
	require.NoError(t, err)
	require.Empty(t, tree.Items)
	require.Equal(t, 2, tree.UnassignedEmployeeCount)
	require.False(t, tree.CanManageStructure)
	_, err = svc.CreateUnit(ctx, 2, domain.CreateOrganizationUnitRequest{Name: "Запрет"})
	require.Error(t, err)
	root, err := svc.CreateUnit(ctx, 1, domain.CreateOrganizationUnitRequest{Name: " Компания ", UnitType: "department", ManagerID: ptrContactID(1), DisplayOrder: 2})
	require.NoError(t, err)
	require.Equal(t, "Компания", root.Name)
	child, err := svc.CreateUnit(ctx, 1, domain.CreateOrganizationUnitRequest{Name: "Отдел", ParentUnitID: &root.ID})
	require.NoError(t, err)
	leaf, err := svc.CreateUnit(ctx, 1, domain.CreateOrganizationUnitRequest{Name: "Команда", ParentUnitID: &child.ID})
	require.NoError(t, err)
	sibling, err := svc.CreateUnit(ctx, 1, domain.CreateOrganizationUnitRequest{Name: "Другой корень", DisplayOrder: 1})
	require.NoError(t, err)
	foreign, err := svc.CreateUnit(ctx, 9, domain.CreateOrganizationUnitRequest{Name: "Чужой"})
	require.NoError(t, err)
	require.NoError(t, svc.AssignUser(ctx, 1, 2, domain.AssignUserToUnitRequest{UnitID: &leaf.ID}))
	_, err = db.Exec(ctx, `UPDATE users SET team_id=$1 WHERE id IN (3,4)`, leaf.ID)
	require.NoError(t, err)
	tree, err = svc.OrganizationTree(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 4, tree.TotalUnits)
	require.Len(t, tree.Items, 2)
	require.Equal(t, sibling.ID, tree.Items[0].ID)
	require.Equal(t, root.ID, tree.Items[1].ID)
	require.Equal(t, int64(1), tree.Items[1].Manager.ID)
	require.Equal(t, 1, tree.Items[1].SubtreeEmployeeCount)
	require.Equal(t, 1, tree.Items[1].Children[0].Children[0].EmployeeCount)
	require.Equal(t, 2, tree.ActiveEmployeeCount)
	require.Equal(t, 1, tree.UnassignedEmployeeCount)
	ownerTree, err := svc.OrganizationTree(ctx, 1)
	require.NoError(t, err)
	require.True(t, ownerTree.CanManageStructure)
	// Valid promotion to an ancestor was previously rejected; moving under a descendant was accepted.
	_, err = svc.UpdateUnit(ctx, 1, leaf.ID, domain.UpdateOrganizationUnitRequest{ParentUnitID: &root.ID})
	require.NoError(t, err)
	_, err = svc.UpdateUnit(ctx, 1, root.ID, domain.UpdateOrganizationUnitRequest{ParentUnitID: &leaf.ID})
	require.Error(t, err)
	_, err = svc.UpdateUnit(ctx, 1, root.ID, domain.UpdateOrganizationUnitRequest{ParentUnitID: &root.ID})
	require.Error(t, err)
	_, err = svc.UpdateUnit(ctx, 1, root.ID, domain.UpdateOrganizationUnitRequest{ParentUnitID: &foreign.ID})
	require.Error(t, err)
	_, err = svc.UpdateUnit(ctx, 1, foreign.ID, domain.UpdateOrganizationUnitRequest{ClearParent: true})
	require.Error(t, err)
	_, err = svc.UpdateUnit(ctx, 1, root.ID, domain.UpdateOrganizationUnitRequest{ManagerID: ptrContactID(9)})
	require.Error(t, err)
	_, err = svc.UpdateUnit(ctx, 1, root.ID, domain.UpdateOrganizationUnitRequest{ManagerID: ptrContactID(3)})
	require.Error(t, err)
	_, err = svc.UpdateUnit(ctx, 1, root.ID, domain.UpdateOrganizationUnitRequest{ClearManager: true})
	require.NoError(t, err)
	require.Error(t, svc.ArchiveUnit(ctx, 1, root.ID))
	require.Error(t, svc.ArchiveUnit(ctx, 1, leaf.ID))
	name := "Переименован"
	order := 0
	_, err = svc.UpdateUnit(ctx, 1, leaf.ID, domain.UpdateOrganizationUnitRequest{ClearParent: true, Name: &name, DisplayOrder: &order})
	require.NoError(t, err)
	tree, err = svc.OrganizationTree(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, leaf.ID, tree.Items[0].ID)
	require.Equal(t, name, tree.Items[0].Name)
	require.NoError(t, svc.ArchiveUnit(ctx, 1, child.ID))
	require.Error(t, svc.AssignUser(ctx, 1, 2, domain.AssignUserToUnitRequest{UnitID: &child.ID}))
	// Concurrent reverse moves cannot both succeed, including direct DB writes.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, pair := range [][2]uuid.UUID{{root.ID, sibling.ID}, {sibling.ID, root.ID}} {
		go func(pair [2]uuid.UUID) {
			<-start
			_, e := db.Exec(ctx, `UPDATE teams SET parent_team_id=$2 WHERE id=$1`, pair[0], pair[1])
			results <- e
		}(pair)
	}
	close(start)
	first, second := <-results, <-results
	require.True(t, (first == nil) != (second == nil))
	_, err = db.Exec(ctx, `UPDATE teams SET parent_team_id=NULL WHERE id IN ($1,$2)`, root.ID, sibling.ID)
	require.NoError(t, err)
	// Archive versus assignment uses the same lock: never leave a user in an archived unit.
	race, err := svc.CreateUnit(ctx, 1, domain.CreateOrganizationUnitRequest{Name: "Гонка"})
	require.NoError(t, err)
	start = make(chan struct{})
	results = make(chan error, 2)
	go func() {
		<-start
		_, e := db.Exec(ctx, `UPDATE teams SET deleted_at=NOW() WHERE id=$1`, race.ID)
		results <- e
	}()
	go func() { <-start; _, e := db.Exec(ctx, `UPDATE users SET team_id=$1 WHERE id=1`, race.ID); results <- e }()
	close(start)
	first, second = <-results, <-results
	require.True(t, (first == nil) != (second == nil))
	var dangling bool
	err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN teams t ON t.id=u.team_id WHERE u.deleted_at IS NULL AND t.deleted_at IS NOT NULL)`).Scan(&dangling)
	require.NoError(t, err)
	require.False(t, dangling)
	// Legacy corruption must be explicit, rather than silently omitting nodes.
	_, err = db.Exec(ctx, `ALTER TABLE teams DISABLE TRIGGER organization_unit_tree_guard;UPDATE teams SET parent_team_id=$2 WHERE id=$1;UPDATE teams SET parent_team_id=$1 WHERE id=$2;ALTER TABLE teams ENABLE TRIGGER organization_unit_tree_guard`, root.ID, sibling.ID)
	require.NoError(t, err)
	_, err = svc.OrganizationTree(ctx, 1)
	require.Error(t, err)
	_, err = db.Exec(ctx, parts[1])
	require.NoError(t, err)
	_, err = db.Exec(ctx, priorParts[1])
	require.NoError(t, err)
}
