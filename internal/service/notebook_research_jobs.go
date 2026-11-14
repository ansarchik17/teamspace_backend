package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

// Stages of a research request, reported through NotebookResearchProgress.
const (
	NotebookStageQueued    = "queued"
	NotebookStageReading   = "reading"
	NotebookStageAnalyzing = "analyzing"
	NotebookStageAnswering = "answering"
	NotebookStageVerifying = "verifying"
	NotebookStageDone      = "done"
)

// NotebookResearchProgressEvent is the WebSocket message type sent to the
// job owner on every status or progress change.
const NotebookResearchProgressEvent = "notebook.research.progress"

const (
	notebookJobLease     = 2 * time.Minute
	notebookJobHeartbeat = 30 * time.Second
	// Covers the longest synchronous path (thinking: 210 s) plus queueing for
	// a slot and the map phase without a request deadline.
	notebookJobTimeout   = 6 * time.Minute
	notebookJobRetention = 7 * 24 * time.Hour
	notebookJobSweep     = time.Minute
	// Jobs executing at once on one instance; the rest wait as queued.
	notebookJobSlots    = 4
	notebookJobDBWrite  = 10 * time.Second
	notebookOperationID = 128

	NotebookJobErrorInterrupted = "NOTEBOOK_JOB_INTERRUPTED"
)

var (
	errNotebookJobCancelled   = errors.New("notebook research job cancelled")
	errNotebookJobInterrupted = errors.New("notebook research job interrupted")
	errNotebookJobPanicked    = errors.New("notebook research job panicked")
)

// NotebookResearchProgress is reported while a research request runs. Calls
// are serialized; within the analyzing stage ChunksDone never decreases, and
// later stages repeat the last counts. ChunksTotal counts the parts scheduled
// for analysis (at most notebookMaxChunks).
type NotebookResearchProgress struct {
	Stage       string `json:"stage"`
	ChunksDone  int    `json:"chunks_done"`
	ChunksTotal int    `json:"chunks_total"`
}

type notebookProgressFunc func(NotebookResearchProgress)

func (p notebookProgressFunc) report(stage string, done, total int) {
	if p != nil {
		p(NotebookResearchProgress{Stage: stage, ChunksDone: done, ChunksTotal: total})
	}
}

// NotebookResearchJobError is what the synchronous /ask would have answered:
// HTTPStatus and Body are its status and JSON body, Code and Message are
// copied from the body for convenience.
type NotebookResearchJobError struct {
	HTTPStatus int             `json:"http_status"`
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	Body       json.RawMessage `json:"body,omitempty" swaggertype:"object"`
}

// NotebookResearchJob is the public view of an asynchronous research request.
type NotebookResearchJob struct {
	JobID       uuid.UUID                 `json:"job_id"`
	ChatID      uuid.UUID                 `json:"chat_id"`
	OperationID string                    `json:"operation_id"`
	Status      string                    `json:"status"`
	Progress    NotebookResearchProgress  `json:"progress"`
	Result      *NotebookResearchResponse `json:"result,omitempty"`
	Error       *NotebookResearchJobError `json:"error,omitempty"`
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
	StartedAt   *time.Time                `json:"started_at,omitempty"`
	FinishedAt  *time.Time                `json:"finished_at,omitempty"`
}

// NotebookResearchJobEvent is the content of a NotebookResearchProgressEvent.
// It never carries the result: on status "done" the client fetches the job.
type NotebookResearchJobEvent struct {
	JobID       uuid.UUID                 `json:"job_id"`
	ChatID      uuid.UUID                 `json:"chat_id"`
	OperationID string                    `json:"operation_id"`
	Status      string                    `json:"status"`
	Progress    NotebookResearchProgress  `json:"progress"`
	Error       *NotebookResearchJobError `json:"error,omitempty"`
}

// NotebookJobErrorRenderer converts an execution error into the response the
// synchronous route would give. ctx carries the request's locale.
type NotebookJobErrorRenderer func(ctx context.Context, err error) NotebookResearchJobError

