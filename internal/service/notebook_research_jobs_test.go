package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

// notebookMemoryJobs mirrors the SQL of NotebookResearchJobRepository.
type notebookMemoryJobs struct {
	mu       sync.Mutex
	jobs     map[uuid.UUID]*repository.NotebookResearchJob
	progress []NotebookResearchProgress
}

func newNotebookMemoryJobs() *notebookMemoryJobs {
	return &notebookMemoryJobs{jobs: map[uuid.UUID]*repository.NotebookResearchJob{}}
}

func notebookJobActive(state string) bool {
	return state == repository.NotebookJobQueued || state == repository.NotebookJobRunning
}

func (m *notebookMemoryJobs) CreateJob(_ context.Context, job repository.NotebookResearchJob) (*repository.NotebookResearchJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var active *repository.NotebookResearchJob
	for _, j := range m.jobs {
		if j.UserID == job.UserID && j.OperationID == job.OperationID {
			copy := *j
			return &copy, false, nil
		}
		if j.UserID == job.UserID && notebookJobActive(j.State) {
			active = j
		}
	}
	if active != nil {
		copy := *active
		return &copy, false, nil
	}
	now := time.Now()
	job.State, job.Stage, job.CreatedAt, job.UpdatedAt = repository.NotebookJobQueued, NotebookStageQueued, now, now
	stored := job
	m.jobs[job.ID] = &stored
	return &job, true, nil
}

func (m *notebookMemoryJobs) GetJob(_ context.Context, userID int64, chatID, jobID uuid.UUID) (*repository.NotebookResearchJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil || j.UserID != userID || j.ChatID != chatID {
		return nil, nil
	}
	copy := *j
	return &copy, nil
}

func (m *notebookMemoryJobs) StartJob(_ context.Context, jobID uuid.UUID, lease time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil || j.State != repository.NotebookJobQueued {
		return false, nil
	}
	now := time.Now()
	j.State, j.Stage, j.StartedAt, j.LeaseUntil = repository.NotebookJobRunning, NotebookStageReading, &now, lease
	return true, nil
}

func (m *notebookMemoryJobs) UpdateProgress(_ context.Context, jobID uuid.UUID, stage string, done, total int, lease time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil || j.State != repository.NotebookJobRunning {
		return false, nil
	}
	j.Stage, j.ChunksDone, j.ChunksTotal, j.LeaseUntil = stage, done, total, lease
	m.progress = append(m.progress, NotebookResearchProgress{Stage: stage, ChunksDone: done, ChunksTotal: total})
	return true, nil
}

func (m *notebookMemoryJobs) ExtendLease(_ context.Context, jobID uuid.UUID, lease time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil || !notebookJobActive(j.State) {
		return false, nil
	}
	j.LeaseUntil = lease
	return true, nil
}

func (m *notebookMemoryJobs) FinishJob(_ context.Context, jobID uuid.UUID, state, stage string, result, jobErr json.RawMessage) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil || !notebookJobActive(j.State) {
		return false, nil
	}
	now := time.Now()
	j.State, j.Stage, j.Result, j.Error, j.FinishedAt = state, stage, result, jobErr, &now
	return true, nil
}

func (m *notebookMemoryJobs) CancelJob(_ context.Context, userID int64, chatID, jobID uuid.UUID) (*repository.NotebookResearchJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	if j == nil || j.UserID != userID || j.ChatID != chatID {
		return nil, false, nil
	}
	cancelled := false
	if notebookJobActive(j.State) {
		now := time.Now()
		j.State, j.FinishedAt, cancelled = repository.NotebookJobCancelled, &now, true
	}
	copy := *j
	return &copy, cancelled, nil
}

func (m *notebookMemoryJobs) FailExpired(_ context.Context, now time.Time, jobErr json.RawMessage) ([]repository.NotebookResearchJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failed []repository.NotebookResearchJob
	for _, j := range m.jobs {
		if notebookJobActive(j.State) && j.LeaseUntil.Before(now) {
			j.State, j.Error, j.FinishedAt = repository.NotebookJobFailed, jobErr, &now
			failed = append(failed, *j)
		}
	}
	return failed, nil
}

func (m *notebookMemoryJobs) DeleteFinishedBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (m *notebookMemoryJobs) state(id uuid.UUID) repository.NotebookResearchJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.jobs[id]
}

type notebookEventSink struct {
	mu     sync.Mutex
	events map[int64][]NotebookResearchJobEvent
}

