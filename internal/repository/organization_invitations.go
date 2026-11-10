package repository

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const invitationColumns = `i.id,i.organization_id,o.name,i.email,i.team_id,
 CASE WHEN i.status='pending' AND i.expires_at<=now() THEN 'expired' ELSE i.status END,
 i.expires_at,i.created_at,i.accepted_at,i.accepted_by,i.registered_account,
 i.code_hash,i.code_expires_at,i.code_sent_at,i.code_attempts`

func scanInvitation(row pgx.Row) (*domain.OrganizationInvitation, error) {
	var i domain.OrganizationInvitation
	err := row.Scan(&i.ID, &i.OrganizationID, &i.OrganizationName, &i.Email, &i.TeamID, &i.Status, &i.ExpiresAt, &i.CreatedAt, &i.AcceptedAt, &i.AcceptedBy, &i.RegisteredAccount, &i.CodeHash, &i.CodeExpiresAt, &i.CodeSentAt, &i.CodeAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.NotFound("invitation")
	}
	return &i, err
}

func invitationPending(i *domain.OrganizationInvitation) error {
	if i.Status != "pending" {
		return apperrors.New(apperrors.ErrCodeInvitationUnavailable, "invitation unavailable", 409)
	}
	return nil
}

// Organization before invitation is the lock order for every mutation, including acceptance.
func (r *OrganizationRepository) invitationTx(ctx context.Context, tokenHash string) (pgx.Tx, *domain.OrganizationInvitation, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (pgx.Tx, *domain.OrganizationInvitation, error) {
		_ = tx.Rollback(ctx)
		return nil, nil, e
	}
	var org uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT organization_id FROM organization_invitations WHERE token_hash=$1`, tokenHash).Scan(&org); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperrors.NotFound("invitation")
		}
		return fail(err)
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&status); err != nil {
		return fail(err)
	}
	if status != "active" {
		return fail(apperrors.Forbidden("Организация недоступна"))
	}
	i, err := scanInvitation(tx.QueryRow(ctx, `SELECT `+invitationColumns+` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id WHERE i.token_hash=$1 FOR UPDATE OF i`, tokenHash))
	if err != nil {
		return fail(err)
	}
	return tx, i, nil
}

func (r *OrganizationRepository) InvitationPreview(ctx context.Context, tokenHash string) (*domain.OrganizationInvitation, error) {
	i, err := scanInvitation(r.db.QueryRow(ctx, `SELECT `+invitationColumns+` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id WHERE i.token_hash=$1 AND o.status='active'`, tokenHash))
	if err != nil {
		return nil, err
	}
	if err = invitationPending(i); err != nil {
		return nil, err
	}
	return i, nil
}

func (r *OrganizationRepository) ListInvitations(ctx context.Context, actor int64, org *uuid.UUID, limit, offset int) (*domain.InvitationPage, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger}
	scope, err := inner.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	out := &domain.InvitationPage{Items: []domain.OrganizationInvitation{}}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_invitations WHERE organization_id=$1`, scope.ID).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+invitationColumns+` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id WHERE i.organization_id=$1 ORDER BY i.created_at DESC,i.id LIMIT $2 OFFSET $3`, scope.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		i, e := scanInvitation(rows)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, *i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// send executes before commit. An SMTP failure rolls back rotation, leaving the prior link valid.
