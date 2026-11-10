package service

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestKanbanRolesAuditIntegration(t *testing.T) {
	ctx, db, s, board, col, doneCol, task := newKanbanAuditFixture(t)
	var err error
	for _, user := range []int64{1, 2, 3, 4, 5} {
		_, err = s.GetTask(ctx, user, task)
		require.NoError(t, err)
	}
	require.NoError(t, s.RemoveMember(ctx, 4, board, 2))
	_, err = s.GetTask(ctx, 2, task)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.AddMembers(ctx, 4, board, AddMembersRequest{Members: []MemberItem{{UserID: 2, Role: "viewer"}}}))
	for _, user := range []int64{2, 5, 6} {
		writes := []func() error{
			func() error { return s.MoveTask(ctx, user, task, doneCol) },
			func() error {
				_, err := s.UpdateTaskStatus(ctx, user, task, UpdateTaskStatusRequest{Status: domain.StatusDone})
				return err
			},
			func() error {
				title := "blocked"
				_, err := s.UpdateTask(ctx, user, task, &domain.UpdateTaskPatch{Title: &title})
				return err
			},
			func() error { return s.DeleteTask(ctx, user, task) },
			func() error {
				_, err := s.CreateProject(ctx, user, board, CreateProjectRequest{Name: "blocked"})
				return err
			},
			func() error {
				_, err := s.CreateChecklistItem(ctx, user, task, domain.CreateTaskChecklistItem{Title: "blocked"})
				return err
			},
			func() error {
				_, err := s.AddComment(ctx, user, task, CreateCommentRequest{Content: "blocked"})
				return err
			},
			func() error { return s.AddWorklog(ctx, user, task, &domain.TaskWorklog{TimeSpentSec: 60}) },
			func() error { return s.AttachFile(ctx, user, task, uuid.New()) },
			func() error { _, err := s.ResolveTaskMovedRecipients(ctx, user, board, task, col, doneCol); return err },
		}
		for i, write := range writes {
			require.Equal(t, 403, apperrors.ToAppError(write()).StatusCode, "user=%d operation=%d", user, i)
		}
	}
	require.Error(t, s.UpdateMemberRole(ctx, 3, board, 2, "admin"), "editor cannot escalate privileges")
	require.NoError(t, s.UpdateMemberRole(ctx, 4, board, 2, "Viewer"))
	require.Error(t, s.UpdateMemberRole(ctx, 4, board, 1, "viewer"))
	require.Error(t, s.RemoveMember(ctx, 4, board, 1))
	require.Error(t, s.AddMembers(ctx, 4, board, AddMembersRequest{Members: []MemberItem{{UserID: 6, Role: "owner"}}}))
	require.NoError(t, s.AddMembers(ctx, 4, board, AddMembersRequest{Members: []MemberItem{{UserID: 6, Role: "viewer"}}}))
	require.NoError(t, s.RemoveMember(ctx, 4, board, 6))
	for _, user := range []int64{1, 3, 4} {
		title := "changed by editor/admin"
		_, err = s.UpdateTask(ctx, user, task, &domain.UpdateTaskPatch{Title: &title})
		require.NoError(t, err)
		require.NoError(t, s.MoveTask(ctx, user, task, doneCol))
		_, err = s.CreateProject(ctx, user, board, CreateProjectRequest{Name: "Project"})
		require.NoError(t, err)
	}
	project, err := s.CreateProject(ctx, 3, board, CreateProjectRequest{Name: "Audit project"})
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE tasks SET project_id=$1 WHERE id=$2`, project.ID, task)
	require.NoError(t, err)
	require.NoError(t, s.DeleteProject(ctx, 3, board, project.ID))
	goal, sprint := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO goals(id,board_id)VALUES($1,$2)`, goal, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO sprints(id,board_id)VALUES($1,$2)`, sprint, board)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE tasks SET goal_id=$1,sprint_id=$2 WHERE id=$3`, goal, sprint, task)
	require.NoError(t, err)
	require.NoError(t, s.DeleteGoal(ctx, 3, board, goal))
	require.NoError(t, s.DeleteSprint(ctx, 3, board, sprint))
	item, err := s.CreateChecklistItem(ctx, 3, task, domain.CreateTaskChecklistItem{Title: "Check"})
	require.NoError(t, err)
	complete := true
	_, err = s.UpdateChecklistItem(ctx, 3, task, item.ID, domain.UpdateTaskChecklistItem{IsCompleted: &complete})
	require.NoError(t, err)
	require.NoError(t, s.DeleteChecklistItem(ctx, 3, task, item.ID))
	comment, err := s.AddComment(ctx, 3, task, CreateCommentRequest{Content: "Before"})
	require.NoError(t, err)
	require.NoError(t, s.UpdateComment(ctx, 3, comment.ID, UpdateCommentRequest{Content: "After"}))
	require.NoError(t, s.DeleteComment(ctx, 3, comment.ID))
	file := &domain.TaskAttachment{TaskID: task, UploadedBy: 3, FileName: "file.txt", ObjectName: "test", MimeType: "text/plain"}
	require.NoError(t, s.taskRepo.AddAttachment(ctx, file))
	require.NoError(t, s.taskRepo.DeleteAttachment(ctx, file.ID, 3))
	label := uuid.New()
	require.NoError(t, s.taskRepo.AddLabel(ctx, task, label, 3))
	require.NoError(t, s.taskRepo.RemoveLabel(ctx, task, label, 3))
	_, err = db.Exec(ctx, `INSERT INTO kanban_labels(id,board_id,name,color)VALUES($1,$2,'tag','blue')`, label, board)
	require.NoError(t, err)
	require.NoError(t, s.taskRepo.AddLabel(ctx, task, label, 3))
	require.NoError(t, s.DeleteLabel(ctx, 3, label))
	require.NoError(t, s.AddWorklog(ctx, 3, task, &domain.TaskWorklog{TimeSpentSec: 60, StartedAt: time.Now()}))
	history, err := s.GetTaskHistory(ctx, 2, task)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	actions := map[string]bool{}
	for _, h := range history {
		actions[h.Action] = true
		require.NotZero(t, h.UserID)
		require.True(t, h.CreatedAt.After(time.Now().Add(-time.Hour)))
	}
	for _, action := range []string{"updated", "moved", "checklist_created", "checklist_updated", "checklist_deleted", "comment_added", "comment_updated", "comment_deleted", "attachment_created", "attachment_deleted", "label_added", "label_removed", "worklog_added"} {
		require.True(t, actions[action], action)
	}
	for _, sql := range []string{`UPDATE task_history SET action='forged'`, `DELETE FROM task_history`, `TRUNCATE task_history`} {
		_, err = db.Exec(ctx, sql)
		require.Error(t, err)
		require.Contains(t, err.Error(), "append-only")
	}
	// Two concurrent editors must produce a contiguous chain of old/new values.
	baseline, err := s.taskRepo.GetByID(ctx, task)
	require.NoError(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, title := range []string{"concurrent A", "concurrent B"} {
		wg.Add(1)
		go func(title string) {
			defer wg.Done()
			errs <- s.taskRepo.Update(ctx, 3, task, &domain.UpdateTaskPatch{Title: &title})
		}(title)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	rows, err := db.Query(ctx, `SELECT old_value,new_value FROM task_history WHERE task_id=$1 AND field_name='title' AND new_value LIKE 'concurrent %' ORDER BY created_at,id`, task)
	require.NoError(t, err)
	previous := baseline.Title
	changes := 0
	for rows.Next() {
		var before, after string
		require.NoError(t, rows.Scan(&before, &after))
		require.Equal(t, previous, before)
		previous = after
		changes++
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.Equal(t, 2, changes)
	// Integration callbacks must not bypass board roles through an assignment ID.
	_, err = db.Exec(ctx, `UPDATE tasks SET source_system='documentolog',source_assignment_id='audit-assignment' WHERE id=$1`, task)
	require.NoError(t, err)
	_, _, err = s.UpsertDocumentologAssignment(ctx, 2, DocumentologAssignmentUpsertRequest{AssignmentID: "audit-assignment", Status: "completed"})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	// Force an audit-storage failure. No mutation is allowed to survive it.
	_, err = db.Exec(ctx, `CREATE FUNCTION fail_audit_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER fail_audit_test BEFORE INSERT ON task_history FOR EACH ROW EXECUTE FUNCTION fail_audit_test();`)
	require.NoError(t, err)
	before, err := s.taskRepo.GetByID(ctx, task)
	require.NoError(t, err)
	require.Error(t, s.MoveTask(ctx, 3, task, col))
	require.Error(t, s.DeleteTask(ctx, 3, task))
	newTask := &domain.Task{BoardID: board, ColumnID: col, CreatedBy: 3, Title: "creation must rollback", Priority: "medium", Status: domain.StatusTodo}
	require.Error(t, s.taskRepo.Create(ctx, newTask))
	var exists bool
	require.NoError(t, db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1)`, newTask.ID).Scan(&exists))
	require.False(t, exists)
	title := "must rollback"
	_, err = s.UpdateTask(ctx, 3, task, &domain.UpdateTaskPatch{Title: &title})
	require.Error(t, err)
	_, err = s.CreateChecklistItem(ctx, 3, task, domain.CreateTaskChecklistItem{Title: "must rollback"})
	require.Error(t, err)
	_, err = s.AddComment(ctx, 3, task, CreateCommentRequest{Content: "must rollback"})
	require.Error(t, err)
	after, err := s.taskRepo.GetByID(ctx, task)
	require.NoError(t, err)
	require.Equal(t, before.Title, after.Title)
	require.Equal(t, before.ColumnID, after.ColumnID)
	items, err := s.ListChecklist(ctx, 3, task)
	require.NoError(t, err)
	require.Empty(t, items)
	comments, err := s.ListComments(ctx, 3, task)
	require.NoError(t, err)
	require.Empty(t, comments)
	_, err = db.Exec(ctx, `DROP TRIGGER fail_audit_test ON task_history`)
	require.NoError(t, err)
	require.NoError(t, s.DeleteTask(ctx, 3, task))
	history, err = s.GetTaskHistory(ctx, 2, task)
	require.NoError(t, err)
	require.Equal(t, "deleted", history[0].Action)
	count := len(history)
	_, err = db.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, task)
	require.NoError(t, err)
	history, err = s.GetTaskHistory(ctx, 2, task)
	require.NoError(t, err)
	require.Len(t, history, count)
	_, err = db.Exec(ctx, `DELETE FROM users WHERE id=3`)
	require.NoError(t, err)
	history, err = s.GetTaskHistory(ctx, 2, task)
	require.NoError(t, err)
	require.Len(t, history, count)
	require.NoError(t, s.RemoveMember(ctx, 4, board, 2))
	_, err = s.GetTaskHistory(ctx, 2, task)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	otherTask := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by)VALUES($1,$2,$3,1)`, otherTask, board, col)
	require.NoError(t, err)
	require.NoError(t, s.boardRepo.DeleteBoard(ctx, board, 4))
	var actor int64
	require.NoError(t, db.QueryRow(ctx, `SELECT user_id FROM task_history WHERE task_id=$1 AND action='deleted'`, otherTask).Scan(&actor))
	require.Equal(t, int64(4), actor)
}
