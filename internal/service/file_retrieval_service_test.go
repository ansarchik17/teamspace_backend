package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type testFileCorpus struct {
	lexical, neighbors []documentsearch.Passage
	total, ready       int
	calls              []string
	user               int64
	ids                []uuid.UUID
}

func (f *testFileCorpus) Lexical(_ context.Context, user int64, ids []uuid.UUID, _ []string) ([]documentsearch.Passage, error) {
	f.calls = append(f.calls, "lexical")
	f.user = user
	f.ids = ids
	return f.lexical, nil
}
func (f *testFileCorpus) Semantic(context.Context, int64, []uuid.UUID, string, string, int) ([]documentsearch.Passage, error) {
	return nil, fmt.Errorf("unexpected embedding lookup")
}
func (f *testFileCorpus) Neighbors(_ context.Context, user int64, _ []uuid.UUID, _ []documentsearch.Passage) ([]documentsearch.Passage, error) {
	requireUser := f.user
	if user != requireUser {
		return nil, fmt.Errorf("identity changed")
	}
	f.calls = append(f.calls, "reauthorize")
	return f.neighbors, nil
}
func (f *testFileCorpus) Coverage(context.Context, int64, []uuid.UUID) (int, int, error) {
	return f.total, f.ready, nil
}

type testFileIndex struct {
	failureContextErr error
	enqueued          []uuid.UUID
	job               *repository.FileIndexJob
	saved, failed     bool
	chunks            []documentsearch.TextChunk
}

func (f *testFileIndex) Discover(context.Context) error { return nil }
func (f *testFileIndex) Enqueue(_ context.Context, id uuid.UUID) error {
	f.enqueued = append(f.enqueued, id)
	return nil
}
func (f *testFileIndex) Claim(context.Context, *uuid.UUID) (*repository.FileIndexJob, error) {
	return f.job, nil
}
func (f *testFileIndex) MarkReady(_ context.Context, _ repository.FileIndexJob, _ string, chunks []documentsearch.TextChunk) error {
	f.saved = true
	f.chunks = chunks
	return nil
}
func (f *testFileIndex) MarkFailed(ctx context.Context, _ repository.FileIndexJob, _ string) error {
	f.failed = true
	f.failureContextErr = ctx.Err()
	return nil
}
func (f *testFileIndex) Delete(context.Context, []uuid.UUID) error { return nil }
func (f *testFileIndex) PendingEmbeddingTexts(context.Context, string) ([]string, error) {
	return nil, nil
}

type testFileAccess struct {
	allowed []uuid.UUID
	file    *domain.DriveFile
	checks  int
}

func (f *testFileAccess) GetFileByID(context.Context, uuid.UUID) (*domain.DriveFile, error) {
	return f.file, nil
}
func (f *testFileAccess) ListAccessibleDriveFileIDs(context.Context, int64, []uuid.UUID) ([]uuid.UUID, error) {
	f.checks++
	return f.allowed, nil
}

type testFileStorage struct {
	reads int
	body  string
}

func (f *testFileStorage) DownloadFile(context.Context, string) (io.ReadCloser, string, int64, error) {
	f.reads++
	return io.NopCloser(strings.NewReader(f.body)), "text/plain", int64(len(f.body)), nil
}

type testFileExtractor struct{}

func (testFileExtractor) ExtractTextBytes(_ string, body []byte) (string, error) {
	return string(body), nil
}

