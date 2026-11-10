package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestSecurityAuditIsolationFiltersAndImmutability(t *testing.T) {
	ctx, db, a, b := newAdministrationStorageTestDB(t)
	_, err := db.Exec(ctx, `INSERT INTO organization_admin_audit(organization_id,actor_id,action,object_type,object_id) VALUES($1,1,'organization.update','organization',$2)`, a, a.String())
	require.NoError(t, err)
	data, err := os.ReadFile("../../db/migrations/20261012000500_security_audit.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	r := NewOrganizationRepository(db, zap.NewNop())
	event := func(actor *int64, org *uuid.UUID, ip, action string) {
		t.Helper()
		require.NoError(t, r.RecordSecurityEvent(ctx, domain.SecurityAuditEvent{ActorID: actor, OrganizationID: org, IP: &ip, Action: action, Outcome: "success", RequestID: "safe-request", Method: "POST", Route: "/api/v1/drive/files", Status: 201}))
	}
	actorA, actorB := int64(3), int64(5)
	event(&actorA, nil, "192.0.2.10", "file.files.create")
	event(&actorA, nil, "2001:db8::1", "task.tasks.update")
	event(&actorB, nil, "192.0.2.20", "file.files.create")
	event(nil, nil, "192.0.2.30", "auth.login")
	f := domain.SecurityAuditFilter{Limit: 100}
	own, err := r.SecurityAudit(ctx, 2, nil, false, f)
	require.NoError(t, err)
	require.Len(t, own.Items, 3)
	for _, e := range own.Items {
		require.NotNil(t, e.OrganizationID)
		require.Equal(t, a, *e.OrganizationID)
	}
	require.Nil(t, own.Items[2].IP, "legacy audit does not invent an IP")
	require.NotNil(t, own.Items[2].AdministrationAuditID)
	global, err := r.SecurityAudit(ctx, 1, nil, true, f)
	require.NoError(t, err)
	require.Len(t, global.Items, 5)
	_, err = r.SecurityAudit(ctx, 3, nil, false, f)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	_, err = r.SecurityAudit(ctx, 2, &b, true, f)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	for _, tc := range []struct {
		ip    string
		count int
	}{{"192.0.2.10", 1}, {"192.0.2.0/24", 3}, {"2001:db8::/32", 1}, {"198.51.100.0/24", 0}} {
		filter := f
		filter.IP = tc.ip
		page, err := r.SecurityAudit(ctx, 1, nil, true, filter)
		require.NoError(t, err)
		require.Len(t, page.Items, tc.count)
	}
	filter := f
	filter.ActorID = &actorA
	filter.Action = "task.tasks.update"
	matched, err := r.SecurityAudit(ctx, 1, nil, true, filter)
	require.NoError(t, err)
	require.Len(t, matched.Items, 1)
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	filter.From = &from
	filter.To = &to
	matched, err = r.SecurityAudit(ctx, 1, nil, true, filter)
	require.NoError(t, err)
	require.Len(t, matched.Items, 1)
	future := time.Now().Add(2 * time.Hour)
	filter.From = &to
	filter.To = &future
	matched, err = r.SecurityAudit(ctx, 1, nil, true, filter)
	require.NoError(t, err)
	require.Empty(t, matched.Items)
	cursor := f
	cursor.Limit = 1
	first, err := r.SecurityAudit(ctx, 1, nil, true, cursor)
	require.NoError(t, err)
	require.NotNil(t, first.NextBeforeID)
	event(&actorA, nil, "192.0.2.11", "file.files.create")
	cursor.MaxID = first.SnapshotID
	cursor.BeforeID = *first.NextBeforeID
	second, err := r.SecurityAudit(ctx, 1, nil, true, cursor)
	require.NoError(t, err)
	require.Less(t, second.Items[0].ID, first.Items[0].ID)
	for _, query := range []string{`UPDATE security_audit_events SET action='forged'`, `DELETE FROM security_audit_events`, `TRUNCATE security_audit_events`} {
		_, err = db.Exec(ctx, query)
		require.Error(t, err)
	}
	// Transactional administration mirror gets trusted HTTP metadata and rolls back with its source.
	tx, err := db.Begin(ctx)
	require.NoError(t, err)
	requestCtx := domain.WithSecurityMetadata(ctx, &domain.SecurityRequestMetadata{IP: "192.0.2.44", RequestID: "request-44"})
	require.NoError(t, writeAdministrationAudit(requestCtx, tx, a, 1, "organization.update", "organization", a.String(), nil, nil))
	require.NoError(t, tx.Commit(ctx))
	filter = f
	filter.IP = "192.0.2.44"
	matched, err = r.SecurityAudit(ctx, 2, nil, false, filter)
	require.NoError(t, err)
	require.Len(t, matched.Items, 1)
	require.Equal(t, "request-44", matched.Items[0].RequestID)
	tx, err = db.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, writeAdministrationAudit(requestCtx, tx, a, 1, "organization.update", "organization", a.String(), nil, nil))
	require.NoError(t, tx.Rollback(ctx))
	matched, err = r.SecurityAudit(ctx, 2, nil, false, filter)
	require.NoError(t, err)
	require.Len(t, matched.Items, 1)

	// A rejected security mirror must reject its source administration write.
	var beforeCount, afterCount int64
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit`).Scan(&beforeCount))
	_, err = db.Exec(ctx, `ALTER TABLE security_audit_events ADD CONSTRAINT reject_security_mirror CHECK(action <> 'admin.organization.update') NOT VALID`)
	require.NoError(t, err)
	tx, err = db.Begin(ctx)
	require.NoError(t, err)
	require.Error(t, writeAdministrationAudit(requestCtx, tx, a, 1, "organization.update", "organization", a.String(), nil, nil))
	require.NoError(t, tx.Rollback(ctx))
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit`).Scan(&afterCount))
	require.Equal(t, beforeCount, afterCount)
	_, err = db.Exec(ctx, `ALTER TABLE security_audit_events DROP CONSTRAINT reject_security_mirror`)
	require.NoError(t, err)
	// Recorded tenant is a snapshot: changing a user's company does not move old events.
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=3`, b)
	require.NoError(t, err)
	filter = f
	filter.ActorID = &actorA
	matched, err = r.SecurityAudit(ctx, 2, nil, false, filter)
	require.NoError(t, err)
	require.Len(t, matched.Items, 3)
	_, err = db.Exec(ctx, `UPDATE users SET role='user' WHERE id=1`)
	require.NoError(t, err)
	_, err = r.SecurityAudit(context.Background(), 1, nil, true, f)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
}
