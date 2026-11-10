package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
)

func TestContactImportParsers(t *testing.T) {
	csv := "\ufeffusername;email;first_name;last_name;phone\nalice;Alice@example.com;Алиса;Иванова;+7 (701) 123-45-67\n"
	rows, issues, err := ParseContactImport("employees.csv", strings.NewReader(csv))
	require.NoError(t, err)
	require.Empty(t, issues)
	require.Empty(t, ValidateContactImport(rows))
	require.Equal(t, "alice@example.com", rows[0].Email)
	require.Equal(t, "+77011234567", *rows[0].Phone)
	workbook := excelize.NewFile()
	defer workbook.Close()
	require.NoError(t, workbook.SetSheetRow("Sheet1", "A1", &[]string{"username", "email", "first_name", "last_name"}))
	require.NoError(t, workbook.SetSheetRow("Sheet1", "A2", &[]string{"alice", "alice@example.com", "Алиса", "Иванова"}))
	buf, err := workbook.WriteToBuffer()
	require.NoError(t, err)
	rows, issues, err = ParseContactImport("employees.xlsx", buf)
	require.NoError(t, err)
	require.Empty(t, issues)
	require.Len(t, rows, 1)
	for _, data := range []string{"email,first_name,last_name\na@b.com,A,B", "username,username,email,first_name,last_name\na,a,a@b.com,A,B", "username,email,first_name,last_name,role\na,a@b.com,A,B,admin"} {
		_, _, err = ParseContactImport("bad.csv", strings.NewReader(data))
		require.Error(t, err)
	}
	_, _, err = ParseContactImport("employees.xls", bytes.NewReader([]byte("legacy")))
	require.Error(t, err)
	rows, _, err = ParseContactImport("employees.csv", strings.NewReader("username,email,first_name,last_name,phone\na,a@b.com,A,B,bad\na,a@b.com,A,B,bad"))
	require.NoError(t, err)
	require.NotEmpty(t, ValidateContactImport(rows))
}

func TestDirectorySettingsAndEncryption(t *testing.T) {
	t.Setenv("CONTACTS_DIRECTORY_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	config, err := normalizeDirectorySettings(domain.DirectorySettings{Type: "ad", URL: "ldaps://directory.example:636", BaseDN: "dc=example,dc=com", BindDN: "cn=reader,dc=example,dc=com", Enabled: true})
	require.NoError(t, err)
	require.Equal(t, "objectGUID", config.Attributes["external_id"])
	encrypted, err := encryptDirectoryPassword("test-bind-secret", "org1")
	require.NoError(t, err)
	require.NotContains(t, string(encrypted), "test-bind-secret")
	decrypted, err := decryptDirectoryPassword(encrypted, "org1")
	require.NoError(t, err)
	require.Equal(t, "test-bind-secret", decrypted)
	_, err = decryptDirectoryPassword(encrypted, "org2")
	require.Error(t, err)
	config.URL = "https://directory.example"
	_, err = normalizeDirectorySettings(config)
	require.Error(t, err)
	config.URL = "ldaps://directory.example:636"
	t.Setenv("CONTACTS_LDAP_ALLOWED_HOSTS", "")
	_, err = fetchLDAPContacts(context.Background(), config, "secret")
	require.Error(t, err)
}

func TestADDirectoryMapping(t *testing.T) {
	settings, err := normalizeDirectorySettings(domain.DirectorySettings{Type: "ad", URL: "ldaps://ad.example:636", BaseDN: "dc=example,dc=com", BindDN: "reader"})
	require.NoError(t, err)
	entry := ldap.NewEntry("CN=Alice,DC=example,DC=com", map[string][]string{"objectGUID": {string(bytes.Repeat([]byte{1}, 16))}, "sAMAccountName": {"alice"}, "mail": {"alice@example.com"}, "givenName": {"Alice"}, "sn": {"Test"}, "userAccountControl": {"514"}})
	rows, err := directoryEntriesToContacts([]*ldap.Entry{entry}, settings)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("01", 16), rows[0].ExternalID)
	require.False(t, *rows[0].IsActive)
	entry.Attributes = append(entry.Attributes, &ldap.EntryAttribute{Name: "manager", Values: []string{"CN=Outside,DC=example,DC=com"}})
	_, err = directoryEntriesToContacts([]*ldap.Entry{entry}, settings)
	require.Error(t, err)
}

func TestContactImportIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	t.Setenv("CONTACTS_DIRECTORY_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "cnt005_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
 CREATE TABLE users(id BIGSERIAL PRIMARY KEY,organization_id UUID,role TEXT,deleted_at TIMESTAMPTZ,is_active BOOLEAN DEFAULT TRUE,is_first_login BOOLEAN DEFAULT TRUE,keycloak_id TEXT,full_name_local TEXT,first_name TEXT,last_name TEXT,email TEXT UNIQUE,username TEXT,position TEXT,avatar_url TEXT,availability_status TEXT,team_id UUID,manager_id BIGINT,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW());`)
	require.NoError(t, err)
	org, other, unit := uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1,'active',1,now()),($2,'active',9,now());INSERT INTO users(id,organization_id,role,username,email,first_name,last_name) VALUES(1,$1,'user','owner','owner@example.com','Owner','Test'),(9,$2,'user','foreign','foreign@example.com','Foreign','Test');SELECT setval(pg_get_serial_sequence('users','id'),9,true);INSERT INTO teams(id,organization_id,name,unit_type) VALUES($3,$1,'Engineering','department')`, org, other, unit)
	require.NoError(t, err)
	migrations := []string{"20261001120000_employee_contacts.sql", "20261001140000_organization_tree_guards.sql", "20261001160000_employee_directory.sql", "20261001180000_contact_privacy.sql"}
	downs := []string{}
	for _, name := range migrations {
		data, e := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, e)
		parts := strings.Split(string(data), "-- +goose Down")
		_, e = db.Exec(ctx, parts[0])
		require.NoError(t, e)
		downs = append(downs, parts[1])
	}
	svc := NewOrganizationService(repository.NewOrganizationRepository(db, zap.NewNop()), zap.NewNop())
	csv := "external_id,username,email,first_name,last_name,department,manager_username,phone\nbob-id,bob,bob@example.com,Bob,Test,Engineering,alice,+77011234567\nalice-id,alice,alice@example.com,Alice,Test,Engineering,owner,\n"
	preview, err := svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(csv), true)
	require.NoError(t, err)
	require.Empty(t, preview.Issues)
	require.Equal(t, 2, preview.Created)
	require.False(t, preview.Applied)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count))
	require.Equal(t, 2, count)
	result, err := svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(csv), false)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, 2, result.Created)
	result, err = svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(csv), false)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.True(t, result.Applied)
	require.Equal(t, 0, result.Created)
	require.Equal(t, 2, result.Updated)
	var bob, alice int64
	require.NoError(t, db.QueryRow(ctx, `SELECT id FROM users WHERE username='bob'`).Scan(&bob))
	require.NoError(t, db.QueryRow(ctx, `SELECT id FROM users WHERE username='alice'`).Scan(&alice))
	var manager int64
	require.NoError(t, db.QueryRow(ctx, `SELECT manager_id FROM users WHERE id=$1`, bob).Scan(&manager))
	require.Equal(t, alice, manager)
	_, err = svc.ImportContacts(ctx, bob, "test.csv", strings.NewReader(csv), false)
	require.Error(t, err)
	bad := strings.Replace(csv, "alice@example.com", "foreign@example.com", 1)
	result, err = svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(strings.Replace(bad, "alice-id,alice", "new-id,new-user", 1)), false)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.NotEmpty(t, result.Issues)
	cycle := strings.Replace(csv, "Engineering,owner,", "Engineering,bob,", 1)
	result, err = svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(cycle), false)
	require.NoError(t, err)
	require.NotEmpty(t, result.Issues)
	require.False(t, result.Applied)
	_, err = db.Exec(ctx, `UPDATE employee_contacts SET personal_email='private.import@example.net',personal_phone='+77015550002',personal_email_visibility='private',personal_phone_visibility='managers' WHERE user_id=$1`, alice)
	require.NoError(t, err)
	renamed := strings.Replace(csv, "alice-id,alice,alice@example.com", "alice-id,new-login,new-work@example.com", 1)
	renamed = strings.Replace(renamed, "Engineering,alice,", "Engineering,new-login,", 1)
	result, err = svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(renamed), false)
	require.NoError(t, err)
	require.True(t, result.Applied)
	var username, email, work string
	require.NoError(t, db.QueryRow(ctx, `SELECT u.username,u.email,c.work_email FROM users u JOIN employee_contacts c ON c.user_id=u.id WHERE u.id=$1`, alice).Scan(&username, &email, &work))
	require.Equal(t, "alice", username)
	require.Equal(t, "alice@example.com", email)
	require.Equal(t, "new-work@example.com", work)
	var personalEmail, personalPhone, emailVisibility, phoneVisibility string
	require.NoError(t, db.QueryRow(ctx, `SELECT personal_email,personal_phone,personal_email_visibility,personal_phone_visibility FROM employee_contacts WHERE user_id=$1`, alice).Scan(&personalEmail, &personalPhone, &emailVisibility, &phoneVisibility))
	require.Equal(t, "private.import@example.net", personalEmail)
	require.Equal(t, "+77015550002", personalPhone)
	require.Equal(t, "private", emailVisibility)
	require.Equal(t, "managers", phoneVisibility)

	// Concurrent copies of the same file resolve to the same employee set.
	start := make(chan struct{})
	results := make(chan *domain.ContactImportResult, 2)
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			r, e := svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(csv), false)
			results <- r
			errors <- e
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		r := <-results
		require.NoError(t, <-errors)
		require.True(t, r.Applied)
		require.Zero(t, r.Created)
	}
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count))
	require.Equal(t, 4, count)
	// A failure in a later row rolls back earlier profile changes.
	badDepartment := strings.Replace(csv, "Engineering,owner,", "Missing,owner,", 1)
	result, err = svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(badDepartment), false)
	require.NoError(t, err)
	require.NotEmpty(t, result.Issues)
	require.False(t, result.Applied)
	protect := "username,email,first_name,last_name,is_active\nowner,owner@example.com,Owner,Test,false\n"
	result, err = svc.ImportContacts(ctx, 1, "test.csv", strings.NewReader(protect), false)
	require.NoError(t, err)
	require.NotEmpty(t, result.Issues)
	require.False(t, result.Applied)
	settings := domain.DirectorySettings{Type: "ldap", URL: "ldaps://ldap.example:636", BaseDN: "dc=example,dc=com", BindDN: "cn=reader,dc=example,dc=com", Enabled: true}
	password := "test-secret"
	connector, err := svc.SaveContactDirectoryConfig(ctx, 1, domain.DirectoryConfigRequest{DirectorySettings: settings, BindPassword: &password})
	require.NoError(t, err)
	require.True(t, connector.HasBindPassword)
	encoded, err := json.Marshal(connector)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), password)
	require.NotContains(t, string(encoded), "\"bind_password\"")
	svc.directoryFetch = func(context.Context, domain.DirectorySettings, string) ([]domain.ContactImportRow, error) {
		return []domain.ContactImportRow{{Row: 1, ExternalID: "ldap-alice", Username: "alice", Email: "alice@example.com", FirstName: "Directory", LastName: "Alice"}}, nil
	}
	result, err = svc.SyncContactDirectory(ctx, 1, false)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, 1, result.Updated)
	connector, err = svc.ContactDirectoryConfig(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, connector.LastSyncAt)
	// Two simultaneous remote reads cannot overwrite a newer applied snapshot.
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	syncErrors := make(chan error, 2)
	svc.directoryFetch = func(context.Context, domain.DirectorySettings, string) ([]domain.ContactImportRow, error) {
		entered <- struct{}{}
		<-release
		return []domain.ContactImportRow{{Row: 1, ExternalID: "ldap-alice", Username: "alice", Email: "alice@example.com", FirstName: "Directory", LastName: "Alice"}}, nil
	}
	for i := 0; i < 2; i++ {
		go func() { _, e := svc.SyncContactDirectory(ctx, 1, false); syncErrors <- e }()
	}
	<-entered
	<-entered
	close(release)
	firstSync, secondSync := <-syncErrors, <-syncErrors
	require.True(t, (firstSync == nil) != (secondSync == nil))
	// Configuration changes while the remote read is in progress cannot apply a stale result.
	svc.directoryFetch = func(context.Context, domain.DirectorySettings, string) ([]domain.ContactImportRow, error) {
		_, e := db.Exec(ctx, `UPDATE employee_directory_configs SET revision=revision+1 WHERE organization_id=$1`, org)
		require.NoError(t, e)
		return []domain.ContactImportRow{{Row: 1, ExternalID: "ldap-new", Username: "ldap-new", Email: "ldap-new@example.com", FirstName: "New", LastName: "Test"}}, nil
	}
	_, err = svc.SyncContactDirectory(ctx, 1, false)
	require.Error(t, err)
	settings.URL = "ldaps://changed.example:636"
	_, err = svc.SaveContactDirectoryConfig(ctx, 1, domain.DirectoryConfigRequest{DirectorySettings: settings})
	require.Error(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT personal_email,personal_phone,personal_email_visibility,personal_phone_visibility FROM employee_contacts WHERE user_id=$1`, alice).Scan(&personalEmail, &personalPhone, &emailVisibility, &phoneVisibility))
	require.Equal(t, "private.import@example.net", personalEmail)
	require.Equal(t, "+77015550002", personalPhone)
	require.Equal(t, "private", emailVisibility)
	require.Equal(t, "managers", phoneVisibility)
	for i := len(downs) - 1; i >= 0; i-- {
		_, err = db.Exec(ctx, downs[i])
		require.NoError(t, err)
	}
}
