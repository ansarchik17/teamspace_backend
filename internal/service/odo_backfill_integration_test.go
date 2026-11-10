package service

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func TestODOBackfillLegacyAdoptionIntegration(t *testing.T) {
	for _, scenario := range []string{"success", "changed", "foreign_recipient", "foreign_author", "attached_context", "archived", "stale", "audit_rollback"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, s, org, _ := newODOFixture(t)
			migration, err := os.ReadFile("../../db/migrations/20261014000400_odo_assignment_backfill_audit.sql")
			require.NoError(t, err)
			_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
			require.NoError(t, err)
			board, task := uuid.New(), uuid.New()
			_, err = db.Exec(ctx, `INSERT INTO boards(id,owner_id,system_key) VALUES($1,1,'documentolog-assignments')`, board)
			require.NoError(t, err)
			_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,created_by,source_system,source_assignment_id,source_document_id,source_author_sub) VALUES($1,$2,1,'documentolog','asg-1','doc-1','subject-2')`, task, board)
			require.NoError(t, err)
			_, err = db.Exec(ctx, `INSERT INTO task_assignees(task_id,user_id) VALUES($1,3)`, task)
			require.NoError(t, err)
			hash, err := repository.ODOTaskFingerprint(ctx, db, task)
			require.NoError(t, err)
			event := odoTestEvent(org)
			request := domain.ODOAssignmentBackfill{OperationID: uuid.New(), TaskID: &task, ExpectedTaskHash: hash, Reason: "reviewed historical import", Event: event}
			expected := 0
			switch scenario {
			case "changed":
				_, err = db.Exec(ctx, `UPDATE tasks SET title='Changed after review' WHERE id=$1`, task)
				expected = 409
			case "foreign_recipient":
				_, err = db.Exec(ctx, `INSERT INTO task_assignees(task_id,user_id) VALUES($1,5)`, task)
				expected = 403
			case "foreign_author":
				_, err = db.Exec(ctx, `UPDATE tasks SET source_author_sub='subject-5' WHERE id=$1`, task)
				request.Event.AuthorSub = "subject-5"
				expected = 403
			case "audit_rollback":
				_, err = db.Exec(ctx, `ALTER TABLE odo_assignment_backfill_audit ADD CONSTRAINT test_audit_failure CHECK (false)`)
				expected = 500
			case "attached_context":
				_, err = db.Exec(ctx, `INSERT INTO task_comments(id,task_id,user_id) VALUES($1,$2,3)`, uuid.New(), task)
				expected = 403
			case "archived":
				_, err = db.Exec(ctx, `UPDATE users SET is_active=false,deleted_at=now() WHERE id IN (2,3)`)
				request.Event.Status = "closed"
			case "stale":
				_, err = db.Exec(ctx, `UPDATE tasks SET source_organization_id=$2,source_revision=3 WHERE id=$1`, task, org)
				expected = 409
			}
			require.NoError(t, err)
			if scenario == "foreign_recipient" || scenario == "foreign_author" || scenario == "attached_context" {
				request.ExpectedTaskHash, err = repository.ODOTaskFingerprint(ctx, db, task)
				require.NoError(t, err)
			}
			receipt, err := s.ApplyODOAssignmentBackfill(ctx, 1, request, "https://odo.example.test")
			if expected != 0 {
				require.Error(t, err)
				require.Equal(t, expected, apperrors.ToAppError(err).StatusCode)
				var count int
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM odo_assignment_events`).Scan(&count))
				require.Zero(t, count)
				var unchanged uuid.UUID
				require.NoError(t, db.QueryRow(ctx, `SELECT board_id FROM tasks WHERE id=$1`, task).Scan(&unchanged))
				require.Equal(t, board, unchanged)
				return
			}
			require.NoError(t, err)
			require.Equal(t, task, receipt.Task.ID)
			require.NotEqual(t, board, receipt.Task.BoardID)
			require.Equal(t, &org, receipt.Task.SourceOrganizationID)
			require.Equal(t, []int64{3}, receipt.Task.AssigneeIDs)
			repeated, err := s.ApplyODOAssignmentBackfill(ctx, 1, request, "https://odo.example.test")
			require.NoError(t, err)
			require.Equal(t, "duplicate", repeated.Outcome)
			var count int
			require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM odo_assignment_backfill_audit WHERE task_id=$1`, task).Scan(&count))
			require.Equal(t, 1, count)
			normal, err := s.ApplyODOAssignmentEvent(ctx, 1, request.Event, "https://odo.example.test")
			require.NoError(t, err)
			require.Equal(t, "duplicate", normal.Outcome)
			if scenario == "archived" {
				request.Event.EventID = uuid.New()
				request.Event.Revision++
				_, err = s.ApplyODOAssignmentEvent(ctx, 1, request.Event, "https://odo.example.test")
				require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			}
		})
	}
}
