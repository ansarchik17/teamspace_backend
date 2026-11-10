package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func invitationFixture(t *testing.T) (context.Context, *OrganizationRepository, *pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	_, db, a, b := userCreationFixture(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `ALTER TABLE organizations ADD COLUMN slug TEXT DEFAULT 'company',ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now();
    CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID REFERENCES organizations(id),deleted_at TIMESTAMPTZ);
    CREATE TABLE organization_user_blocks(user_id BIGINT PRIMARY KEY REFERENCES users(id));`)
	require.NoError(t, err)
	for _, file := range []string{"20261012000100_company_administration_audit.sql", "20261025000100_organization_invitations.sql"} {
		data, e := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, e)
	}
	return ctx, NewOrganizationRepository(db, zap.NewNop()), db, a, b
}
func issueTestInvite(t *testing.T, ctx context.Context, r *OrganizationRepository, email, hash string) *domain.OrganizationInvitation {
	t.Helper()
	i, err := r.IssueInvitation(ctx, 1, nil, nil, domain.CreateInvitationRequest{Email: email}, hash, nil)
	require.NoError(t, err)
	return i
}
func sendTestCode(t *testing.T, ctx context.Context, r *OrganizationRepository, hash string) {
	t.Helper()
	require.NoError(t, r.SendInvitationCode(ctx, hash, "proof", func(*domain.OrganizationInvitation) error { return nil }))
}

func TestInvitationAdministrationAndRotation(t *testing.T) {
	ctx, r, db, a, b := invitationFixture(t)
	foreignTeam := uuid.New()
	_, err := db.Exec(ctx, `INSERT INTO teams(id,organization_id) VALUES($1,$2)`, foreignTeam, b)
	require.NoError(t, err)
	_, err = r.IssueInvitation(ctx, 1, nil, nil, domain.CreateInvitationRequest{Email: "person@example.invalid", TeamID: &foreignTeam}, "foreign", nil)
	require.Error(t, err)
	i := issueTestInvite(t, ctx, r, "person@example.invalid", "initial")
	_, err = r.ListInvitations(ctx, 2, &a, 10, 0)
	require.Error(t, err)
	_, err = r.IssueInvitation(ctx, 2, &a, nil, domain.CreateInvitationRequest{Email: i.Email}, "attack", nil)
	require.Error(t, err)
	_, err = r.IssueInvitation(ctx, 1, nil, nil, domain.CreateInvitationRequest{Email: i.Email}, "duplicate", nil)
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_invitations SET created_at=now()-interval '2 minutes' WHERE id=$1`, i.ID)
	require.NoError(t, err)
	_, err = r.IssueInvitation(ctx, 1, nil, &i.ID, domain.CreateInvitationRequest{}, "failed", func(*domain.OrganizationInvitation) error { return errors.New("smtp unavailable") })
	require.Error(t, err)
	_, err = r.InvitationPreview(ctx, "initial")
	require.NoError(t, err)
	_, err = r.InvitationPreview(ctx, "failed")
	require.Error(t, err)
	_, err = r.IssueInvitation(ctx, 1, nil, &i.ID, domain.CreateInvitationRequest{}, "rotated", nil)
	require.NoError(t, err)
	_, err = r.InvitationPreview(ctx, "initial")
	require.Error(t, err)
	sendTestCode(t, ctx, r, "rotated")
	page, err := r.ListInvitations(ctx, 1, nil, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	data, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(data), "proof")
	require.NotContains(t, string(data), "code_hash")
	require.NotContains(t, string(data), "rotated")
	require.NoError(t, r.RevokeInvitation(ctx, 1, nil, i.ID))
	_, err = r.InvitationPreview(ctx, "rotated")
	require.Error(t, err)
	var audits int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit`).Scan(&audits))
	require.Equal(t, 3, audits)
}