type notebookJobStore interface {
	CreateJob(context.Context, repository.NotebookResearchJob) (*repository.NotebookResearchJob, bool, error)
	GetJob(context.Context, int64, uuid.UUID, uuid.UUID) (*repository.NotebookResearchJob, error)
	StartJob(context.Context, uuid.UUID, time.Time) (bool, error)
	UpdateProgress(context.Context, uuid.UUID, string, int, int, time.Time) (bool, error)
	ExtendLease(context.Context, uuid.UUID, time.Time) (bool, error)
	FinishJob(context.Context, uuid.UUID, string, string, json.RawMessage, json.RawMessage) (bool, error)
	CancelJob(context.Context, int64, uuid.UUID, uuid.UUID) (*repository.NotebookResearchJob, bool, error)
	FailExpired(context.Context, time.Time, json.RawMessage) ([]repository.NotebookResearchJob, error)
	DeleteFinishedBefore(context.Context, time.Time) (int64, error)
}

type notebookJobNotifier interface {
	SendToUser(int64, *websocket.Message) int
}

// NotebookResearchJobService runs NotebookResearchService.Ask in the
// background. A job is executed by the instance that accepted it, under the
// paid context verified for the requesting user; the database row is the
// shared state for polling, cancellation and crash detection.
type NotebookResearchJobService struct {
	research    *NotebookResearchService
	store       notebookJobStore
	notifier    notebookJobNotifier
	renderError NotebookJobErrorRenderer
	logger      *zap.Logger
	now         func() time.Time
	slots       chan struct{}
	lease       time.Duration
	heartbeat   time.Duration
	timeout     time.Duration

	mu        sync.Mutex
	running   map[uuid.UUID]context.CancelCauseFunc
	stopping  bool
	wg        sync.WaitGroup
	startOnce sync.Once
}

func NewNotebookResearchJobService(research *NotebookResearchService, store notebookJobStore, notifier notebookJobNotifier, logger *zap.Logger) *NotebookResearchJobService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &NotebookResearchJobService{
		research: research, store: store, notifier: notifier, logger: logger.With(zap.String("service", "notebook_research_jobs")),
		now: time.Now, slots: make(chan struct{}, notebookJobSlots), lease: notebookJobLease, heartbeat: notebookJobHeartbeat,
		timeout: notebookJobTimeout, running: map[uuid.UUID]context.CancelCauseFunc{},
	}
}

// SetErrorRenderer must be called at startup, before serving requests.
func (s *NotebookResearchJobService) SetErrorRenderer(render NotebookJobErrorRenderer) {
	s.renderError = render
}

// Create validates the request like Ask does before any model call (shape,
// payer, notebook and source access), stores a queued job and starts it. A
// repeated operation_id returns the existing job with created=false.
func (s *NotebookResearchJobService) Create(ctx context.Context, userID int64, chatID uuid.UUID, req NotebookResearchRequest) (*NotebookResearchJob, bool, error) {
	req.OperationID = strings.TrimSpace(req.OperationID)
	if len(req.OperationID) > notebookOperationID {
		return nil, false, apperrors.Validation("operation_id is too long")
	}
	paidCtx, err := s.research.validateAsk(ctx, userID, chatID, req)
	if err != nil {
		return nil, false, err
	}
	jobID := uuid.New()
	if req.OperationID == "" {
		req.OperationID = jobID.String()
	}
	request, err := json.Marshal(req)
	if err != nil {
		return nil, false, err
	}
	job, inserted, err := s.store.CreateJob(ctx, repository.NotebookResearchJob{
		ID: jobID, UserID: userID, ChatID: chatID, OperationID: req.OperationID, Request: request, LeaseUntil: s.now().Add(s.lease),
	})
	if err != nil {
		return nil, false, err
	}
	if !inserted {
		if job != nil && job.OperationID == req.OperationID && job.ChatID == chatID {
			return notebookJobView(job), false, nil
		}
		if job != nil && job.OperationID == req.OperationID {
			return nil, false, apperrors.Conflict("operation_id уже использован в другом блокноте")
		}
		conflict := apperrors.Conflict("Глубокий анализ уже выполняется. Дождитесь результата или отмените его.")
		if job != nil {
			conflict = conflict.WithDetail("job_id", job.ID.String()).WithDetail("chat_id", job.ChatID.String())
		}
		return nil, false, conflict
	}
	s.launch(paidCtx, job, req)
	return notebookJobView(job), true, nil
}

