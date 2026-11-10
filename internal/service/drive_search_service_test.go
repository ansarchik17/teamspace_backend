package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestDriveSearchServiceSearchBuildsACLQueryAndParsesSources(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alem_drive_chunks/_search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capturedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hits": {
				"hits": [
					{
						"_score": 99.5,
						"_source": {
							"file_id": "11111111-1111-4111-8111-111111111111",
							"file_name": "Доклад Qaztech.docx",
							"folder_path": "Docs/Migration",
							"updated_at": "2026-02-24T07:38:39Z"
						},
						"inner_hits": {
							"top_chunks_per_file": {
								"hits": {
									"hits": [
											{
												"_score": 99.5,
												"_source": {
													"chunk_index": 0,
													"chunk_count": 2,
													"section_index": 0,
													"section_id": "11111111-1111-4111-8111-111111111111:section-0",
													"section_content": "Стратегия миграции",
													"content": "Стратегия миграции"
												},
											"highlight": {
												"content": ["<em>миграции</em>"]
											}
										}
									]
								}
							}
						}
					}
				]
			}
		}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(config.DriveSearchConfig{
		ElasticsearchURL: server.URL,
		Index:            "alem_drive_chunks",
		RequestTimeout:   5 * time.Second,
		ResultLimit:      10,
		ChunksPerFile:    3,
	}, zap.NewNop())

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{uuid.MustParse("11111111-1111-4111-8111-111111111111"): {ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), RealName: "Доклад Qaztech.docx"}}}
	}
	resp, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "миграция"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !strings.Contains(capturedBody, `"allowed_user_ids":38`) {
		t.Fatalf("expected ACL filter in query body, got %s", capturedBody)
	}
	if strings.Contains(capturedBody, `"is_public":true`) {
		t.Fatalf("expected search query to ignore is_public ACL shortcut, got %s", capturedBody)
	}
	if resp.TotalFiles != 1 {
		t.Fatalf("expected TotalFiles=1, got %d", resp.TotalFiles)
	}
	if got := resp.Sources[0].FileName; got != "Доклад Qaztech.docx" {
		t.Fatalf("unexpected file name: %s", got)
	}
	if len(resp.Sources[0].Chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(resp.Sources[0].Chunks))
	}
}

func TestDriveSearchServiceSearchReturnsElasticErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad query"}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(config.DriveSearchConfig{
		ElasticsearchURL: server.URL,
		Index:            "alem_drive_chunks",
		RequestTimeout:   5 * time.Second,
	}, zap.NewNop())

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{}}
	}
	_, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "миграция"})
	if err == nil || !strings.Contains(err.Error(), "status=400") {
		t.Fatalf("expected elastic status error, got %v", err)
	}
}

func TestDriveSearchServiceSearchRequestIsValidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		query := payload["query"].(map[string]any)
		boolQuery := query["bool"].(map[string]any)
		should := boolQuery["should"].([]any)
		if len(should) == 0 {
			t.Fatalf("expected query should clauses")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(config.DriveSearchConfig{
		ElasticsearchURL: server.URL,
		Index:            "alem_drive_chunks",
		RequestTimeout:   5 * time.Second,
	}, zap.NewNop())

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{}}
	}
	if _, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "что написано про миграцию"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
}

func TestDriveSearchServiceSearchAnalyticalQueryAddsBoostClauses(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(config.DriveSearchConfig{
		ElasticsearchURL: server.URL,
		Index:            "alem_drive_chunks",
		RequestTimeout:   5 * time.Second,
	}, zap.NewNop())

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{}}
	}
	if _, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{
		Question:      "какие показатели по министерству",
		ChunksPerFile: 8,
	}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	query := payload["query"].(map[string]any)
	boolQuery := query["bool"].(map[string]any)
	should := boolQuery["should"].([]any)
	if len(should) < 5 {
		t.Fatalf("expected analytical query to add extra should clauses, got %d", len(should))
	}

	collapse := payload["collapse"].(map[string]any)
	innerHits := collapse["inner_hits"].(map[string]any)
	if got := int(innerHits["size"].(float64)); got != 8 {
		t.Fatalf("expected chunks_per_file size=8, got %d", got)
	}
}