func TestInvitationProofLimitsAndDeliveryRollback(t *testing.T) {
	ctx, r, db, _, _ := invitationFixture(t)
	i := issueTestInvite(t, ctx, r, "proof@example.invalid", "secret")
	err := r.SendInvitationCode(ctx, "secret", "failed", func(*domain.OrganizationInvitation) error { return errors.New("mail failed") })
	require.Error(t, err)
	var code *string
	require.NoError(t, db.QueryRow(ctx, `SELECT code_hash FROM organization_invitations WHERE id=$1`, i.ID).Scan(&code))
	require.Nil(t, code)
	sendTestCode(t, ctx, r, "secret")
	require.Error(t, r.SendInvitationCode(ctx, "secret", "second", func(*domain.OrganizationInvitation) error { return nil }))
	for n := 0; n < 5; n++ {
		_, err = r.AcceptInvitation(ctx, "secret", "wrong", 1, nil, nil)
		require.Error(t, err)
	}
	_, err = r.AcceptInvitation(ctx, "secret", "proof", 1, nil, nil)
	require.Error(t, err)
	var attempts int
	require.NoError(t, db.QueryRow(ctx, `SELECT code_attempts FROM organization_invitations WHERE id=$1`, i.ID).Scan(&attempts))
	require.Equal(t, 5, attempts)
	_, err = db.Exec(ctx, `UPDATE organization_invitations SET code_sent_at=now()-interval '2 minutes' WHERE id=$1`, i.ID)
	require.NoError(t, err)
	sendTestCode(t, ctx, r, "secret")
	out, err := r.AcceptInvitation(ctx, "secret", "proof", 1, nil, nil)
	require.NoError(t, err)
	require.True(t, out.AlreadyMember)
	out, err = r.AcceptInvitation(ctx, "secret", "proof", 1, nil, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), out.UserID)
	_, err = r.AcceptInvitation(ctx, "secret", "proof", 2, nil, nil)
	require.Error(t, err)
}