func fileRetrievalForTest(t *testing.T, corpus *testFileCorpus, access *testFileAccess, index *testFileIndex) *FileRetrievalService {
	pipeline, err := documentsearch.NewPipeline(documentsearch.Config{}, nil, zap.NewNop())
	require.NoError(t, err)
	return &FileRetrievalService{corpus: corpus, files: access, index: index, pipeline: pipeline, logger: zap.NewNop()}
}
func TestFileRetrievalRejectsScopeBeforeIndexing(t *testing.T) {
	selected := uuid.New()
	corpus := &testFileCorpus{}
	access := &testFileAccess{}
	index := &testFileIndex{}
	svc := fileRetrievalForTest(t, corpus, access, index)
	_, err := svc.Search(context.Background(), 42, domain.DriveSearchQueryRequest{Question: "test", FileIDs: []string{selected.String()}})
	require.Error(t, err)
	require.Empty(t, index.enqueued)
	require.Empty(t, corpus.calls)
	_, err = svc.Search(context.Background(), 42, domain.DriveSearchQueryRequest{Question: "test", FileIDs: []string{"invalid"}})
	require.Error(t, err)
	require.Equal(t, 1, access.checks)
}
func TestFileRetrievalUsesSameScopeAndDropsRevokedEvidence(t *testing.T) {
	selected := uuid.New()
	hit := documentsearch.Passage{ID: uuid.NewString(), DocumentID: selected.String(), Text: "Секретный договор", Ordinal: 1, Name: "Положение.docx"}
	corpus := &testFileCorpus{lexical: []documentsearch.Passage{hit}, neighbors: nil, total: 2, ready: 1}
	index := &testFileIndex{}
	svc := fileRetrievalForTest(t, corpus, &testFileAccess{allowed: []uuid.UUID{selected}}, index)
	result, err := svc.Search(context.Background(), 42, domain.DriveSearchQueryRequest{Question: "договор", FileIDs: []string{selected.String()}})
	require.NoError(t, err)
	require.Empty(t, result.Sources, "revoked source must not return cached text")
	require.Equal(t, int64(42), corpus.user)
	require.Equal(t, []uuid.UUID{selected}, corpus.ids)
	require.Equal(t, []string{"lexical", "reauthorize"}, corpus.calls)
	require.True(t, result.Indexing)
	require.NotEmpty(t, result.Warnings)
}
func TestFileRetrievalKeepsCoordinatesAndContextBoundaries(t *testing.T) {
	id := uuid.NewString()
	hits := []documentsearch.Passage{{ID: "anchor", DocumentID: id, Ordinal: 1, Text: "главный факт", Name: "Документ.docx", Score: 1}}
	neighbors := []documentsearch.Passage{{ID: "previous", DocumentID: id, Ordinal: 0, Text: "предыдущий контекст"}, hits[0], {ID: "next", DocumentID: id, Ordinal: 2, Text: "следующий контекст"}}
	neighbors[1].Locator = map[string]any{"start_line": 12, "end_line": 13, "revision": "v1"}
	result := buildFileSearchResponse("question", hits, neighbors, 1, 1, 100)
	require.Len(t, result.Sources, 1)
	require.Len(t, result.Sources[0].Chunks, 3)
	require.Equal(t, []int{1}, result.Sources[0].ChunkIndices)
	require.Equal(t, 12, result.Sources[0].Chunks[0].Locator["start_line"])
	require.False(t, result.Sources[0].Chunks[0].IsContext)
	require.True(t, result.Sources[0].Chunks[1].IsContext)
	short := buildFileSearchResponse("question", hits, neighbors, 1, 1, len([]rune("главный факт")))
	require.Len(t, short.Sources[0].Chunks, 1)
}
func TestFileRetrievalWorkerPublishesOneRevisionAndDefersFailure(t *testing.T) {
	id := uuid.New()
	updated := time.Now()
	index := &testFileIndex{job: &repository.FileIndexJob{FileID: id, SourceUpdatedAt: updated, Token: uuid.New()}}
	access := &testFileAccess{file: &domain.DriveFile{ID: id, UpdatedAt: updated, Scope: "drive", Size: 20, RealName: "test.txt"}}
	storage := &testFileStorage{body: "Содержимое договора"}
	svc := fileRetrievalForTest(t, &testFileCorpus{}, access, index)
	svc.storage = storage
	svc.extractor = testFileExtractor{}
	require.NoError(t, svc.WorkOnce(context.Background()))
	require.True(t, index.saved)
	require.NotEmpty(t, index.chunks)
	require.False(t, index.failed)
	index.saved = false
	access.file.UpdatedAt = updated.Add(time.Second)
	require.NoError(t, svc.WorkOnce(context.Background()))
	require.False(t, index.saved)
	require.True(t, index.failed)
	require.Equal(t, 1, storage.reads, "stale job must not download a newer revision")
}
func TestFileRetrievalToolEvidenceFitsContextAndKeepsAnchor(t *testing.T) {
	source := domain.DriveSearchSource{FileID: uuid.NewString(), FileName: "file.txt", Chunks: []domain.DriveSearchChunk{{Content: strings.Repeat("Я", 3000)}, {Content: strings.Repeat("Ю", 3000), ChunkIndex: 1}, {Content: "context", IsContext: true}}}
	response, truncated := boundFileEvidence(&domain.DriveSearchQueryResponse{Sources: []domain.DriveSearchSource{source}})
	require.True(t, truncated)
	require.True(t, fileEvidenceFitsEnvelope(response))
	require.NotEmpty(t, response.Warnings)
	require.Len(t, source.Chunks[1].Content, 6000, "original response must stay unchanged")
	chunks, truncated := boundChatEvidence([]repository.AIDocumentChunk{{Text: strings.Repeat("П", 3000) + strings.Repeat("Ф", 3000), Locator: map[string]any{"anchor_offset_runes": 3000}}})
	require.True(t, truncated)
	require.True(t, strings.HasPrefix(chunks[0].Text, "Ф"))
	require.True(t, fileEvidenceFitsEnvelope(chunks))
	require.Equal(t, "drive_content_answer", fileEvidenceWidget(response).Type)
}

// An actual runtime extractor implements the context-aware interface; the test
// makes the legacy method fail loudly so dropping cancellation cannot pass.
type testContextFileExtractor struct {
	cancel context.CancelFunc
	used   bool
}

func (f *testContextFileExtractor) ExtractTextBytes(string, []byte) (string, error) {
	panic("legacy extraction must not discard the job context")
}
func (f *testContextFileExtractor) ExtractTextBytesContext(ctx context.Context, _ string, _ []byte) (string, error) {
	f.used = true
	f.cancel()
	return "", ctx.Err()
}
func TestFileRetrievalPropagatesExtractionCancellationAndPersistsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := uuid.New()
	updated := time.Now()
	index := &testFileIndex{job: &repository.FileIndexJob{FileID: id, SourceUpdatedAt: updated, Token: uuid.New()}}
	access := &testFileAccess{file: &domain.DriveFile{ID: id, UpdatedAt: updated, Scope: "drive", Size: 20, RealName: "test.txt"}}
	svc := fileRetrievalForTest(t, &testFileCorpus{}, access, index)
	extractor := &testContextFileExtractor{cancel: cancel}
	svc.storage = &testFileStorage{body: "text"}
	svc.extractor = extractor
	require.NoError(t, svc.WorkOnce(ctx))
	require.True(t, extractor.used)
	require.False(t, index.saved)
	require.True(t, index.failed)
	require.NoError(t, index.failureContextErr, "cleanup needs a fresh bounded context after extraction cancellation")
}
