package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
	"go.uber.org/zap"
)

var ErrAIDocumentLeaseLost = errors.New("AI document job attempt no longer owns its lease")

type AIDocument struct {
	JobAttempt     int       `json:"-"`
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	OwnerID        int64     `json:"-"`
	Name           string    `json:"name"`
	DeclaredMIME   *string   `json:"declared_mime,omitempty"`
	DetectedType   string    `json:"detected_type"`
	Bytes          int64     `json:"bytes"`
	SHA256         string    `json:"-"`
	ObjectKey      string    `json:"-"`
	Status         string    `json:"status"`
	ErrorCode      *string   `json:"error_code,omitempty"`
	ErrorMessage   *string   `json:"error_message,omitempty"`
	PageCount      *int      `json:"page_count,omitempty"`
	TextChars      int       `json:"text_chars"`
	ParserVersion  string    `json:"parser_version"`
	IndexVersion   string    `json:"index_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type AIDocumentChunk struct {
	ID             string
	DocumentID     string
	ConversationID string
	Name           string
	Ordinal        int
	Text           string
	Locator        map[string]any `json:"locator"`
	TokenCount     int
}

type AIDocumentRepository struct {
	db       *pgxpool.Pool
	logger   *zap.Logger
	semantic *documentsearch.Search
	pipeline *documentsearch.Pipeline
}

func NewAIDocumentRepository(db *pgxpool.Pool, logger *zap.Logger) *AIDocumentRepository {
	pipeline, _ := documentsearch.NewPipeline(documentsearch.Config{}, nil, logger)
	return &AIDocumentRepository{db: db, logger: logger, pipeline: pipeline}
}

func (r *AIDocumentRepository) CreateDocument(ctx context.Context, doc AIDocument) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin ai document creation: %w", err)
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO ai_documents
		(id, conversation_id, owner_id, name, declared_mime, detected_type, bytes, sha256, object_key, status, parser_version, index_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, id, doc.ConversationID, doc.OwnerID, doc.Name, doc.DeclaredMIME, doc.DetectedType, doc.Bytes,
		doc.SHA256, doc.ObjectKey, doc.Status, doc.ParserVersion, doc.IndexVersion)
	if err != nil {
		return "", fmt.Errorf("create ai document: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO ai_document_jobs (document_id, state) VALUES ($1, 'queued')
	`, id)
	if err != nil {
		return "", fmt.Errorf("create ai document job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit ai document creation: %w", err)
	}
	return id.String(), nil
}

func (r *AIDocumentRepository) GetDocument(ctx context.Context, userID int64, conversationID, documentID string) (*AIDocument, error) {
	var doc AIDocument
	err := r.db.QueryRow(ctx, `
		SELECT id, conversation_id, owner_id, name, declared_mime, detected_type, bytes, sha256, object_key,
		       status, error_code, error_message, page_count, text_chars, parser_version, index_version, created_at, updated_at
		FROM ai_documents
		WHERE id = $1 AND conversation_id = $2 AND owner_id = $3 AND deleted_at IS NULL
	`, documentID, conversationID, userID).Scan(
		&doc.ID, &doc.ConversationID, &doc.OwnerID, &doc.Name, &doc.DeclaredMIME, &doc.DetectedType,
		&doc.Bytes, &doc.SHA256, &doc.ObjectKey, &doc.Status, &doc.ErrorCode, &doc.ErrorMessage,
		&doc.PageCount, &doc.TextChars, &doc.ParserVersion, &doc.IndexVersion, &doc.CreatedAt, &doc.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ai document: %w", err)
	}
	return &doc, nil
}