func (r *OrganizationRepository) IssueInvitation(ctx context.Context, actor int64, org *uuid.UUID, id *uuid.UUID, req domain.CreateInvitationRequest, hash string, send func(*domain.OrganizationInvitation) error) (*domain.OrganizationInvitation, error) {
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var owner *int64
	var status string
	if err = tx.QueryRow(ctx, `SELECT owner_id,status FROM organizations WHERE id=$1 FOR UPDATE`, scope.ID).Scan(&owner, &status); err != nil {
		return nil, err
	}
	if err = authorizeAdministrationLocked(ctx, tx, actor, owner, org != nil); err != nil {
		return nil, err
	}
	if status != "active" {
		return nil, apperrors.Conflict("Организация недоступна")
	}
	// Re-resolve current-company scope under the actor lock; a concurrent transfer cannot cross scopes.
	if org == nil {
		var current *uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT organization_id FROM users WHERE id=$1`, actor).Scan(&current); err != nil {
			return nil, err
		}
		if current != nil && *current != scope.ID {
			return nil, apperrors.Forbidden("Компания пользователя изменилась")
		}
	}
	action := "invitation.create"
	inviteID := uuid.New()
	if id != nil {
		inviteID = *id
		action = "invitation.resend"
		old, e := scanInvitation(tx.QueryRow(ctx, `SELECT `+invitationColumns+` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id WHERE i.id=$1 AND i.organization_id=$2 FOR UPDATE OF i`, inviteID, scope.ID))
		if e != nil {
			return nil, e
		}
		if old.Status != "pending" && old.Status != "expired" {
			return nil, apperrors.Conflict("Приглашение уже закрыто")
		}
		if old.CreatedAt.After(time.Now().Add(-time.Minute)) {
			return nil, apperrors.TooManyRequests("Повторите отправку через минуту")
		}
		req.Email = old.Email
		req.TeamID = old.TeamID
	} else {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_invitations WHERE organization_id=$1 AND email=$2 AND status='pending' AND expires_at>now())`, scope.ID, req.Email).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, apperrors.New(apperrors.ErrCodeInvitationAlreadyExists, "invitation already exists", 409)
		}
		if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET status='revoked',updated_at=now() WHERE organization_id=$1 AND email=$2 AND status='pending'`, scope.ID, req.Email); err != nil {
			return nil, err
		}
	}
	if req.TeamID != nil {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL)`, *req.TeamID, scope.ID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, apperrors.Validation("Подразделение не принадлежит организации")
		}
	}
	if id == nil {
		_, err = tx.Exec(ctx, `INSERT INTO organization_invitations(id,organization_id,email,team_id,created_by,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '7 days')`, inviteID, scope.ID, req.Email, req.TeamID, actor, hash)
	} else {
		_, err = tx.Exec(ctx, `UPDATE organization_invitations SET token_hash=$1,expires_at=now()+interval '7 days',created_at=now(),updated_at=now(),code_hash=NULL,code_expires_at=NULL,code_sent_at=NULL,code_attempts=0 WHERE id=$2`, hash, inviteID)
	}
	if err != nil {
		return nil, err
	}
	i, err := scanInvitation(tx.QueryRow(ctx, `SELECT `+invitationColumns+` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id WHERE i.id=$1`, inviteID))
	if err != nil {
		return nil, err
	}
	if send != nil {
		if err = send(i); err != nil {
			return nil, err
		}
	}
	after, _ := json.Marshal(map[string]any{"status": "pending", "team_id": i.TeamID})
	if err = writeAdministrationAudit(ctx, tx, scope.ID, actor, action, "invitation", i.ID.String(), nil, after); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return i, nil
}

