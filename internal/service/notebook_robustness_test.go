package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

// notebookFlakyJobs fails selected writes the way PostgreSQL does.
type notebookFlakyJobs struct {
	*notebookMemoryJobs
	// rejectNUL mirrors SQLSTATE 22P05: JSONB cannot store U+0000.
	rejectNUL bool
	// failDone fails every attempt to store a successful result.
	failDone bool
	// extendPanic panics inside the heartbeat after closing panicked.
	extendPanic bool
	panicked    chan struct{}
	finishCalls int
}

func (f *notebookFlakyJobs) FinishJob(ctx context.Context, jobID uuid.UUID, state, stage string, result, jobErr json.RawMessage) (bool, error) {
	f.mu.Lock()
	f.finishCalls++
	f.mu.Unlock()
	if f.rejectNUL && (bytes.Contains(result, []byte(`\u0000`)) || bytes.Contains(jobErr, []byte(`\u0000`))) {
		return false, errors.New("pq: unsupported Unicode escape sequence (22P05)")
	}
	if f.failDone && state == repository.NotebookJobDone {
		return false, errors.New("pq: could not store result")
	}
	return f.notebookMemoryJobs.FinishJob(ctx, jobID, state, stage, result, jobErr)
}

func (f *notebookFlakyJobs) ExtendLease(ctx context.Context, jobID uuid.UUID, lease time.Time) (bool, error) {
	if f.extendPanic {
		close(f.panicked)
		panic("heartbeat store exploded")
	}
	return f.notebookMemoryJobs.ExtendLease(ctx, jobID, lease)
}

func notebookFlakyJobsService(t *testing.T, store *notebookFlakyJobs, texts ...string) (*NotebookResearchJobService, *notebookEventSink, uuid.UUID, []uuid.UUID, *notebookDeepLLM) {
	t.Helper()
	research, chat, ids, llm, _ := notebookDeepService(t, texts...)
	store.notebookMemoryJobs = newNotebookMemoryJobs()
	sink := &notebookEventSink{}
	jobs := NewNotebookResearchJobService(research, store, sink, zap.NewNop())
	t.Cleanup(jobs.Wait)
	return jobs, sink, chat, ids, llm
}

func notebookJobErrorStatus(t *testing.T, raw json.RawMessage) int {
	t.Helper()
	var jobErr NotebookResearchJobError
	if err := json.Unmarshal(raw, &jobErr); err != nil {
		t.Fatalf("stored error %s: %v", raw, err)
	}
	return jobErr.HTTPStatus
}

func TestNotebookResearchJobPanicFailsJobAndKeepsServing(t *testing.T) {
	jobs, store, sink, chat, ids, _, _ := notebookJobsService(t, "Короткий документ: бюджет 100.")
	// Storage was unavailable at startup: a nil *FileService reaches the
	// reader and dereferences nil inside the background job.
	var missing *FileService
	healthy := jobs.research.drive.fileSvc
	jobs.research.drive.fileSvc = missing

	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobs.Wait()
	final := store.state(job.JobID)
	if final.State != repository.NotebookJobFailed || len(final.Result) != 0 || notebookJobErrorStatus(t, final.Error) != http.StatusInternalServerError {
		t.Fatalf("panicking job not failed: %+v %s", final, final.Error)
	}
	if events := sink.of(7); events[len(events)-1].Status != repository.NotebookJobFailed {
		t.Fatalf("failure not announced: %+v", events)
	}

	// The slot and the per-user limit are released: the next job runs.
	jobs.research.drive.fileSvc = healthy
	next, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobs.Wait()
	if got := store.state(next.JobID); got.State != repository.NotebookJobDone {
		t.Fatalf("next job after a panic: %+v", got)
	}
}

func TestNotebookResearchJobHeartbeatPanicFailsJob(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	store := &notebookFlakyJobs{extendPanic: true, panicked: make(chan struct{})}
	jobs, sink, chat, ids, llm := notebookFlakyJobsService(t, store, doc)
	jobs.heartbeat = 10 * time.Millisecond
	base := llm.mapReply
	llm.mapReply = func(prompt string) (string, error) {
		<-store.panicked
		return base(prompt)
	}
	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobs.Wait()
	final := store.state(job.JobID)
	if final.State != repository.NotebookJobFailed || notebookJobErrorStatus(t, final.Error) != http.StatusInternalServerError {
		t.Fatalf("job without heartbeat not failed: %+v %s", final, final.Error)
	}
	if len(llm.finals) != 0 {
		t.Fatal("final answer requested after the heartbeat stopped")
	}
	if events := sink.of(7); events[len(events)-1].Status != repository.NotebookJobFailed {
		t.Fatalf("failure not announced: %+v", events)
	}
}