func (n *notebookEventSink) SendToUser(userID int64, message *websocket.Message) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	if message.Type != NotebookResearchProgressEvent || message.To != userID {
		panic("unexpected websocket message " + message.Type)
	}
	if n.events == nil {
		n.events = map[int64][]NotebookResearchJobEvent{}
	}
	n.events[userID] = append(n.events[userID], message.Content.(NotebookResearchJobEvent))
	return 1
}

func (n *notebookEventSink) of(userID int64) []NotebookResearchJobEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]NotebookResearchJobEvent(nil), n.events[userID]...)
}

func notebookJobsService(t *testing.T, texts ...string) (*NotebookResearchJobService, *notebookMemoryJobs, *notebookEventSink, uuid.UUID, []uuid.UUID, *notebookDeepLLM, *notebookLockedStore) {
	t.Helper()
	research, chat, ids, llm, accounting := notebookDeepService(t, texts...)
	store, sink := newNotebookMemoryJobs(), &notebookEventSink{}
	jobs := NewNotebookResearchJobService(research, store, sink, zap.NewNop())
	t.Cleanup(jobs.Wait)
	return jobs, store, sink, chat, ids, llm, accounting
}

func TestNotebookResearchJobRunsDeepResearchWithProgress(t *testing.T) {
	doc, fact := notebookBigDocument(4000)
	jobs, store, sink, chat, ids, llm, accounting := notebookJobsService(t, doc)
	job, created, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "Какой бюджет проекта?", FileIDs: ids, OperationID: "op-async"})
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if job.Status != repository.NotebookJobQueued || job.OperationID != "op-async" || job.Result != nil {
		t.Fatalf("unexpected new job %+v", job)
	}
	jobs.Wait()

	got, err := jobs.Get(context.Background(), 7, chat, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != repository.NotebookJobDone || got.Progress.Stage != NotebookStageDone || got.Error != nil || got.Result == nil {
		t.Fatalf("job not done: %+v", got)
	}
	r := got.Result
	if r.Status != NotebookStatusAnswered || r.Stats == nil || r.Stats.Mode != "deep" || !strings.Contains(r.Content, "start_line=") {
		t.Fatalf("unexpected result %+v", r)
	}
	if !strings.Contains(r.Content, "777000") || len(llm.finals) != 1 {
		t.Fatalf("result lacks the fact at L%d: %s", fact, r.Content)
	}
	if got.Progress.ChunksTotal != len(llm.maps) || got.Progress.ChunksDone != got.Progress.ChunksTotal {
		t.Fatalf("progress %+v for %d map calls", got.Progress, len(llm.maps))
	}
	// Every part is reported once and in order, then the answer stage.
	analyzed := []int{}
	for _, p := range store.progress {
		if p.Stage == NotebookStageAnalyzing {
			analyzed = append(analyzed, p.ChunksDone)
		}
	}
	for i, done := range analyzed {
		if done != i {
			t.Fatalf("analysis progress not monotonic: %v", analyzed)
		}
	}
	if len(analyzed) != len(llm.maps)+1 || store.progress[len(store.progress)-1].Stage != NotebookStageAnswering {
		t.Fatalf("unexpected progress %+v", store.progress)
	}
	// The accounting keys are those of the synchronous route.
	if accounting.keys[len(accounting.keys)-1] != "notebook:op-async:1" {
		t.Fatalf("accounting keys %v", accounting.keys)
	}
	events := sink.of(7)
	if len(events) < 3 || events[0].Status != repository.NotebookJobRunning || events[len(events)-1].Status != repository.NotebookJobDone {
		t.Fatalf("unexpected events %+v", events)
	}
	for _, e := range events {
		if e.JobID != job.JobID || e.ChatID != chat {
			t.Fatalf("event for another job: %+v", e)
		}
	}
	if len(sink.of(8)) != 0 {
		t.Fatal("events leaked to another user")
	}
}