func TestDriveSearchServiceSearchAddsHybridKNNWhenEmbedderConfigured(t *testing.T) {
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(
		config.DriveSearchConfig{
			ElasticsearchURL: server.URL,
			Index:            "alem_drive_chunks",
			RequestTimeout:   5 * time.Second,
			EmbeddingModel:   "BAAI/bge-m3",
			EmbeddingDims:    3,
			VectorField:      "content_vector",
			HybridK:          17,
			NumCandidates:    33,
		},
		zap.NewNop(),
		WithDriveSearchEmbedder(&fakeDriveSearchEmbedder{
			vectors: [][]float64{{0.1, 0.2, 0.3}},
		}),
	)

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{}}
	}
	if _, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "миграция"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected 2 elastic searches, got %d", len(payloads))
	}
	if _, ok := payloads[0]["query"]; !ok {
		t.Fatalf("expected bm25 query payload, got %v", payloads[0])
	}
	knn, ok := payloads[1]["knn"].(map[string]any)
	if !ok {
		t.Fatalf("expected knn clause in payload, got %v", payloads[1])
	}
	if got := knn["field"].(string); got != "content_vector" {
		t.Fatalf("unexpected knn field: %s", got)
	}
	if got := int(knn["k"].(float64)); got != 17 {
		t.Fatalf("unexpected knn k: %d", got)
	}
	if got := int(knn["num_candidates"].(float64)); got != 33 {
		t.Fatalf("unexpected knn num_candidates: %d", got)
	}
}

func TestDriveSearchServiceUsesQueryRewriteAndHydeForHybridSearch(t *testing.T) {
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer server.Close()

	embedder := &fakeDriveSearchEmbedder{
		vectors: [][]float64{{0.1, 0.2, 0.3}},
		chatResponses: []string{
			`{"rewritten_query":"kazakhstan railway lines 2026"}`,
			"Hypothetical document about kazakhstan railway lines planned for 2026.",
		},
	}

	svc := NewDriveSearchService(
		config.DriveSearchConfig{
			ElasticsearchURL:  server.URL,
			Index:             "alem_drive_chunks",
			RequestTimeout:    5 * time.Second,
			EmbeddingModel:    "BAAI/bge-m3",
			EmbeddingDims:     3,
			VectorField:       "content_vector",
			QueryRewriteModel: "Qwen/Qwen3-8B",
			HydeModel:         "Qwen/Qwen3-8B",
			HybridK:           17,
			NumCandidates:     33,
		},
		zap.NewNop(),
		WithDriveSearchEmbedder(embedder),
	)

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{}}
	}
	if _, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "пути 2026?"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected 2 elastic searches, got %d", len(payloads))
	}
	rawLexical, _ := json.Marshal(payloads[0])
	if !strings.Contains(string(rawLexical), "kazakhstan railway lines 2026") {
		t.Fatalf("expected rewritten query in lexical payload, got %s", string(rawLexical))
	}
	if len(embedder.embeddingInputs) != 1 {
		t.Fatalf("expected one embedding input for HyDE, got %d", len(embedder.embeddingInputs))
	}
	if got := embedder.embeddingInputs[0]; got != "Hypothetical document about kazakhstan railway lines planned for 2026." {
		t.Fatalf("unexpected embedding input: %s", got)
	}
}