// Get returns a job of this user and notebook; other users' jobs are not found.
func (s *NotebookResearchJobService) Get(ctx context.Context, userID int64, chatID, jobID uuid.UUID) (*NotebookResearchJob, error) {
	job, err := s.store.GetJob(ctx, userID, chatID, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, apperrors.NotFound("research job")
	}
	return notebookJobView(job), nil
}

// Cancel stops an unfinished job and returns its state. A finished job is
// returned unchanged. Paid steps already completed stay charged.
func (s *NotebookResearchJobService) Cancel(ctx context.Context, userID int64, chatID, jobID uuid.UUID) (*NotebookResearchJob, error) {
	job, cancelled, err := s.store.CancelJob(ctx, userID, chatID, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, apperrors.NotFound("research job")
	}
	if cancelled {
		// A job running on another instance notices the state on its next
		// heartbeat or progress update.
		s.mu.Lock()
		stop := s.running[jobID]
		s.mu.Unlock()
		if stop != nil {
			stop(errNotebookJobCancelled)
		}
		s.publish(job.UserID, notebookJobEvent(job))
	}
	return notebookJobView(job), nil
}

// Start runs the sweeper that fails jobs abandoned by a stopped process and
// removes old results. When ctx ends, jobs of this instance are interrupted.
func (s *NotebookResearchJobService) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(notebookJobSweep)
			defer ticker.Stop()
			for {
				s.sweep(ctx)
				select {
				case <-ctx.Done():
					s.stop()
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

// Wait blocks until every job started by this instance has finished.
func (s *NotebookResearchJobService) Wait() { s.wg.Wait() }

func (s *NotebookResearchJobService) stop() {
	s.mu.Lock()
	s.stopping = true
	stops := make([]context.CancelCauseFunc, 0, len(s.running))
	for _, stop := range s.running {
		stops = append(stops, stop)
	}
	s.mu.Unlock()
	for _, stop := range stops {
		stop(errNotebookJobInterrupted)
	}
}

func (s *NotebookResearchJobService) sweep(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("notebook research job sweep panicked", zap.Any("panic", r), zap.Stack("stack"))
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, notebookJobDBWrite)
	defer cancel()
	interrupted, _ := json.Marshal(notebookInterruptedError())
	expired, err := s.store.FailExpired(ctx, s.now(), interrupted)
	if err != nil {
		s.logger.Warn("failing expired notebook research jobs", zap.Error(err))
	}
	for i := range expired {
		s.publish(expired[i].UserID, notebookJobEvent(&expired[i]))
	}
	if _, err := s.store.DeleteFinishedBefore(ctx, s.now().Add(-notebookJobRetention)); err != nil {
		s.logger.Warn("deleting old notebook research jobs", zap.Error(err))
	}
}

func (s *NotebookResearchJobService) launch(paidCtx context.Context, job *repository.NotebookResearchJob, req NotebookResearchRequest) {
	// The job outlives the HTTP request. WithoutCancel keeps the verified
	// paying user and company (and the locale) and drops the request's
	// cancellation; the job is stopped only by Cancel, timeout or shutdown.
	ctx, stop := context.WithCancelCause(context.WithoutCancel(paidCtx))
	s.mu.Lock()
	s.running[job.ID] = stop
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		stop(errNotebookJobInterrupted)
	}
	s.wg.Add(2)
	go s.keepAlive(ctx, stop, job.ID)
	go s.run(ctx, stop, *job, req)
}

