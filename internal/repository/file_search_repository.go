package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
)

const fileSearchParserVersion = "retrieval-v2"
const searchableDriveFileSQL = `f.scope='drive' AND f.deleted_at IS NULL AND f.size BETWEEN 1 AND 52428800
 AND lower(f.real_name) ~ '\.(pdf|docx|txt|xlsx|pptx)$'`

type FileSearchRepository struct{ db *pgxpool.Pool }

func NewFileSearchRepository(db *pgxpool.Pool) *FileSearchRepository {
	return &FileSearchRepository{db: db}
}

type FileIndexJob struct {
	FileID          uuid.UUID
	SourceUpdatedAt time.Time
	Token           uuid.UUID
}

// Discover is a durable, bounded reconciliation of file revisions. It repairs
// missed upload events and indexes pre-existing files without a fire-and-forget queue.
func (r *FileSearchRepository) Discover(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM ai_file_search_documents WHERE file_id IN(
      SELECT d.file_id FROM ai_file_search_documents d JOIN files f ON f.id=d.file_id
      WHERE f.deleted_at IS NOT NULL OR f.scope<>'drive' ORDER BY d.file_id LIMIT 20)`); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `INSERT INTO ai_file_search_documents(file_id,source_updated_at,parser_version)
 SELECT f.id,f.updated_at,$1 FROM files f WHERE `+searchableDriveFileSQL+`
 AND NOT EXISTS(SELECT 1 FROM ai_file_search_documents d WHERE d.file_id=f.id AND d.source_updated_at=f.updated_at AND d.parser_version=$1)
 ORDER BY f.updated_at,f.id LIMIT 20
 ON CONFLICT(file_id) DO UPDATE SET source_updated_at=EXCLUDED.source_updated_at,parser_version=EXCLUDED.parser_version,
 state='queued',attempts=0,next_attempt_at=now(),lease_until=NULL,lease_token=NULL,error_code=NULL,updated_at=now()`, fileSearchParserVersion)
	return err
}
func (r *FileSearchRepository) Enqueue(ctx context.Context, fileID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `INSERT INTO ai_file_search_documents(file_id,source_updated_at,parser_version)
 SELECT f.id,f.updated_at,$2 FROM files f WHERE f.id=$1 AND `+searchableDriveFileSQL+`
 ON CONFLICT(file_id) DO UPDATE SET source_updated_at=EXCLUDED.source_updated_at,parser_version=EXCLUDED.parser_version,
 state='queued',attempts=0,next_attempt_at=now(),lease_until=NULL,lease_token=NULL,error_code=NULL,updated_at=now()
 WHERE ai_file_search_documents.source_updated_at<>EXCLUDED.source_updated_at OR ai_file_search_documents.parser_version<>EXCLUDED.parser_version`, fileID, fileSearchParserVersion)
	return err
}
func (r *FileSearchRepository) Claim(ctx context.Context, fileID *uuid.UUID) (*FileIndexJob, error) {
	_, err := r.db.Exec(ctx, `UPDATE ai_file_search_documents SET state='failed',error_code='lease_expired',lease_token=NULL,lease_until=NULL
 WHERE state='processing' AND lease_until<now() AND attempts>=3`)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var job FileIndexJob
	err = tx.QueryRow(ctx, `SELECT d.file_id,d.source_updated_at FROM ai_file_search_documents d JOIN files f ON f.id=d.file_id
 WHERE d.source_updated_at=f.updated_at AND `+searchableDriveFileSQL+`
 AND ($1::uuid IS NULL OR d.file_id=$1) AND d.attempts<3
 AND ((d.state IN ('queued','failed') AND d.next_attempt_at<=now()) OR (d.state='processing' AND d.lease_until<now()))
 ORDER BY d.updated_at,d.file_id FOR UPDATE OF d SKIP LOCKED LIMIT 1`, fileID).Scan(&job.FileID, &job.SourceUpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.Token = uuid.New()
	_, err = tx.Exec(ctx, `UPDATE ai_file_search_documents SET state='processing',attempts=attempts+1,lease_until=now()+INTERVAL '2 minutes',lease_token=$2,updated_at=now() WHERE file_id=$1`, job.FileID, job.Token)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *FileSearchRepository) MarkReady(ctx context.Context, job FileIndexJob, text string, chunks []documentsearch.TextChunk) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Lock the source revision while publishing: a stale worker cannot overwrite
	// a newer upload, even if its lease expired and another worker took over.
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT f.updated_at FROM files f WHERE f.id=$1 AND `+searchableDriveFileSQL+` FOR SHARE`, job.FileID).Scan(&updated)
	if err != nil {
		return err
	}
	if !updated.Equal(job.SourceUpdatedAt) {
		return fmt.Errorf("file revision changed during indexing")
	}
	result, err := tx.Exec(ctx, `UPDATE ai_file_search_documents SET state='ready',text_chars=$4,revision=$5,error_code=NULL,lease_until=NULL,lease_token=NULL,updated_at=now()
 WHERE file_id=$1 AND source_updated_at=$2 AND lease_token=$3`, job.FileID, job.SourceUpdatedAt, job.Token, len([]rune(text)), documentsearch.Hash(text))
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("file indexing lease was replaced")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM ai_file_search_chunks WHERE file_id=$1`, job.FileID); err != nil {
		return err
	}
	revision := documentsearch.Hash(text)
	for _, chunk := range chunks {
		locator, _ := json.Marshal(map[string]any{"start_line": chunk.StartLine, "end_line": chunk.EndLine, "start_char": chunk.StartChar, "end_char": chunk.EndChar, "revision": revision, "chunk": chunk.Ordinal + 1})
		_, err = tx.Exec(ctx, `INSERT INTO ai_file_search_chunks(id,file_id,ordinal,text,locator) VALUES($1,$2,$3,$4,$5::jsonb)`, uuid.New(), job.FileID, chunk.Ordinal, chunk.Text, string(locator))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (r *FileSearchRepository) MarkFailed(ctx context.Context, job FileIndexJob, code string) error {
	_, err := r.db.Exec(ctx, `UPDATE ai_file_search_documents SET state='failed',error_code=$3,next_attempt_at=now()+INTERVAL '5 minutes',lease_until=NULL,lease_token=NULL,updated_at=now() WHERE file_id=$1 AND lease_token=$2`, job.FileID, job.Token, code)
	return err
}
func (r *FileSearchRepository) Delete(ctx context.Context, ids []uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM ai_file_search_documents WHERE file_id=ANY($1::uuid[])`, ids)
	return err
}