func TestNotebookResearchJobFailureKeepsSynchronousErrorContract(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	jobs, _, sink, chat, ids, _, accounting := notebookJobsService(t, doc)
	accounting.deny = func(string) error {
		return &domain.AIReservationDenied{Reason: "COMPANY_WEEKLY_EXHAUSTED", Scope: "company"}
	}
	var rendered error
	jobs.SetErrorRenderer(func(_ context.Context, err error) NotebookResearchJobError {
		rendered = err
		return NotebookResearchJobError{HTTPStatus: 429, Code: "AI_CREDIT_LIMIT", Message: "limit"}
	})
	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobs.Wait()
	got, err := jobs.Get(context.Background(), 7, chat, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var denied *domain.AIReservationDenied
	if !errors.As(rendered, &denied) {
		t.Fatalf("renderer got %v", rendered)
	}
	if got.Status != repository.NotebookJobFailed || got.Result != nil || got.Error == nil || got.Error.Code != "AI_CREDIT_LIMIT" || got.Error.HTTPStatus != 429 {
		t.Fatalf("unexpected failed job %+v", got)
	}
	if got.Progress.Stage != NotebookStageAnalyzing {
		t.Fatalf("failed stage %q", got.Progress.Stage)
	}
	events := sink.of(7)
	last := events[len(events)-1]
	if last.Status != repository.NotebookJobFailed || last.Error == nil || last.Error.Code != "AI_CREDIT_LIMIT" {
		t.Fatalf("unexpected final event %+v", last)
	}
}

func TestNotebookResearchJobCancelStopsAnalysis(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	jobs, store, sink, chat, ids, llm, _ := notebookJobsService(t, doc)
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	base := llm.mapReply
	llm.mapReply = func(prompt string) (string, error) {
		entered <- struct{}{}
		<-release
		return base(prompt)
	}
	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := jobs.Cancel(context.Background(), 8, chat, job.JobID); !notebookIsNotFound(err) {
		t.Fatalf("another user cancelled the job: %v", err)
	}
	cancelled, err := jobs.Cancel(context.Background(), 7, chat, job.JobID)
	if err != nil || cancelled.Status != repository.NotebookJobCancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	close(release)
	jobs.Wait()
	final := store.state(job.JobID)
	if final.State != repository.NotebookJobCancelled || len(final.Result) != 0 || len(final.Error) != 0 {
		t.Fatalf("cancelled job overwritten: %+v", final)
	}
	llm.mu.Lock()
	maps, finals := len(llm.maps), len(llm.finals)
	llm.mu.Unlock()
	if finals != 0 || maps >= 8 {
		t.Fatalf("analysis continued after cancel: %d map, %d final calls", maps, finals)
	}
	// Cancelling again changes nothing and announces nothing new.
	before := len(sink.of(7))
	again, err := jobs.Cancel(context.Background(), 7, chat, job.JobID)
	if err != nil || again.Status != repository.NotebookJobCancelled || len(sink.of(7)) != before {
		t.Fatalf("repeated cancel: %+v %v", again, err)
	}
	if last := sink.of(7)[before-1]; last.Status != repository.NotebookJobCancelled {
		t.Fatalf("last event %+v", last)
	}
}

func TestNotebookResearchJobCancelledElsewhereStopsOnProgress(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	jobs, store, _, chat, ids, llm, _ := notebookJobsService(t, doc)
	var once sync.Once
	var jobID uuid.UUID
	ready := make(chan struct{})
	base := llm.mapReply
	llm.mapReply = func(prompt string) (string, error) {
		<-ready
		// Another instance cancels the row; this one has no local handle.
		once.Do(func() {
			store.mu.Lock()
			store.jobs[jobID].State = repository.NotebookJobCancelled
			store.mu.Unlock()
		})
		return base(prompt)
	}
	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobID = job.JobID
	close(ready)
	jobs.Wait()
	if final := store.state(job.JobID); final.State != repository.NotebookJobCancelled || len(final.Result) != 0 {
		t.Fatalf("job finished despite cancellation: %+v", final)
	}
	if len(llm.finals) != 0 {
		t.Fatal("final answer requested after cancellation")
	}
}

func TestNotebookResearchJobIsolationAndIdempotency(t *testing.T) {
	jobs, _, _, chat, ids, _, _ := notebookJobsService(t, "Короткий документ: бюджет 100.")
	ctx := context.Background()
	job, created, err := jobs.Create(ctx, 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids, OperationID: "op-1"})
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	// The same operation returns the same job instead of a second paid run.
	again, created, err := jobs.Create(ctx, 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids, OperationID: "op-1"})
	if err != nil || created || again.JobID != job.JobID {
		t.Fatalf("repeated operation: %+v %v %v", again, created, err)
	}
	jobs.Wait()
	for _, tc := range []struct {
		user int64
		chat uuid.UUID
	}{{8, chat}, {7, uuid.New()}} {
		if _, err := jobs.Get(ctx, tc.user, tc.chat, job.JobID); !notebookIsNotFound(err) {
			t.Fatalf("job visible to user %d in chat %s: %v", tc.user, tc.chat, err)
		}
	}
	// Another user cannot start a job in this notebook.
	if _, _, err := jobs.Create(ctx, 8, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids}); err == nil {
		t.Fatal("foreign notebook accepted")
	}
	got, err := jobs.Get(ctx, 7, chat, job.JobID)
	if err != nil || got.Status != repository.NotebookJobDone || got.Result.Stats.Mode != "single" {
		t.Fatalf("owner view: %+v %v", got, err)
	}
}

