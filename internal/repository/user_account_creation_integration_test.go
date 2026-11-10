package repository

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func userCreationFixture(t *testing.T) (*UserRepository, *pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "user_creation_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,name TEXT,owner_id BIGINT,status TEXT DEFAULT 'active');
	CREATE TABLE users(
	 id BIGSERIAL PRIMARY KEY,keycloak_id TEXT UNIQUE,iin TEXT UNIQUE,email TEXT UNIQUE,username TEXT,
	 first_name TEXT,last_name TEXT,avatar_url TEXT,bio TEXT,position TEXT,
	 is_active BOOLEAN,is_first_login BOOLEAN,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ,
	 role TEXT,availability_status TEXT,full_name_local TEXT,can_view_full_drive_activity BOOLEAN,
	 organization_id UUID REFERENCES organizations(id),team_id UUID,manager_id BIGINT,language TEXT DEFAULT 'ru');`)
	require.NoError(t, err)
	beles, sultan := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO organizations(id,name,owner_id) VALUES($1,'Beles',1),($2,'Sultan Corp',2);
	`, beles, sultan)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO users(keycloak_id,iin,email,username,first_name,last_name,role,is_active,organization_id)
	 VALUES('beles-director','111111111111','director@beles.invalid','beles.director','Director','Beles','user',true,$1),
	 ('sultan-director','222222222222','director@sultan.invalid','sultan.director','Director','Sultan','user',true,$2)`, beles, sultan)
	require.NoError(t, err)
	return NewUserRepository(pool, zap.NewNop()), pool, beles, sultan
}

func newUserCreationCandidate(company uuid.UUID) *domain.User {
	kcID, iin := uuid.NewString(), "333333333333"
	return &domain.User{KeycloakID: &kcID, IIN: &iin, Email: kcID + "@employee.invalid", Username: kcID,
		FirstName: "Test", LastName: "Testovich", Role: domain.RoleUser, IsActive: true,
		OrganizationID: &company, CreatedAt: time.Now(), UpdatedAt: time.Now()}
}

func userCreationSnapshot(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	var snapshot string
	require.NoError(t, db.QueryRow(context.Background(), `SELECT json_build_object(
	 'users',(SELECT json_agg(u ORDER BY id) FROM users u),
	 'organizations',(SELECT json_agg(o ORDER BY name) FROM organizations o))::text`).Scan(&snapshot))
	return snapshot
}

func TestCreateNewUserPreservesCompanyOwnersOnIdentityConflict(t *testing.T) {
	repo, db, beles, sultan := userCreationFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		change func(*domain.User)
	}{
		{"other company director IIN without subdivision", func(u *domain.User) { *u.IIN = "111111111111" }},
		{"own company director IIN without subdivision", func(u *domain.User) { *u.IIN = "222222222222" }},
		{"other company director email", func(u *domain.User) { u.Email = "director@beles.invalid" }},
		{"normalized other company director email", func(u *domain.User) { u.Email = " DIRECTOR@BELES.INVALID " }},
		{"other company director login", func(u *domain.User) { u.Username = "beles.director" }},
		{"normalized other company director login", func(u *domain.User) { u.Username = " BELES. DIRECTOR " }},
		{"login matches other company director IIN", func(u *domain.User) { u.Username = "111111111111" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := newUserCreationCandidate(sultan)
			tc.change(candidate)
			before := userCreationSnapshot(t, db)
			require.ErrorIs(t, repo.CheckNewUserIdentity(ctx, candidate.Email, candidate.Username, *candidate.IIN), ErrUserIdentityConflict)
			require.ErrorIs(t, repo.CreateNewUser(ctx, candidate), ErrUserIdentityConflict)
			require.Zero(t, candidate.ID)
			require.Equal(t, before, userCreationSnapshot(t, db), "creation changed a director, membership, or organization owner")
		})
	}
	t.Run("database unique conflict never overwrites", func(t *testing.T) {
		candidate := newUserCreationCandidate(sultan)
		*candidate.KeycloakID = "beles-director"
		before := userCreationSnapshot(t, db)
		require.ErrorIs(t, repo.CreateNewUser(ctx, candidate), ErrUserIdentityConflict)
		require.Equal(t, before, userCreationSnapshot(t, db))
	})
	t.Run("unique employee without subdivision stays in selected company", func(t *testing.T) {
		candidate := newUserCreationCandidate(sultan)
		require.NoError(t, repo.CheckNewUserIdentity(ctx, candidate.Email, candidate.Username, *candidate.IIN))
		require.NoError(t, repo.CreateNewUser(ctx, candidate))
		require.Greater(t, candidate.ID, int64(2))
		var company uuid.UUID
		var team *uuid.UUID
		require.NoError(t, db.QueryRow(ctx, `SELECT organization_id,team_id FROM users WHERE id=$1`, candidate.ID).Scan(&company, &team))
		require.Equal(t, sultan, company)
		require.Nil(t, team)
		var owners []int64
		rows, err := db.Query(ctx, `SELECT owner_id FROM organizations WHERE id=ANY($1) ORDER BY owner_id`, []uuid.UUID{beles, sultan})
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var owner int64
			require.NoError(t, rows.Scan(&owner))
			owners = append(owners, owner)
		}
		require.NoError(t, rows.Err())
		require.Equal(t, []int64{1, 2}, owners)
	})
}

func TestCreateNewUserSerializesConcurrentIdentityClaims(t *testing.T) {
	for _, field := range []string{"iin", "email", "username"} {
		t.Run(field, func(t *testing.T) {
			repo, db, _, sultan := userCreationFixture(t)
			ctx := context.Background()
			const attempts = 8
			start := make(chan struct{})
			results := make(chan error, attempts)
			var wg sync.WaitGroup
			for i := 0; i < attempts; i++ {
				candidate := newUserCreationCandidate(sultan)
				if field != "iin" {
					*candidate.IIN = strconv.FormatInt(444444444440+int64(i), 10)
				}
				if field == "email" {
					candidate.Email = "shared@employee.invalid"
					if i%2 == 0 {
						candidate.Email = " SHARED@EMPLOYEE.INVALID "
					}
				}
				if field == "username" {
					candidate.Username = "shared.employee"
					if i%2 == 0 {
						candidate.Username = " SHARED. EMPLOYEE "
					}
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					results <- repo.CreateNewUser(ctx, candidate)
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else if !errors.Is(err, ErrUserIdentityConflict) {
					t.Fatal(err)
				}
			}
			require.Equal(t, 1, successes)
			var count int
			require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count))
			require.Equal(t, 3, count)
			var owners string
			require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(owner_id::text,',' ORDER BY owner_id) FROM organizations`).Scan(&owners))
			require.Equal(t, "1,2", owners)
		})
	}
}