func TestNotebookDeepResearchCountsPanickingChunkAsFailed(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	s, chat, ids, llm, _ := notebookDeepService(t, doc)
	var once sync.Once
	base := llm.mapReply
	llm.mapReply = func(prompt string) (string, error) {
		once.Do(func() { panic("chunk analysis exploded") })
		return base(prompt)
	}
	// Without recovery the errgroup goroutine panic stops the test binary.
	r, err := s.Ask(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stats == nil || r.Stats.Mode != "deep" || r.Stats.ChunksAnalyzed != r.Stats.ChunksTotal-1 || r.Status != NotebookStatusPartial {
		t.Fatalf("panicking chunk not counted as failed: %+v status=%q", r.Stats, r.Status)
	}
}

func TestNotebookDriveDependenciesIgnoreNilStorage(t *testing.T) {
	// cmd/api passes a nil *FileService when MinIO is unavailable at startup.
	var missing *FileService
	drive := NewDriveSearchService(config.DriveSearchConfig{}, zap.NewNop(), WithDriveIndexDependencies(nil, missing, nil))
	if drive.fileSvc != nil || drive.repo != nil || drive.extractor != nil {
		t.Fatal("nil dependencies stored as non-nil interfaces")
	}
	s, chat, ids, _ := notebookTestService("Бюджет 100")
	s.drive.fileSvc = drive.fileSvc
	_, err := s.Context(context.Background(), 7, chat, NotebookResearchRequest{FileIDs: ids})
	if appErr := apperrors.GetAppError(err); appErr == nil || appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("missing storage must be an error, got %v", err)
	}
	if _, err := s.readText(context.Background(), s.notebooks.(notebookTestReader).files[0]); !errors.Is(err, errNotebookReaderUnavailable) {
		t.Fatalf("readText without storage: %v", err)
	}
}

func TestNotebookLineTextStripsNUL(t *testing.T) {
	// UTF-16LE text decoded as UTF-8 interleaves U+0000 with the characters.
	if got := notebookLineText("B\x00u\x00d\x00g\x00e\x00t\x00\r\n\x00100\x00"); got != "Budget\n100" {
		t.Fatalf("NUL kept: %q", got)
	}
}

func TestNotebookResearchRejectsNULInRequest(t *testing.T) {
	jobs, store, _, chat, ids, _, _ := notebookJobsService(t, "Документ")
	ctx := context.Background()
	for name, req := range map[string]NotebookResearchRequest{
		"question":  {Question: "бюджет\x00", FileIDs: ids},
		"operation": {Question: "бюджет", FileIDs: ids, OperationID: "op\x00"},
		"focus":     {Preset: "cards", Focus: "бюджет\x00", FileIDs: ids},
	} {
		_, _, err := jobs.Create(ctx, 7, chat, req)
		if appErr := apperrors.GetAppError(err); appErr == nil || appErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: async NUL accepted: %v", name, err)
		}
		_, err = jobs.research.Ask(ctx, 7, chat, req)
		if appErr := apperrors.GetAppError(err); appErr == nil || appErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: sync NUL accepted: %v", name, err)
		}
	}
	if len(store.jobs) != 0 {
		t.Fatalf("requests with NUL were queued: %d", len(store.jobs))
	}
}

func TestNotebookResearchJobStoresSourceTextWithNUL(t *testing.T) {
	store := &notebookFlakyJobs{rejectNUL: true}
	jobs, _, chat, ids, _ := notebookFlakyJobsService(t, store, "B\x00u\x00d\x00g\x00e\x00t\x00 100")
	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobs.Wait()
	final := store.state(job.JobID)
	if final.State != repository.NotebookJobDone || !strings.Contains(string(final.Result), "Budget 100") {
		t.Fatalf("result with source text not stored: %+v %s", final, final.Error)
	}
}

func TestNotebookResearchJobFailsWhenResultCannotBeStored(t *testing.T) {
	store := &notebookFlakyJobs{failDone: true}
	jobs, sink, chat, ids, _ := notebookFlakyJobsService(t, store, "Короткий документ: бюджет 100.")
	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobs.Wait()
	// Without the fallback the job stays running (409 for the user) until
	// the sweeper interrupts it after the lease.
	final := store.state(job.JobID)
	if final.State != repository.NotebookJobFailed || len(final.Result) != 0 || notebookJobErrorStatus(t, final.Error) != http.StatusInternalServerError {
		t.Fatalf("unstorable result left the job unfinished: %+v %s", final, final.Error)
	}
	if store.finishCalls != 2 {
		t.Fatalf("expected one retry, got %d writes", store.finishCalls)
	}
	events := sink.of(7)
	if last := events[len(events)-1]; last.Status != repository.NotebookJobFailed || last.Error == nil {
		t.Fatalf("failure not announced: %+v", last)
	}
}

func TestNotebookTextCacheSharedLoadSurvivesCallerCancellation(t *testing.T) {
	c := newNotebookTextCache(notebookTextCacheBytes, notebookTextCacheTTL)
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	extract := func(ctx context.Context) (string, error) {
		calls++
		close(started)
		select {
		case <-release:
			return "text", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	first, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := c.load(first, "k", extract)
		firstErr <- err
	}()
	<-started
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: %v", err)
	}
	// The extraction is still in flight; a concurrent request joins it.
	second := make(chan string, 1)
	go func() {
		text, err := c.load(context.Background(), "k", extract)
		if err != nil {
			text = err.Error()
		}
		second <- text
	}()
	close(release)
	if got := <-second; got != "text" || calls != 1 {
		t.Fatalf("shared load after cancellation: %q, %d extractions", got, calls)
	}

	if _, err := c.load(context.Background(), "p", func(context.Context) (string, error) { panic("parser exploded") }); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("extraction panic: %v", err)
	}
}
