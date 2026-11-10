package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestBAS022SearchAndAgentTenantIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "tenant_isolation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE TABLE users (
 id BIGINT PRIMARY KEY, keycloak_id TEXT, iin TEXT, email TEXT DEFAULT '', username TEXT DEFAULT '',
 password_hash TEXT, first_name TEXT DEFAULT '', last_name TEXT DEFAULT '', avatar_url TEXT, bio TEXT, position TEXT,
 is_active BOOLEAN DEFAULT TRUE, is_first_login BOOLEAN DEFAULT TRUE, last_login_at TIMESTAMPTZ, last_seen_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ,
 role TEXT DEFAULT 'user', availability_status TEXT, full_name_local TEXT,
 can_view_full_drive_activity BOOLEAN DEFAULT FALSE, organization_id UUID, team_id UUID, manager_id BIGINT,
 ministry_id BIGINT, department_id BIGINT);
 INSERT INTO users (id) VALUES (1), (2);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261009000100_user_language.sql")
	require.NoError(t, err)
	parts := strings.Split(string(migration), "-- +goose Down")
	_, err = pool.Exec(ctx, parts[0])
	require.NoError(t, err)

	a, b := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO users(id,organization_id,ministry_id,first_name,email) VALUES
 (11,$1,9,'CANARY own','own@test'),(12,$1,9,'CANARY colleague','colleague@test'),
 (21,$2,9,'CANARY foreign','foreign@test'),(31,NULL,9,'CANARY legacy','legacy@test'),
 (41,$1,9,'CANARY disabled','disabled@test');`, a, b)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=41`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `ALTER TABLE users ADD COLUMN state_body_id bigint, ADD COLUMN division_id bigint, ADD COLUMN prime_minister_id bigint, ADD COLUMN deputy_prime_minister_id bigint`)
	require.NoError(t, err)
	repo := repository.NewUserRepository(pool, zap.NewNop())
	directory, err := repo.GetAllVisibleTo(ctx, 11)
	require.NoError(t, err)
	for _, u := range directory {
		require.Contains(t, []int64{11, 12, 41}, u.ID)
	}
	search := NewSearchService(repo, nil, nil, zap.NewNop())
	for _, actor := range []int64{11, 21, 31} {
		t.Run(strconv.FormatInt(actor, 10), func(t *testing.T) {
			users, err := search.SearchUsers(ctx, actor, "CANARY", 50)
			require.NoError(t, err)
			for _, u := range users {
				if actor == 11 {
					require.Contains(t, []int64{11, 12}, u.ID)
				} else {
					require.Equal(t, actor, u.ID)
				}
			}
		})
	}
	own, err := search.SearchUsers(ctx, 11, "CANARY", 50)
	require.NoError(t, err)
	require.Len(t, own, 2)
	global, err := search.SearchGlobal(ctx, 11, "CANARY", 50)
	require.NoError(t, err)
	encoded, err := json.Marshal(global)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "foreign@test")
	providers := search.buildGlobalSearchProviders(SearchAllDependencies{})
	found, err := providers[0].Search(ctx, 11, "CANARY", globalSearchExecutionOptions{providerLimit: 50})
	require.NoError(t, err)
	require.Len(t, found["users"].users, 2)
	calendar := &CalendarService{userRepo: repo}
	invitees, err := calendar.SearchUsers(ctx, 11, "CANARY", 50, 0)
	require.NoError(t, err)
	require.Len(t, invitees, 2)
	assistant := &AIAssistantService{userRepo: repo}
	contacts, err := assistant.SearchContacts(ctx, 11, "CANARY")
	require.NoError(t, err)
	require.Equal(t, 2, contacts.Count)
	agent := &SmartAgentService{userRepo: repo}
	result, widget := agent.executeTool(ctx, 11, "search_users", `{"query":"CANARY","user_id":21,"organization_id":"`+b.String()+`"}`)
	require.NotNil(t, widget)
	require.NotContains(t, result, "foreign@test")
	require.Contains(t, result, "colleague@test")
	_, err = search.SearchUsers(ctx, 41, "CANARY", 50)
	require.Error(t, err)
	_, err = search.SearchUsers(ctx, 999, "CANARY", 50)
	require.Error(t, err)
	// Re-resolve scope on every call; no stale scope after a company transfer.
	_, err = pool.Exec(ctx, "UPDATE users SET organization_id=$1 WHERE id=11", b)
	require.NoError(t, err)
	moved, err := search.SearchUsers(ctx, 11, "CANARY", 50)
	require.NoError(t, err)
	require.Len(t, moved, 2)
	contacts, err = assistant.SearchContacts(ctx, 11, "CANARY")
	require.NoError(t, err)
	for _, u := range contacts.Items {
		require.NotEqual(t, int64(12), u.ID)
	}
	for _, u := range moved {
		require.NotEqual(t, int64(12), u.ID)
	}
}