const authorizedDrivePassagesSQL = `, authorized AS NOT MATERIALIZED (
 SELECT c.id,c.file_id,c.ordinal,c.text,c.search_simple,c.search_russian,c.locator,f.real_name
 FROM ai_file_search_chunks c JOIN ai_file_search_documents d ON d.file_id=c.file_id
 JOIN files f ON f.id=c.file_id JOIN accessible a ON a.file_id=c.file_id
 WHERE d.state='ready' AND d.source_updated_at=f.updated_at AND d.parser_version='retrieval-v2'
)`

func (r *FileSearchRepository) Lexical(ctx context.Context, userID int64, ids []uuid.UUID, queries []string) ([]documentsearch.Passage, error) {
	if ids == nil {
		ids = []uuid.UUID{}
	}
	query := strings.Join(queries, " ")
	rows, err := r.db.Query(ctx, driveFileAccessCTE+authorizedDrivePassagesSQL+`, q AS (
 SELECT (SELECT string_agg(quote_literal(t),' | ')::tsquery FROM unnest(tsvector_to_array(to_tsvector('simple',$3)))t) exact,
 (SELECT string_agg(quote_literal(t),' | ')::tsquery FROM unnest(tsvector_to_array(to_tsvector('russian',$3)))t) russian
 )
 SELECT c.id,c.file_id,c.real_name,c.ordinal,c.text,c.locator FROM authorized c CROSS JOIN q
 WHERE c.search_simple@@q.exact OR c.search_russian@@q.russian
 ORDER BY (COALESCE(ts_rank_cd(c.search_simple,q.exact),0)+COALESCE(ts_rank_cd(c.search_russian,q.russian),0)) DESC,c.id LIMIT 60`, ids, userID, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFilePassages(rows)
}
func (r *FileSearchRepository) Semantic(ctx context.Context, userID int64, ids []uuid.UUID, model, vector string, dims int) ([]documentsearch.Passage, error) {
	if ids == nil {
		ids = []uuid.UUID{}
	}
	rows, err := r.db.Query(ctx, driveFileAccessCTE+authorizedDrivePassagesSQL+`, candidates AS MATERIALIZED (
 SELECT c.*,e.embedding FROM authorized c JOIN ai_text_embeddings e ON e.model=$3 AND e.content_hash=encode(sha256(convert_to(c.text,'UTF8')),'hex')
 WHERE e.embedding IS NOT NULL AND vector_dims(e.embedding)=$5)
 SELECT id,file_id,real_name,ordinal,text,locator FROM candidates ORDER BY embedding<=>$4::vector,id LIMIT 60`, ids, userID, model, vector, dims)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFilePassages(rows)
}
func (r *FileSearchRepository) Neighbors(ctx context.Context, userID int64, ids []uuid.UUID, anchors []documentsearch.Passage) ([]documentsearch.Passage, error) {
	if ids == nil {
		ids = []uuid.UUID{}
	}
	anchorIDs := []uuid.UUID{}
	for _, p := range anchors {
		id, err := uuid.Parse(p.ID)
		if err != nil {
			return nil, err
		}
		anchorIDs = append(anchorIDs, id)
	}
	rows, err := r.db.Query(ctx, driveFileAccessCTE+authorizedDrivePassagesSQL+`
 SELECT DISTINCT ON(c.id) c.id,c.file_id,c.real_name,c.ordinal,c.text,c.locator FROM authorized c
 JOIN authorized a ON a.id=ANY($3::uuid[]) AND a.file_id=c.file_id AND c.ordinal BETWEEN a.ordinal-1 AND a.ordinal+1 ORDER BY c.id`, ids, userID, anchorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFilePassages(rows)
}
func scanFilePassages(rows pgx.Rows) ([]documentsearch.Passage, error) {
	result := []documentsearch.Passage{}
	for rows.Next() {
		var p documentsearch.Passage
		var locator []byte
		if err := rows.Scan(&p.ID, &p.DocumentID, &p.Name, &p.Ordinal, &p.Text, &locator); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(locator, &p.Locator); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (r *FileSearchRepository) Coverage(ctx context.Context, userID int64, ids []uuid.UUID) (int, int, error) {
	if ids == nil {
		ids = []uuid.UUID{}
	}
	var total, ready int
	err := r.db.QueryRow(ctx, driveFileAccessCTE+` SELECT count(*),count(*) FILTER(WHERE d.state='ready' AND d.source_updated_at=f.updated_at AND d.parser_version=$3)
 FROM files f JOIN accessible a ON a.file_id=f.id LEFT JOIN ai_file_search_documents d ON d.file_id=f.id WHERE `+searchableDriveFileSQL, ids, userID, fileSearchParserVersion).Scan(&total, &ready)
	return total, ready, err
}
func (r *FileSearchRepository) PendingEmbeddingTexts(ctx context.Context, model string) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT c.text FROM ai_file_search_chunks c JOIN ai_file_search_documents d ON d.file_id=c.file_id JOIN files f ON f.id=c.file_id
 LEFT JOIN ai_text_embeddings e ON e.model=$1 AND e.content_hash=encode(sha256(convert_to(c.text,'UTF8')),'hex')
 WHERE d.state='ready' AND f.deleted_at IS NULL AND d.source_updated_at=f.updated_at AND e.embedding IS NULL AND(e.retry_after IS NULL OR e.retry_after<=now()) ORDER BY c.id LIMIT 16`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	texts := []string{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	return texts, rows.Err()
}
