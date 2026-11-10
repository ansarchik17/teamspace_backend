package service

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func odoTestEvent(org uuid.UUID) domain.ODOAssignmentEvent {
	date := "2026-10-20"
	department := (*string)(nil)
	link := "https://odo.example.test/documents/doc-1"
	return domain.ODOAssignmentEvent{SchemaVersion: 1, EventID: uuid.New(), EventType: "created", OccurredAt: time.Now().UTC(), Revision: 1, OrganizationID: org, SourceSystem: "documentolog", IdentityIssuer: "https://id.example.test/realms/workspace", AssignmentID: "asg-1", DocumentID: "doc-1", AuthorSub: "subject-2", AssigneeSub: "subject-3", Title: "Поручение", Description: "Первый текст", DepartmentID: department, Priority: "high", DueDate: &date, Timezone: "Asia/Almaty", Status: "assigned", SourceURL: &link}
}

func TestODODepartmentUsesCompanyTeamsIntegration(t *testing.T) {
	ctx, db, s, company, foreignCompany := newODOFixture(t)
	own, foreign, missing := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO teams(id,organization_id,name) VALUES($1,$2,'Own team'),($3,$4,'Foreign team')`, own, company, foreign, foreignCompany)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, department string
		allowed          bool
	}{
		{"own", own, true},
		{"foreign", foreign, false},
		{"missing", missing, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := odoTestEvent(company)
			e.AssignmentID = "department-" + tc.name
			e.DepartmentID = &tc.department
			receipt, err := s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
			if tc.allowed {
				require.NoError(t, err)
				require.True(t, receipt.Applied)
				require.Equal(t, own, receipt.Task.SourceDepartmentID)
			} else {
				require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			}
			legacy := DocumentologAssignmentUpsertRequest{OrganizationID: company.String(), SourceSystem: "documentolog", AssignmentID: "legacy-department-" + tc.name, DocumentID: "doc-1", AuthorSub: "subject-2", AssigneeSub: "subject-3", Title: "Department check", Priority: "medium", Status: "assigned", DepartmentID: tc.department}
			_, _, err = s.UpsertODOIntegrationAssignment(ctx, 1, company, legacy)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			}
		})
	}
}

func TestODOReceiverMigrationPreservesLegacyIntegration(t *testing.T) {
	ctx, db, s, a, b := newODOFixture(t, true)
	scoped, global := uuid.New(), uuid.New()
	_, err := db.Exec(ctx, `INSERT INTO boards(id,owner_id,system_key) VALUES($1,1,$2),($3,1,'documentolog-assignments')`, scoped, "documentolog-assignments:"+a.String(), global)
	require.NoError(t, err)
	known, unknown, mixed := uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO tasks(id,board_id,created_by,source_system,source_assignment_id,source_document_id,source_author_sub) VALUES
	 ($1,$4,1,'documentolog','legacy-known','doc-known','subject-2'),
	 ($2,$5,1,'documentolog','legacy-unknown','doc-unknown','subject-2'),
	 ($3,$4,1,'documentolog','legacy-mixed','doc-mixed','subject-2')`, known, unknown, mixed, scoped, global)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO task_assignees(task_id,user_id) VALUES($1,3),($2,3),($3,5)`, known, unknown, mixed)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261014000300_odo_assignment_sync_v1.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	var org *uuid.UUID
	var revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT source_organization_id,source_revision FROM tasks WHERE id=$1`, known).Scan(&org, &revision))
	require.Equal(t, &a, org)
	require.Zero(t, revision)
	for _, id := range []uuid.UUID{unknown, mixed} {
		require.NoError(t, db.QueryRow(ctx, `SELECT source_organization_id FROM tasks WHERE id=$1`, id).Scan(&org))
		require.Nil(t, org)
	}
	for _, asg := range []string{"legacy-unknown", "legacy-mixed"} {
		e := odoTestEvent(a)
		e.AssignmentID = asg
		_, err = s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
		require.Equal(t, apperrors.ErrorCode("ODO_LEGACY_CONFLICT"), apperrors.ToAppError(err).Code)
	}
	// Tenant uniqueness replaces the previous global index without deleting rows.
	e := odoTestEvent(b)
	e.AssignmentID = "legacy-known"
	e.AuthorSub = "subject-5"
	e.AssigneeSub = "subject-6"
	_, err = s.ApplyODOAssignmentEvent(ctx, 6, e, "https://odo.example.test")
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id=ANY($1)`, []uuid.UUID{known, unknown, mixed}).Scan(&count))
	require.Equal(t, 3, count)
}

func TestODOSnapshotLifecycleIntegration(t *testing.T) {
	ctx, db, s, a, b := newODOFixture(t)
	e := odoTestEvent(a)
	dept := uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO teams(id,organization_id,name) VALUES($1,$2,'Company department')`, dept, a)
	require.NoError(t, err)
	e.DepartmentID = &dept
	first, err := s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
	require.NoError(t, err)
	require.True(t, first.Applied)
	require.Equal(t, "created", first.Outcome)
	require.Equal(t, []int64{3}, first.Task.AssigneeIDs)
	require.Equal(t, int64(1), first.Task.SourceRevision)
	for _, user := range []int64{1, 3, 4} {
		_, err = s.GetBoard(ctx, user, first.BoardID)
		require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		_, err = s.GetTask(ctx, user, first.Task.ID)
		require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		_, err = s.UpdateTaskStatus(ctx, user, first.Task.ID, UpdateTaskStatusRequest{Status: "DONE"})
		require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	}
	search, err := s.taskRepo.SearchLightweight(ctx, 3, domain.TaskFilters{BoardID: &first.BoardID})
	require.NoError(t, err)
	require.Empty(t, search)
	duplicate, err := s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
	require.NoError(t, err)
	require.False(t, duplicate.Applied)
	require.Equal(t, "duplicate", duplicate.Outcome)
	require.Equal(t, first.Task.UpdatedAt, duplicate.Task.UpdatedAt)
	// Equal revision with another event ID and equal snapshot is also harmless.
	equal := e
	equal.EventID = uuid.New()
	equal.EventType = "updated"
	equal.OccurredAt = e.OccurredAt.Add(time.Second)
	duplicate, err = s.ApplyODOAssignmentEvent(ctx, 1, equal, "https://odo.example.test")
	require.NoError(t, err)
	require.Equal(t, "duplicate", duplicate.Outcome)
	bad := equal
	bad.EventID = uuid.New()
	bad.Title = "different"
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, bad, "https://odo.example.test")
	require.Equal(t, apperrors.ErrorCode("ODO_REVISION_CONFLICT"), apperrors.ToAppError(err).Code)
	bad = e
	bad.Title = "reused event"
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, bad, "https://odo.example.test")
	require.Equal(t, apperrors.ErrorCode("ODO_EVENT_CONFLICT"), apperrors.ToAppError(err).Code)
	update := e
	update.EventID = uuid.New()
	update.EventType = "updated"
	update.Revision = 4
	update.Title = "Новый заголовок"
	update.Description = ""
	update.Priority = "low"
	update.DueDate = nil
	update.SourceURL = nil
	update.DepartmentID = nil
	update.AssigneeSub = "subject-4"
	update.Status = "in_progress"
	updated, err := s.ApplyODOAssignmentEvent(ctx, 1, update, "https://odo.example.test")
	require.NoError(t, err)
	require.Equal(t, first.Task.ID, updated.Task.ID)
	require.Equal(t, first.Task.TaskKey, updated.Task.TaskKey)
	require.Equal(t, first.Task.CreatedAt, updated.Task.CreatedAt)
	require.Equal(t, "", *updated.Task.Description)
	require.Nil(t, updated.Task.DueAt)
	require.Nil(t, updated.Task.SourceDueDate)
	require.Empty(t, updated.Task.SourceURL)
	require.Empty(t, updated.Task.SourceDepartmentID)
	require.Equal(t, domain.StatusWorking, updated.Task.Status)
	oldList, err := s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Empty(t, oldList)
	newList, err := s.ListMyDocumentologAssignments(ctx, 4)
	require.NoError(t, err)
	require.Len(t, newList, 1)
	require.Equal(t, int64(4), newList[0].SourceRevision)
	require.Equal(t, &a, newList[0].SourceOrganizationID)
	stale := e
	stale.EventID = uuid.New()
	stale.Revision = 3
	staleReceipt, err := s.ApplyODOAssignmentEvent(ctx, 1, stale, "https://odo.example.test")
	require.NoError(t, err)
	require.Equal(t, "stale", staleReceipt.Outcome)
	require.Equal(t, int64(4), staleReceipt.CurrentRevision)
	require.Equal(t, updated.Task.UpdatedAt, staleReceipt.Task.UpdatedAt)
	// Old exact event replay acknowledges the current committed projection.
	duplicate, err = s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
	require.NoError(t, err)
	require.Equal(t, int64(4), duplicate.CurrentRevision)
	// A retired original author does not stop status synchronization.
	_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now(),organization_id=$1 WHERE id=2`, b)
	require.NoError(t, err)
	for _, status := range []string{"completed", "closed", "cancelled"} {
		update.EventID = uuid.New()
		update.Revision++
		update.Status = status
		if status == "completed" {
			now := time.Now().UTC()
			update.CompletedAt = &now
		}
		if status == "closed" {
			now := time.Now().UTC()
			update.ClosedAt = &now
		}
		if status == "cancelled" {
			update.CompletedAt = nil
			update.ClosedAt = nil
		}
		result, err := s.ApplyODOAssignmentEvent(ctx, 1, update, "https://odo.example.test")
		require.NoError(t, err)
		require.Equal(t, status, result.Task.SourceStatus)
		if status == "cancelled" {
			require.Equal(t, "cancelled", result.Task.Status)
		} else {
			require.Equal(t, domain.StatusDone, result.Task.Status)
		}
	}
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM task_history WHERE task_id=$1 AND action='odo_assignment_synced'`, first.Task.ID).Scan(&count))
	require.Equal(t, 5, count)
	// The same source assignment ID is valid in another company.
	foreign := odoTestEvent(b)
	foreign.AuthorSub = "subject-5"
	foreign.AssigneeSub = "subject-6"
	other, err := s.ApplyODOAssignmentEvent(ctx, 6, foreign, "https://odo.example.test")
	require.NoError(t, err)
	require.NotEqual(t, first.Task.ID, other.Task.ID)
	foreign.EventID = e.EventID
	_, err = s.ApplyODOAssignmentEvent(ctx, 6, foreign, "https://odo.example.test")
	require.Equal(t, apperrors.ErrorCode("ODO_EVENT_CONFLICT"), apperrors.ToAppError(err).Code)
	legacy := DocumentologAssignmentUpsertRequest{AssignmentID: e.AssignmentID, DocumentID: e.DocumentID, Title: "old sender", AuthorSub: e.AuthorSub, AssigneeSub: "subject-4", Status: "assigned"}
	_, _, err = s.UpsertODOIntegrationAssignment(ctx, 1, a, legacy)
	require.Equal(t, apperrors.ErrorCode("ODO_LEGACY_CONFLICT"), apperrors.ToAppError(err).Code)
}

