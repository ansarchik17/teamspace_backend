package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

// The test schema has no admission function, so check the deactivation that offboarding writes.
func offboardingUserActive(t *testing.T, db *pgxpool.Pool, id int64) bool {
	t.Helper()
	var active bool
	require.NoError(t, db.QueryRow(context.Background(), `SELECT is_active AND deleted_at IS NULL FROM users WHERE id=$1`, id).Scan(&active))
	return active
}

func TestContactOffboardingIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "cnt007_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	// Keep production's prepared-statement mode to detect accidental parameterized multi-statements.
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,status TEXT,owner_id BIGINT,updated_at TIMESTAMPTZ);
 CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID,parent_team_id UUID,name TEXT,unit_type TEXT,manager_id BIGINT,display_order INT DEFAULT 0,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ);
 CREATE TABLE users(id BIGINT PRIMARY KEY,keycloak_id TEXT,organization_id UUID,role TEXT,deleted_at TIMESTAMPTZ,is_active BOOLEAN DEFAULT TRUE,full_name_local TEXT,first_name TEXT,last_name TEXT,email TEXT,username TEXT,position TEXT,avatar_url TEXT,availability_status TEXT,team_id UUID,manager_id BIGINT,updated_at TIMESTAMPTZ);
 CREATE TABLE boards(id UUID PRIMARY KEY,owner_id BIGINT,name TEXT,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE board_members(board_id UUID,user_id BIGINT,role TEXT,joined_at TIMESTAMPTZ DEFAULT NOW(),PRIMARY KEY(board_id,user_id));
 CREATE TABLE projects(id UUID PRIMARY KEY,board_id UUID,goal_id UUID,name TEXT,description TEXT,status TEXT,created_by BIGINT,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE folders(id UUID PRIMARY KEY,owner_id BIGINT,parent_id UUID,name TEXT,is_public BOOL DEFAULT FALSE,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE files(id UUID PRIMARY KEY,owner_id BIGINT,folder_id UUID,object_name TEXT,size BIGINT,scope TEXT,is_public BOOL DEFAULT FALSE,onlyoffice_key TEXT,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE file_versions(id UUID PRIMARY KEY,file_id UUID,object_name TEXT,size BIGINT,created_by BIGINT);
 CREATE TABLE permissions(resource_type TEXT,resource_id UUID,user_id BIGINT,role TEXT,created_at TIMESTAMPTZ,UNIQUE(resource_type,resource_id,user_id));
 CREATE TABLE drive_shared_links(token UUID PRIMARY KEY,resource_type TEXT,resource_id UUID,created_by BIGINT,expires_at TIMESTAMPTZ);
 CREATE TABLE retained_history(id UUID PRIMARY KEY,user_id BIGINT,content TEXT);`)
	require.NoError(t, err)
	org, other := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1,'active',1,NOW()),($2,'active',9,NOW())`, org, other)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id,role,manager_id,email,username) VALUES(1,$1,'user',NULL,'owner@work.test','owner'),(2,$1,'user',1,'employee@work.test','employee'),(3,$1,'user',2,'successor@work.test','successor'),(4,$1,'user',2,'report@work.test','report'),(5,$1,'guest',NULL,'guest@work.test','guest'),(6,$1,'admin',NULL,'admin@work.test','admin'),(7,$1,'user',NULL,'inactive@work.test','inactive'),(8,$1,'manager',NULL,'manager@work.test','manager'),(9,$2,'admin',NULL,'foreign@work.test','foreign')`, org, other)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=FALSE WHERE id=7`)
	require.NoError(t, err)
	board, sharedBoard, foreignBoard, project, sharedProject, foreignProject := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, b := range []struct {
		id    uuid.UUID
		owner int64
	}{{board, 2}, {sharedBoard, 1}, {foreignBoard, 9}} {
		_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id,name) VALUES($1,$2,'Board')`, b.id, b.owner)
		require.NoError(t, err)
	}
	for _, p := range []struct {
		id, board uuid.UUID
		creator   int64
	}{{project, board, 2}, {sharedProject, sharedBoard, 2}, {foreignProject, foreignBoard, 9}} {
		_, err = db.Exec(ctx, `INSERT INTO projects(id,board_id,name,status,created_by) VALUES($1,$2,'Project','active',$3)`, p.id, p.board, p.creator)
		require.NoError(t, err)
	}
	downs := []string{}
	for _, name := range []string{"20261001120000_employee_contacts.sql", "20261001140000_organization_tree_guards.sql", "20261001180000_contact_privacy.sql", "20260927140000_drive_storage_quotas.sql", "20261001200000_employee_offboarding.sql"} {
		data, e := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, e)
		parts := strings.Split(string(data), "-- +goose Down")
		_, e = db.Exec(ctx, parts[0])
		require.NoError(t, e, name)
		downs = append(downs, parts[1])
	}
	var ownerID int64
	require.NoError(t, db.QueryRow(ctx, `SELECT owner_id FROM projects WHERE id=$1`, project).Scan(&ownerID))
	require.Equal(t, int64(2), ownerID)
	folder, nested, foreignFolder, file, trash, chatFile, version, link, unit, history := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO folders(id,owner_id,name,is_public) VALUES($1,2,'Original',TRUE),($2,9,'Foreign',FALSE)`, folder, foreignFolder)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO folders(id,owner_id,parent_id,name) VALUES($1,2,$2,'Nested')`, nested, folder)
	require.NoError(t, err)
	for _, f := range []struct {
		id, folder uuid.UUID
		scope      string
	}{{file, nested, "drive"}, {trash, foreignFolder, "drive"}, {chatFile, folder, "chat"}} {
		_, err = db.Exec(ctx, `INSERT INTO files(id,owner_id,folder_id,object_name,size,scope,is_public,onlyoffice_key) VALUES($1,2,$2,$3,10,$4,TRUE,'old-key')`, f.id, f.folder, f.id.String(), f.scope)
		require.NoError(t, err)
	}
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW() WHERE id=$1`, trash)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO file_versions VALUES($1,$2,'old-version',20,2)`, version, file)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO permissions VALUES('file',$1,2,'owner',NOW()),('file',$1,4,'viewer',NOW()),('file',$1,3,'viewer',NOW())`, file)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO drive_shared_links VALUES($1,'file',$2,2,NULL)`, link, file)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO teams(id,organization_id,name,unit_type,manager_id) VALUES($1,$2,'Unit','department',2)`, unit, org)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO retained_history VALUES($1,2,'Original author and content')`, history)
	require.NoError(t, err)
	repo := repository.NewOrganizationRepository(db, zap.NewNop())
	svc := NewOrganizationService(repo, zap.NewNop())
	users := repository.NewUserRepository(db, zap.NewNop())
	req := domain.ContactOffboardingRequest{SuccessorID: 3, Reason: "  Departure  "}
	for _, actor := range []int64{2, 3, 5, 7, 8, 9} {
		_, e := svc.OffboardContact(ctx, actor, 2, req, true)
		require.Error(t, e)
	}
	for _, successor := range []int64{2, 5, 7, 9, 99} {
		_, e := svc.OffboardContact(ctx, 1, 2, domain.ContactOffboardingRequest{SuccessorID: successor, Reason: "Departure"}, true)
		require.Error(t, e)
	}
	for _, target := range []int64{1, 6, 9, 99} {
		_, e := svc.OffboardContact(ctx, 1, target, req, true)
		require.Error(t, e)
	}
	preview, err := svc.OffboardContact(ctx, 1, 2, req, true)
	require.NoError(t, err)
	require.True(t, preview.DryRun)
	require.False(t, preview.Applied)
	require.Equal(t, 1, preview.Boards)
	require.Equal(t, 2, preview.Projects)
	require.Equal(t, 2, preview.Files)
	require.Equal(t, 2, preview.Folders)
	require.Equal(t, 1, preview.Units)
	require.Equal(t, 2, preview.DirectReports)
	require.True(t, offboardingUserActive(t, db, 2))
	_, err = svc.ContactOffboarding(ctx, 1, 2)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM folders WHERE owner_id=3`).Scan(&count))
	require.Zero(t, count)
	// Quota failure must roll back project/board transfers as well as account deactivation.
	_, err = db.Exec(ctx, `INSERT INTO drive_storage_accounts(user_id,quota_bytes) VALUES(3,1)`)
	require.NoError(t, err)
	_, err = svc.OffboardContact(ctx, 1, 2, req, false)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
	require.True(t, offboardingUserActive(t, db, 2))
	require.NoError(t, db.QueryRow(ctx, `SELECT owner_id FROM projects WHERE id=$1`, project).Scan(&ownerID))
	require.Equal(t, int64(2), ownerID)
	_, err = db.Exec(ctx, `UPDATE drive_storage_accounts SET quota_bytes=1000 WHERE user_id=3`)
	require.NoError(t, err)
	// A project on a foreign-owned board cannot leak through a handover.
	_, err = db.Exec(ctx, `UPDATE projects SET owner_id=2 WHERE id=$1`, foreignProject)
	require.NoError(t, err)
	_, err = svc.OffboardContact(ctx, 1, 2, req, false)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE projects SET owner_id=9 WHERE id=$1`, foreignProject)
	require.NoError(t, err)
	// Repeated concurrent submissions transfer once and return the same persisted result.
	results := make(chan *domain.ContactOffboardingResult, 2)
	errs := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; r, e := svc.OffboardContact(ctx, 1, 2, req, false); results <- r; errs <- e }()
	}
	close(start)
	first, second := <-results, <-results
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Equal(t, first, second)
	require.True(t, first.Applied)
	require.NotNil(t, first.HandoverFolderID)
	require.False(t, offboardingUserActive(t, db, 2))
	require.Error(t, users.CheckNotOffboarded(ctx, 2))
	require.True(t, offboardingUserActive(t, db, 3))
	_, err = svc.GetContactProfile(ctx, 3, 2)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	for _, query := range []string{`UPDATE users SET is_active=TRUE WHERE id=2`, `UPDATE users SET deleted_at=NOW() WHERE id=2`, `UPDATE users SET keycloak_id='new-identity' WHERE id=2`, `INSERT INTO boards(id,owner_id) VALUES(gen_random_uuid(),2)`, `INSERT INTO projects(id,board_id,created_by) SELECT gen_random_uuid(),id,2 FROM boards LIMIT 1`, `INSERT INTO files(id,owner_id,object_name,size,scope) VALUES(gen_random_uuid(),2,'late',1,'drive')`, `INSERT INTO folders(id,owner_id,name) VALUES(gen_random_uuid(),2,'late')`} {
		_, e := db.Exec(ctx, query)
		require.Error(t, e, query)
	}
	_, err = svc.OffboardContact(ctx, 1, 2, domain.ContactOffboardingRequest{SuccessorID: 4, Reason: "Different successor"}, false)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	var author int64
	var object, key string
	var parent uuid.UUID
	var public bool
	require.NoError(t, db.QueryRow(ctx, `SELECT owner_id,object_name,onlyoffice_key,is_public,folder_id FROM files WHERE id=$1`, file).Scan(&ownerID, &object, &key, &public, &parent))
	require.Equal(t, int64(3), ownerID)
	require.Equal(t, file.String(), object)
	require.NotEqual(t, "old-key", key)
	require.False(t, public)
	require.Equal(t, nested, parent)
	require.NoError(t, db.QueryRow(ctx, `SELECT folder_id FROM files WHERE id=$1`, trash).Scan(&parent))
	require.Equal(t, *first.HandoverFolderID, parent)
	require.NoError(t, db.QueryRow(ctx, `SELECT owner_id FROM files WHERE id=$1`, chatFile).Scan(&ownerID))
	require.Equal(t, int64(2), ownerID)
	require.NoError(t, db.QueryRow(ctx, `SELECT created_by FROM file_versions WHERE id=$1`, version).Scan(&author))
	require.Equal(t, int64(2), author)
	require.NoError(t, db.QueryRow(ctx, `SELECT user_id FROM retained_history WHERE id=$1`, history).Scan(&author))
	require.Equal(t, int64(2), author)
	p, err := repository.NewBoardRepository(db, zap.NewNop()).GetProject(ctx, project)
	require.NoError(t, err)
	require.Equal(t, int64(2), p.CreatedBy)
	require.Equal(t, int64(3), p.OwnerID)
	var boardRole string
	require.NoError(t, db.QueryRow(ctx, `SELECT role FROM board_members WHERE board_id=$1 AND user_id=3`, board).Scan(&boardRole))
	require.Equal(t, "owner", boardRole)
	require.NoError(t, db.QueryRow(ctx, `SELECT role FROM board_members WHERE board_id=$1 AND user_id=3`, sharedBoard).Scan(&boardRole))
	require.Equal(t, "editor", boardRole)
	var fileRole string
	require.NoError(t, db.QueryRow(ctx, `SELECT role FROM permissions WHERE resource_type='file' AND resource_id=$1 AND user_id=4`, file).Scan(&fileRole))
	require.Equal(t, "viewer", fileRole)
	var expired bool
	require.NoError(t, db.QueryRow(ctx, `SELECT expires_at<=NOW() FROM drive_shared_links WHERE token=$1`, link).Scan(&expired))
	require.True(t, expired)
	require.NoError(t, db.QueryRow(ctx, `SELECT manager_id FROM users WHERE id=3`).Scan(&author))
	require.Equal(t, int64(1), author)
	require.NoError(t, db.QueryRow(ctx, `SELECT manager_id FROM teams WHERE id=$1`, unit).Scan(&author))
	require.Equal(t, int64(3), author)
	// Persistent retries survive network errors. Only successful disable/logout completes the outbox.
	attempts := 0
	svc.SetOffboardingIdentitySync(func(context.Context, int64) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary outage")
		}
		return nil
	})
	svc.syncOffboardingIdentities(ctx)
	require.Equal(t, 1, attempts)
	record, err := svc.ContactOffboarding(ctx, 1, 2)
	require.NoError(t, err)
	require.Equal(t, "pending", record.IdentitySyncStatus)
	svc.syncOffboardingIdentities(ctx)
	require.Equal(t, 1, attempts)
	_, err = db.Exec(ctx, `UPDATE employee_offboardings SET identity_sync_next_at=NOW() WHERE user_id=2`)
	require.NoError(t, err)
	svc.syncOffboardingIdentities(ctx)
	require.Equal(t, 2, attempts)
	record, err = svc.ContactOffboarding(ctx, 6, 2)
	require.NoError(t, err)
	require.Equal(t, "complete", record.IdentitySyncStatus)
	require.Equal(t, "Departure", record.Reason)
	_, err = svc.ContactOffboarding(ctx, 9, 2)
	require.Error(t, err)
	indexed := []uuid.UUID{}
	svc.SetOffboardingFileReindex(func(_ context.Context, id uuid.UUID) error { indexed = append(indexed, id); return nil })
	svc.reindexOffboardingFiles(ctx, 2)
	require.ElementsMatch(t, []uuid.UUID{file, trash}, indexed)
	for i := len(downs) - 1; i >= 0; i-- {
		_, err = db.Exec(ctx, downs[i])
		require.NoError(t, err)
	}
}