func TestInvitationExistingAccountBoundaries(t *testing.T) {
	for _, state := range []string{"foreign", "blocked", "inactive", "deleted", "platform", "owner_without_company", "unassigned"} {
		t.Run(state, func(t *testing.T) {
			ctx, r, db, a, _ := invitationFixture(t)
			issueTestInvite(t, ctx, r, "join@example.invalid", "secret")
			sendTestCode(t, ctx, r, "secret")
			if state != "foreign" {
				_, err := db.Exec(ctx, `UPDATE users SET organization_id=NULL WHERE id=2`)
				require.NoError(t, err)
			}
			switch state {
			case "blocked":
				_, err := db.Exec(ctx, `INSERT INTO organization_user_blocks(user_id) VALUES(2)`)
				require.NoError(t, err)
			case "inactive":
				_, err := db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=2`)
				require.NoError(t, err)
			case "deleted":
				_, err := db.Exec(ctx, `UPDATE users SET deleted_at=now() WHERE id=2`)
				require.NoError(t, err)
			case "platform":
				_, err := db.Exec(ctx, `UPDATE users SET role='admin' WHERE id=2`)
				require.NoError(t, err)
			}
			actor := int64(2)
			if state == "unassigned" {
				require.NoError(t, db.QueryRow(ctx, `INSERT INTO users(email,username,role,is_active,position,manager_id)VALUES('unassigned@example.invalid','unassigned','user',true,'Old position',1)RETURNING id`).Scan(&actor))
			}
			result, err := r.AcceptInvitation(ctx, "secret", "proof", actor, nil, nil)
			if state == "unassigned" {
				require.NoError(t, err)
				require.Equal(t, a, result.OrganizationID)
				var org uuid.UUID
				require.NoError(t, db.QueryRow(ctx, `SELECT organization_id FROM users WHERE id=$1`, actor).Scan(&org))
				require.Equal(t, a, org)
			} else {
				require.Error(t, err)
				var status string
				require.NoError(t, db.QueryRow(ctx, `SELECT status FROM organization_invitations`).Scan(&status))
				require.Equal(t, "pending", status)
			}
		})
	}
}

func TestInvitationConcurrentRegistrationAndActivation(t *testing.T) {
	ctx, r, db, a, _ := invitationFixture(t)
	i := issueTestInvite(t, ctx, r, "new@example.invalid", "secret")
	sendTestCode(t, ctx, r, "secret")
	var calls atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	ids := make(chan int64, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u := newUserCreationCandidate(a)
			u.Username = "new.employee"
			out, err := r.AcceptInvitation(ctx, "secret", "proof", 0, u, func(_ *domain.OrganizationInvitation, user *domain.User) error {
				calls.Add(1)
				kcID := "new-kc-id"
				user.KeycloakID = &kcID
				time.Sleep(15 * time.Millisecond)
				return nil
			})
			errs <- err
			if err == nil {
				ids <- out.UserID
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), calls.Load())
	var accepted int64
	for id := range ids {
		if accepted == 0 {
			accepted = id
		}
		require.Equal(t, accepted, id)
	}
	var count int
	var first bool
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM users WHERE email='new@example.invalid'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(ctx, `SELECT is_first_login FROM users WHERE id=$1`, accepted).Scan(&first))
	require.False(t, first)
	members, err := r.ListMembers(ctx, 1)
	require.NoError(t, err)
	var invited *domain.OrganizationMember
	for n := range members {
		if members[n].ID == accepted {
			invited = &members[n]
		}
	}
	require.NotNil(t, invited)
	require.Equal(t, "Test", invited.FirstName)
	require.Equal(t, "Testovich", invited.LastName)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM user_verified_contacts WHERE user_id=$1`, accepted).Scan(&count))
	require.Equal(t, 1, count)
	require.Error(t, r.ActivateInvitationAccount(ctx, "secret", accepted, func(string) error { return errors.New("keycloak down") }))
	pending, err := r.PendingInvitationActivations(ctx)
	require.NoError(t, err)
	require.Equal(t, accepted, pending["secret"])
	enabled := 0
	require.NoError(t, r.ActivateInvitationAccount(ctx, "secret", accepted, func(kc string) error { require.Equal(t, "new-kc-id", kc); enabled++; return nil }))
	require.NoError(t, r.ActivateInvitationAccount(ctx, "secret", accepted, func(string) error { enabled++; return nil }))
	require.Equal(t, 1, enabled)
	pending, err = r.PendingInvitationActivations(ctx)
	require.NoError(t, err)
	require.Empty(t, pending)
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM organization_invitations WHERE id=$1`, i.ID).Scan(&status))
	require.Equal(t, "accepted", status)
}

func TestInvitationFailureExpiryAndBlockedActivation(t *testing.T) {
	ctx, r, db, a, _ := invitationFixture(t)
	i := issueTestInvite(t, ctx, r, "retry@example.invalid", "secret")
	sendTestCode(t, ctx, r, "secret")
	u := newUserCreationCandidate(a)
	_, err := r.AcceptInvitation(ctx, "secret", "proof", 0, u, func(*domain.OrganizationInvitation, *domain.User) error { return errors.New("identity service down") })
	require.Error(t, err)
	_, err = r.InvitationPreview(ctx, "secret")
	require.NoError(t, err)
	out, err := r.AcceptInvitation(ctx, "secret", "proof", 0, u, func(_ *domain.OrganizationInvitation, u *domain.User) error {
		kc := "retry-kc"
		u.KeycloakID = &kc
		return nil
	})
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_user_blocks(user_id) VALUES($1)`, out.UserID)
	require.NoError(t, err)
	require.Error(t, r.ActivateInvitationAccount(ctx, "secret", out.UserID, func(string) error { t.Fatal("blocked identity was enabled"); return nil }))
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a)
	require.NoError(t, err)
	_, err = r.InvitationPreview(ctx, "secret")
	require.Error(t, err)
	require.Error(t, r.ActivateInvitationAccount(ctx, "secret", out.UserID, func(string) error { t.Fatal("suspended company identity was enabled"); return nil }))
	_, err = db.Exec(ctx, `UPDATE organizations SET status='active' WHERE id=$1`, a)
	require.NoError(t, err)
	next := issueTestInvite(t, ctx, r, "expired@example.invalid", "expired")
	_, err = db.Exec(ctx, `UPDATE organization_invitations SET expires_at=now()-interval '1 second' WHERE id=$1`, next.ID)
	require.NoError(t, err)
	_, err = r.InvitationPreview(ctx, "expired")
	require.Error(t, err)
	require.Error(t, r.SendInvitationCode(ctx, "expired", "code", func(*domain.OrganizationInvitation) error { t.Fatal("expired invite sent email"); return nil }))
	require.NoError(t, r.RevokeInvitation(ctx, 1, nil, next.ID))
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM organization_invitations WHERE id=$1`, i.ID).Scan(&status))
	require.Equal(t, "accepted", status)
}

func TestInvitationVerifiedContactSurvivesCorporateEmailChange(t *testing.T) {
	ctx, r, db, a, _ := invitationFixture(t)
	issueTestInvite(t, ctx, r, "personal@example.invalid", "first")
	sendTestCode(t, ctx, r, "first")
	u := newUserCreationCandidate(a)
	joined, err := r.AcceptInvitation(ctx, "first", "proof", 0, u, func(_ *domain.OrganizationInvitation, u *domain.User) error {
		kc := "contact-kc"
		u.KeycloakID = &kc
		return nil
	})
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET email='work@company.invalid' WHERE id=$1`, joined.UserID)
	require.NoError(t, err)
	issueTestInvite(t, ctx, r, "personal@example.invalid", "second")
	sendTestCode(t, ctx, r, "second")
	candidate := newUserCreationCandidate(a)
	*candidate.IIN = "555555555555"
	_, err = r.AcceptInvitation(ctx, "second", "proof", 0, candidate, func(*domain.OrganizationInvitation, *domain.User) error {
		t.Fatal("confirmed personal email created a duplicate identity")
		return nil
	})
	require.Error(t, err)
	_, err = r.AcceptInvitation(ctx, "second", "proof", 1, nil, nil)
	require.Error(t, err, "another signed-in user took over the contact")
	accepted, err := r.AcceptInvitation(ctx, "second", "proof", joined.UserID, nil, nil)
	require.NoError(t, err)
	require.True(t, accepted.AlreadyMember)
}

