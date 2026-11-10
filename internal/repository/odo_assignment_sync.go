package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func odoConflict(code string) error {
	return apperrors.New(apperrors.ErrorCode(code), "ODO assignment synchronization conflict", 409)
}

const odoSourceColumns = `COALESCE(source_system,''),COALESCE(source_assignment_id,''),COALESCE(source_document_id,''),
 COALESCE(source_url,''),COALESCE(source_author_sub,''),COALESCE(source_department_id,''),
 source_organization_id,source_revision,COALESCE(source_status,''),source_due_date,COALESCE(source_timezone,''),source_completed_at,source_closed_at`

func scanODOTask(row pgx.Row) (*domain.Task, error) {
	t := &domain.Task{}
	err := row.Scan(&t.ID, &t.BoardID, &t.GoalID, &t.ProjectID, &t.SprintID, &t.ColumnID, &t.CreatedBy, &t.TaskNumber, &t.TaskKey,
		&t.Title, &t.Description, &t.Priority, &t.DueAt, &t.OriginalEstimateSec, &t.TimeSpentSec, &t.CreatedAt, &t.UpdatedAt, &t.ParentID, &t.Status,
		&t.SourceSystem, &t.SourceAssignmentID, &t.SourceDocumentID, &t.SourceURL, &t.SourceAuthorSub, &t.SourceDepartmentID,
		&t.SourceOrganizationID, &t.SourceRevision, &t.SourceStatus, &t.SourceDueDate, &t.SourceTimezone, &t.SourceCompletedAt, &t.SourceClosedAt)
	if err != nil {
		return nil, err
	}
	setTaskLink(t)
	return t, nil
}

// ApplyODOAssignment owns the entire transaction, including authorization,
// projection, participant replacement, audit and durable event receipt.
// legacyBoard is used only by the explicitly separated v0 rollout path.
func (r *TaskRepository) ApplyODOAssignment(ctx context.Context, actor int64, e domain.ODOAssignmentEvent, legacyBoard string) (*domain.ODOAssignmentReceipt, error) {
	return r.applyODOAssignment(ctx, actor, e, legacyBoard, nil)
}

