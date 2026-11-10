package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestODOCommandHTTPConfirmation(t *testing.T) {
	command := domain.ODOCompleteCommand{CommandID: uuid.New(), OrganizationID: uuid.New(), IdentityIssuer: "https://id.example.test", ActorSub: "executor", DocumentID: "doc", ExpectedRevision: 4, ResultComment: "Результат"}
	for _, scenario := range []string{"confirmed", "duplicate", "foreign_receipt", "wrong_revision", "invalid_json", "error", "timeout", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("user token followed redirect") }))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer user-access-token", r.Header.Get("Authorization"))
				require.Equal(t, "odo-command:"+command.CommandID.String(), r.Header.Get("Idempotency-Key"))
				require.Equal(t, "/api/v1/workspace/assignments/asg/complete", r.URL.Path)
				var received domain.ODOCompleteCommand
				require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
				require.Equal(t, command, received)
				switch scenario {
				case "error":
					http.Error(w, "sensitive provider error", 409)
					return
				case "redirect":
					http.Redirect(w, r, destination.URL, 302)
					return
				case "timeout":
					time.Sleep(60 * time.Millisecond)
				case "invalid_json":
					w.Write([]byte(`{"confirmed":true}`))
					return
				}
				receipt := domain.ODOCompleteReceipt{CommandID: command.CommandID, OrganizationID: command.OrganizationID, IdentityIssuer: command.IdentityIssuer, ActorSub: command.ActorSub, AssignmentID: "asg", DocumentID: command.DocumentID, ExpectedRevision: 4, Revision: 5, Status: "completed", EventID: uuid.New(), CompletedAt: time.Now().UTC(), Outcome: "completed"}
				switch scenario {
				case "duplicate":
					receipt.Outcome = "duplicate"
				case "foreign_receipt":
					receipt.OrganizationID = uuid.New()
				case "wrong_revision":
					receipt.Revision = 6
				}
				json.NewEncoder(w).Encode(receipt)
			}))
			defer server.Close()
			timeout := time.Second
			if scenario == "timeout" {
				timeout = 10 * time.Millisecond
			}
			client, err := NewODOCommandClient(server.URL, command.IdentityIssuer, true, timeout)
			require.NoError(t, err)
			receipt, err := client.complete(context.Background(), "asg", command, "user-access-token")
			if scenario == "confirmed" || scenario == "duplicate" {
				require.NoError(t, err)
				require.Equal(t, int64(5), receipt.Revision)
			} else {
				require.Error(t, err)
				require.Nil(t, receipt)
				require.NotContains(t, err.Error(), "sensitive provider error")
				if scenario == "error" {
					require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
				}
			}
			require.Zero(t, calls)
		})
	}
}

func TestODOCommandsPreserveProjectionAndCheckAccessIntegration(t *testing.T) {
	ctx, db, s, org, _ := newODOFixture(t)
	event := odoTestEvent(org)
	original, err := s.ApplyODOAssignmentEvent(ctx, 1, event, "https://odo.example.test")
	require.NoError(t, err)
	command := domain.ODOCompleteRequest{CommandID: uuid.New(), ExpectedRevision: 1, ResultComment: "Результат выполнения"}
	calls := 0
	completion := time.Now().UTC()
	eventID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var received domain.ODOCompleteCommand
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		require.Equal(t, "subject-3", received.ActorSub)
		require.Equal(t, org, received.OrganizationID)
		receipt := domain.ODOCompleteReceipt{CommandID: received.CommandID, OrganizationID: org, IdentityIssuer: event.IdentityIssuer, ActorSub: received.ActorSub, AssignmentID: event.AssignmentID, DocumentID: event.DocumentID, ExpectedRevision: 1, Revision: 2, Status: "completed", EventID: eventID, CompletedAt: completion, Outcome: "completed"}
		if calls > 1 {
			receipt.Outcome = "duplicate"
		}
		json.NewEncoder(w).Encode(receipt)
	}))
	defer server.Close()
	client, err := NewODOCommandClient(server.URL, event.IdentityIssuer, true, time.Second)
	require.NoError(t, err)
	s.SetODOCommandClient(client)
	for _, actor := range []int64{2, 4, 6} {
		_, err = s.CompleteODOAssignment(ctx, actor, original.Task.ID, command, "user-access-token")
		require.Error(t, err)
	}
	require.Zero(t, calls)
	receipt, err := s.CompleteODOAssignment(ctx, 3, original.Task.ID, command, "user-access-token")
	require.NoError(t, err)
	require.Equal(t, int64(2), receipt.Revision)
	var status string
	var revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT source_status,source_revision FROM tasks WHERE id=$1`, original.Task.ID).Scan(&status, &revision))
	require.Equal(t, "assigned", status)
	require.Equal(t, int64(1), revision, "HTTP confirmation must not forge a projection update")
	event.EventID = eventID
	event.Revision = 2
	event.EventType = "status_updated"
	event.Status = "completed"
	event.CompletedAt = &completion
	_, err = s.ApplyODOAssignmentEvent(ctx, 1, event, "https://odo.example.test")
	require.NoError(t, err)
	_, err = s.CompleteODOAssignment(ctx, 3, original.Task.ID, command, "user-access-token")
	require.NoError(t, err, "same command remains retryable after projection caught up")
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=3`)
	require.NoError(t, err)
	_, err = s.CompleteODOAssignment(ctx, 3, original.Task.ID, command, "user-access-token")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=3;UPDATE organization_modules SET enabled=false`)
	require.NoError(t, err)
	_, err = s.CompleteODOAssignment(ctx, 3, original.Task.ID, command, "user-access-token")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 2, calls)
}

func TestODOCommandManagedPermissionIntegration(t *testing.T) {
	ctx, db, s, org, _ := newODOFixture(t)
	original, err := s.ApplyODOAssignmentEvent(ctx, 1, odoTestEvent(org), "https://odo.example.test")
	require.NoError(t, err)
	position := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organization_access_apps VALUES($1,'odo',true)`, org)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_access_assignments VALUES($1,3,$2)`, org, position)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_position_permissions VALUES($1,$2,'odo',ARRAY['document.read','assignment.read'])`, org, position)
	require.NoError(t, err)
	request := domain.ODOCompleteRequest{CommandID: uuid.New(), ExpectedRevision: 1, ResultComment: "done"}
	_, err = s.CompleteODOAssignment(ctx, 3, original.Task.ID, request, "user-token")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE organization_position_permissions SET permissions=ARRAY['document.read','assignment.read','assignment.complete']`)
	require.NoError(t, err)
	_, err = s.CompleteODOAssignment(ctx, 3, original.Task.ID, request, "user-token")
	require.Equal(t, 503, apperrors.ToAppError(err).StatusCode, "passed access check but commands disabled")
}