func TestNotebookResearchJobRejectsInvalidRequestsBeforeQueueing(t *testing.T) {
	jobs, store, _, chat, ids, _, _ := notebookJobsService(t, "Документ")
	ctx := context.Background()
	for name, req := range map[string]NotebookResearchRequest{
		"empty question":   {FileIDs: ids},
		"bad format":       {Question: "q", Format: "poem", FileIDs: ids},
		"compare one file": {Question: "q", Format: "compare", FileIDs: ids},
		"unattached file":  {Question: "q", FileIDs: []uuid.UUID{uuid.New()}},
		"long operation":   {Question: "q", FileIDs: ids, OperationID: strings.Repeat("x", 129)},
	} {
		if _, _, err := jobs.Create(ctx, 7, chat, req); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if len(store.jobs) != 0 {
		t.Fatalf("invalid requests were queued: %d", len(store.jobs))
	}
}

func TestNotebookResearchJobAllowsOneUnfinishedJobPerUser(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	jobs, _, _, chat, ids, llm, _ := notebookJobsService(t, doc)
	release := make(chan struct{})
	base := llm.mapReply
	llm.mapReply = func(prompt string) (string, error) {
		<-release
		return base(prompt)
	}
	first, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "другой вопрос", FileIDs: ids})
	appErr := apperrors.GetAppError(err)
	if appErr == nil || appErr.Code != apperrors.ErrCodeConflict || appErr.Details["job_id"] != first.JobID.String() {
		t.Fatalf("expected conflict naming the active job, got %v", err)
	}
	close(release)
}

func TestNotebookResearchJobShutdownInterruptsAndSweeperFailsExpired(t *testing.T) {
	doc, _ := notebookBigDocument(4000)
	jobs, store, sink, chat, ids, llm, _ := notebookJobsService(t, doc)
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	base := llm.mapReply
	llm.mapReply = func(prompt string) (string, error) {
		entered <- struct{}{}
		<-release
		return base(prompt)
	}
	job, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	jobs.stop() // what Start does when the server context ends
	close(release)
	jobs.Wait()
	final := store.state(job.JobID)
	if final.State != repository.NotebookJobFailed || !strings.Contains(string(final.Error), NotebookJobErrorInterrupted) {
		t.Fatalf("shutdown did not interrupt the job: %+v %s", final, final.Error)
	}
	if events := sink.of(7); events[len(events)-1].Error == nil || events[len(events)-1].Error.Code != NotebookJobErrorInterrupted {
		t.Fatalf("interrupted job not announced: %+v", events)
	}
	// A job accepted during shutdown is interrupted before any model call.
	maps := len(llm.maps)
	late, _, err := jobs.Create(context.Background(), 7, chat, NotebookResearchRequest{Question: "бюджет", FileIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	jobs.Wait()
	if got := store.state(late.JobID); got.State != repository.NotebookJobFailed || len(llm.maps) != maps {
		t.Fatalf("job ran during shutdown: %+v", got)
	}

	// A job left by a stopped process is failed once its lease expires.
	orphan := uuid.New()
	store.mu.Lock()
	store.jobs[orphan] = &repository.NotebookResearchJob{ID: orphan, UserID: 9, ChatID: chat, OperationID: "orphan", State: repository.NotebookJobRunning, LeaseUntil: time.Now().Add(-time.Second)}
	store.mu.Unlock()
	jobs.sweep(context.Background())
	if got := store.state(orphan); got.State != repository.NotebookJobFailed {
		t.Fatalf("orphan not failed: %+v", got)
	}
	if events := sink.of(9); len(events) != 1 || events[0].Error == nil || events[0].Error.Code != NotebookJobErrorInterrupted {
		t.Fatalf("orphan events %+v", events)
	}
}

func TestNotebookAskReportsNoProgressWithoutCallback(t *testing.T) {
	// The synchronous route keeps working without a callback (nil-safe).
	var p notebookProgressFunc
	p.report(NotebookStageReading, 0, 0)
}

func notebookIsNotFound(err error) bool {
	appErr := apperrors.GetAppError(err)
	return appErr != nil && appErr.Code == apperrors.ErrCodeNotFound
}