func TestODOConcurrentDeliveryIntegration(t *testing.T) {
	ctx, db, s, a, _ := newODOFixture(t)
	e := odoTestEvent(a)
	var wg sync.WaitGroup
	results := make(chan *domain.ODOAssignmentReceipt, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
			errs <- err
			results <- r
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		require.NoError(t, err)
	}
	applied := 0
	for r := range results {
		if r.Applied {
			applied++
		}
	}
	require.Equal(t, 1, applied)
	errs = make(chan error, 12)
	for i := 2; i <= 13; i++ {
		ev := e
		ev.EventID = uuid.New()
		ev.Revision = int64(i)
		ev.Title = "version"
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ApplyODOAssignmentEvent(ctx, 1, ev, "https://odo.example.test")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT source_revision FROM tasks WHERE source_assignment_id=$1`, e.AssignmentID).Scan(&revision))
	require.Equal(t, int64(13), revision)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM odo_assignment_events`).Scan(&count))
	require.Equal(t, 13, count)
}

func TestODOProjectionRollbackAndGuardsIntegration(t *testing.T) {
	ctx, db, s, a, b := newODOFixture(t)
	e := odoTestEvent(a)
	// A database audit failure rolls back board, task, members and receipt.
	_, err := db.Exec(ctx, `ALTER TABLE task_history ADD CONSTRAINT fail_odo_history CHECK(action<>'odo_assignment_synced')`)
	require.NoError(t, err)
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
	require.Error(t, err)
	for _, query := range []string{`SELECT count(*) FROM odo_assignment_events`, `SELECT count(*) FROM boards WHERE system_key LIKE 'documentolog-assignments:%'`, `SELECT count(*) FROM tasks WHERE source_system='documentolog'`} {
		var n int
		require.NoError(t, db.QueryRow(ctx, query).Scan(&n))
		require.Zero(t, n)
	}
	_, err = db.Exec(ctx, `ALTER TABLE task_history DROP CONSTRAINT fail_odo_history`)
	require.NoError(t, err)
	for _, change := range []func(*domain.ODOAssignmentEvent){func(e *domain.ODOAssignmentEvent) { e.AssigneeSub = "subject-5" }, func(e *domain.ODOAssignmentEvent) { e.AuthorSub = "subject-5" }, func(e *domain.ODOAssignmentEvent) { e.OrganizationID = b }} {
		ev := e
		change(&ev)
		_, err = s.ApplyODOAssignmentEvent(ctx, 1, ev, "https://odo.example.test")
		require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	}
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=3`)
	require.NoError(t, err)
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=3`)
	require.NoError(t, err)
	// Backfill may first arrive as a terminal revision, without invented dates.
	e.Status = "closed"
	e.Revision = 20
	first, err := s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
	require.NoError(t, err)
	require.Equal(t, "closed", first.Task.SourceStatus)
	require.Nil(t, first.Task.SourceCompletedAt)
	require.Nil(t, first.Task.SourceClosedAt)
	_, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=false WHERE organization_id=$1`, a)
	require.NoError(t, err)
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, e, "https://odo.example.test")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	// Reordered JSON has identical canonical hashes.
	body, _ := json.Marshal(e)
	decoded, err := domain.DecodeODOAssignmentEvent(body, "https://odo.example.test")
	require.NoError(t, err)
	full, _ := e.Hashes()
	decodedFull, _ := decoded.Hashes()
	require.Equal(t, full, decodedFull)
}
