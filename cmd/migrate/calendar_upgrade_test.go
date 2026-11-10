package main

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

const calendarMainBaseline int64 = 20260928000100

const (
	calendarReleaseStartVersion int64 = 20260929000100
	notesReleaseStartVersion    int64 = 20260929000600
)

func calendarReleaseMigration(name string) bool {
	for _, suffix := range []string{
		"_create_calendar_visibility_settings.sql", "_calendar_event_exceptions.sql",
		"_add_tentative_status_to_attendees.sql", "_create_meeting_rooms_and_bookings.sql",
		"_calendar_integrations_tz_qmeet.sql",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func TestCalendarReleaseMigrationsFollowMain(t *testing.T) {
	migrations, err := goose.CollectMigrations("../../db/migrations", 0, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, migration := range migrations {
		if calendarReleaseMigration(filepath.Base(migration.Source)) {
			count++
			if migration.Version <= calendarMainBaseline {
				t.Fatalf("calendar migration %s must follow main version %d", migration.Source, calendarMainBaseline)
			}
		}
	}
	if count != 5 {
		t.Fatalf("got %d calendar release migrations, want 5", count)
	}
}

// A disposable schema models the calendar tables and applied migration ledger
// of main. This tests an upgrade, not just applying isolated SQL files.
func TestCalendarReleaseUpgradeFromMainIntegration(t *testing.T) {
	testReleaseUpgrade(t, false, false)
}

func TestNotesReleaseUpgradeFromCalendarIntegration(t *testing.T) {
	testReleaseUpgrade(t, true, false)
}

func TestNewsReleaseUpgradeFromAudioIntegration(t *testing.T) {
	testReleaseUpgrade(t, false, true)
}

func notesReleaseMigration(name string) bool {
	return strings.HasSuffix(name, "_notes_formatting_color.sql") || strings.HasSuffix(name, "_notes_navigation_tags_favorites.sql")
}

func TestNotesReleaseMigrationsFollowCalendar(t *testing.T) {
	migrations, err := goose.CollectMigrations("../../db/migrations", 0, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, migration := range migrations {
		if notesReleaseMigration(filepath.Base(migration.Source)) {
			count++
			if migration.Version <= 20260929000500 {
				t.Fatalf("notes migration predates deployed calendar: %s", migration.Source)
			}
		}
	}
	if count != 2 {
		t.Fatalf("got %d notes release migrations, want 2", count)
	}
}

func testReleaseUpgrade(t *testing.T, calendarApplied, audioApplied bool) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := "calendar_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := db.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		-- Never inherit or mutate the public schema's migration ledger.
		CREATE TABLE goose_db_version(id BIGSERIAL PRIMARY KEY,version_id BIGINT NOT NULL,is_applied BOOLEAN NOT NULL,tstamp TIMESTAMP NOT NULL DEFAULT now());
		INSERT INTO goose_db_version(version_id,is_applied) VALUES(0,true);
		-- Match lifecycle/identity columns from 001 and 019, recorded as applied below.
		CREATE TABLE users(id BIGINT PRIMARY KEY,position VARCHAR(255),is_active BOOLEAN NOT NULL DEFAULT true,
		 deleted_at TIMESTAMPTZ,keycloak_id VARCHAR(64),iin VARCHAR(12),email VARCHAR(255),username VARCHAR(50),updated_at TIMESTAMPTZ DEFAULT now());
		CREATE TABLE groups(id UUID PRIMARY KEY);
		CREATE TABLE projects(id UUID PRIMARY KEY,created_by BIGINT REFERENCES users(id));
		CREATE TABLE boards(id UUID PRIMARY KEY,owner_id BIGINT REFERENCES users(id),system_key TEXT,deleted_at TIMESTAMPTZ);
		CREATE TABLE tasks(id UUID PRIMARY KEY,board_id UUID REFERENCES boards(id),deleted_at TIMESTAMPTZ);
		CREATE TABLE task_assignees(task_id UUID REFERENCES tasks(id),user_id BIGINT REFERENCES users(id));
		CREATE TABLE user_calendars(id UUID PRIMARY KEY,user_id BIGINT REFERENCES users(id));
		CREATE TABLE events(id UUID PRIMARY KEY,user_id BIGINT REFERENCES users(id),deleted_at TIMESTAMPTZ,
			title VARCHAR(255) NOT NULL DEFAULT '',location VARCHAR(255),is_online BOOLEAN NOT NULL DEFAULT FALSE,meeting_url TEXT);
		CREATE TABLE event_attendees(event_id UUID REFERENCES events(id),user_id BIGINT REFERENCES users(id),status TEXT);
		CREATE TABLE files(id UUID PRIMARY KEY,owner_id BIGINT REFERENCES users(id),object_name TEXT,size BIGINT,scope TEXT DEFAULT 'drive',deleted_at TIMESTAMPTZ);
		CREATE TABLE folders(id UUID PRIMARY KEY,owner_id BIGINT REFERENCES users(id));
		CREATE TABLE file_versions(id UUID PRIMARY KEY,file_id UUID REFERENCES files(id),object_name TEXT,size BIGINT);
		CREATE TABLE notes(id UUID PRIMARY KEY,user_id BIGINT REFERENCES users(id),title TEXT,content TEXT,content_text TEXT,
			type TEXT NOT NULL DEFAULT 'text',is_deleted BOOLEAN NOT NULL DEFAULT FALSE,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,
			search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple',coalesce(title,'') || ' ' || coalesce(content_text,'') || ' ' || coalesce(content,''))) STORED);
		INSERT INTO users(id) VALUES(1);
		INSERT INTO user_calendars VALUES('00000000-0000-0000-0000-000000000001',1);
		INSERT INTO events(id,user_id,deleted_at) VALUES('00000000-0000-0000-0000-000000000002',1,NULL);
		INSERT INTO event_attendees VALUES('00000000-0000-0000-0000-000000000002',1,'accepted');
		INSERT INTO notes(id,user_id,title,content,content_text,updated_at) VALUES('00000000-0000-0000-0000-000000000003',1,'Old note','preserved','searchable',now());
	`)
	if err != nil {
		t.Fatal(err)
	}
	// These migrations are recorded as applied below. Materialize their schema
	// locally so later migrations cannot depend on tables in public. AI history,
	// corporate structure and Growy role mappings are part of main and are
	// required by the later message card, position access and role upgrades.
	// Notebook chats own the later research job table; AI document chunks are
	// indexed by the later semantic search migration.
	for _, name := range []string{
		"20260128141500_create_notebook_tables.sql",
		"20260915000000_add_external_source_to_tasks.sql",
		"026_create_ai_tables.sql",
		"20260925000000_add_ai_document_context.sql",
		"20260925000100_ai_documents_production.sql",
		"20260630000100_create_publications.sql",
		"20260701000200_create_publication_views.sql",
		"20260912000000_create_corporate_structure.sql",
		"20260913000000_extend_organization_units.sql",
		"20260928000100_create_organization_growy_position_roles.sql",
		"20260927140000_drive_storage_quotas.sql",
	} {
		data, err := os.ReadFile(filepath.Join("../../db/migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, strings.Split(string(data), "-- +goose Down")[0]); err != nil {
			t.Fatalf("prepare release baseline %s: %v", name, err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO ai_conversations(id,user_id,title,model_name)
		VALUES('00000000-0000-0000-0000-000000000005',1,'Existing chat','test-model');
		INSERT INTO ai_messages(id,conversation_id,role,content,attachment_name,extracted_text)
		VALUES('00000000-0000-0000-0000-000000000006','00000000-0000-0000-0000-000000000005',
			'assistant','Existing answer','document.txt','Existing document context');
		INSERT INTO organizations(id,name,slug)
		VALUES('00000000-0000-0000-0000-000000000004','Upgrade company','upgrade-company');
		UPDATE users SET organization_id='00000000-0000-0000-0000-000000000004' WHERE id=1;
		INSERT INTO drive_storage_accounts(user_id,quota_bytes) VALUES(1,12345);
		INSERT INTO files(id,owner_id,object_name,size) VALUES('00000000-0000-0000-0000-000000000007',1,'legacy-drive-object',80);
		INSERT INTO organization_growy_position_roles(organization_id,position_key,position_name,growy_role)
		VALUES('00000000-0000-0000-0000-000000000004','teacher','Teacher','tenant_admin');
	`); err != nil {
		t.Fatalf("seed legacy AI history and Growy role mapping: %v", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if _, err := goose.EnsureDBVersionContext(ctx, db); err != nil {
		t.Fatal(err)
	}
	migrations, err := goose.CollectMigrations("../../db/migrations", 0, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	// The Notes baseline records the calendar release as already applied.
	// Materialize those tables too; a ledger entry alone is not a deployed schema.
	if calendarApplied {
		for _, migration := range migrations {
			if !calendarReleaseMigration(filepath.Base(migration.Source)) {
				continue
			}
			data, readErr := os.ReadFile(migration.Source)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, prepareErr := db.ExecContext(ctx, strings.Split(string(data), "-- +goose Down")[0]); prepareErr != nil {
				t.Fatalf("prepare deployed calendar baseline %s: %v", migration.Source, prepareErr)
			}
		}
	}
	pendingFromVersion := notesReleaseStartVersion
	if !calendarApplied {
		pendingFromVersion = calendarReleaseStartVersion
	}
	for _, migration := range migrations {
		// Do not seed migrations after the release under test. Goose requires
		// every lower migration to be applied before a later version can be current.
		if migration.Version >= pendingFromVersion {
			continue
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,true)`, migration.Version); err != nil {
			t.Fatal(err)
		}
	}
	if audioApplied {
		// Reproduce production: audio is applied while the four news migrations
		// are not yet present in the released image.
		provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../db/migrations"), goose.WithExcludeVersions(delayedNewsVersions))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.UpTo(ctx, 20260930000100); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := applyMigrations(ctx, db, "../../db/migrations"); err != nil {
			t.Fatalf("upgrade run %d: %v", i+1, err)
		}
	}

	// Administration migrations preserve existing storage policy and files and
	// keep existing company modules enabled without inventing a company budget.
	var storagePreserved bool
	if err := db.QueryRowContext(ctx, `SELECT a.quota_bytes=12345 AND u.used_bytes=80
 AND (SELECT count(*) FROM organization_modules WHERE organization_id=$1 AND available AND enabled)=5
 AND NOT EXISTS(SELECT 1 FROM organization_storage_accounts WHERE organization_id=$1 AND quota_bytes IS NOT NULL)
 FROM drive_storage_accounts a CROSS JOIN organization_storage_usage($1::uuid) u WHERE a.user_id=1`,
		"00000000-0000-0000-0000-000000000004").Scan(&storagePreserved); err != nil {
		t.Fatalf("read upgraded administration state: %v", err)
	}
	if !storagePreserved {
		t.Fatal("administration upgrade changed legacy quota, file usage or module availability")
	}
	var aiHistoryPreserved bool
	if err := db.QueryRowContext(ctx, `
		SELECT content='Existing answer' AND attachment_name='document.txt'
			AND extracted_text='Existing document context'
			AND data_cards='[]'::jsonb AND data_sources='[]'::jsonb
		FROM ai_messages WHERE id='00000000-0000-0000-0000-000000000006'
	`).Scan(&aiHistoryPreserved); err != nil {
		t.Fatalf("read upgraded AI history: %v", err)
	}
	if !aiHistoryPreserved {
		t.Fatal("AI message card upgrade did not preserve history and populate empty defaults")
	}
	var growyRolePreserved bool
	if err := db.QueryRowContext(ctx, `
		SELECT growy_role='tenant_admin' AND growy_roles=ARRAY['tenant_admin']::TEXT[]
		FROM organization_growy_position_roles
		WHERE organization_id='00000000-0000-0000-0000-000000000004' AND position_key='teacher'
	`).Scan(&growyRolePreserved); err != nil {
		t.Fatalf("read upgraded Growy role mapping: %v", err)
	}
	if !growyRolePreserved {
		t.Fatal("Growy multiple-role upgrade did not preserve the legacy role")
	}
	if audioApplied {
		for _, version := range delayedNewsVersions {
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=$1 AND is_applied", version).Scan(&count); err != nil || count != 1 {
				t.Fatalf("news migration %d must be applied exactly once: count=%d err=%v", version, count, err)
			}
		}
		// The exception must not silently admit an unrelated missing migration.
		if _, err := db.ExecContext(ctx, "DELETE FROM goose_db_version WHERE version_id=20260930000100"); err != nil {
			t.Fatal(err)
		}
		if err := applyMigrations(ctx, db, "../../db/migrations"); err == nil || !strings.Contains(err.Error(), "unexpected missing migration 20260930000100") {
			t.Fatalf("expected rejection of an unrelated migration gap, got %v", err)
		}
	}
	var noteContent, noteColor string
	var favorite bool
	var tags int
	if err := db.QueryRowContext(ctx, "SELECT content,color,is_favorite,cardinality(tags) FROM notes").Scan(&noteContent, &noteColor, &favorite, &tags); err != nil {
		t.Fatal(err)
	}
	if noteContent != "preserved" || noteColor != "" || favorite || tags != 0 {
		t.Fatal("notes upgrade did not preserve data/defaults")
	}
	var searchable bool
	if err := db.QueryRowContext(ctx, "SELECT search_vector @@ plainto_tsquery('simple','searchable') FROM notes").Scan(&searchable); err != nil {
		t.Fatal(err)
	}
	if !searchable {
		t.Fatal("notes search vector not populated")
	}
	if calendarApplied {
		return
	}
	var visible, rooms int
	var timezone, status string
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM user_calendar_settings").Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM meeting_rooms").Scan(&rooms); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT timezone FROM events").Scan(&timezone); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT status FROM event_attendees").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if visible != 1 || rooms != 4 || timezone != "Asia/Almaty" || status != "accepted" {
		t.Fatalf("unexpected upgrade data: visible=%d rooms=%d timezone=%s status=%s", visible, rooms, timezone, status)
	}
	var constraint bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'room_bookings'::regclass AND conname = 'no_overlapping_room_bookings')`).Scan(&constraint); err != nil {
		t.Fatal(err)
	}
	if !constraint {
		t.Fatal("double-booking exclusion constraint is missing")
	}
}