func TestDriveSearchServiceSearchAppliesRerankToMergedHits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hits": {
				"hits": [
					{
						"_score": 10,
						"_source": {
							"file_id": "11111111-1111-4111-8111-111111111111",
							"file_name": "first.docx",
							"folder_path": "Docs",
							"updated_at": "2026-02-24T07:38:39Z"
						},
						"inner_hits": {
							"top_chunks_per_file": {
								"hits": {
									"hits": [
										{
											"_score": 10,
											"_source": {
												"chunk_index": 0,
												"chunk_count": 1,
												"section_index": 0,
												"section_id": "11111111-1111-4111-8111-111111111111:section-0",
												"section_content": "first content",
												"content": "first content"
											}
										}
									]
								}
							}
						}
					},
					{
						"_score": 9,
						"_source": {
							"file_id": "22222222-2222-4222-8222-222222222222",
							"file_name": "second.docx",
							"folder_path": "Docs",
							"updated_at": "2026-02-24T07:38:40Z"
						},
						"inner_hits": {
							"top_chunks_per_file": {
								"hits": {
									"hits": [
										{
											"_score": 9,
											"_source": {
												"chunk_index": 0,
												"chunk_count": 1,
												"section_index": 0,
												"section_id": "22222222-2222-4222-8222-222222222222:section-0",
												"section_content": "second content",
												"content": "second content"
											}
										}
									]
								}
							}
						}
					}
				]
			}
		}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(
		config.DriveSearchConfig{
			ElasticsearchURL: server.URL,
			Index:            "alem_drive_chunks",
			RequestTimeout:   5 * time.Second,
			EmbeddingModel:   "BAAI/bge-m3",
			EmbeddingDims:    3,
			VectorField:      "content_vector",
			RerankModel:      "BAAI/bge-reranker-v2-m3",
			RerankTopN:       2,
		},
		zap.NewNop(),
		WithDriveSearchEmbedder(&fakeDriveSearchEmbedder{
			vectors: [][]float64{{0.1, 0.2, 0.3}},
			rerank: []alem.RerankResult{
				{Index: 0, RelevanceScore: 0.1},
				{Index: 1, RelevanceScore: 0.9},
			},
		}),
	)

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{uuid.MustParse("11111111-1111-4111-8111-111111111111"): {ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), RealName: "first.docx"}, uuid.MustParse("22222222-2222-4222-8222-222222222222"): {ID: uuid.MustParse("22222222-2222-4222-8222-222222222222"), RealName: "second.docx"}}}
	}
	resp, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "миграция"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(resp.Sources))
	}
	if got := resp.Sources[0].FileName; got != "second.docx" {
		t.Fatalf("expected reranked file first, got %s", got)
	}
}

func TestDriveSearchServiceSearchReturnsParentContentFromIndexedSections(t *testing.T) {
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hits": {
				"hits": [
					{
						"_score": 7,
						"_source": {
							"file_id": "11111111-1111-4111-8111-111111111111",
							"file_name": "report.docx",
							"folder_path": "Docs",
							"updated_at": "2026-06-11T08:00:00Z"
						},
						"inner_hits": {
							"top_chunks_per_file": {
								"hits": {
									"hits": [
										{
											"_score": 7,
											"_source": {
												"chunk_index": 0,
												"chunk_count": 1,
												"section_index": 0,
												"section_id": "11111111-1111-4111-8111-111111111111:section-0",
												"section_content": "Первая секция\n\nСодержит контекст",
												"content": "Содержит контекст"
											}
										}
									]
								}
							}
						}
					}
				]
			}
		}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(
		config.DriveSearchConfig{
			ElasticsearchURL: server.URL,
			Index:            "alem_drive_chunks",
			RequestTimeout:   5 * time.Second,
		},
		zap.NewNop(),
	)

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{uuid.MustParse("11111111-1111-4111-8111-111111111111"): {ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), RealName: "report.docx"}}}
	}
	resp, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "миграция"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if searchCalls != 1 {
		t.Fatalf("expected one search call when parent content is already indexed, got %d", searchCalls)
	}
	if len(resp.Sources) != 1 || len(resp.Sources[0].Chunks) != 1 {
		t.Fatalf("unexpected response shape: %#v", resp.Sources)
	}
	if got := resp.Sources[0].Chunks[0].ParentContent; got != "Первая секция\n\nСодержит контекст" {
		t.Fatalf("unexpected parent content: %q", got)
	}
}

