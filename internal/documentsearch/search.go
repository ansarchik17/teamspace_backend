// Package documentsearch provides opt-in semantic search using a self-hosted
// Ollama model and PostgreSQL/pgvector. It never calls paid AI providers.
package documentsearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	URL           string
	Model         string
	Dimensions    int
	Timeout       time.Duration
	QueryURL      string
	QueryModel    string
	QueryTimeout  time.Duration
	RerankURL     string
	RerankTimeout time.Duration
}

type Search struct {
	db     *pgxpool.Pool
	cfg    Config
	client *http.Client
}

func New(ctx context.Context, db *pgxpool.Pool, cfg Config) (*Search, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("document search requires a self-hosted Ollama HTTP URL")
	}
	if strings.TrimSpace(cfg.Model) == "" || cfg.Dimensions < 1 || cfg.Dimensions > 16000 || cfg.Timeout <= 0 {
		return nil, fmt.Errorf("invalid document embedding model, dimensions or timeout")
	}
	var available bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('ai_text_embeddings') IS NOT NULL`).Scan(&available); err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("install pgvector and run deploy/production/document-search.sql first")
	}
	return &Search{db: db, cfg: cfg, client: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// ModelKey separates different embedding spaces, including model revisions.
// Pin an immutable local model tag and change it when replacing its weights.
func (s *Search) ModelKey() string { return fmt.Sprintf("%s:%d", s.cfg.Model, s.cfg.Dimensions) }
func (s *Search) Dimensions() int  { return s.cfg.Dimensions }
func Hash(text string) string      { h := sha256.Sum256([]byte(text)); return hex.EncodeToString(h[:]) }

func (s *Search) Embed(ctx context.Context, texts []string) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > 16 {
		return nil, fmt.Errorf("embedding batch exceeds 16 texts")
	}
	body, err := json.Marshal(map[string]any{"model": s.cfg.Model, "input": texts, "truncate": false, "keep_alive": "10m"})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.URL, "/")+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("local embedding request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local embedding HTTP status %d", resp.StatusCode)
	}
	var result struct {
		Embeddings [][]float64 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode local embeddings: %w", err)
	}
	if result.Error != "" || len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("local embedding response count mismatch or model error")
	}
	vectors := make([]string, len(texts))
	for i, v := range result.Embeddings {
		if len(v) != s.cfg.Dimensions {
			return nil, fmt.Errorf("embedding dimensions: got %d want %d", len(v), s.cfg.Dimensions)
		}
		norm := 0.0
		for _, x := range v {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, fmt.Errorf("non-finite embedding")
			}
			norm += x * x
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, fmt.Errorf("invalid embedding norm")
		}
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		vectors[i] = string(data)
	}
	return vectors, nil
}

// IndexTexts caches embeddings only, never file text. Callers must check ACLs
// before supplying text or retrieving results. Hashes are cache IDs, not ACLs.
func (s *Search) IndexTexts(ctx context.Context, texts []string) error {
	for start := 0; start < len(texts); start += 16 {
		end := start + 16
		if end > len(texts) {
			end = len(texts)
		}
		missing := []string{}
		seen := map[string]bool{}
		for _, text := range texts[start:end] {
			hash := Hash(text)
			if seen[hash] {
				continue
			}
			seen[hash] = true
			var exists bool
			err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_text_embeddings WHERE model=$1 AND content_hash=$2 AND embedding IS NOT NULL)`, s.ModelKey(), hash).Scan(&exists)
			if err != nil {
				return err
			}
			if !exists {
				missing = append(missing, text)
			}
		}
		if len(missing) == 0 {
			continue
		}
		vectors, err := s.Embed(ctx, missing)
		if err != nil {
			return err
		}
		for i, text := range missing {
			_, err = s.db.Exec(ctx, `INSERT INTO ai_text_embeddings(model,content_hash,embedding) VALUES($1,$2,$3::vector)
    ON CONFLICT(model,content_hash) DO UPDATE SET embedding=EXCLUDED.embedding, retry_after=NULL`, s.ModelKey(), Hash(text), vectors[i])
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// RankTexts restricts nearest-neighbor search to these already-authorized texts.
func (s *Search) RankTexts(ctx context.Context, query string, texts []string) ([]int, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > 512 {
		return nil, fmt.Errorf("semantic excerpt limit exceeded")
	}
	if err := s.IndexTexts(ctx, texts); err != nil {
		return nil, err
	}
	vectors, err := s.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	hashes := make([]string, len(texts))
	for i, text := range texts {
		hashes[i] = Hash(text)
	}
	rows, err := s.db.Query(ctx, `SELECT h.ordinality::int-1 FROM unnest($2::text[]) WITH ORDINALITY h(hash,ordinality)
 JOIN ai_text_embeddings e ON e.content_hash=h.hash AND e.model=$1
 WHERE e.embedding IS NOT NULL AND vector_dims(e.embedding)=$4
 ORDER BY e.embedding <=> $3::vector,h.ordinality`, s.ModelKey(), hashes, vectors[0], s.cfg.Dimensions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []int{}
	for rows.Next() {
		var i int
		if err := rows.Scan(&i); err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}

// IndexTextsDeferred persists a cooldown independently of the provider deadline.
func (s *Search) IndexTextsDeferred(ctx context.Context, texts []string) error {
	err := s.IndexTexts(ctx, texts)
	if err == nil {
		return nil
	}
	cooldownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, text := range texts {
		if _, markErr := s.db.Exec(cooldownCtx, `INSERT INTO ai_text_embeddings(model,content_hash,retry_after) VALUES($1,$2,now()+INTERVAL '5 minutes')
   ON CONFLICT(model,content_hash) DO UPDATE SET retry_after=EXCLUDED.retry_after WHERE ai_text_embeddings.embedding IS NULL`, s.ModelKey(), Hash(text)); markErr != nil {
			return fmt.Errorf("embedding retry persistence: %w", markErr)
		}
	}
	return err
}
