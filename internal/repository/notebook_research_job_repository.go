package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Notebook research job states. queued and running are unfinished; at most
// one unfinished job exists per user.
const (
	NotebookJobQueued    = "queued"
	NotebookJobRunning   = "running"
	NotebookJobDone      = "done"
	NotebookJobFailed    = "failed"
	NotebookJobCancelled = "cancelled"
)

type NotebookResearchJob struct {
	ID          uuid.UUID
	UserID      int64
	ChatID      uuid.UUID
	OperationID string
	State       string
	Stage       string
	ChunksDone  int
	ChunksTotal int
	Request     json.RawMessage
	Result      json.RawMessage
	Error       json.RawMessage
	LeaseUntil  time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
}

type NotebookResearchJobRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewNotebookResearchJobRepository(db *pgxpool.Pool, logger *zap.Logger) *NotebookResearchJobRepository {
	return &NotebookResearchJobRepository{db: db, logger: logger}
}

const notebookJobColumns = `id, user_id, chat_id, operation_id, state, stage, chunks_done, chunks_total,
	request, result, error, lease_until, created_at, updated_at, started_at, finished_at`

func scanNotebookJob(row pgx.Row) (*NotebookResearchJob, error) {
	var job NotebookResearchJob
	var request, result, jobErr []byte
	err := row.Scan(&job.ID, &job.UserID, &job.ChatID, &job.OperationID, &job.State, &job.Stage, &job.ChunksDone,
		&job.ChunksTotal, &request, &result, &jobErr, &job.LeaseUntil, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.Request, job.Result, job.Error = request, result, jobErr
	return &job, nil
}

// CreateJob inserts a queued job. When the user already has a job with the
// same operation_id, or another unfinished job, nothing is inserted and that
// job is returned with inserted=false. A nil job with inserted=false means the
// conflicting job finished in between; the caller may retry.
func (r *NotebookResearchJobRepository) CreateJob(ctx context.Context, job NotebookResearchJob) (*NotebookResearchJob, bool, error) {
	created, err := scanNotebookJob(r.db.QueryRow(ctx, `
		INSERT INTO notebook_research_jobs (id, user_id, chat_id, operation_id, state, stage, request, lease_until)
		VALUES ($1, $2, $3, $4, 'queued', 'queued', $5, $6)
		ON CONFLICT DO NOTHING
		RETURNING `+notebookJobColumns,
		job.ID, job.UserID, job.ChatID, job.OperationID, []byte(job.Request), job.LeaseUntil))
	if err != nil {
		return nil, false, fmt.Errorf("create notebook research job: %w", err)
	}
	if created != nil {
		return created, true, nil
	}
	existing, err := scanNotebookJob(r.db.QueryRow(ctx, `
		SELECT `+notebookJobColumns+` FROM notebook_research_jobs
		WHERE user_id = $1 AND (operation_id = $2 OR state IN ('queued', 'running'))
		ORDER BY (operation_id = $2) DESC
		LIMIT 1`, job.UserID, job.OperationID))
	if err != nil {
		return nil, false, fmt.Errorf("find conflicting notebook research job: %w", err)
	}
	return existing, false, nil
}

// GetJob returns nil when the job does not exist or belongs to another user
// or notebook.
func (r *NotebookResearchJobRepository) GetJob(ctx context.Context, userID int64, chatID, jobID uuid.UUID) (*NotebookResearchJob, error) {
	job, err := scanNotebookJob(r.db.QueryRow(ctx, `
		SELECT `+notebookJobColumns+` FROM notebook_research_jobs
		WHERE id = $1 AND user_id = $2 AND chat_id = $3`, jobID, userID, chatID))
	if err != nil {
		return nil, fmt.Errorf("get notebook research job: %w", err)
	}
	return job, nil
}

// StartJob moves a queued job to running. It reports false when the job is
// no longer queued (cancelled or expired).
func (r *NotebookResearchJobRepository) StartJob(ctx context.Context, jobID uuid.UUID, leaseUntil time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notebook_research_jobs
		SET state = 'running', stage = 'reading', started_at = NOW(), updated_at = NOW(), lease_until = $2
		WHERE id = $1 AND state = 'queued'`, jobID, leaseUntil)
	if err != nil {
		return false, fmt.Errorf("start notebook research job: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// UpdateProgress records the stage and part counts and extends the lease. It
// reports false when the job is no longer running.
func (r *NotebookResearchJobRepository) UpdateProgress(ctx context.Context, jobID uuid.UUID, stage string, done, total int, leaseUntil time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notebook_research_jobs
		SET stage = $2, chunks_done = $3, chunks_total = $4, lease_until = $5, updated_at = NOW()
		WHERE id = $1 AND state = 'running'`, jobID, stage, done, total, leaseUntil)
	if err != nil {
		return false, fmt.Errorf("update notebook research job progress: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ExtendLease keeps an unfinished job alive. It reports false when the job
// has finished or was cancelled, possibly by another instance.
func (r *NotebookResearchJobRepository) ExtendLease(ctx context.Context, jobID uuid.UUID, leaseUntil time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notebook_research_jobs SET lease_until = $2
		WHERE id = $1 AND state IN ('queued', 'running')`, jobID, leaseUntil)
	if err != nil {
		return false, fmt.Errorf("extend notebook research job lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// FinishJob stores the outcome of an unfinished job. It reports false when the
// job was already finished or cancelled; the outcome is then discarded.
func (r *NotebookResearchJobRepository) FinishJob(ctx context.Context, jobID uuid.UUID, state, stage string, result, jobErr json.RawMessage) (bool, error) {
	if state != NotebookJobDone && state != NotebookJobFailed {
		return false, fmt.Errorf("invalid final notebook research job state %q", state)
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE notebook_research_jobs
		SET state = $2, stage = $3, result = $4, error = $5, finished_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND state IN ('queued', 'running')`, jobID, state, stage, nullJSON(result), nullJSON(jobErr))
	if err != nil {
		return false, fmt.Errorf("finish notebook research job: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CancelJob cancels an unfinished job of this user and notebook. It returns
// the current row and whether this call cancelled it; a finished job is
// returned unchanged and nil means not found.
func (r *NotebookResearchJobRepository) CancelJob(ctx context.Context, userID int64, chatID, jobID uuid.UUID) (*NotebookResearchJob, bool, error) {
	job, err := scanNotebookJob(r.db.QueryRow(ctx, `
		UPDATE notebook_research_jobs
		SET state = 'cancelled', finished_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND chat_id = $3 AND state IN ('queued', 'running')
		RETURNING `+notebookJobColumns, jobID, userID, chatID))
	if err != nil {
		return nil, false, fmt.Errorf("cancel notebook research job: %w", err)
	}
	if job != nil {
		return job, true, nil
	}
	job, err = r.GetJob(ctx, userID, chatID, jobID)
	return job, false, err
}

// FailExpired fails unfinished jobs whose lease ran out, which happens only
// when the executing process stopped. Their paid steps cannot be replayed
// under the same operation, so they are not retried.
func (r *NotebookResearchJobRepository) FailExpired(ctx context.Context, now time.Time, jobErr json.RawMessage) ([]NotebookResearchJob, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE notebook_research_jobs
		SET state = 'failed', error = $2, finished_at = $1, updated_at = $1
		WHERE state IN ('queued', 'running') AND lease_until < $1
		RETURNING `+notebookJobColumns, now, []byte(jobErr))
	if err != nil {
		return nil, fmt.Errorf("fail expired notebook research jobs: %w", err)
	}
	defer rows.Close()
	var jobs []NotebookResearchJob
	for rows.Next() {
		job, err := scanNotebookJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}

// DeleteFinishedBefore removes finished jobs; results hold document excerpts
// and are kept only as long as a client may still fetch them.
func (r *NotebookResearchJobRepository) DeleteFinishedBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM notebook_research_jobs WHERE finished_at IS NOT NULL AND finished_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("delete finished notebook research jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

func nullJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}
