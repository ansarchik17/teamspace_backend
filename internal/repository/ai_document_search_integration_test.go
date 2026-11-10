package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
	"go.uber.org/zap"
)

// Use a disposable DB with CREATE SCHEMA permission; pgvector is optional for
// the lexical half. The test never installs extensions or touches app tables.
func TestDocumentSearchScopeAndHybridIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DOCUMENT_SEARCH_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DOCUMENT_SEARCH_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "document_search_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
 CREATE TABLE ai_conversations(id uuid PRIMARY KEY,user_id bigint);
 CREATE TABLE ai_documents(id uuid PRIMARY KEY,owner_id bigint,name text,status text,deleted_at timestamptz);
 CREATE TABLE ai_document_chunks(id uuid PRIMARY KEY,document_id uuid,conversation_id uuid,owner_id bigint,
 ordinal int,text text,search_vector tsvector,locator jsonb,token_count int,created_at timestamptz DEFAULT NOW());`)
	require.NoError(t, err)
	chat, otherChat := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO ai_conversations VALUES($1,1),($2,2)`, chat, otherChat)
	require.NoError(t, err)
	docs := make([]string, 5)
	for i := range docs {
		docs[i] = uuid.NewString()
		owner := int64(1)
		scope := chat
		status := "ready"
		var deleted *time.Time
		if i == 1 {
			owner = 2
			scope = otherChat
		}
		if i == 2 {
			status = "processing"
		}
		if i == 3 {
			now := time.Now()
			deleted = &now
		}
		text := "Расторжение трудового договора: уведомить за месяц."
		if i == 4 {
			text = "Код договора ABC74291"
		}
		_, err = db.Exec(ctx, `INSERT INTO ai_documents VALUES($1,$2,$3,$4,$5)`, docs[i], owner, "file.txt", status, deleted)
		require.NoError(t, err)
		_, err = db.Exec(ctx, `INSERT INTO ai_document_chunks(id,document_id,conversation_id,owner_id,ordinal,text,search_vector,locator,token_count)
   VALUES($1,$2,$3,$4,0,$5,to_tsvector('simple',$5),'{}',8)`, uuid.NewString(), docs[i], scope, owner, text)
		require.NoError(t, err)
	}
	repo := NewAIDocumentRepository(db, zap.NewNop())
	hits, err := repo.SearchChunks(ctx, 1, chat, "договоры", nil, 12)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	hits, err = repo.SearchChunks(ctx, 1, chat, "ABC74291", []string{docs[4]}, 12)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, docs[4], hits[0].DocumentID)
	scopedHits := append([]AIDocumentChunk(nil), hits...)
	hits, err = repo.SearchChunks(ctx, 1, chat, "договор", []string{docs[1]}, 12)
	require.NoError(t, err)
	require.Empty(t, hits)
	_, err = repo.SearchChunks(ctx, 1, chat, "договор", []string{"invalid"}, 12)
	require.Error(t, err)

	var vectorInstalled bool
	require.NoError(t, db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='vector')`).Scan(&vectorInstalled))
	if !vectorInstalled {
		t.Log("lexical ACL checks passed; install pgvector to exercise semantic checks")
		return
	}
	_, err = db.Exec(ctx, `CREATE TABLE ai_text_embeddings(model text,content_hash text,embedding vector,retry_after timestamptz,PRIMARY KEY(model,content_hash))`)
	require.NoError(t, err)
	var offline atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		var body struct{ Input []string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		vectors := make([][]float64, len(body.Input))
		for i := range vectors {
			vectors[i] = []float64{1, 0, 0}
			if strings.Contains(body.Input[i], "ABC74291") {
				vectors[i] = []float64{0, 1, 0}
			}
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors}))
	}))
	defer server.Close()
	semantic, err := documentsearch.New(ctx, db, documentsearch.Config{URL: server.URL, Model: "test-local", Dimensions: 3, Timeout: time.Second})
	require.NoError(t, err)
	repo.SetSemanticSearch(semantic)
	require.NoError(t, repo.BackfillEmbeddings(ctx))
	hits, err = repo.SearchChunks(ctx, 1, chat, "Как уйти из компании?", []string{docs[0]}, 12)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, docs[0], hits[0].DocumentID)
	hits, err = repo.SearchChunks(ctx, 1, chat, "Как уйти из компании?", []string{docs[1]}, 12)
	require.NoError(t, err)
	require.Empty(t, hits)
	hits, err = repo.SearchChunks(ctx, 1, chat, "Как уйти из компании?", nil, 12)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	require.Equal(t, docs[0], hits[0].DocumentID)
	offline.Store(true)
	hits, err = repo.SearchChunks(ctx, 1, chat, "ABC74291", nil, 12)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	// Changing embedding spaces must never mix old vectors into new rankings.
	other, err := documentsearch.New(ctx, db, documentsearch.Config{URL: server.URL, Model: "other-model", Dimensions: 3, Timeout: time.Second})
	require.NoError(t, err)
	repo.SetSemanticSearch(other)
	offline.Store(false)
	hits, err = repo.SearchChunks(ctx, 1, chat, "Как уйти из компании?", nil, 12)
	require.NoError(t, err)
	require.Empty(t, hits)
	_, err = db.Exec(ctx, `UPDATE ai_documents SET deleted_at=now() WHERE id=$1`, docs[4])
	require.NoError(t, err)
	expanded, err := repo.expandDocumentContext(ctx, 1, chat, scopedHits, 32000)
	require.NoError(t, err)
	require.Empty(t, expanded, "revoked/deleted evidence cannot return through context expansion")

}