func TestInvitationRevokedAdministrationAndArchivedUnit(t *testing.T) {
	ctx, r, db, a, _ := invitationFixture(t)
	_, err := db.Exec(ctx, `INSERT INTO users(keycloak_id,email,username,role,is_active,organization_id)VALUES('ordinary','ordinary@example.invalid','ordinary','user',true,$1)`, a)
	require.NoError(t, err)
	_, err = r.ListInvitations(ctx, 3, nil, 10, 0)
	require.Error(t, err)
	_, err = r.IssueInvitation(ctx, 3, nil, nil, domain.CreateInvitationRequest{Email: "other@example.invalid"}, "unauthorized", nil)
	require.Error(t, err)
	team := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO teams(id,organization_id)VALUES($1,$2)`, team, a)
	require.NoError(t, err)
	i, err := r.IssueInvitation(ctx, 1, nil, nil, domain.CreateInvitationRequest{Email: "team@example.invalid", TeamID: &team}, "team", nil)
	require.NoError(t, err)
	sendTestCode(t, ctx, r, "team")
	_, err = db.Exec(ctx, `UPDATE teams SET deleted_at=now() WHERE id=$1`, team)
	require.NoError(t, err)
	_, err = r.AcceptInvitation(ctx, "team", "proof", 1, nil, nil)
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE organizations SET owner_id=3 WHERE id=$1`, a)
	require.NoError(t, err)
	require.Error(t, r.RevokeInvitation(ctx, 1, nil, i.ID), "revoked administrator could still revoke invitations")
	require.NoError(t, r.RevokeInvitation(ctx, 3, nil, i.ID))
}