func TestEmployeeCreationRequiresActiveDirectorOrPlatformAdmin(t *testing.T) {
	repo, db, _, sultan := userCreationFixture(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `INSERT INTO users(id,username,role,is_active,organization_id,deleted_at) VALUES
	 (10,'employee','user',true,$1,NULL),(11,'platform-admin','admin',true,NULL,NULL),
	 (12,'inactive-admin','admin',false,NULL,NULL),(13,'deleted-admin','admin',true,NULL,now())`, sultan)
	require.NoError(t, err)
	for _, tc := range []struct {
		actor   int64
		allowed bool
	}{{1, true}, {2, true}, {10, false}, {11, true}, {12, false}, {13, false}, {999, false}} {
		allowed, err := repo.CanCreateUser(ctx, tc.actor)
		require.NoError(t, err)
		require.Equal(t, tc.allowed, allowed, "actor %d", tc.actor)
	}
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, sultan)
	require.NoError(t, err)
	allowed, err := repo.CanCreateUser(ctx, 2)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = db.Exec(ctx, `UPDATE organizations SET status='active',owner_id=10 WHERE id=$1`, sultan)
	require.NoError(t, err)
	allowed, err = repo.CanCreateUser(ctx, 2)
	require.NoError(t, err)
	require.False(t, allowed, "former director retained creation access")
	allowed, err = repo.CanCreateUser(ctx, 10)
	require.NoError(t, err)
	require.True(t, allowed, "new director was denied creation access")
}