// keepAlive extends the lease while the job waits or runs, and stops the job
// once it is no longer unfinished in the database (cancelled elsewhere).
func (s *NotebookResearchJobService) keepAlive(ctx context.Context, stop context.CancelCauseFunc, jobID uuid.UUID) {
	defer s.wg.Done()
	// Without heartbeats the job cannot be tracked; run stores it as failed.
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("notebook research job heartbeat panicked", zap.String("job_id", jobID.String()), zap.Any("panic", r), zap.Stack("stack"))
			stop(errNotebookJobPanicked)
		}
	}()
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notebookJobDBWrite)
		active, err := s.store.ExtendLease(dbCtx, jobID, s.now().Add(s.lease))
		cancel()
		if err != nil {
			s.logger.Warn("extending notebook research job lease", zap.String("job_id", jobID.String()), zap.Error(err))
		} else if !active {
			stop(errNotebookJobCancelled)
			return
		}
	}
}

func (s *NotebookResearchJobService) run(ctx context.Context, stop context.CancelCauseFunc, job repository.NotebookResearchJob, req NotebookResearchRequest) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.running, job.ID)
		s.mu.Unlock()
		stop(nil)
	}()
	db := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), notebookJobDBWrite)
	}
	event := notebookJobEvent(&job)
	defer s.recoverJob(ctx, job.UserID, &event)

	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		s.finish(ctx, job.UserID, &event, nil, context.Cause(ctx))
		return
	}
	dbCtx, cancel := db()
	started, err := s.store.StartJob(dbCtx, job.ID, s.now().Add(s.lease))
	cancel()
	if err != nil {
		s.finish(ctx, job.UserID, &event, nil, err)
		return
	}
	if !started {
		return // cancelled or expired while queued
	}
	event.Status = repository.NotebookJobRunning
	event.Progress = NotebookResearchProgress{Stage: NotebookStageReading}
	s.publish(job.UserID, event)

	progress := func(p NotebookResearchProgress) {
		dbCtx, cancel := db()
		active, err := s.store.UpdateProgress(dbCtx, job.ID, p.Stage, p.ChunksDone, p.ChunksTotal, s.now().Add(s.lease))
		cancel()
		if err != nil {
			s.logger.Warn("updating notebook research job progress", zap.String("job_id", job.ID.String()), zap.Error(err))
		} else if !active {
			stop(errNotebookJobCancelled)
			return
		}
		event.Progress = p
		s.publish(job.UserID, event)
	}
	runCtx, cancelRun := context.WithTimeout(ctx, s.timeout)
	defer cancelRun()
	result, err := s.research.ask(runCtx, job.UserID, job.ChatID, req, progress)
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}
	s.finish(ctx, job.UserID, &event, result, err)
}

// recoverJob stores a panicking job as failed. The HTTP recovery middleware
// does not cover background goroutines, so a panic would stop the process.
func (s *NotebookResearchJobService) recoverJob(ctx context.Context, userID int64, event *NotebookResearchJobEvent) {
	r := recover()
	if r == nil {
		return
	}
	s.logger.Error("notebook research job panicked", zap.String("job_id", event.JobID.String()), zap.Any("panic", r), zap.Stack("stack"))
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("storing panicked notebook research job", zap.String("job_id", event.JobID.String()), zap.Any("panic", r))
		}
	}()
	s.finish(ctx, userID, event, nil, errNotebookJobPanicked)
}

