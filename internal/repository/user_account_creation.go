package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"sort"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/domain"
)

var ErrUserIdentityConflict = errors.New("user identity already exists")

func (r *UserRepository) CanCreateUser(ctx context.Context, actor int64) (bool, error) {
	var allowed bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM users u LEFT JOIN organizations o ON o.id=u.organization_id
		WHERE u.id=$1 AND u.deleted_at IS NULL AND u.is_active
		AND (u.role='admin' OR (o.owner_id=u.id AND o.status='active'))
	)`, actor).Scan(&allowed)
	return allowed, err
}

// CheckNewUserIdentity is deliberately global: an identity already belonging to
// another company must be rejected, never claimed by employee creation.
func (r *UserRepository) CheckNewUserIdentity(ctx context.Context, email, username, iin string) error {
	return checkNewUserIdentity(ctx, r.db, email, username, iin)
}

func checkNewUserIdentity(ctx context.Context, db userRowQuerier, email, username, iin string) error {
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM users WHERE
			(NULLIF(iin, '') IS NOT NULL AND iin = ANY($1::text[]))
			OR ($2 <> '' AND LOWER(TRIM(email)) = $2)
			OR ($3 <> '' AND LOWER(REGEXP_REPLACE(TRIM(username), '\s+', '', 'g')) = $3)
	)`, []string{normalizeKeycloakIIN(iin), normalizeKeycloakIIN(normalizeKeycloakUsername(username))},
		normalizeKeycloakEmail(email), normalizeKeycloakUsername(username)).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check new user identity: %w", err)
	}
	if exists {
		return ErrUserIdentityConflict
	}
	return nil
}

// CreateNewUser only inserts. The login/linking upsert is unsafe here because it
// can overwrite an existing employee while retaining an organization's owner ID.
func (r *UserRepository) CreateNewUser(ctx context.Context, user *domain.User) error {
	if user == nil || user.KeycloakID == nil || *user.KeycloakID == "" {
		return fmt.Errorf("new user requires a Keycloak identity")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	iin := derefNormalizedKeycloakValue(user.IIN, normalizeKeycloakIIN)
	keys := []string{"email:" + normalizeKeycloakEmail(user.Email), "username:" + normalizeKeycloakUsername(user.Username)}
	if iin != "" {
		keys = append(keys, "iin:"+iin)
	}
	if usernameIIN := normalizeKeycloakIIN(normalizeKeycloakUsername(user.Username)); usernameIIN != "" {
		keys = append(keys, "iin:"+usernameIIN)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "new-user:"+key); err != nil {
			return err
		}
	}
	// Repeat after acquiring locks: another creation may have finished while
	// this request was provisioning its new SSO identity.
	if err := checkNewUserIdentity(ctx, tx, user.Email, user.Username, iin); err != nil {
		return err
	}
	if err := r.createUser(ctx, tx, user); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrUserIdentityConflict
		}
		return err
	}
	if admission := user.EmploymentAdmission; admission != nil {
		if user.OrganizationID == nil || *user.OrganizationID != admission.OrganizationID || admission.RequestID == uuid.Nil || admission.ActorID <= 0 || admission.StartDate == nil || !domain.ValidEmploymentDate(*admission.StartDate) {
			return fmt.Errorf("invalid employment admission")
		}
		result, err := tx.Exec(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,employee_id,start_date)
		 VALUES($1,$2,$3,$4,$5) ON CONFLICT(organization_id,request_id) DO UPDATE
		 SET employee_id=EXCLUDED.employee_id,start_date=EXCLUDED.start_date,updated_at=now()
		 WHERE employee_admissions.actor_id=EXCLUDED.actor_id AND employee_admissions.employee_id IS NULL`, admission.OrganizationID, admission.RequestID, admission.ActorID, user.ID, *admission.StartDate)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrUserIdentityConflict
		}
		user.EmploymentAdmission, err = employmentAdmission(ctx, tx, admission.OrganizationID, admission.RequestID)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