func TestDriveSearchServiceExpandsParentContextChunks(t *testing.T) {
	searchCalls := 0
	var lexicalBody, contextBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		searchCalls++
		if searchCalls == 1 {
			lexicalBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"hits": {
					"hits": [
						{
							"_score": 8,
							"_source": {
								"file_id": "11111111-1111-4111-8111-111111111111",
								"file_name": "report.docx",
								"folder_path": "Docs",
								"updated_at": "2026-06-11T08:00:00Z"
							},
							"inner_hits": {
								"top_chunks_per_file": {
									"hits": {
										"hits": [
											{
												"_score": 8,
						"_source": {
							"chunk_index": 1,
							"chunk_count": 3,
							"content": "middle chunk"
						},
												"highlight": {
													"content": ["<em>middle</em>"]
												}
											}
										]
									}
								}
							}
						}
					]
				}
			}`))
			return
		}
		contextBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hits": {
				"hits": [
					{
						"_score": 0,
						"_source": {
							"chunk_index": 0,
							"chunk_count": 3,
							"section_index": 0,
							"section_id": "11111111-1111-4111-8111-111111111111:section-0",
							"section_content": "previous chunk",
							"content": "previous chunk"
						}
					},
					{
						"_score": 0,
						"_source": {
							"chunk_index": 2,
							"chunk_count": 3,
							"section_index": 0,
							"section_id": "11111111-1111-4111-8111-111111111111:section-0",
							"section_content": "next chunk",
							"content": "next chunk"
						}
					}
				]
			}
		}`))
	}))
	defer server.Close()

	svc := NewDriveSearchService(
		config.DriveSearchConfig{
			ElasticsearchURL:    server.URL,
			Index:               "alem_drive_chunks",
			RequestTimeout:      5 * time.Second,
			ParentContextWindow: 1,
		},
		zap.NewNop(),
	)

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{uuid.MustParse("11111111-1111-4111-8111-111111111111"): {ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), RealName: "report.docx"}}}
	}
	resp, err := svc.Search(context.Background(), 38, domain.DriveSearchQueryRequest{Question: "миграция"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if searchCalls != 2 {
		t.Fatalf("expected lexical search plus context fetch, got %d calls", searchCalls)
	}
	if !strings.Contains(lexicalBody, `"allowed_user_ids":38`) {
		t.Fatalf("expected ACL filter in lexical query, got %s", lexicalBody)
	}
	if !strings.Contains(contextBody, `"chunk_index":[0,1,2]`) {
		t.Fatalf("expected neighbor chunk lookup in context query, got %s", contextBody)
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(resp.Sources))
	}
	if len(resp.Sources[0].Chunks) != 3 {
		t.Fatalf("expected 3 chunks after context expansion, got %d", len(resp.Sources[0].Chunks))
	}
	if !resp.Sources[0].Chunks[0].IsContext || resp.Sources[0].Chunks[1].IsContext || !resp.Sources[0].Chunks[2].IsContext {
		t.Fatalf("unexpected context flags: %#v", resp.Sources[0].Chunks)
	}
}