func (r *AIDocumentRepository) GetDocumentBySHA256(ctx context.Context, userID int64, conversationID, sha string) (*AIDocument, error) {
	var doc AIDocument
	err := r.db.QueryRow(ctx, `
		SELECT id, conversation_id, owner_id, name, declared_mime, detected_type, bytes, sha256, object_key,
		       status, error_code, error_message, page_count, text_chars, parser_version, index_version, created_at, updated_at
		FROM ai_documents
		WHERE conversation_id=$1 AND owner_id=$2 AND sha256=$3 AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, conversationID, userID, sha).Scan(
		&doc.ID, &doc.ConversationID, &doc.OwnerID, &doc.Name, &doc.DeclaredMIME, &doc.DetectedType,
		&doc.Bytes, &doc.SHA256, &doc.ObjectKey, &doc.Status, &doc.ErrorCode, &doc.ErrorMessage,
		&doc.PageCount, &doc.TextChars, &doc.ParserVersion, &doc.IndexVersion, &doc.CreatedAt, &doc.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ai document by hash: %w", err)
	}
	return &doc, nil
}

func (r *AIDocumentRepository) ListDocuments(ctx context.Context, userID int64, conversationID string) ([]AIDocument, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, conversation_id, owner_id, name, declared_mime, detected_type, bytes, sha256, object_key,
		       status, error_code, error_message, page_count, text_chars, parser_version, index_version, created_at, updated_at
		FROM ai_documents
		WHERE conversation_id = $1 AND owner_id = $2 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, conversationID, userID)
	if err != nil {
		return nil, fmt.Errorf("list ai documents: %w", err)
	}
	defer rows.Close()
	items := make([]AIDocument, 0)
	for rows.Next() {
		var doc AIDocument
		if err := rows.Scan(&doc.ID, &doc.ConversationID, &doc.OwnerID, &doc.Name, &doc.DeclaredMIME, &doc.DetectedType,
			&doc.Bytes, &doc.SHA256, &doc.ObjectKey, &doc.Status, &doc.ErrorCode, &doc.ErrorMessage,
			&doc.PageCount, &doc.TextChars, &doc.ParserVersion, &doc.IndexVersion, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan ai document: %w", err)
		}
		items = append(items, doc)
	}
	return items, rows.Err()
}

func (r *AIDocumentRepository) CountActive(ctx context.Context, userID int64, conversationID string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM ai_documents
		WHERE owner_id=$1 AND conversation_id=$2 AND deleted_at IS NULL AND status IN ('uploading','uploaded','processing','ready')
	`, userID, conversationID).Scan(&count)
	return count, err
}

func (r *AIDocumentRepository) ClaimNextJob(ctx context.Context, lease time.Duration) (*AIDocument, string, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `WITH exhausted AS (
      UPDATE ai_document_jobs SET state='failed',error_code='lease_expired',lease_until=NULL,updated_at=now()
      WHERE attempts>=3 AND (state='queued' OR(state='processing' AND(lease_until IS NULL OR lease_until<now()))) RETURNING document_id
    ) UPDATE ai_documents SET status='failed',error_code='lease_expired',
      error_message='Обработка документа прервана. Повторите попытку.',updated_at=now()
      WHERE id IN(SELECT document_id FROM exhausted) AND deleted_at IS NULL AND status IN ('uploaded','processing')`); err != nil {
		return nil, "", err
	}
	var doc AIDocument
	var jobID string
	err = tx.QueryRow(ctx, `
		SELECT j.id, d.id, d.conversation_id, d.owner_id, d.name, d.declared_mime, d.detected_type, d.bytes,
		       d.sha256, d.object_key, d.status, d.error_code, d.error_message, d.page_count, d.text_chars,
		       d.parser_version, d.index_version, d.created_at, d.updated_at,j.attempts
		FROM ai_document_jobs j
		JOIN ai_documents d ON d.id = j.document_id
		WHERE ((j.state = 'queued' AND j.next_attempt_at <= NOW())
		    OR (j.state = 'processing' AND j.lease_until IS NOT NULL AND j.lease_until < NOW()))
		  AND j.attempts < 3 AND d.deleted_at IS NULL
		ORDER BY j.created_at
		FOR UPDATE OF j SKIP LOCKED
		LIMIT 1
	`).Scan(&jobID, &doc.ID, &doc.ConversationID, &doc.OwnerID, &doc.Name, &doc.DeclaredMIME, &doc.DetectedType,
		&doc.Bytes, &doc.SHA256, &doc.ObjectKey, &doc.Status, &doc.ErrorCode, &doc.ErrorMessage, &doc.PageCount,
		&doc.TextChars, &doc.ParserVersion, &doc.IndexVersion, &doc.CreatedAt, &doc.UpdatedAt, &doc.JobAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return nil, "", err
		}
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("claim ai document job: %w", err)
	}
	now := time.Now().UTC()
	if err = tx.QueryRow(ctx, `UPDATE ai_document_jobs SET state='processing', attempts=attempts+1, lease_until=$2, updated_at=$3 WHERE id=$1 RETURNING attempts`, jobID, now.Add(lease), now).Scan(&doc.JobAttempt); err != nil {
		return nil, "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_documents SET status='processing', updated_at=$2 WHERE id=$1`, doc.ID, now); err != nil {
		return nil, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return &doc, jobID, nil
}

