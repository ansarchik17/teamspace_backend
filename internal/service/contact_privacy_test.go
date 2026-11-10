package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func testContactPrivacy(t *testing.T, ctx context.Context, db *pgxpool.Pool, svc *OrganizationService) {
	_, err := db.Exec(ctx, `UPDATE users SET role='manager' WHERE id=2; UPDATE users SET role='admin' WHERE id IN (3,9);
 INSERT INTO users(id,organization_id,role,full_name_local,email,username) SELECT 5,organization_id,'guest','Guest','guest@example.com','guest' FROM users WHERE id=1;
 INSERT INTO users(id,organization_id,role,full_name_local,email,username) SELECT 6,organization_id,'user','Peer','peer@example.com','peer' FROM users WHERE id=1;`)
	require.NoError(t, err)
	personalEmail, personalPhone := " private@example.net ", "+7 (701) 555-00-01"
	require.NoError(t, svc.UpdateContact(ctx, 4, 4, domain.UpdateContactRequest{PersonalEmail: &personalEmail, PersonalPhone: &personalPhone}))
	own, err := svc.GetContactProfile(ctx, 4, 4)
	require.NoError(t, err)
	require.Equal(t, domain.ContactAdmins, own.Privacy.PersonalEmailVisibility)
	require.Equal(t, "private@example.net", *own.PersonalEmail)
	require.Equal(t, "+77015550001", *own.PersonalPhone)
	for _, setting := range []domain.ContactVisibility{domain.ContactPrivate, domain.ContactAdmins, domain.ContactManagers, domain.ContactCompany} {
		require.NoError(t, svc.UpdateContact(ctx, 4, 4, domain.UpdateContactRequest{PersonalEmailVisibility: &setting, PersonalPhoneVisibility: &setting}))
		for _, actor := range []int64{1, 2, 3, 4, 5, 6} {
			profile, e := svc.GetContactProfile(ctx, actor, 4)
			require.NoError(t, e)
			allowed := actor == 4 || setting == domain.ContactCompany || (setting != domain.ContactPrivate && (actor == 1 || actor == 3)) || (setting == domain.ContactManagers && actor == 2)
			require.Equal(t, allowed, profile.PersonalEmail != nil, "setting=%s actor=%d", setting, actor)
			require.Equal(t, allowed, profile.PersonalPhone != nil, "setting=%s actor=%d", setting, actor)
			require.Equal(t, actor == 4, profile.Privacy != nil)
			require.Equal(t, "report@example.com", profile.Actions.Email.Address)
			encoded, e := json.Marshal(profile)
			require.NoError(t, e)
			if !allowed {
				require.NotContains(t, string(encoded), "private@example.net")
				require.NotContains(t, string(encoded), "77015550001")
			}
		}
	}
	// Fields have independent permissions; omitted settings and values survive unrelated writes.
	private, company := domain.ContactPrivate, domain.ContactCompany
	require.NoError(t, svc.UpdateContact(ctx, 4, 4, domain.UpdateContactRequest{PersonalEmailVisibility: &private, PersonalPhoneVisibility: &company}))
	work := "work.report@example.com"
	require.NoError(t, svc.UpdateContact(ctx, 4, 4, domain.UpdateContactRequest{WorkEmail: &work}))
	peer, err := svc.GetContactProfile(ctx, 6, 4)
	require.NoError(t, err)
	require.Nil(t, peer.PersonalEmail)
	require.NotNil(t, peer.PersonalPhone)
	for _, actor := range []int64{1, 3, 9} {
		require.Error(t, svc.UpdateContact(ctx, actor, 4, domain.UpdateContactRequest{PersonalEmailVisibility: &company}))
	}
	_, err = svc.GetContactProfile(ctx, 9, 4)
	require.Error(t, err)
	// Directory/search/reports never contain personal fields, including visible ones.
	page, err := svc.ListContacts(ctx, 6, domain.ContactFilter{Query: "private@example.net", Limit: 100})
	require.NoError(t, err)
	require.Zero(t, page.Total)
	reports, err := svc.DirectReports(ctx, 6, 1, domain.ContactFilter{Limit: 100})
	require.NoError(t, err)
	encoded, err := json.Marshal(reports)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "77015550001")
	// Revoked role/membership takes effect on the next read; no stored role decisions.
	admins := domain.ContactAdmins
	require.NoError(t, svc.UpdateContact(ctx, 4, 4, domain.UpdateContactRequest{PersonalEmailVisibility: &admins}))
	_, err = db.Exec(ctx, `UPDATE users SET role='user' WHERE id=3`)
	require.NoError(t, err)
	peer, err = svc.GetContactProfile(ctx, 3, 4)
	require.NoError(t, err)
	require.Nil(t, peer.PersonalEmail)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=FALSE WHERE id=6`)
	require.NoError(t, err)
	_, err = svc.GetContactProfile(ctx, 6, 4)
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=TRUE WHERE id=6;UPDATE users SET role='user' WHERE id IN (2,9)`)
	require.NoError(t, err)
	// Empty clears values; null pointers preserve settings. Invalid writes do not partially apply.
	empty, invalid := "", domain.ContactVisibility("ADMIN")
	require.Error(t, svc.UpdateContact(ctx, 4, 4, domain.UpdateContactRequest{PersonalEmail: &empty, PersonalEmailVisibility: &invalid}))
	require.NoError(t, svc.UpdateContact(ctx, 4, 4, domain.UpdateContactRequest{PersonalEmail: &empty, PersonalPhone: &empty}))
	own, err = svc.GetContactProfile(ctx, 4, 4)
	require.NoError(t, err)
	require.Nil(t, own.PersonalEmail)
	require.Nil(t, own.PersonalPhone)
	require.Equal(t, admins, own.Privacy.PersonalEmailVisibility)
	_, err = db.Exec(ctx, `UPDATE employee_contacts SET personal_phone_visibility='invalid' WHERE user_id=4`)
	require.Error(t, err)
	_, err = db.Exec(ctx, `UPDATE employee_contacts SET personal_phone='123' WHERE user_id=4`)
	require.Error(t, err)
}

func TestPersonalContactValidation(t *testing.T) {
	for _, value := range []string{"not an email", "Name <a@example.com>", "a@example.com\r\nBcc:x@example.com"} {
		_, err := (domain.UpdateContactRequest{PersonalEmail: &value}).Normalize()
		require.Error(t, err)
	}
	for _, value := range []string{"123", "+0123456789", "+1234567890123456"} {
		_, err := (domain.UpdateContactRequest{PersonalPhone: &value}).Normalize()
		require.Error(t, err)
	}
	for _, value := range []domain.ContactVisibility{"", "ADMIN", "unknown"} {
		_, err := (domain.UpdateContactRequest{PersonalPhoneVisibility: &value}).Normalize()
		require.Error(t, err)
	}
}