func TestDriveSearchServiceIndexFileAddsVectorFieldToMappingAndBulkDocs(t *testing.T) {
	fileID := uuid.New()
	var mappingBody string
	var bulkBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alem_drive_chunks":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/alem_drive_chunks/_mapping":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read mapping body: %v", err)
			}
			mappingBody = string(body)
			w.WriteHeader(http.StatusOK)
		case "/alem_drive_chunks/_delete_by_query":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"deleted":0}`))
		case "/alem_drive_chunks/_bulk":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read bulk body: %v", err)
			}
			bulkBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":false}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc := NewDriveSearchService(
		config.DriveSearchConfig{
			ElasticsearchURL: server.URL,
			Index:            "alem_drive_chunks",
			RequestTimeout:   5 * time.Second,
			EmbeddingModel:   "BAAI/bge-m3",
			EmbeddingDims:    3,
			VectorField:      "content_vector",
		},
		zap.NewNop(),
		WithDriveSearchEmbedder(&fakeDriveSearchEmbedder{
			vectors: [][]float64{{0.1, 0.2, 0.3}},
		}),
	)
	svc.repo = fakeDriveSearchRepo{
		fileByID: map[uuid.UUID]*domain.DriveFile{
			fileID: {
				ID:         fileID,
				OwnerID:    77,
				ObjectName: "drive/report.docx",
				RealName:   "report.docx",
				Scope:      "drive",
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			},
		},
		allowedUsersByFileID: map[uuid.UUID][]domain.UserPermission{
			fileID: {{ID: 77}},
		},
	}
	svc.fileSvc = fakeDriveSearchFileDownloader{
		dataByObjectName: map[string][]byte{
			"drive/report.docx": []byte("fake-docx"),
		},
	}
	svc.extractor = fakeDriveSearchExtractor{
		textByFilename: map[string]string{
			"report.docx": "Документ через fallback",
		},
	}

	if err := svc.IndexFile(context.Background(), fileID); err != nil {
		t.Fatalf("IndexFile() error = %v", err)
	}
	if !strings.Contains(mappingBody, `"dense_vector"`) {
		t.Fatalf("expected dense_vector mapping, got %s", mappingBody)
	}
	if !strings.Contains(bulkBody, `"content_vector":[0.1,0.2,0.3]`) {
		t.Fatalf("expected bulk body to include content_vector, got %s", bulkBody)
	}
	if !strings.Contains(bulkBody, `"section_content":"Документ через fallback"`) {
		t.Fatalf("expected bulk body to include section_content, got %s", bulkBody)
	}
	if !strings.Contains(bulkBody, `"section_index":0`) {
		t.Fatalf("expected bulk body to include section_index, got %s", bulkBody)
	}
}

func TestDriveSearchServiceSearchIndexesRequestedFilesWhenInitialResultIsEmpty(t *testing.T) {
	fileID := uuid.New()
	searchCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alem_drive_chunks/_search":
			searchCalls++
			w.Header().Set("Content-Type", "application/json")
			if searchCalls == 1 {
				_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"hits": {
					"hits": [
						{
							"_score": 42,
							"_source": {
								"file_id": "` + fileID.String() + `",
								"file_name": "report.docx",
								"updated_at": "2026-06-11T08:00:00Z"
							},
							"inner_hits": {
								"top_chunks_per_file": {
									"hits": {
										"hits": [
										{
											"_score": 42,
											"_source": {
												"chunk_index": 0,
												"chunk_count": 1,
												"section_index": 0,
												"section_id": "11111111-1111-4111-8111-111111111111:section-0",
												"section_content": "Документ через fallback",
												"content": "Документ через fallback"
											}
											}
										]
									}
								}
							}
						}
					]
				}
			}`))
		case "/alem_drive_chunks":
			if r.Method != http.MethodHead && r.Method != http.MethodPut {
				t.Fatalf("unexpected method for index path: %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		case "/alem_drive_chunks/_mapping":
			w.WriteHeader(http.StatusOK)
		case "/alem_drive_chunks/_delete_by_query":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"deleted":0}`))
		case "/alem_drive_chunks/_bulk":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":false}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc := NewDriveSearchService(config.DriveSearchConfig{
		ElasticsearchURL: server.URL,
		Index:            "alem_drive_chunks",
		RequestTimeout:   5 * time.Second,
	}, zap.NewNop())
	svc.repo = fakeDriveSearchRepo{
		fileByID: map[uuid.UUID]*domain.DriveFile{
			fileID: {
				ID:         fileID,
				OwnerID:    77,
				ObjectName: "drive/report.docx",
				RealName:   "report.docx",
				Scope:      "drive",
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			},
		},
		allowedUsersByFileID: map[uuid.UUID][]domain.UserPermission{
			fileID: {{ID: 77}},
		},
	}
	svc.fileSvc = fakeDriveSearchFileDownloader{
		dataByObjectName: map[string][]byte{
			"drive/report.docx": []byte("fake-docx"),
		},
	}
	svc.extractor = fakeDriveSearchExtractor{
		textByFilename: map[string]string{
			"report.docx": "Документ через fallback",
		},
	}

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{}}
	}
	resp, err := svc.Search(context.Background(), 77, domain.DriveSearchQueryRequest{
		Question: "содержание",
		FileIDs:  []string{fileID.String()},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if searchCalls != 2 {
		t.Fatalf("expected 2 search attempts, got %d", searchCalls)
	}
	if resp.TotalFiles != 1 {
		t.Fatalf("expected TotalFiles=1, got %d", resp.TotalFiles)
	}
}

func TestDriveSearchServiceSearchIndexesRequestedFilesWhenIndexMissing(t *testing.T) {
	fileID := uuid.New()
	searchCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alem_drive_chunks/_search":
			searchCalls++
			w.Header().Set("Content-Type", "application/json")
			if searchCalls == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"index_not_found_exception"}`))
				return
			}
			_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
		case "/alem_drive_chunks":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusOK)
				return
			}
			t.Fatalf("unexpected method for index path: %s", r.Method)
		case "/alem_drive_chunks/_mapping":
			w.WriteHeader(http.StatusOK)
		case "/alem_drive_chunks/_delete_by_query":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"deleted":0}`))
		case "/alem_drive_chunks/_bulk":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":false}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc := NewDriveSearchService(config.DriveSearchConfig{
		ElasticsearchURL: server.URL,
		Index:            "alem_drive_chunks",
		RequestTimeout:   5 * time.Second,
	}, zap.NewNop())
	svc.repo = fakeDriveSearchRepo{
		fileByID: map[uuid.UUID]*domain.DriveFile{
			fileID: {
				ID:         fileID,
				OwnerID:    77,
				ObjectName: "drive/report.docx",
				RealName:   "report.docx",
				Scope:      "drive",
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			},
		},
		allowedUsersByFileID: map[uuid.UUID][]domain.UserPermission{
			fileID: {{ID: 77}},
		},
	}
	svc.fileSvc = fakeDriveSearchFileDownloader{
		dataByObjectName: map[string][]byte{
			"drive/report.docx": []byte("fake-docx"),
		},
	}
	svc.extractor = fakeDriveSearchExtractor{
		textByFilename: map[string]string{
			"report.docx": "Документ через fallback",
		},
	}

	if svc.repo == nil {
		svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{}}
	}
	if _, err := svc.Search(context.Background(), 77, domain.DriveSearchQueryRequest{
		Question: "содержание",
		FileIDs:  []string{fileID.String()},
	}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if searchCalls != 2 {
		t.Fatalf("expected 2 search attempts, got %d", searchCalls)
	}
}

type fakeDriveSearchRepo struct {
	fileByID             map[uuid.UUID]*domain.DriveFile
	allowedUsersByFileID map[uuid.UUID][]domain.UserPermission
	accessibleIDs        []uuid.UUID
}

func (f fakeDriveSearchRepo) GetActiveDriveFilesByIDs(_ context.Context, fileIDs []uuid.UUID) ([]*domain.DriveFile, error) {
	files := make([]*domain.DriveFile, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		if file, ok := f.fileByID[fileID]; ok {
			files = append(files, file)
		}
	}
	return files, nil
}

func (f fakeDriveSearchRepo) ListAccessibleDriveFileIDs(_ context.Context, _ int64, fileIDs []uuid.UUID) ([]uuid.UUID, error) {
	if f.accessibleIDs != nil {
		return f.accessibleIDs, nil
	}
	return fileIDs, nil
}

func (f fakeDriveSearchRepo) GetFileByID(_ context.Context, fileID uuid.UUID) (*domain.DriveFile, error) {
	file, ok := f.fileByID[fileID]
	if !ok {
		return nil, errors.New("file not found")
	}
	return file, nil
}

func (f fakeDriveSearchRepo) ListUsersWithAccess(_ context.Context, _ domain.ResourceType, resourceID uuid.UUID) ([]domain.UserPermission, error) {
	return f.allowedUsersByFileID[resourceID], nil
}

type fakeDriveSearchFileDownloader struct {
	dataByObjectName map[string][]byte
}

func (f fakeDriveSearchFileDownloader) DownloadFile(_ context.Context, objectName string) (io.ReadCloser, string, int64, error) {
	data, ok := f.dataByObjectName[objectName]
	if !ok {
		return nil, "", 0, errors.New("object not found")
	}
	return io.NopCloser(strings.NewReader(string(data))), "application/octet-stream", int64(len(data)), nil
}

type fakeDriveSearchExtractor struct {
	textByFilename map[string]string
}

func (f fakeDriveSearchExtractor) ExtractTextBytes(filename string, _ []byte) (string, error) {
	text, ok := f.textByFilename[filename]
	if !ok {
		return "", errors.New("extractor missing file")
	}
	return text, nil
}

type fakeDriveSearchEmbedder struct {
	vectors         [][]float64
	rerank          []alem.RerankResult
	chatResponses   []string
	embeddingInputs []string
	rerankInputs    []string
	err             error
}

func (f *fakeDriveSearchEmbedder) CreateChatCompletion(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.chatResponses) > 0 {
		resp := f.chatResponses[0]
		f.chatResponses = f.chatResponses[1:]
		return &alem.ChatCompletionResponse{
			Choices: []alem.Choice{{Message: alem.Message{Content: resp}}},
		}, nil
	}
	systemPrompt := ""
	userPrompt := ""
	if len(req.Messages) > 0 {
		systemPrompt = strings.TrimSpace(fmt.Sprintf("%v", req.Messages[0].Content))
	}
	if len(req.Messages) > 1 {
		userPrompt = strings.TrimSpace(fmt.Sprintf("%v", req.Messages[len(req.Messages)-1].Content))
	}
	queryText := userPrompt
	if idx := strings.Index(queryText, "User query:"); idx >= 0 {
		queryText = strings.TrimSpace(queryText[idx+len("User query:"):])
	}
	if idx := strings.Index(queryText, "Query:"); idx >= 0 {
		queryText = strings.TrimSpace(queryText[idx+len("Query:"):])
	}
	if strings.Contains(systemPrompt, "rewritten_query") {
		if queryText == "" {
			queryText = "fallback query"
		}
		return &alem.ChatCompletionResponse{
			Choices: []alem.Choice{
				{
					Message: alem.Message{
						Content: `{"rewritten_query":"` + queryText + `"}`,
					},
				},
			},
		}, nil
	}
	if queryText == "" {
		queryText = "fallback query"
	}
	return &alem.ChatCompletionResponse{
		Choices: []alem.Choice{
			{
				Message: alem.Message{
					Content: "Hypothetical document passage relevant to " + queryText,
				},
			},
		},
	}, nil
}

func (f *fakeDriveSearchEmbedder) CreateEmbeddings(_ context.Context, req alem.EmbeddingRequest) (*alem.EmbeddingResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.embeddingInputs = append(f.embeddingInputs, req.Input...)
	data := make([]alem.EmbeddingData, 0, len(req.Input))
	for i := range req.Input {
		vector := []float64(nil)
		if len(f.vectors) > i {
			vector = f.vectors[i]
		}
		data = append(data, alem.EmbeddingData{
			Index:     i,
			Embedding: vector,
		})
	}
	return &alem.EmbeddingResponse{Data: data}, nil
}

func (f *fakeDriveSearchEmbedder) Rerank(_ context.Context, req alem.RerankRequest) (*alem.RerankResponse, error) {
	f.rerankInputs = append(f.rerankInputs, req.Documents...)
	if f.err != nil {
		return nil, f.err
	}
	results := make([]alem.RerankResult, 0, len(req.Documents))
	if len(f.rerank) > 0 {
		return &alem.RerankResponse{Results: f.rerank}, nil
	}
	for i := range req.Documents {
		results = append(results, alem.RerankResult{
			Index:          i,
			RelevanceScore: float64(len(req.Documents) - i),
		})
	}
	return &alem.RerankResponse{Results: results}, nil
}

func TestBAS022StaleIndexCannotLeakToReranker(t *testing.T) {
	own, foreign := uuid.New(), uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"hits":{"hits":[{"_source":{"file_id":"%s","file_name":"own"},"inner_hits":{"top_chunks_per_file":{"hits":{"hits":[{"_source":{"content":"OWN_CANARY"}}]}}}},{"_source":{"file_id":"%s","file_name":"FOREIGN_CANARY"},"inner_hits":{"top_chunks_per_file":{"hits":{"hits":[{"_source":{"content":"FOREIGN_CANARY"}}]}}}}]}}`, own, foreign)
	}))
	defer server.Close()
	embedder := &fakeDriveSearchEmbedder{}
	svc := NewDriveSearchService(config.DriveSearchConfig{ElasticsearchURL: server.URL, Index: "test", RerankModel: "test", EmbeddingModel: "test", EmbeddingDims: 3}, zap.NewNop(), WithDriveSearchEmbedder(embedder))
	svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{own: {ID: own, RealName: "own"}, foreign: {ID: foreign, RealName: "FOREIGN_CANARY"}}, accessibleIDs: []uuid.UUID{own}}
	result, err := svc.Search(context.Background(), 11, domain.DriveSearchQueryRequest{Question: "canary"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if result.TotalFiles != 1 || strings.Contains(string(data), "FOREIGN_CANARY") {
		t.Fatalf("foreign result: %s", data)
	}
	if len(embedder.rerankInputs) != 1 || strings.Contains(strings.Join(embedder.rerankInputs, " "), "FOREIGN_CANARY") {
		t.Fatalf("foreign context sent: %v", embedder.rerankInputs)
	}
	// A revoked grant must be checked again even when the index still returns the same hits.
	svc.repo = fakeDriveSearchRepo{fileByID: map[uuid.UUID]*domain.DriveFile{own: {ID: own}, foreign: {ID: foreign}}, accessibleIDs: []uuid.UUID{}}
	result, err = svc.Search(context.Background(), 11, domain.DriveSearchQueryRequest{Question: "canary", FileIDs: []string{foreign.String()}})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalFiles != 0 || len(embedder.rerankInputs) != 1 {
		t.Fatal("revoked grant reused or foreign data sent")
	}

}

func TestBAS022DriveSearchWithoutLiveACLIsDenied(t *testing.T) {
	contacted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted = true }))
	defer server.Close()
	svc := NewDriveSearchService(config.DriveSearchConfig{ElasticsearchURL: server.URL, Index: "test"}, zap.NewNop())
	if _, err := svc.Search(context.Background(), 11, domain.DriveSearchQueryRequest{Question: "canary"}); err == nil {
		t.Fatal("search without live ACL accepted")
	}
	if contacted {
		t.Fatal("unvalidated search contacted index")
	}
}
