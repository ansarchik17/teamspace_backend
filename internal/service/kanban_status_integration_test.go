package service

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
	"time"
)

func TestKanbanStatusColumnAndDeadlineIntegration(t *testing.T) {
	ctx, db, s, board, todo, done, task := newKanbanAuditFixture(t)
	migration, err := os.ReadFile("../../db/migrations/20260924000200_kanban_deadline_deliveries.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO task_assignees(task_id,user_id)VALUES($1,3)`, task)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE tasks SET due_at=NOW()-INTERVAL '1 day' WHERE id=$1`, task)
	require.NoError(t, err)
	sender := &realtimeSenderStub{}
	s.realtime = &kanbanRealtimeNotifier{boards: &realtimeBoardStub{board: &domain.Board{ID: board, OwnerID: 1}}, sender: sender, logger: zap.NewNop()}
	reminders, err := s.taskRepo.ListTaskDeadlineReminders(ctx, time.Now())
	require.NoError(t, err)
	require.Len(t, reminders, 1)
	require.Equal(t, "overdue", reminders[0].Kind)
	for _, status := range []string{" done ", domain.StatusTodo, domain.StatusDone, "TODO"} {
		previous, err := s.GetTask(ctx, 3, task)
		require.NoError(t, err)
		updated, err := s.UpdateTaskStatus(ctx, 3, task, UpdateTaskStatusRequest{Status: status})
		require.NoError(t, err)
		expectedColumn, expectedStatus := todo, domain.StatusTodo
		if normalizeKanbanTaskStatus(status) == domain.ColDone {
			expectedColumn = done
			expectedStatus = domain.StatusDone
		}
		require.Equal(t, expectedColumn, updated.ColumnID)
		require.Equal(t, expectedStatus, updated.Status)
		event := sender.events[len(sender.events)-1]
		require.Equal(t, "kanban_task_moved", event.Type)
		require.Equal(t, previous.ColumnID, *event.FromColumnID)
		require.Equal(t, expectedColumn, *event.ToColumnID)
		reminders, err = s.taskRepo.ListTaskDeadlineReminders(ctx, time.Now())
		require.NoError(t, err)
		if expectedColumn == done {
			require.Empty(t, reminders, "completed tasks must not receive overdue reminders")
		} else {
			require.Len(t, reminders, 1)
		}
	}
	for _, status := range []string{"", "   ", "unknown", "Просрочена"} {
		_, err = s.UpdateTaskStatus(ctx, 3, task, UpdateTaskStatusRequest{Status: status})
		require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	}
	_, err = s.UpdateTaskStatus(ctx, 2, task, UpdateTaskStatusRequest{Status: "DONE"})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	// Custom columns preserve their status instead of silently becoming TODO.
	custom := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO kanban_columns(id,board_id,key,name)VALUES($1,$2,'BLOCK','Blocked')`, custom, board)
	require.NoError(t, err)
	updated, err := s.UpdateTaskStatus(ctx, 3, task, UpdateTaskStatusRequest{Status: "block"})
	require.NoError(t, err)
	require.Equal(t, custom, updated.ColumnID)
	require.Equal(t, "BLOCK", updated.Status)
	// The general card update emits the same move event when changing columns.
	count := len(sender.events)
	status := "DONE"
	updated, err = s.UpdateTask(ctx, 3, task, &domain.UpdateTaskPatch{Status: &status})
	require.NoError(t, err)
	require.Equal(t, done, updated.ColumnID)
	require.Len(t, sender.events, count+1)
	// Reopening with a future/no deadline must not produce an overdue reminder.
	for _, due := range []string{"NOW()+INTERVAL '2 days'", "NULL"} {
		_, err = db.Exec(ctx, `UPDATE tasks SET due_at=`+due+` WHERE id=$1`, task)
		require.NoError(t, err)
		_, err = s.UpdateTaskStatus(ctx, 3, task, UpdateTaskStatusRequest{Status: "TODO"})
		require.NoError(t, err)
		reminders, err = s.taskRepo.ListTaskDeadlineReminders(ctx, time.Now())
		require.NoError(t, err)
		require.Empty(t, reminders)
	}
	// Failure to append the audit must preserve BOTH column and status and send no event.
	_, err = db.Exec(ctx, `CREATE FUNCTION fail_status_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER fail_status_audit BEFORE INSERT ON task_history FOR EACH ROW EXECUTE FUNCTION fail_status_audit()`)
	require.NoError(t, err)
	count = len(sender.events)
	_, err = s.UpdateTaskStatus(ctx, 3, task, UpdateTaskStatusRequest{Status: "DONE"})
	require.Error(t, err)
	updated, err = s.GetTask(ctx, 3, task)
	require.NoError(t, err)
	require.Equal(t, todo, updated.ColumnID)
	require.Equal(t, domain.StatusTodo, updated.Status)
	require.Len(t, sender.events, count)
}
