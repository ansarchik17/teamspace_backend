package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
)

func TestFileSearchRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DOCUMENT_SEARCH_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DOCUMENT_SEARCH_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "file_retrieval_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `
 CREATE TABLE files(id uuid PRIMARY KEY,owner_id bigint,folder_id uuid,real_name text,size bigint,scope text,updated_at timestamptz,deleted_at timestamptz,is_public boolean DEFAULT false);
 CREATE TABLE folders(id uuid PRIMARY KEY,parent_id uuid,owner_id bigint,deleted_at timestamptz);
 CREATE TABLE permissions(resource_type text,resource_id uuid,user_id bigint);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261022150000_shared_file_retrieval.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	top, child := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO folders VALUES($1,NULL,7,NULL),($2,$1,7,NULL)`, top, child)
	require.NoError(t, err)
	ids := make([]uuid.UUID, 7)
	jobs := make([]FileIndexJob, 7)
	for i := range ids {
		ids[i] = uuid.New()
		owner := int64(42)
		var folder *uuid.UUID
		var deleted *time.Time
		name := "test.txt"
		if i == 1 || i == 2 || i == 3 {
			owner = 7
		}
		if i == 2 {
			folder = &child
		}
		if i == 4 {
			now := time.Now()
			deleted = &now
		}
		if i == 6 {
			name = "REPORT.PDF"
		}
		_, err = db.Exec(ctx, `INSERT INTO files(id,owner_id,folder_id,real_name,size,scope,updated_at,deleted_at,is_public) VALUES($1,$2,$3,$4,100,'drive',now(),$5,$6)`, ids[i], owner, folder, name, deleted, i == 3)
		require.NoError(t, err)
	}
	_, err = db.Exec(ctx, `INSERT INTO permissions VALUES('file',$1,42),('folder',$2,42)`, ids[1], top)
	require.NoError(t, err)
	repo := NewFileSearchRepository(db)
	require.NoError(t, repo.Discover(ctx))
	text := "Трудовой договор ABC-74291: порядок расторжения."
	for i, id := range ids {
		job, err := repo.Claim(ctx, &id)
		require.NoError(t, err)
		if i == 4 {
			require.Nil(t, job)
			continue
		}
		require.NotNil(t, job, "supported extension must be discovered, including uppercase PDF")
		jobs[i] = *job
		second, err := repo.Claim(ctx, &id)
		require.NoError(t, err)
		require.Nil(t, second, "active lease cannot be claimed twice")
		require.NoError(t, repo.MarkReady(ctx, *job, text, documentsearch.Split(text, 3000, 300)))
	}
	hits, err := repo.Lexical(ctx, 42, nil, []string{"договоры"})
	require.NoError(t, err)
	require.Len(t, hits, 5)
	selected := []uuid.UUID{ids[3]}
	hits, err = repo.Lexical(ctx, 42, selected, []string{"договор"})
	require.NoError(t, err)
	require.Empty(t, hits, "is_public never substitutes for authenticated ACL")
	total, ready, err := repo.Coverage(ctx, 42, nil)
	require.NoError(t, err)
	require.Equal(t, 5, total)
	require.Equal(t, total, ready)
	_, err = db.Exec(ctx, `DELETE FROM permissions WHERE resource_id=ANY($1::uuid[])`, []uuid.UUID{ids[1], top})
	require.NoError(t, err)
	hits, err = repo.Lexical(ctx, 42, nil, []string{"ABC-74291"})
	require.NoError(t, err)
	require.Len(t, hits, 3, "file and inherited folder ACL revocations must be live")
	hits, err = repo.Lexical(ctx, 7, nil, []string{"договор"})
	require.NoError(t, err)
	require.Len(t, hits, 3)
	_, err = db.Exec(ctx, `UPDATE files SET updated_at=updated_at+INTERVAL '1 second' WHERE id=$1`, ids[5])
	require.NoError(t, err)
	hits, err = repo.Lexical(ctx, 42, []uuid.UUID{ids[5]}, []string{"договор"})
	require.NoError(t, err)
	require.Empty(t, hits, "old revision must disappear immediately")
	require.NoError(t, repo.Discover(ctx))
	fresh, err := repo.Claim(ctx, &ids[5])
	require.NoError(t, err)
	require.NotNil(t, fresh)
	require.Error(t, repo.MarkReady(ctx, jobs[5], "obsolete", documentsearch.Split("obsolete", 3000, 300)))
	_, err = db.Exec(ctx, `UPDATE ai_file_search_documents SET lease_until=now()-INTERVAL '1 second' WHERE file_id=$1`, ids[5])
	require.NoError(t, err)
	reclaimed, err := repo.Claim(ctx, &ids[5])
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	require.NotEqual(t, fresh.Token, reclaimed.Token)
	require.Error(t, repo.MarkReady(ctx, *fresh, "stale lease", documentsearch.Split("stale lease", 3000, 300)))
	require.NoError(t, repo.MarkReady(ctx, *reclaimed, text, documentsearch.Split(text, 3000, 300)))
	hits, err = repo.Lexical(ctx, 42, []uuid.UUID{ids[5]}, []string{"договор"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	neighbors, err := repo.Neighbors(ctx, 7, nil, hits)
	require.NoError(t, err)
	require.Empty(t, neighbors, "neighbor expansion must repeat ACL checks")

	var vectorAvailable bool
	require.NoError(t, db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='vector')`).Scan(&vectorAvailable))
	if vectorAvailable {
		_, err = db.Exec(ctx, `CREATE TABLE ai_text_embeddings(model text,content_hash text,embedding vector,retry_after timestamptz,PRIMARY KEY(model,content_hash))`)
		require.NoError(t, err)
		_, err = db.Exec(ctx, `INSERT INTO ai_text_embeddings VALUES('local:3',$1,'[1,0,0]'::vector,NULL)`, documentsearch.Hash(text))
		require.NoError(t, err)
		semantic, err := repo.Semantic(ctx, 42, nil, "local:3", "[1,0,0]", 3)
		require.NoError(t, err)
		require.Len(t, semantic, 3)
		semantic, err = repo.Semantic(ctx, 42, []uuid.UUID{ids[3]}, "local:3", "[1,0,0]", 3)
		require.NoError(t, err)
		require.Empty(t, semantic)
		semantic, err = repo.Semantic(ctx, 42, nil, "different-model:3", "[1,0,0]", 3)
		require.NoError(t, err)
		require.Empty(t, semantic)
	} else {
		t.Log("install pgvector for vector branch verification")
	}
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=now() WHERE id=$1`, ids[0])
	require.NoError(t, err)
	require.NoError(t, repo.Discover(ctx))
	var retained int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_file_search_chunks WHERE file_id=$1`, ids[0]).Scan(&retained))
	require.Zero(t, retained)
}
