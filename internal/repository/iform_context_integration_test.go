package repository

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestIFormContextTenantIsolationAndRevocation(t *testing.T) {
	dsn := os.Getenv("IFORM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set IFORM_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "iform_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(ctx, `
 CREATE TABLE organizations(id uuid PRIMARY KEY,name text,status text,owner_id bigint);
 CREATE TABLE users(id bigint PRIMARY KEY,organization_id uuid,full_name_local text,first_name text,last_name text,position text,team_id uuid,manager_id bigint,is_active boolean,deleted_at timestamptz,email text DEFAULT '',username text DEFAULT '');
 CREATE TABLE user_mailboxes(mailbox_login text);
 CREATE TABLE teams(id uuid PRIMARY KEY,organization_id uuid,name text,parent_team_id uuid,manager_id bigint,deleted_at timestamptz);
 CREATE TABLE organization_access_apps(organization_id uuid,application text CHECK(application IN ('odo','sd')),enabled boolean,revision bigint DEFAULT 0,PRIMARY KEY(organization_id,application));
 CREATE TABLE organization_access_positions(id uuid PRIMARY KEY,organization_id uuid,name text);
 CREATE TABLE organization_access_assignments(organization_id uuid,user_id bigint,position_id uuid);
 CREATE TABLE organization_position_permissions(organization_id uuid,position_id uuid,application text CHECK(application IN ('odo','sd')),permissions text[],revision bigint DEFAULT 1);
 `)
	if err != nil {
		t.Fatal(err)
	}
	admissionMigration, err := os.ReadFile("../../db/migrations/20261022120000_employment_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, strings.Split(string(admissionMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../db/migrations/20261006000100_iform_access.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	a, b, unitA, unitB, posA, posB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1,'Company A','active',1),($2,'Company B private','active',10)`, a, b); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO users(id,organization_id,first_name,last_name,team_id,manager_id,is_active) VALUES (1,$1,'Owner A','',$3,NULL,true),(2,$1,'Employee A','',$3,1,true),(10,$2,'Owner B private','',$4,NULL,true),(11,$2,'Employee B private','',$4,10,true),(99,NULL,'Platform','',NULL,NULL,true)`, a, b, unitA, unitB); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO teams VALUES($1,$3,'Same unit name',NULL,1,NULL),($2,$4,'Same unit name',NULL,10,NULL)`, unitA, unitB, a, b); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO organization_access_apps(organization_id,application,enabled) VALUES($1,'iform',true),($2,'iform',true)`, a, b); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO organization_access_positions VALUES($1,$3,'Same title'),($2,$4,'Same title')`, posA, posB, a, b); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO organization_access_assignments VALUES($1,2,$3),($2,11,$4);`, a, b, posA, posB); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO organization_position_permissions(organization_id,position_id,application,permissions) VALUES($1,$2,'iform',ARRAY['iform.use']),($3,$4,'iform',ARRAY['iform.use'])`, a, posA, b, posB); err != nil {
		t.Fatal(err)
	}
	moduleMigration, err := os.ReadFile("../../db/migrations/20261012000300_company_modules.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, strings.Split(string(moduleMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	repo := NewOrganizationRepository(db, zap.NewNop())
	identity, err := repo.IFormIdentity(ctx, 2)
	if err != nil || identity.OrganizationID != a {
		t.Fatalf("incorrect proxy identity: %v", err)
	}
	view, err := repo.IFormContext(ctx, 2, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Positions) != 1 || view.Positions[0].ID != posA.String() || view.Members[1].PositionID != posA.String() {
		t.Fatalf("position identities not company-scoped: %+v", view)
	}
	encoded, _ := json.Marshal(view)
	if len(view.Members[0].Roles) == 0 {
		t.Fatal("live owner lost automatic iForm roles")
	}
	if _, err = repo.IFormIdentity(ctx, 1); err != nil {
		t.Fatalf("live owner without a position cannot enter iForm: %v", err)
	}
	if view.OrganizationID != a || len(view.Members) != 2 || len(view.Units) != 1 || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), unitB.String()) {
		t.Fatalf("foreign directory exposed: %s", encoded)
	}
	if _, err = db.Exec(ctx, `UPDATE users SET email='a.used@company.example' WHERE id=2; INSERT INTO user_mailboxes VALUES('a.mailbox@company.example')`); err != nil {
		t.Fatal(err)
	}
	free, err := repo.FirstAvailableIFormEmail(ctx, []string{"a.used@company.example", "a.mailbox@company.example", "a.free@company.example"})
	if err != nil || free != "a.free@company.example" {
		t.Fatalf("incorrect email availability: %q %v", free, err)
	}
	if free, err = repo.FirstAvailableIFormEmail(ctx, []string{"a.used@company.example"}); err != nil || free != "" {
		t.Fatalf("occupied email reused: %q %v", free, err)
	}
	if _, err = repo.IFormContext(ctx, 2, b); err == nil {
		t.Fatal("browser selected foreign tenant")
	}
	if _, err = repo.IFormContext(ctx, 99, uuid.Nil); err == nil {
		t.Fatal("platform identity received customer directory")
	}
	if _, err = db.Exec(ctx, `UPDATE users SET team_id=$1,manager_id=10 WHERE id=2`, unitB); err != nil {
		t.Fatal(err)
	}
	view, err = repo.IFormContext(ctx, 2, a)
	if err != nil {
		t.Fatal(err)
	}
	if view.Members[1].UnitID != "" || view.Members[1].ManagerID != "" {
		t.Fatal("foreign user/unit relationship leaked")
	}
	if _, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.IFormContext(ctx, 2, a); err == nil {
		t.Fatal("disabled user retained access")
	}
	if _, err = repo.IFormIdentity(ctx, 2); err == nil {
		t.Fatal("proxy retained disabled identity")
	}
	if _, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE organization_modules SET available=false WHERE organization_id=$1 AND application='iform'`, a); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.IFormIdentity(ctx, 2); err == nil {
		t.Fatal("platform disabled module retained proxy access")
	}
	if _, err = repo.IFormContext(ctx, 1, a); err == nil {
		t.Fatal("platform disabled module retained owner access")
	}
	if _, err = db.Exec(ctx, `UPDATE organization_modules SET available=true,enabled=false WHERE organization_id=$1 AND application='iform'`, a); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.IFormIdentity(ctx, 2); err == nil {
		t.Fatal("director disabled module retained proxy access")
	}
	if _, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=true WHERE organization_id=$1 AND application='iform'`, a); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE organization_position_permissions SET permissions='{}' WHERE organization_id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.IFormContext(ctx, 2, a); err == nil {
		t.Fatal("revoked application permissions retained access")
	}
	if _, err = repo.IFormContext(ctx, 1, a); err != nil {
		t.Fatalf("position policy revoked live owner's automatic access: %v", err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO organization_access_assignments VALUES($1,1,$2)`, a, posA); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE organization_position_permissions SET permissions=ARRAY['iform.finance'] WHERE organization_id=$1`, a); err != nil {
		t.Fatal(err)
	}
	view, err = repo.IFormContext(ctx, 1, a)
	if err != nil || !slices.Contains(view.Members[0].Roles, "TENANT_ADMIN") || !slices.Contains(view.Members[0].Roles, "FINANCE_APPROVER") {
		t.Fatalf("position policy narrowed live owner's automatic roles: %v (%v)", view, err)
	}
	if _, err = db.Exec(ctx, `UPDATE organization_access_apps SET enabled=false WHERE organization_id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.IFormContext(ctx, 1, a); err == nil {
		t.Fatal("disabled application retained owner access")
	}
	if _, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, b); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.IFormContext(ctx, 10, b); err == nil {
		t.Fatal("suspended company retained access")
	}
}
