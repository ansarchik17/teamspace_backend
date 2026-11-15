package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

const retiredSeedMigration = "20260827000100_seed_admin_user_fixed.sql"
const seedRemediationMigration = "20261115000100_retire_insecure_seed_admin.sql"

func seedSecurityDatabase(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	_, err = admin.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public")
	require.NoError(t, err)
	schema := "seed_security_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `CREATE TABLE users (
 id BIGSERIAL PRIMARY KEY,email TEXT NOT NULL UNIQUE,username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,first_name TEXT NOT NULL DEFAULT '',last_name TEXT NOT NULL DEFAULT '',
 role TEXT NOT NULL DEFAULT 'user',is_active BOOLEAN NOT NULL DEFAULT true,keycloak_id TEXT,
 organization_id UUID,deleted_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE user_credential_state (user_id BIGINT PRIMARY KEY REFERENCES users(id),invalid_before TIMESTAMPTZ,reset_pending BOOLEAN NOT NULL DEFAULT false);
 CREATE TABLE security_audit_events(id BIGSERIAL PRIMARY KEY,action TEXT NOT NULL,outcome TEXT NOT NULL,object_type TEXT NOT NULL,object_id TEXT NOT NULL);
 CREATE TABLE preserved_records(owner_id BIGINT NOT NULL REFERENCES users(id),content TEXT NOT NULL);`)
	require.NoError(t, err)
	return ctx, db
}

func seedSecurityProvider(t *testing.T, db *sql.DB, files map[string][]byte) *goose.Provider {
	t.Helper()
	source := fstest.MapFS{}
	for name, content := range files {
		source[name] = &fstest.MapFile{Data: content}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, source)
	require.NoError(t, err)
	return provider
}

func seedSecuritySQL(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	require.NoError(t, err)
	return data
}

func TestRetiredAdminSeedDoesNotProvisionOrDeleteAccounts(t *testing.T) {
	ctx, db := seedSecurityDatabase(t)
	provider := seedSecurityProvider(t, db, map[string][]byte{retiredSeedMigration: seedSecuritySQL(t, retiredSeedMigration)})
	_, err := provider.Up(ctx)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count))
	require.Zero(t, count, "fresh migrations must not create a shared administrator")
	_, err = db.ExecContext(ctx, `INSERT INTO users(email,username,password_hash,role) VALUES('admin@admin.kz','admin',crypt('ExplicitProvisioningOnly2026!',gen_salt('bf',4)),'admin')`)
	require.NoError(t, err)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count))
	require.Equal(t, 1, count, "rollback must not delete an explicitly provisioned administrator")
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestRetireInsecureSeedAdminUpgrade(t *testing.T) {
	legacy := seedSecuritySQL(t, "testdata/legacy_admin_seed.sql")
	match := regexp.MustCompile(`crypt\('([^']+)'`).FindSubmatch(legacy)
	require.Len(t, match, 2)
	legacyPassword := string(match[1])
	for _, tc := range []struct {
		name, prepare string
		retired       bool
	}{
		{"unchanged active seed", "", true},
		{"already inactive seed", `UPDATE users SET is_active=false`, true},
		{"rotated password", `UPDATE users SET password_hash=crypt('AlreadyChanged2026!',gen_salt('bf',4))`, false},
		{"Keycloak backed identity", `UPDATE users SET keycloak_id='external-identity'`, false},
		{"previously locked password", `UPDATE users SET password_hash='!locked'`, false},
		{"existing stronger revocation", `INSERT INTO user_credential_state(user_id,invalid_before,reset_pending) SELECT id,now()+interval '1 day',true FROM users`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db := seedSecurityDatabase(t)
			old := seedSecurityProvider(t, db, map[string][]byte{retiredSeedMigration: legacy})
			_, err := old.Up(ctx)
			require.NoError(t, err)
			if tc.prepare != "" {
				_, err = db.ExecContext(ctx, tc.prepare)
				require.NoError(t, err)
			}
			var id int64
			var beforeHash string
			var beforeActive bool
			var created time.Time
			require.NoError(t, db.QueryRowContext(ctx, `SELECT id,password_hash,is_active,created_at FROM users WHERE email='admin@admin.kz'`).Scan(&id, &beforeHash, &beforeActive, &created))
			_, err = db.ExecContext(ctx, `INSERT INTO preserved_records VALUES($1,'history must survive')`, id)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO users(email,username,password_hash,role) VALUES('admin@admin.com','admin.platform',crypt('ExplicitDevelopmentAdmin2026!',gen_salt('bf',4)),'admin')`)
			require.NoError(t, err)
			var explicitHash string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE email='admin@admin.com'`).Scan(&explicitHash))
			provider := seedSecurityProvider(t, db, map[string][]byte{
				retiredSeedMigration:     seedSecuritySQL(t, retiredSeedMigration),
				seedRemediationMigration: seedSecuritySQL(t, seedRemediationMigration),
			})
			_, err = provider.Up(ctx)
			require.NoError(t, err)
			var active, matches bool
			var afterHash string
			var afterCreated time.Time
			require.NoError(t, db.QueryRowContext(ctx, `SELECT is_active,password_hash,created_at FROM users WHERE id=$1`, id).Scan(&active, &afterHash, &afterCreated))
			require.Equal(t, created, afterCreated, "the identity and its history must be preserved")
			var records int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM preserved_records WHERE owner_id=$1`, id).Scan(&records))
			require.Equal(t, 1, records)
			var audit, revoked int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM security_audit_events WHERE action='security.insecure_seed_admin.retired' AND object_type='user' AND object_id=$1`, strconv.FormatInt(id, 10)).Scan(&audit))
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM user_credential_state WHERE user_id=$1 AND invalid_before IS NOT NULL`, id).Scan(&revoked))
			if tc.retired {
				require.False(t, active)
				require.NotEqual(t, beforeHash, afterHash, "the known password must not work after manual reactivation either")
				require.NoError(t, db.QueryRowContext(ctx, `SELECT crypt($1,password_hash)=password_hash FROM users WHERE id=$2`, legacyPassword, id).Scan(&matches))
				require.False(t, matches)
				require.Equal(t, 1, audit)
				require.Equal(t, 1, revoked)
				if tc.name == "existing stronger revocation" {
					var kept bool
					require.NoError(t, db.QueryRowContext(ctx, `SELECT reset_pending AND invalid_before>now()+interval '23 hours' FROM user_credential_state WHERE user_id=$1`, id).Scan(&kept))
					require.True(t, kept, "existing revocation and pending reset must not be weakened")
				}
			} else {
				require.Equal(t, beforeActive, active)
				require.Equal(t, beforeHash, afterHash)
				require.Zero(t, audit)
				require.Zero(t, revoked)
			}
			var explicitActive bool
			var preservedHash string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT is_active,password_hash FROM users WHERE email='admin@admin.com'`).Scan(&explicitActive, &preservedHash))
			require.True(t, explicitActive)
			require.Equal(t, explicitHash, preservedHash, "the user's explicit development administrator must remain untouched")
			// Neither rollback nor a second upgrade may resurrect the compromised login.
			_, err = provider.Down(ctx)
			require.NoError(t, err)
			_, err = provider.Up(ctx)
			require.NoError(t, err)
			var finalHash string
			var finalActive bool
			require.NoError(t, db.QueryRowContext(ctx, `SELECT is_active,password_hash FROM users WHERE id=$1`, id).Scan(&finalActive, &finalHash))
			require.Equal(t, active, finalActive)
			require.Equal(t, afterHash, finalHash)
			var finalAudit int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM security_audit_events`).Scan(&finalAudit))
			require.Equal(t, audit, finalAudit, "repeated upgrade must not duplicate the audit event")
		})
	}
}