func (r *TaskRepository) applyODOAssignment(ctx context.Context, actor int64, e domain.ODOAssignmentEvent, legacyBoard string, backfill *domain.ODOAssignmentBackfill) (*domain.ODOAssignmentReceipt, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	fullHash, snapshotHash := e.Hashes()
	if e.SchemaVersion == 1 {
		// The event lock is global: reusing an event ID across companies conflicts.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "odo-event:"+e.EventID.String()); err != nil {
			return nil, err
		}
	}
	systemKey := "documentolog-assignments:" + e.OrganizationID.String()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, systemKey); err != nil {
		return nil, err
	}
	var org uuid.UUID
	err = tx.QueryRow(ctx, `SELECT o.id FROM users u JOIN organizations o ON o.id=u.organization_id
 WHERE u.id=$1 AND u.organization_id=$2 AND u.is_active AND u.deleted_at IS NULL AND o.status='active' FOR SHARE OF u,o`, actor, e.OrganizationID).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.Forbidden("active integration company membership required")
	}
	if err != nil {
		return nil, err
	}
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT available AND enabled FROM organization_modules WHERE organization_id=$1 AND application='odo' FOR SHARE`, org).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !enabled) {
		return nil, apperrors.Forbidden("ODO company module disabled")
	}
	if err != nil {
		return nil, err
	}

	var receiptTask uuid.UUID
	var receivedHash string
	if e.SchemaVersion == 1 {
		err = tx.QueryRow(ctx, `SELECT task_id,request_hash FROM odo_assignment_events WHERE event_id=$1`, e.EventID).Scan(&receiptTask, &receivedHash)
		if err == nil && receivedHash != fullHash {
			return nil, odoConflict("ODO_EVENT_CONFLICT")
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	var t *domain.Task
	if receiptTask != uuid.Nil {
		t, err = scanODOTask(tx.QueryRow(ctx, `SELECT `+taskSelectColumns+`,`+odoSourceColumns+` FROM tasks WHERE id=$1 AND source_organization_id=$2 AND source_assignment_id=$3 AND deleted_at IS NULL FOR UPDATE`, receiptTask, org, e.AssignmentID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, odoConflict("ODO_EVENT_CONFLICT")
		}
		if err != nil {
			return nil, err
		}
	} else {
		t, err = scanODOTask(tx.QueryRow(ctx, `SELECT `+taskSelectColumns+`,`+odoSourceColumns+` FROM tasks WHERE source_organization_id=$1 AND source_system='documentolog' AND source_assignment_id=$2 AND deleted_at IS NULL FOR UPDATE`, org, e.AssignmentID))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	adopting := false
	var previousBoard *uuid.UUID
	if backfill != nil && receiptTask == uuid.Nil && backfill.TaskID != nil {
		if t != nil && t.ID != *backfill.TaskID {
			return nil, odoConflict("ODO_LEGACY_CONFLICT")
		}
		if t == nil {
			t, err = scanODOTask(tx.QueryRow(ctx, `SELECT `+taskSelectColumns+`,`+odoSourceColumns+` FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, *backfill.TaskID))
			if err != nil {
				return nil, err
			}
			if t.SourceSystem != "documentolog" || t.SourceAssignmentID != e.AssignmentID || t.SourceDocumentID != e.DocumentID {
				return nil, odoConflict("ODO_LEGACY_CONFLICT")
			}
			if err = validateODOAdoption(ctx, tx, t, org); err != nil {
				return nil, err
			}
			adopting = true
		}
		hash, hashErr := ODOTaskFingerprint(ctx, tx, t.ID)
		if hashErr != nil {
			return nil, hashErr
		}
		if hash != backfill.ExpectedTaskHash {
			return nil, odoConflict("ODO_BACKFILL_CHANGED")
		}
		old := t.BoardID
		previousBoard = &old
	}
	outcome := "created"
	applied := true
	if t != nil {
		outcome = "updated"
		if e.SchemaVersion == 0 && t.SourceRevision > 0 {
			return nil, odoConflict("ODO_LEGACY_CONFLICT")
		}
		if e.SchemaVersion == 1 {
			switch {
			case receiptTask != uuid.Nil:
				outcome = "duplicate"
				applied = false
			case e.Revision < t.SourceRevision:
				if backfill != nil {
					return nil, odoConflict("ODO_BACKFILL_CHANGED")
				}
				outcome = "stale"
				applied = false
			case e.Revision == t.SourceRevision:
				var hash string
				if err = tx.QueryRow(ctx, `SELECT COALESCE(odo_snapshot_hash,'') FROM tasks WHERE id=$1`, t.ID).Scan(&hash); err != nil {
					return nil, err
				}
				if hash != snapshotHash {
					return nil, odoConflict("ODO_REVISION_CONFLICT")
				}
				outcome = "duplicate"
				applied = false
			}
		}
	}
	if applied {
		var unresolved bool
		var adoptedID *uuid.UUID
		if adopting {
			adoptedID = &t.ID
		}
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE source_system='documentolog' AND source_assignment_id=$1 AND source_organization_id IS NULL AND deleted_at IS NULL AND ($2::uuid IS NULL OR id<>$2))`, e.AssignmentID, adoptedID).Scan(&unresolved); err != nil {
			return nil, err
		}
		if unresolved {
			return nil, odoConflict("ODO_LEGACY_CONFLICT")
		}
		if t != nil && t.SourceDocumentID != e.DocumentID {
			return nil, odoConflict("ODO_REVISION_CONFLICT")
		}
		if t != nil && t.SourceRevision > 0 {
			var issuer string
			if err = tx.QueryRow(ctx, `SELECT COALESCE(source_identity_issuer,'') FROM tasks WHERE id=$1`, t.ID).Scan(&issuer); err != nil {
				return nil, err
			}
			if issuer != e.IdentityIssuer {
				return nil, apperrors.Forbidden("assignment identity issuer changed")
			}
		}
		// A retired original author remains historical source identity. Any changed
		// author and every executor must have verified membership in this company.
		archival := backfill != nil && (e.Status == "completed" || e.Status == "closed" || e.Status == "cancelled")
		if t == nil || t.SourceAuthorSub != e.AuthorSub || adopting {
			var authorID int64
			err = tx.QueryRow(ctx, `SELECT id FROM users WHERE keycloak_id::text=$1 AND organization_id=$2 AND (deleted_at IS NULL OR $3) FOR SHARE`, e.AuthorSub, org, archival).Scan(&authorID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, apperrors.Forbidden("assignment author is outside the company")
			}
			if err != nil {
				return nil, err
			}
		}
		var assignee int64
		err = tx.QueryRow(ctx, `SELECT id FROM users WHERE keycloak_id::text=$1 AND organization_id=$2 AND ((is_active AND deleted_at IS NULL) OR $3) FOR SHARE`, e.AssigneeSub, org, archival).Scan(&assignee)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.Forbidden("active company executor required")
		}
		if err != nil {
			return nil, err
		}
		if e.DepartmentID != nil {
			dept, parseErr := uuid.Parse(*e.DepartmentID)
			if parseErr != nil {
				return nil, apperrors.Validation("invalid assignment department")
			}
			var found uuid.UUID
			err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 AND organization_id=$2 FOR SHARE`, dept, org).Scan(&found)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, apperrors.Forbidden("assignment department is outside the company")
			}
			if err != nil {
				return nil, err
			}
		}
		board, err := ensureODOBoard(ctx, tx, actor, org, systemKey, legacyBoard)
		if err != nil {
			return nil, err
		}
		if t != nil && t.BoardID != board.ID {
			if !adopting {
				return nil, apperrors.Forbidden("assignment board does not belong to company")
			}
			if err = moveODOProjection(ctx, tx, t, board.ID); err != nil {
				return nil, err
			}
		}
		columnKey, status := domain.ColTODO, domain.StatusTodo
		switch e.Status {
		case "in_progress":
			columnKey, status = domain.ColInProgress, domain.StatusWorking
		case "completed", "closed":
			columnKey, status = domain.ColDone, domain.StatusDone
		case "cancelled":
			status = "cancelled"
		}
		var column uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM kanban_columns WHERE board_id=$1 AND key=$2 ORDER BY position,id LIMIT 1 FOR SHARE`, board.ID, columnKey).Scan(&column)
		if err != nil {
			return nil, err
		}
		if t == nil {
			t = &domain.Task{ID: uuid.New(), BoardID: board.ID, CreatedBy: actor}
			var prefix string
			err = tx.QueryRow(ctx, `SELECT COALESCE(task_key_prefix,'ODO'),COALESCE(task_sequence,0)+1 FROM boards WHERE id=$1 FOR UPDATE`, board.ID).Scan(&prefix, &t.TaskNumber)
			if err != nil {
				return nil, err
			}
			t.TaskKey = fmt.Sprintf("%s-%d", prefix, t.TaskNumber)
			_, err = tx.Exec(ctx, `INSERT INTO tasks(id,board_id,column_id,created_by,task_number,task_key,title,priority,status,assignee_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, t.ID, t.BoardID, column, actor, t.TaskNumber, t.TaskKey, e.Title, e.Priority, status, assignee)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `UPDATE boards SET task_sequence=$2,updated_at=now() WHERE id=$1`, board.ID, t.TaskNumber)
			if err != nil {
				return nil, err
			}
		}
		snapshot, _ := json.Marshal(e)
		_, err = tx.Exec(ctx, `UPDATE tasks SET title=$2,description=$3,priority=$4,due_at=$5,column_id=$6,status=$7,assignee_id=$8,
   source_system='documentolog',source_assignment_id=$9,source_document_id=$10,source_author_sub=$11,source_department_id=$12,source_url=$13,
   source_organization_id=$14,source_revision=$15,source_status=$16,source_due_date=$17,source_timezone=$18,source_completed_at=$19,source_closed_at=$20,
   source_identity_issuer=$21,odo_snapshot=$22,odo_snapshot_hash=$23,updated_at=now() WHERE id=$1`,
			t.ID, e.Title, e.Description, e.Priority, e.DueAt(), column, status, assignee, e.AssignmentID, e.DocumentID, e.AuthorSub, e.DepartmentID, e.SourceURL, org, e.Revision, e.Status, e.DueDate, e.Timezone, e.CompletedAt, e.ClosedAt, e.IdentityIssuer, snapshot, snapshotHash)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM task_assignees WHERE task_id=$1`, t.ID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO task_assignees(task_id,user_id) VALUES($1,$2)`, t.ID, assignee); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO board_members(board_id,user_id,role) VALUES($1,$2,'member') ON CONFLICT(board_id,user_id) DO NOTHING`, board.ID, assignee); err != nil {
			return nil, err
		}
		if err = r.addHistoryInTx(ctx, tx, &domain.TaskHistory{TaskID: t.ID, UserID: actor, Action: "odo_assignment_synced", Metadata: map[string]interface{}{"source_system": "documentolog", "assignment_id": e.AssignmentID, "revision": e.Revision, "outcome": outcome}}); err != nil {
			return nil, err
		}
	}
	if e.SchemaVersion == 1 && receiptTask == uuid.Nil {
		_, err = tx.Exec(ctx, `INSERT INTO odo_assignment_events(event_id,organization_id,assignment_id,task_id,received_revision,request_hash) VALUES($1,$2,$3,$4,$5,$6)`, e.EventID, org, e.AssignmentID, t.ID, e.Revision, fullHash)
		if err != nil {
			return nil, err
		}
		if backfill != nil {
			_, err = tx.Exec(ctx, `INSERT INTO odo_assignment_backfill_audit(operation_id,event_id,organization_id,assignment_id,task_id,previous_board_id,previous_task_hash,actor_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, backfill.OperationID, e.EventID, org, e.AssignmentID, t.ID, previousBoard, backfill.ExpectedTaskHash, actor, backfill.Reason)
			if err != nil {
				return nil, err
			}
		}
	}
	t, err = scanODOTask(tx.QueryRow(ctx, `SELECT `+taskSelectColumns+`,`+odoSourceColumns+` FROM tasks WHERE id=$1`, t.ID))
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT user_id FROM task_assignees WHERE task_id=$1 ORDER BY user_id`, t.ID)
	if err != nil {
		return nil, err
	}
	t.AssigneeIDs = []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		t.AssigneeIDs = append(t.AssigneeIDs, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &domain.ODOAssignmentReceipt{Task: t, BoardID: t.BoardID, SourceSystem: "documentolog", OrganizationID: org, AssignmentID: e.AssignmentID, EventID: e.EventID, ReceivedRevision: e.Revision, CurrentRevision: t.SourceRevision, Applied: applied, Outcome: outcome}, nil
}

func ensureODOBoard(ctx context.Context, tx pgx.Tx, actor int64, org uuid.UUID, key, requested string) (*domain.Board, error) {
	b := &domain.Board{SystemKey: key}
	err := tx.QueryRow(ctx, `SELECT b.id,b.owner_id,b.name FROM boards b JOIN users u ON u.id=b.owner_id WHERE b.system_key=$1 AND b.deleted_at IS NULL AND u.organization_id=$2 FOR UPDATE OF b`, key, org).Scan(&b.ID, &b.OwnerID, &b.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		if requested != "" {
			return nil, apperrors.Forbidden("integration board is outside the company")
		}
		// A corrupt/moved owner must not cause adoption or a second board.
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM boards WHERE system_key=$1 AND deleted_at IS NULL)`, key).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, apperrors.Forbidden("integration board owner is outside the company")
		}
		b.ID, b.OwnerID, b.Name = uuid.New(), actor, "Поручения ЭДО"
		_, err = tx.Exec(ctx, `INSERT INTO boards(id,owner_id,name,system_key,task_key_prefix) VALUES($1,$2,$3,$4,'ODO')`, b.ID, actor, b.Name, key)
		if err != nil {
			return nil, err
		}
		for _, col := range domain.DefaultColumns {
			_, err = tx.Exec(ctx, `INSERT INTO kanban_columns(id,board_id,user_id,key,name,position,is_system,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,true,$7,$7)`, uuid.New(), b.ID, actor, col.Key, col.Name, col.Position, time.Now())
			if err != nil {
				return nil, err
			}
		}
	} else if err != nil {
		return nil, err
	}
	if requested != "" && requested != b.ID.String() {
		return nil, apperrors.Forbidden("integration board is outside the company")
	}
	role := "admin"
	if b.OwnerID == actor {
		role = "owner"
	}
	_, err = tx.Exec(ctx, `INSERT INTO board_members(board_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT(board_id,user_id) DO NOTHING`, b.ID, actor, role)
	return b, err
}
