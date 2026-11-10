package repository

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
	"go.uber.org/zap"
)

func TestContactsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "cnt001_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
 CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID,parent_team_id UUID,name TEXT,unit_type TEXT,display_order INT DEFAULT 0,deleted_at TIMESTAMPTZ);
 CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,role TEXT,deleted_at TIMESTAMPTZ,is_active BOOLEAN DEFAULT TRUE,full_name_local TEXT,first_name TEXT,last_name TEXT,email TEXT,username TEXT,position TEXT,avatar_url TEXT,availability_status TEXT,team_id UUID,manager_id BIGINT);`)
	require.NoError(t, err)
	org, other, unit, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1,'active',1,now()),($2,'active',9,now()); INSERT INTO teams(id,organization_id,name,unit_type) VALUES($3,$1,'Разработка','department'),($4,$2,'Чужой','department')`, org, other, unit, foreign)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id,role,full_name_local,email,username,position,team_id,manager_id) VALUES
 (1,$1,'user','Ансар Келес','ansar@example.com','ansar',' Разработчик ',$3,9),
 (2,$1,'user','Анна','anna@example.com','anna','разработчик',$3,1),
 (3,$1,'user','Процент %_','literal@example.com','literal','Директор',NULL,NULL),
 (4,$1,'user','Неактивный','inactive@example.com','inactive','Скрытый',$3,NULL),
 (5,$1,'user','Удалённый','deleted@example.com','deleted','Скрытый',$3,NULL),
 (9,$2,'user','Ансар чужой','foreign@example.com','foreign','Чужая должность',$4,NULL);
 UPDATE users SET is_active=FALSE WHERE id=4; UPDATE users SET deleted_at=now() WHERE id=5;`, org, other, unit, foreign)
	require.NoError(t, err)
	repo := NewOrganizationRepository(db, zap.NewNop())
	page, err := repo.ListContacts(ctx, 1, domain.ContactFilter{Query: "АНСАР", UnitID: &unit, Position: "разработчик", Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	require.Len(t, page.Items, 1)
	require.Nil(t, page.Items[0].ManagerID)
	require.Equal(t, "Разработка", *page.Items[0].UnitName)
	for _, q := range []string{"ANSAR@", "ansar", "%_"} {
		p, e := repo.ListContacts(ctx, 1, domain.ContactFilter{Query: q})
		require.NoError(t, e)
		require.Equal(t, 1, p.Total)
	}
	page, err = repo.ListContacts(ctx, 1, domain.ContactFilter{Limit: 1, Offset: 100})
	require.NoError(t, err)
	require.Equal(t, 3, page.Total)
	require.Empty(t, page.Items)
	page, err = repo.ListContacts(ctx, 1, domain.ContactFilter{UnitID: &foreign})
	require.NoError(t, err)
	require.Zero(t, page.Total)
	page, err = repo.ListContacts(ctx, 1, domain.ContactFilter{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Equal(t, 3, page.Total)
	require.Len(t, page.Items, 1)
	filters, err := repo.ContactFilters(ctx, 1)
	require.NoError(t, err)
	require.Len(t, filters.Units, 1)
	require.Len(t, filters.Positions, 2)
	_, err = repo.ListContacts(ctx, 99, domain.ContactFilter{})
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, org)
	require.NoError(t, err)
	_, err = repo.ContactFilters(ctx, 1)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
}