// The monotonically increasing attempt is a fencing token for a job's lease.
// An old worker cannot replace chunks or downgrade a newer worker's result.
func (r *AIDocumentRepository) MarkReady(ctx context.Context, jobID string, doc AIDocument, chunks []AIDocumentChunk) error {
	if doc.JobAttempt <= 0 {
		return ErrAIDocumentLeaseLost
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE ai_document_jobs SET state='completed',lease_until=NULL,error_code=NULL,updated_at=now()
 WHERE id=$1 AND document_id=$2 AND state='processing' AND attempts=$3`, jobID, doc.ID, doc.JobAttempt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAIDocumentLeaseLost
	}
	// Lock/update the document before replacing chunks. A concurrent delete cannot
	// resurrect content, and all changes roll back if the document became unavailable.
	result, err = tx.Exec(ctx, `UPDATE ai_documents SET status='ready',text_chars=$2,error_code=NULL,error_message=NULL,updated_at=now(),index_version=$3
 WHERE id=$1 AND deleted_at IS NULL`, doc.ID, doc.TextChars, doc.IndexVersion)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("AI document is no longer available")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM ai_document_chunks WHERE document_id=$1`, doc.ID); err != nil {
		return err
	}
	for _, chunk := range chunks {
		_, err = tx.Exec(ctx, `INSERT INTO ai_document_chunks(id,document_id,conversation_id,owner_id,ordinal,text,search_vector,locator,token_count)
  VALUES($1,$2,$3,$4,$5,$6,to_tsvector('simple',$6),$7::jsonb,$8)`, uuid.New(), doc.ID, doc.ConversationID, doc.OwnerID, chunk.Ordinal, chunk.Text, mustJSON(chunk.Locator), chunk.TokenCount)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *AIDocumentRepository) MarkFailed(ctx context.Context, jobID, documentID, code, message string, retry bool, attempt int) error {
	if attempt <= 0 {
		return ErrAIDocumentLeaseLost
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Use separate prepared statements in one transaction. Lock the job before
	// the document, matching ClaimNextJob and MarkReady; re-read deletion under
	// the row lock so a concurrent delete cannot turn into a queued retry.
	var ownedAttempt int
	err = tx.QueryRow(ctx, `SELECT attempts FROM ai_document_jobs WHERE id=$1 AND document_id=$2 AND state='processing' AND attempts=$3 FOR UPDATE`, jobID, documentID, attempt).Scan(&ownedAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAIDocumentLeaseLost
	}
	if err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT deleted_at IS NULL FROM ai_documents WHERE id=$1 FOR UPDATE`, documentID).Scan(&active); err != nil {
		return err
	}
	state, status := "failed", "failed"
	if retry && active && ownedAttempt < 3 {
		state, status = "queued", "uploaded"
	}
	_, err = tx.Exec(ctx, `UPDATE ai_document_jobs SET state=$2,error_code=$3,
 next_attempt_at=CASE WHEN $2='queued' THEN now()+INTERVAL '30 seconds' ELSE next_attempt_at END,
 lease_until=NULL,updated_at=now() WHERE id=$1`, jobID, state, code)
	if err != nil {
		return err
	}
	if active {
		if _, err = tx.Exec(ctx, `UPDATE ai_documents SET status=$2,error_code=$3,error_message=$4,updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, documentID, status, code, message); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetSemanticSearch is called during startup, before serving requests.
func (r *AIDocumentRepository) SetSemanticSearch(search *documentsearch.Search) { r.semantic = search }
func (r *AIDocumentRepository) SetRetrievalPipeline(pipeline *documentsearch.Pipeline) {
	if pipeline != nil {
		r.pipeline = pipeline
	}
}

func documentSearchIDs(values []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("invalid selected document ID")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (r *AIDocumentRepository) SearchChunks(ctx context.Context, userID int64, conversationID, query string, documentIDs []string, limit int) ([]AIDocumentChunk, error) {
	if limit <= 0 || limit > 20 {
		limit = 12
	}
	selectedIDs, err := documentSearchIDs(documentIDs)
	if err != nil {
		return nil, err
	}
	plan := r.pipeline.Plan(ctx, query)
	rows, err := r.db.Query(ctx, `
 WITH q AS (
 SELECT
  (SELECT string_agg(quote_literal(t),' | ')::tsquery FROM unnest(tsvector_to_array(to_tsvector('simple',$3))) t) AS exact,
  (SELECT string_agg(quote_literal(t),' | ')::tsquery FROM unnest(tsvector_to_array(to_tsvector('russian',$3))) t) AS russian
 )
 SELECT c.id,c.document_id,c.conversation_id,d.name,c.ordinal,c.text,c.locator,c.token_count
 FROM ai_document_chunks c JOIN ai_documents d ON d.id=c.document_id
 JOIN ai_conversations a ON a.id=c.conversation_id CROSS JOIN q
 WHERE c.owner_id=$1 AND d.owner_id=$1 AND a.user_id=$1 AND c.conversation_id=$2
 AND d.status='ready' AND d.deleted_at IS NULL
 AND (cardinality($4::uuid[])=0 OR c.document_id=ANY($4::uuid[]))
 AND (c.search_vector @@ q.exact OR to_tsvector('russian',c.text) @@ q.russian)
 ORDER BY (COALESCE(ts_rank_cd(c.search_vector,q.exact),0)
   + COALESCE(ts_rank_cd(to_tsvector('russian',c.text),q.russian),0)
   + 2*ts_rank_cd(c.search_vector,plainto_tsquery('simple',$5))) DESC,c.id
 LIMIT 40`, userID, conversationID, strings.Join(plan.Lexical, " "), selectedIDs, query)
	if err != nil {
		return nil, fmt.Errorf("search ai document chunks: %w", err)
	}
	lexical, err := scanAIDocumentChunks(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	var semantic []AIDocumentChunk
	if r.semantic != nil {
		semantic, err = r.semanticChunks(ctx, userID, conversationID, plan.Semantic, selectedIDs)
		if err != nil {
			r.logger.Warn("semantic document search unavailable; using lexical search", zap.Error(err))
		}
	}
	ranked := rankDocumentChunks(ctx, r.pipeline, query, lexical, semantic, limit)
	return r.expandDocumentContext(ctx, userID, conversationID, ranked, 32000)
}

// Reciprocal rank fusion keeps lexical and semantic score scales independent.
func chunkPassages(chunks []AIDocumentChunk) []documentsearch.Passage {
	passages := make([]documentsearch.Passage, len(chunks))
	for i, c := range chunks {
		passages[i] = documentsearch.Passage{ID: c.ID, DocumentID: c.DocumentID, Name: c.Name, Ordinal: c.Ordinal, Text: c.Text, Locator: c.Locator}
	}
	return passages
}

func rankedChunks(ranked []documentsearch.Passage, lexical, semantic []AIDocumentChunk) []AIDocumentChunk {
	byID := map[string]AIDocumentChunk{}
	for _, list := range [][]AIDocumentChunk{lexical, semantic} {
		for _, c := range list {
			byID[c.ID] = c
		}
	}
	result := make([]AIDocumentChunk, 0, len(ranked))
	for _, hit := range ranked {
		c := byID[hit.ID]
		result = append(result, c)
	}
	return result
}

func fuseDocumentChunks(lexical, semantic []AIDocumentChunk, limit int) []AIDocumentChunk {
	ranked := documentsearch.Fuse(chunkPassages(lexical), chunkPassages(semantic))
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return rankedChunks(ranked, lexical, semantic)
}

func rankDocumentChunks(ctx context.Context, pipeline *documentsearch.Pipeline, query string, lexical, semantic []AIDocumentChunk, limit int) []AIDocumentChunk {
	return rankedChunks(pipeline.Rank(ctx, query, chunkPassages(lexical), chunkPassages(semantic), limit), lexical, semantic)
}

// Neighbor context is loaded only after retrieval and with the same live scope.
// Keep anchor IDs for citations; locators describe the entire returned window.
func (r *AIDocumentRepository) expandDocumentContext(ctx context.Context, userID int64, chatID string, hits []AIDocumentChunk, budget int) ([]AIDocumentChunk, error) {
	if len(hits) == 0 {
		return hits, nil
	}
	ids := make([]uuid.UUID, len(hits))
	for i, c := range hits {
		var err error
		ids[i], err = uuid.Parse(c.ID)
		if err != nil {
			return nil, err
		}
	}
	rows, err := r.db.Query(ctx, `
 SELECT DISTINCT ON(c.id) c.id,c.document_id,c.conversation_id,d.name,c.ordinal,c.text,c.locator,c.token_count
 FROM ai_document_chunks c JOIN ai_documents d ON d.id=c.document_id
 JOIN ai_conversations a ON a.id=c.conversation_id
 JOIN ai_document_chunks anchor ON anchor.id=ANY($3::uuid[]) AND anchor.document_id=c.document_id
 AND c.ordinal BETWEEN anchor.ordinal-1 AND anchor.ordinal+1
 WHERE c.owner_id=$1 AND d.owner_id=$1 AND a.user_id=$1 AND c.conversation_id=$2
 AND d.status='ready' AND d.deleted_at IS NULL
 ORDER BY c.id`, userID, chatID, ids)
	if err != nil {
		return nil, err
	}
	neighbors, err := scanAIDocumentChunks(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	byDocument := map[string]map[int]AIDocumentChunk{}
	for _, c := range neighbors {
		if byDocument[c.DocumentID] == nil {
			byDocument[c.DocumentID] = map[int]AIDocumentChunk{}
		}
		byDocument[c.DocumentID][c.Ordinal] = c
	}
	result := make([]AIDocumentChunk, 0, len(hits))
	covered := map[string]bool{}
	for _, anchor := range hits {
		fresh, authorized := byDocument[anchor.DocumentID][anchor.Ordinal]
		if !authorized || fresh.ID != anchor.ID {
			continue
		}
		anchor = fresh
		if covered[anchor.ID] || len([]rune(anchor.Text)) > budget {
			continue
		}
		anchorChars := len([]rune(anchor.Text))
		anchorOffset := 0
		parts := []string{}
		start, end := anchor.Ordinal, anchor.Ordinal
		cost := len([]rune(anchor.Text))
		tokens := anchor.TokenCount
		if c, ok := byDocument[anchor.DocumentID][anchor.Ordinal-1]; ok && cost+len([]rune(c.Text))+2 <= budget {
			parts = append(parts, c.Text)
			anchorOffset = len([]rune(c.Text)) + 2
			start--
			cost += len([]rune(c.Text)) + 2
			tokens += c.TokenCount
			covered[c.ID] = true
		}
		parts = append(parts, anchor.Text)
		if c, ok := byDocument[anchor.DocumentID][anchor.Ordinal+1]; ok && cost+len([]rune(c.Text))+2 <= budget {
			parts = append(parts, c.Text)
			end++
			cost += len([]rune(c.Text)) + 2
			tokens += c.TokenCount
			covered[c.ID] = true
		}
		covered[anchor.ID] = true
		anchor.Text = strings.Join(parts, "\n\n")
		anchor.TokenCount = tokens
		locator := map[string]any{}
		for k, v := range anchor.Locator {
			locator[k] = v
		}
		locator["context_start_chunk"] = start + 1
		locator["context_end_chunk"] = end + 1
		locator["anchor_chunk"] = anchor.Ordinal + 1
		locator["anchor_offset_runes"] = anchorOffset
		locator["anchor_runes"] = anchorChars
		anchor.Locator = locator
		result = append(result, anchor)
		budget -= cost
	}
	return result, nil
}

func (r *AIDocumentRepository) semanticChunks(ctx context.Context, userID int64, chatID, query string, ids []uuid.UUID) ([]AIDocumentChunk, error) {
	embeddingCtx, cancelEmbedding := context.WithTimeout(ctx, 5*time.Second)
	vectors, err := r.semantic.Embed(embeddingCtx, []string{query})
	cancelEmbedding()
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `
 WITH authorized AS MATERIALIZED (
 SELECT c.*,d.name FROM ai_document_chunks c JOIN ai_documents d ON d.id=c.document_id
 JOIN ai_conversations a ON a.id=c.conversation_id
 WHERE c.owner_id=$1 AND d.owner_id=$1 AND a.user_id=$1 AND c.conversation_id=$2
 AND d.status='ready' AND d.deleted_at IS NULL
 AND (cardinality($3::uuid[])=0 OR c.document_id=ANY($3::uuid[]))
 ), candidates AS MATERIALIZED (
 SELECT c.*,e.embedding FROM authorized c JOIN ai_text_embeddings e
 ON e.content_hash=encode(sha256(convert_to(c.text,'UTF8')),'hex') AND e.model=$4
 WHERE e.embedding IS NOT NULL AND vector_dims(e.embedding)=$6
 )
 SELECT id,document_id,conversation_id,name,ordinal,text,locator,token_count FROM candidates
 ORDER BY embedding <=> $5::vector,id LIMIT 40`, userID, chatID, ids, r.semantic.ModelKey(), vectors[0], r.semantic.Dimensions())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAIDocumentChunks(rows)
}

// Backfill includes old and newly parsed chunks. Small batches and a cooldown
// on provider failure keep extraction working when the local model is offline.
func (r *AIDocumentRepository) BackfillEmbeddings(ctx context.Context) error {
	if r.semantic == nil {
		return nil
	}
	rows, err := r.db.Query(ctx, `
 SELECT c.text FROM ai_document_chunks c JOIN ai_documents d ON d.id=c.document_id
 LEFT JOIN ai_text_embeddings e ON e.model=$1
 AND e.content_hash=encode(sha256(convert_to(c.text,'UTF8')),'hex')
 WHERE d.status='ready' AND d.deleted_at IS NULL AND e.embedding IS NULL
 AND (e.retry_after IS NULL OR e.retry_after<=NOW())
 ORDER BY c.created_at,c.id LIMIT 16`, r.semantic.ModelKey())
	if err != nil {
		return err
	}
	texts := []string{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			rows.Close()
			return err
		}
		texts = append(texts, text)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return r.semantic.IndexTextsDeferred(ctx, texts)
}

// ListReadyChunks returns a bounded, deterministic context for a ready
// document when full-text search has no hits. This is important for broad
// requests such as "summarize this document": the wording of the request is
// not expected to occur inside the document itself.
func (r *AIDocumentRepository) ListReadyChunks(ctx context.Context, userID int64, conversationID string, documentIDs []string, limit int) ([]AIDocumentChunk, error) {
	if limit <= 0 || limit > 20 {
		limit = 12
	}
	selectedIDs, err := documentSearchIDs(documentIDs)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.document_id, c.conversation_id, d.name, c.ordinal, c.text, c.locator, c.token_count
		FROM ai_document_chunks c
		JOIN ai_documents d ON d.id=c.document_id
		WHERE c.owner_id=$1 AND c.conversation_id=$2 AND d.status='ready' AND d.deleted_at IS NULL
		  AND (cardinality($3::uuid[]) = 0 OR c.document_id = ANY($3::uuid[]))
		ORDER BY d.created_at DESC, c.ordinal ASC
		LIMIT $4
	`, userID, conversationID, selectedIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("list ready ai document chunks: %w", err)
	}
	defer rows.Close()
	return scanAIDocumentChunks(rows)
}

func scanAIDocumentChunks(rows pgx.Rows) ([]AIDocumentChunk, error) {
	items := make([]AIDocumentChunk, 0)
	for rows.Next() {
		var c AIDocumentChunk
		var locator []byte
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.ConversationID, &c.Name, &c.Ordinal, &c.Text, &locator, &c.TokenCount); err != nil {
			return nil, err
		}
		c.Locator = map[string]any{}
		_ = jsonUnmarshal(locator, &c.Locator)
		items = append(items, c)
	}
	return items, rows.Err()
}

func (r *AIDocumentRepository) AttachMessage(ctx context.Context, messageID string, documentIDs []string) error {
	for _, documentID := range documentIDs {
		if _, err := r.db.Exec(ctx, `INSERT INTO ai_message_documents(message_id, document_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, messageID, documentID); err != nil {
			return err
		}
	}
	return nil
}

func (r *AIDocumentRepository) SaveAnswerSources(ctx context.Context, messageID string, chunks []AIDocumentChunk) error {
	for rank, chunk := range chunks {
		if _, err := r.db.Exec(ctx, `
			INSERT INTO ai_answer_sources (message_id, document_id, chunk_id, rank, locator)
			VALUES ($1,$2,$3,$4,$5::jsonb)
			ON CONFLICT (message_id, chunk_id) DO UPDATE SET rank=EXCLUDED.rank, locator=EXCLUDED.locator
		`, messageID, chunk.DocumentID, chunk.ID, rank+1, mustJSON(chunk.Locator)); err != nil {
			return fmt.Errorf("save answer source: %w", err)
		}
	}
	return nil
}

func (r *AIDocumentRepository) DeleteDocument(ctx context.Context, userID int64, conversationID, documentID string) error {
	_, err := r.db.Exec(ctx, `UPDATE ai_documents SET status='deleted', deleted_at=NOW(), updated_at=NOW() WHERE id=$1 AND conversation_id=$2 AND owner_id=$3 AND deleted_at IS NULL`, documentID, conversationID, userID)
	return err
}

func (r *AIDocumentRepository) RetryDocument(ctx context.Context, userID int64, conversationID, documentID string) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM ai_documents WHERE id=$1 AND conversation_id=$2 AND owner_id=$3 AND deleted_at IS NULL FOR UPDATE`, documentID, conversationID, userID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("document not found")
		}
		return err
	}
	if status != "failed" {
		return fmt.Errorf("document is not retryable")
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_documents SET status='uploaded', error_code=NULL, error_message=NULL, updated_at=NOW() WHERE id=$1`, documentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ai_document_jobs(document_id, state) VALUES($1,'queued')`, documentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mustJSON(value map[string]any) string {
	data, _ := jsonMarshal(value)
	return string(data)
}

// Small indirections keep the repository easy to test without exposing JSON implementation details.
var jsonMarshal = func(value any) ([]byte, error) { return json.Marshal(value) }
var jsonUnmarshal = func(data []byte, value any) error { return json.Unmarshal(data, value) }

// ReadDocumentPage never crosses the authenticated owner or current chat.
func (r *AIDocumentRepository) ReadDocumentPage(ctx context.Context, userID int64, chatID, documentID string, page int) ([]AIDocumentChunk, error) {
	rows, err := r.db.Query(ctx, `
        SELECT c.id,c.document_id,c.conversation_id,d.name,c.ordinal,c.text,c.locator,c.token_count
        FROM ai_document_chunks c JOIN ai_documents d ON d.id=c.document_id
        JOIN ai_conversations a ON a.id=c.conversation_id
        WHERE c.owner_id=$1 AND d.owner_id=$1 AND a.user_id=$1 AND c.conversation_id=$2
        AND c.document_id=$3 AND d.status='ready' AND d.deleted_at IS NULL
        ORDER BY c.ordinal LIMIT 4 OFFSET $4`, userID, chatID, documentID, (page-1)*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAIDocumentChunks(rows)
}
