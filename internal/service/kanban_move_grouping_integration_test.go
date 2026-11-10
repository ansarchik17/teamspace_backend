package service

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
)

func TestMoveByColumnIDAndMyStatusGroups(t *testing.T) {
	ctx, db, s, board, todo, done, task := newKanbanAuditFixture(t)
	progress, review := uuid.New(), uuid.New()
	_, err := db.Exec(ctx, `INSERT INTO kanban_columns(id,board_id,key,name)VALUES($1,$3,'IN_PROGRESS','В работе'),($2,$3,'TEST','Проверка')`, progress, review, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO task_assignees VALUES($1,3)`, task)
	require.NoError(t, err)
	sender := &realtimeSenderStub{}
	s.realtime = &kanbanRealtimeNotifier{boards: &realtimeBoardStub{board: &domain.Board{ID: board, OwnerID: 1}}, sender: sender, logger: zap.NewNop()}
	for i, target := range []uuid.UUID{progress, review, todo, done} {
		var patch domain.UpdateTaskPatch
		require.NoError(t, json.Unmarshal([]byte(`{"column_id":"`+target.String()+`","title":"Moved"}`), &patch))
		updated, err := s.UpdateTask(ctx, 3, task, &patch)
		require.NoError(t, err)
		require.Equal(t, target, updated.ColumnID)
		grouped, err := s.GetMyTasksByStatus(ctx, 3)
		require.NoError(t, err)
		require.Equal(t, []int{0, 0, 1, 0}[i], grouped.TodoCount)
		require.Equal(t, []int{1, 0, 0, 0}[i], grouped.InProgressCount)
		require.Equal(t, []int{0, 1, 0, 0}[i], grouped.TestCount)
		require.Equal(t, grouped.TodoCount+grouped.InProgressCount+grouped.TestCount, grouped.TotalTasks)
		require.Equal(t, target, *sender.events[len(sender.events)-1].ToColumnID)
	}
	// The dedicated drag-and-drop endpoint follows the same grouping.
	require.NoError(t, s.MoveTask(ctx, 3, task, progress))
	grouped, err := s.GetMyTasksByStatus(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, 1, grouped.InProgressCount)
	// Renaming is presentation-only when key is omitted.
	_, err = s.UpdateColumn(ctx, 3, progress, UpdateColumnRequest{Name: "Doing now"})
	require.NoError(t, err)
	grouped, err = s.GetMyTasksByStatus(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, 1, grouped.InProgressCount)
	// Explicit key changes update all tasks' statuses with audit, not just grouping.
	_, err = s.UpdateColumn(ctx, 3, progress, UpdateColumnRequest{Name: "Review", Key: "TEST"})
	require.NoError(t, err)
	updated, err := s.GetTask(ctx, 3, task)
	require.NoError(t, err)
	require.Equal(t, domain.StatusReview, updated.Status)
	grouped, err = s.GetMyTasksByStatus(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, 1, grouped.TestCount)
	// Two TEST columns are legal, but a status-only move is ambiguous.
	status := "TEST"
	_, err = s.UpdateTask(ctx, 3, task, &domain.UpdateTaskPatch{Status: &status})
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	status = "TODO"
	_, err = s.UpdateTask(ctx, 3, task, &domain.UpdateTaskPatch{ColumnID: &review, Status: &status})
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	foreignBoard, foreignColumn := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id)VALUES($1,4)`, foreignBoard)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO kanban_columns(id,board_id,key,name)VALUES($1,$2,'TODO','Other')`, foreignColumn, foreignBoard)
	require.NoError(t, err)
	_, err = s.UpdateTask(ctx, 3, task, &domain.UpdateTaskPatch{ColumnID: &foreignColumn})
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	for _, body := range []string{`{"column_id":null}`, `{"column_id":"bad"}`, `{"status":null}`} {
		var patch domain.UpdateTaskPatch
		require.Error(t, json.Unmarshal([]byte(body), &patch))
	}
	// Audit failure must roll back title, column, and status together and emit no WS.
	_, err = db.Exec(ctx, `CREATE FUNCTION fail_move_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER fail_move_audit BEFORE INSERT ON task_history FOR EACH ROW EXECUTE FUNCTION fail_move_audit()`)
	require.NoError(t, err)
	before, err := s.GetTask(ctx, 3, task)
	require.NoError(t, err)
	count := len(sender.events)
	title := "must rollback"
	_, err = s.UpdateTask(ctx, 3, task, &domain.UpdateTaskPatch{ColumnID: &todo, Title: &title})
	require.Error(t, err)
	updated, err = s.GetTask(ctx, 3, task)
	require.NoError(t, err)
	require.Equal(t, before.Title, updated.Title)
	require.Equal(t, before.Status, updated.Status)
	require.Equal(t, before.ColumnID, updated.ColumnID)
	require.Len(t, sender.events, count)
}

func TestCollidingMigrationRecoveryIsIdempotent(t *testing.T) {
	ctx, db, _, _, _, _, _ := newKanbanAuditFixture(t)
	// The fixture already applied audit; replay simulates a server whose old
	// 20260924000500 referred to audit, not message sync.
	migration, err := os.ReadFile("../../db/migrations/20260925000300_reconcile_kanban_audit.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE TABLE messages(id UUID PRIMARY KEY,conversation_id UUID,is_deleted BOOLEAN DEFAULT false)`)
	require.NoError(t, err)
	for _, name := range []string{"20260925000200_reconcile_message_sync_tracking.sql", "20260924000500_add_message_sync_tracking.sql", "20260925000200_reconcile_message_sync_tracking.sql"} {
		migration, err = os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	_, err = db.Exec(ctx, `CREATE TABLE ai_conversations(id UUID PRIMARY KEY); CREATE TABLE ai_messages(id UUID PRIMARY KEY)`)
	require.NoError(t, err)
	for _, name := range []string{"20260925000050_kanban_roles_immutable_audit.sql", "20260925000400_reconcile_ai_documents.sql", "20260925000100_ai_documents_production.sql", "20260925000400_reconcile_ai_documents.sql"} {
		data, readErr := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, readErr)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	_, err = db.Exec(ctx, `UPDATE task_history SET action='forged'`)
	require.Error(t, err)
}