// finish stores the outcome unless the job was cancelled; Cancel has already
// stored and announced that state.
func (s *NotebookResearchJobService) finish(ctx context.Context, userID int64, event *NotebookResearchJobEvent, result *NotebookResearchResponse, err error) {
	if errors.Is(err, errNotebookJobCancelled) {
		return
	}
	state, stage := repository.NotebookJobDone, NotebookStageDone
	var payload, failure json.RawMessage
	if err == nil {
		payload, err = json.Marshal(result)
	}
	if err != nil {
		state, stage = repository.NotebookJobFailed, event.Progress.Stage
		jobErr := s.jobError(ctx, err)
		event.Error = &jobErr
		failure, _ = json.Marshal(jobErr)
		payload = nil
		if !errors.Is(err, errNotebookJobInterrupted) {
			s.logger.Warn("notebook research job failed", zap.String("job_id", event.JobID.String()), zap.Error(err))
		}
	}
	stored, storeErr := s.storeOutcome(ctx, event.JobID, state, stage, payload, failure)
	if storeErr != nil {
		// The result or error may be unstorable (e.g. invalid JSONB); the user
		// must not wait for the lease to expire, so store a plain failure.
		s.logger.Error("storing notebook research job outcome", zap.String("job_id", event.JobID.String()), zap.Error(storeErr))
		state, stage = repository.NotebookJobFailed, event.Progress.Stage
		jobErr := s.jobError(ctx, fmt.Errorf("storing notebook research job outcome: %w", storeErr))
		event.Error = &jobErr
		failure, _ = json.Marshal(jobErr)
		stored, storeErr = s.storeOutcome(ctx, event.JobID, state, stage, nil, failure)
		if storeErr != nil {
			// The lease then expires and the sweeper fails the job.
			s.logger.Error("storing notebook research job failure", zap.String("job_id", event.JobID.String()), zap.Error(storeErr))
			return
		}
	}
	if !stored {
		return
	}
	event.Status = state
	event.Progress.Stage = stage
	s.publish(userID, *event)
}

func (s *NotebookResearchJobService) storeOutcome(ctx context.Context, jobID uuid.UUID, state, stage string, payload, failure json.RawMessage) (bool, error) {
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notebookJobDBWrite)
	defer cancel()
	return s.store.FinishJob(dbCtx, jobID, state, stage, payload, failure)
}

func (s *NotebookResearchJobService) jobError(ctx context.Context, err error) NotebookResearchJobError {
	if errors.Is(err, errNotebookJobInterrupted) {
		return notebookInterruptedError()
	}
	if s.renderError != nil {
		return s.renderError(ctx, err)
	}
	appErr := apperrors.ToAppError(err)
	return NotebookResearchJobError{HTTPStatus: appErr.StatusCode, Code: string(appErr.Code), Message: appErr.Message}
}

func notebookInterruptedError() NotebookResearchJobError {
	return NotebookResearchJobError{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       NotebookJobErrorInterrupted,
		Message:    "Анализ прерван: сервер перезапускался. Запустите его снова.",
	}
}

func (s *NotebookResearchJobService) publish(userID int64, event NotebookResearchJobEvent) {
	if s.notifier == nil || userID <= 0 {
		return
	}
	s.notifier.SendToUser(userID, &websocket.Message{
		Type: NotebookResearchProgressEvent, To: userID, Category: "notebook", IsTransient: true,
		Content: event, Timestamp: s.now().Unix(),
	})
}

func notebookJobEvent(job *repository.NotebookResearchJob) NotebookResearchJobEvent {
	view := notebookJobView(job)
	return NotebookResearchJobEvent{JobID: view.JobID, ChatID: view.ChatID, OperationID: view.OperationID, Status: view.Status, Progress: view.Progress, Error: view.Error}
}

func notebookJobView(job *repository.NotebookResearchJob) *NotebookResearchJob {
	view := &NotebookResearchJob{
		JobID: job.ID, ChatID: job.ChatID, OperationID: job.OperationID, Status: job.State,
		Progress:  NotebookResearchProgress{Stage: job.Stage, ChunksDone: job.ChunksDone, ChunksTotal: job.ChunksTotal},
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, StartedAt: job.StartedAt, FinishedAt: job.FinishedAt,
	}
	if len(job.Result) > 0 {
		var result NotebookResearchResponse
		if json.Unmarshal(job.Result, &result) == nil {
			view.Result = &result
		}
	}
	if len(job.Error) > 0 {
		var jobErr NotebookResearchJobError
		if json.Unmarshal(job.Error, &jobErr) == nil {
			view.Error = &jobErr
		}
	}
	return view
}