func (r *OrganizationRepository) RevokeInvitation(ctx context.Context, actor int64, org *uuid.UUID, id uuid.UUID) error {
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner *int64
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM organizations WHERE id=$1 FOR UPDATE`, scope.ID).Scan(&owner); err != nil {
		return err
	}
	if err = authorizeAdministrationLocked(ctx, tx, actor, owner, org != nil); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE organization_invitations SET status='revoked',code_hash=NULL,updated_at=now() WHERE id=$1 AND organization_id=$2 AND status='pending'`, id, scope.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperrors.Conflict("Приглашение не найдено или уже закрыто")
	}
	if err = writeAdministrationAudit(ctx, tx, scope.ID, actor, "invitation.revoke", "invitation", id.String(), nil, json.RawMessage(`{"status":"revoked"}`)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *OrganizationRepository) SendInvitationCode(ctx context.Context, hash, codeHash string, send func(*domain.OrganizationInvitation) error) error {
	tx, i, err := r.invitationTx(ctx, hash)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = invitationPending(i); err != nil {
		return err
	}
	if i.CodeSentAt != nil && time.Since(*i.CodeSentAt) < time.Minute {
		return apperrors.TooManyRequests("Повторите отправку через минуту")
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET code_hash=$1,code_expires_at=now()+interval '10 minutes',code_sent_at=now(),code_attempts=0 WHERE id=$2`, codeHash, i.ID); err != nil {
		return err
	}
	if err = send(i); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// makeUser only provisions a disabled SSO identity. The local insert and invitation consumption are one transaction.
func (r *OrganizationRepository) AcceptInvitation(ctx context.Context, hash, codeHash string, actor int64, candidate *domain.User, makeUser func(*domain.OrganizationInvitation, *domain.User) error) (*domain.InvitationResult, error) {
	tx, i, err := r.invitationTx(ctx, hash)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if i.CodeHash == nil || i.CodeExpiresAt == nil || !i.CodeExpiresAt.After(time.Now()) || i.CodeAttempts >= 5 {
		return nil, apperrors.New(apperrors.ErrCodeInvitationCodeRequired, "new confirmation code required", 400)
	}
	if subtle.ConstantTimeCompare([]byte(*i.CodeHash), []byte(codeHash)) != 1 {
		if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET code_attempts=code_attempts+1 WHERE id=$1 AND code_attempts<5`, i.ID); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, apperrors.New(apperrors.ErrCodeInvitationCodeInvalid, "invalid confirmation code", 400)
	}
	if i.Status == "accepted" {
		if (actor > 0 && i.AcceptedBy != nil && actor == *i.AcceptedBy) || (actor == 0 && i.RegisteredAccount) {
			return &domain.InvitationResult{OrganizationID: i.OrganizationID, UserID: *i.AcceptedBy, AlreadyMember: true, Registered: i.RegisteredAccount}, nil
		}
		return nil, apperrors.Conflict("Приглашение уже принято")
	}
	if err = invitationPending(i); err != nil {
		return nil, err
	}
	// Email ownership survives corporate mailbox sync. Do not create a second
	// identity or attach the contact to a different signed-in account.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "new-user:email:"+normalizeKeycloakEmail(i.Email)); err != nil {
		return nil, err
	}
	var contactOwner *int64
	if err = tx.QueryRow(ctx, `SELECT (SELECT user_id FROM user_verified_contacts WHERE email=$1)`, i.Email).Scan(&contactOwner); err != nil {
		return nil, err
	}
	if contactOwner != nil && (actor == 0 || actor != *contactOwner) {
		return nil, apperrors.InvitationIdentityConflict()
	}
	if actor > 0 {
		var otherAccount bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(trim(email))=$1 AND id<>$2)`, i.Email, actor).Scan(&otherAccount); err != nil {
			return nil, err
		}
		if otherAccount {
			return nil, apperrors.InvitationIdentityConflict()
		}
	}
	if i.TeamID != nil {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL)`, i.TeamID, i.OrganizationID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, apperrors.Conflict("Подразделение удалено. Запросите новое приглашение")
		}
	}
	result := &domain.InvitationResult{OrganizationID: i.OrganizationID}
	if actor > 0 {
		var active, blocked, ownsOtherCompany bool
		var userOrg *uuid.UUID
		var role string
		err = tx.QueryRow(ctx, `SELECT u.is_active,u.organization_id,u.role,EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id),EXISTS(SELECT 1 FROM organizations owned WHERE owned.owner_id=u.id AND owned.id<>$2 AND owned.status<>'deleted') FROM users u WHERE u.id=$1 AND u.deleted_at IS NULL FOR UPDATE OF u`, actor, i.OrganizationID).Scan(&active, &userOrg, &role, &blocked, &ownsOtherCompany)
		if err != nil {
			return nil, err
		}
		if !active || blocked || role == "admin" || (userOrg == nil && role != "user") {
			return nil, apperrors.Forbidden("Этот аккаунт нельзя подключить по приглашению")
		}
		if ownsOtherCompany || (userOrg != nil && *userOrg != i.OrganizationID) {
			return nil, apperrors.New(apperrors.ErrCodeInvitationDifferentOrganization, "account belongs to another company", 409)
		}
		result.UserID = actor
		result.AlreadyMember = userOrg != nil
		if userOrg == nil {
			if _, err = tx.Exec(ctx, `UPDATE users SET organization_id=$1,team_id=$2,manager_id=NULL,position=NULL,updated_at=now() WHERE id=$3`, i.OrganizationID, i.TeamID, actor); err != nil {
				return nil, err
			}
		}
	} else {
		if candidate == nil || candidate.IIN == nil || makeUser == nil {
			return nil, apperrors.Validation("Данные регистрации обязательны")
		}
		candidate.Email = i.Email
		candidate.OrganizationID = &i.OrganizationID
		candidate.TeamID = i.TeamID
		candidate.Role = domain.RoleUser
		candidate.IsActive = true
		candidate.IsFirstLogin = false
		keys := []string{"email:" + normalizeKeycloakEmail(candidate.Email), "username:" + normalizeKeycloakUsername(candidate.Username), "iin:" + *candidate.IIN}
		if key := normalizeKeycloakIIN(candidate.Username); key != "" {
			keys = append(keys, "iin:"+key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "new-user:"+key); err != nil {
				return nil, err
			}
		}
		if err = checkNewUserIdentity(ctx, tx, candidate.Email, candidate.Username, *candidate.IIN); err != nil {
			if errors.Is(err, ErrUserIdentityConflict) {
				return nil, apperrors.InvitationIdentityConflict()
			}
			return nil, err
		}
		// A revoked/expired invitation may have left a disabled SSO identity after
		// a remote failure. Only unfinished invitations for this same company/contact
		// can authorize resuming it after a fresh email confirmation.
		rows, e := tx.Query(ctx, `SELECT id::text FROM organization_invitations WHERE organization_id=$1 AND email=$2 AND accepted_by IS NULL`, i.OrganizationID, i.Email)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			i.ResumableIDs = append(i.ResumableIDs, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if err = makeUser(i, candidate); err != nil {
			return nil, err
		}
		users := &UserRepository{logger: r.logger}
		if err = users.createUser(ctx, tx, candidate); err != nil {
			return nil, err
		}
		// The shared insert defaults to a temporary admin-assigned password; this user chose their own.
		if _, err = tx.Exec(ctx, `UPDATE users SET is_first_login=false WHERE id=$1`, candidate.ID); err != nil {
			return nil, err
		}
		result.UserID = candidate.ID
		result.Registered = true
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_verified_contacts(user_id,email,source) VALUES($1,$2,'organization_invitation') ON CONFLICT(user_id,email) DO NOTHING`, result.UserID, i.Email); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET status='accepted',accepted_by=$1,accepted_at=now(),registered_account=$2,activation_pending=$2,updated_at=now() WHERE id=$3`, result.UserID, result.Registered, i.ID); err != nil {
		return nil, err
	}
	after, _ := json.Marshal(map[string]any{"status": "accepted", "user_id": result.UserID})
	if err = writeAdministrationAudit(ctx, tx, i.OrganizationID, result.UserID, "invitation.accept", "invitation", i.ID.String(), nil, after); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Recheck account/company blocks under locks before enabling a newly registered SSO identity.
// A retry after a lost commit/HTTP response never creates another identity or changes its password.
func (r *OrganizationRepository) ActivateInvitationAccount(ctx context.Context, hash string, userID int64, enable func(string) error) error {
	tx, i, err := r.invitationTx(ctx, hash)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if i.Status != "accepted" || !i.RegisteredAccount || i.AcceptedBy == nil || *i.AcceptedBy != userID {
		return apperrors.Forbidden("Приглашение не принято")
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT activation_pending FROM organization_invitations WHERE id=$1`, i.ID).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	var kcID string
	var active, blocked bool
	var org *uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT u.keycloak_id,u.is_active,u.organization_id,EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id) FROM users u WHERE u.id=$1 AND u.deleted_at IS NULL FOR UPDATE OF u`, userID).Scan(&kcID, &active, &org, &blocked); err != nil {
		return err
	}
	if !active || blocked || org == nil || *org != i.OrganizationID {
		return apperrors.Forbidden("Аккаунт недоступен")
	}
	if err = enable(kcID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET activation_pending=false WHERE id=$1`, i.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *OrganizationRepository) PendingInvitationActivations(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT i.token_hash,i.accepted_by FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id JOIN users u ON u.id=i.accepted_by WHERE i.activation_pending AND i.status='accepted' AND o.status='active' AND u.is_active AND u.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id) ORDER BY i.accepted_at LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var hash string
		var id int64
		if err = rows.Scan(&hash, &id); err != nil {
			return nil, err
		}
		out[hash] = id
	}
	return out, rows.Err()
}
