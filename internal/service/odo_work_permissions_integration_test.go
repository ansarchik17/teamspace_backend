package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestODOWorkManagementRoleNeverGrantsAssignmentRightsIntegration(t *testing.T) {
	for _, role := range []string{"employee", "director", "admin"} {
		t.Run(role, func(t *testing.T) {
			ctx, db, s, org, _ := newODOFixture(t)
			source := odoTestEvent(org)
			initial, err := s.ApplyODOAssignmentEvent(ctx, 1, source, "https://odo.example.test")
			require.NoError(t, err)
			_, err = db.Exec(ctx, `INSERT INTO organization_access_apps VALUES($1,'odo',true)`, org)
			require.NoError(t, err)
			switch role {
			case "director":
				_, err = db.Exec(ctx, `UPDATE organizations SET owner_id=3 WHERE id=$1`, org)
			case "admin":
				_, err = db.Exec(ctx, `UPDATE users SET role='admin' WHERE id=3`)
			}
			require.NoError(t, err)
			command := domain.ODOCompleteRequest{CommandID: uuid.New(), ExpectedRevision: 1, ResultComment: "result"}
			_, err = s.ListMyDocumentologAssignments(ctx, 3)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			_, err = s.CompleteODOAssignment(ctx, 3, initial.Task.ID, command, "user-token")
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			position := uuid.New()
			_, err = db.Exec(ctx, `INSERT INTO organization_access_assignments VALUES($1,3,$2)`, org, position)
			require.NoError(t, err)
			_, err = db.Exec(ctx, `INSERT INTO organization_position_permissions VALUES($1,$2,'odo',ARRAY['assignment.read','assignment.complete'])`, org, position)
			require.NoError(t, err)
			_, err = s.ListMyDocumentologAssignments(ctx, 3)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode, "missing document read cannot become usable assignment access")
			_, err = db.Exec(ctx, `UPDATE organization_position_permissions SET permissions=ARRAY['document.read','assignment.read']`)
			require.NoError(t, err)
			items, err := s.ListMyDocumentologAssignments(ctx, 3)
			require.NoError(t, err)
			require.Len(t, items, 1)
			_, err = s.CompleteODOAssignment(ctx, 3, initial.Task.ID, command, "user-token")
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			_, err = db.Exec(ctx, `UPDATE organization_position_permissions SET permissions=ARRAY['document.read','assignment.read','assignment.complete']`)
			require.NoError(t, err)
			_, err = s.CompleteODOAssignment(ctx, 3, initial.Task.ID, command, "user-token")
			require.Equal(t, 503, apperrors.ToAppError(err).StatusCode, "passed rights checks; test command transport disabled")
			_, err = db.Exec(ctx, `DELETE FROM organization_access_assignments WHERE user_id=3`)
			require.NoError(t, err)
			_, err = s.CompleteODOAssignment(ctx, 3, initial.Task.ID, command, "user-token")
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode, "revoked position cannot reuse an old command")
		})
	}
}

func TestODOWorkCompletionWaitsForSnapshotAndRejectsOldAssigneeIntegration(t *testing.T) {
	ctx, db, s, org, _ := newODOFixture(t)
	source := odoTestEvent(org)
	initial, err := s.ApplyODOAssignmentEvent(ctx, 1, source, "https://odo.example.test")
	require.NoError(t, err)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var command domain.ODOCompleteCommand
		require.NoError(t, json.NewDecoder(r.Body).Decode(&command))
		require.Equal(t, "subject-3", command.ActorSub)
		json.NewEncoder(w).Encode(domain.ODOCompleteReceipt{CommandID: command.CommandID, OrganizationID: org, IdentityIssuer: source.IdentityIssuer, ActorSub: command.ActorSub, AssignmentID: source.AssignmentID, DocumentID: source.DocumentID, ExpectedRevision: command.ExpectedRevision, Revision: command.ExpectedRevision + 1, Status: "completed", EventID: uuid.New(), CompletedAt: time.Now().UTC(), Outcome: "completed"})
	}))
	defer server.Close()
	client, err := NewODOCommandClient(server.URL, source.IdentityIssuer, true, time.Second)
	require.NoError(t, err)
	s.SetODOCommandClient(client)
	command := domain.ODOCompleteRequest{CommandID: uuid.New(), ExpectedRevision: 1, ResultComment: "Result"}
	confirmation, err := s.CompleteODOAssignment(ctx, 3, initial.Task.ID, command, "verified-token")
	require.NoError(t, err)
	items, err := s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, "assigned", items[0].SourceStatus)
	require.Equal(t, int64(1), items[0].SourceRevision)
	completed := source
	completed.EventID = confirmation.EventID
	completed.Revision = 2
	completed.EventType = "status_updated"
	completed.Status = "completed"
	completed.CompletedAt = &confirmation.CompletedAt
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, completed, "https://odo.example.test")
	require.NoError(t, err)
	items, err = s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, "completed", items[0].SourceStatus)
	require.Equal(t, int64(2), items[0].SourceRevision)
	stale := source
	stale.EventID = uuid.New()
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, stale, "https://odo.example.test")
	require.NoError(t, err)
	items, err = s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, "completed", items[0].SourceStatus, "late old event cannot undo completion")
	// A later source assignment replaces participants atomically. The former
	// assignee cannot replay a frozen command through the Workspace projection.
	reassigned := source
	reassigned.EventID = uuid.New()
	reassigned.Revision = 3
	reassigned.EventType = "updated"
	reassigned.AssigneeSub = "subject-4"
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, reassigned, "https://odo.example.test")
	require.NoError(t, err)
	old, err := s.ListMyDocumentologAssignments(ctx, 3)
	require.NoError(t, err)
	require.Empty(t, old)
	next, err := s.ListMyDocumentologAssignments(ctx, 4)
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, initial.Task.ID, next[0].ID)
	_, err = s.CompleteODOAssignment(ctx, 3, initial.Task.ID, command, "verified-token")
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 1, calls)
	var revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT source_revision FROM tasks WHERE id=$1`, initial.Task.ID).Scan(&revision))
	require.Equal(t, int64(3), revision)
}
