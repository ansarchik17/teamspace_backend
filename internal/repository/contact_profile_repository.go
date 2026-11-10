package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) GetContactProfile(ctx context.Context, actor, target int64) (*domain.ContactProfile, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return nil, err
	}
	snapshot := &OrganizationRepository{db: txOrganizationDB{tx}, logger: r.logger}
	profile, err := snapshot.getContactProfile(ctx, actor, target)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return profile, nil
}

func (r *OrganizationRepository) getContactProfile(ctx context.Context, actor, target int64) (*domain.ContactProfile, error) {
	page, err := r.ListContacts(ctx, actor, domain.ContactFilter{IDs: []int64{target}, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, apperrors.NotFound("contact")
	}
	result := &domain.ContactProfile{Contact: page.Items[0], ManagementChain: []domain.Contact{}}
	scope, err := r.currentScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	// Mask in SQL before returning data to services or action builders. No roles supplied by callers.
	var emailVisibility, phoneVisibility domain.ContactVisibility
	err = r.db.QueryRow(ctx, `SELECT c.work_email,c.phone,
 CASE WHEN a.id=$1 OR c.personal_email_visibility='company'
  OR (c.personal_email_visibility IN ('admins','managers') AND (a.role='admin' OR a.id=o.owner_id))
  OR (c.personal_email_visibility='managers' AND a.role='manager') THEN c.personal_email END,
 CASE WHEN a.id=$1 OR c.personal_phone_visibility='company'
  OR (c.personal_phone_visibility IN ('admins','managers') AND (a.role='admin' OR a.id=o.owner_id))
  OR (c.personal_phone_visibility='managers' AND a.role='manager') THEN c.personal_phone END,
 COALESCE(c.personal_email_visibility,'admins'),COALESCE(c.personal_phone_visibility,'admins')
 FROM users a JOIN organizations o ON o.id=a.organization_id AND o.status='active'
 LEFT JOIN employee_contacts c ON c.user_id=$1
 WHERE a.id=$2 AND a.organization_id=$3 AND a.deleted_at IS NULL AND a.is_active=TRUE`, target, actor, scope.ID).
		Scan(&result.WorkEmail, &result.Phone, &result.PersonalEmail, &result.PersonalPhone, &emailVisibility, &phoneVisibility)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.Forbidden("active company membership required")
	}
	if err != nil {
		return nil, err
	}
	if actor == target {
		result.Privacy = &domain.ContactPrivacy{PersonalEmailVisibility: emailVisibility, PersonalPhoneVisibility: phoneVisibility}
	}
	// The path guard handles pre-existing bad data and never traverses another tenant.
	rows, err := r.db.Query(ctx, `WITH RECURSIVE chain AS (
 SELECT id,manager_id,ARRAY[id]::bigint[] AS path,0 AS depth,FALSE AS cycle FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL AND is_active=TRUE
 UNION ALL
 SELECT m.id,m.manager_id,c.path||m.id,c.depth+1,m.id=ANY(c.path)
 FROM chain c JOIN users m ON m.id=c.manager_id AND m.organization_id=$2 AND m.deleted_at IS NULL AND m.is_active=TRUE
 WHERE NOT c.cycle AND c.depth<100
 ) SELECT id,cycle,depth FROM chain WHERE depth>0 ORDER BY depth`, target, scope.ID)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		var cycle bool
		var depth int
		if err = rows.Scan(&id, &cycle, &depth); err != nil {
			rows.Close()
			return nil, err
		}
		if cycle || depth == 100 {
			result.HierarchyTruncated = true
			continue
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		managers, e := r.ListContacts(ctx, actor, domain.ContactFilter{IDs: ids, Limit: 100})
		if e != nil {
			return nil, e
		}
		byID := map[int64]domain.Contact{}
		for _, item := range managers.Items {
			byID[item.ID] = item
		}
		for _, id := range ids {
			if item, ok := byID[id]; ok {
				result.ManagementChain = append(result.ManagementChain, item)
			}
		}
		if len(result.ManagementChain) > 0 {
			result.Manager = &result.ManagementChain[0]
		}
	}
	reports, err := r.ListContacts(ctx, actor, domain.ContactFilter{ManagerID: &target, Limit: 1})
	if err != nil {
		return nil, err
	}
	result.DirectReportsTotal = reports.Total
	return result, nil
}

func (r *OrganizationRepository) UpdateContact(ctx context.Context, actor, target int64, req domain.UpdateContactRequest) error {
	if actor != target {
		return apperrors.Forbidden("only your own contact details can be updated")
	}
	scope, err := r.currentScope(ctx, actor)
	if err != nil {
		return err
	}
	// Insert/merge in one statement and recheck active membership at the write.
	tag, err := r.db.Exec(ctx, `INSERT INTO employee_contacts(user_id,work_email,phone,personal_email,personal_phone,personal_email_visibility,personal_phone_visibility)
 SELECT id,NULLIF($3::text,''),NULLIF($4::text,''),NULLIF($5::text,''),NULLIF($6::text,''),COALESCE($7::text,'admins'),COALESCE($8::text,'admins') FROM users
 WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL AND is_active=TRUE
 ON CONFLICT(user_id) DO UPDATE SET
 work_email=CASE WHEN $3::text IS NULL THEN employee_contacts.work_email ELSE NULLIF($3::text,'') END,
 phone=CASE WHEN $4::text IS NULL THEN employee_contacts.phone ELSE NULLIF($4::text,'') END,
 personal_email=CASE WHEN $5::text IS NULL THEN employee_contacts.personal_email ELSE NULLIF($5::text,'') END,
 personal_phone=CASE WHEN $6::text IS NULL THEN employee_contacts.personal_phone ELSE NULLIF($6::text,'') END,
 personal_email_visibility=COALESCE($7::text,employee_contacts.personal_email_visibility),
 personal_phone_visibility=COALESCE($8::text,employee_contacts.personal_phone_visibility),updated_at=NOW()`, target, scope.ID, req.WorkEmail, req.Phone, req.PersonalEmail, req.PersonalPhone, req.PersonalEmailVisibility, req.PersonalPhoneVisibility)
	if err != nil {
		return fmt.Errorf("update employee contacts: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.NotFound("contact")
	}
	return nil
}

func (r *OrganizationRepository) SetContactAvatar(ctx context.Context, actor int64, avatarURL string) error {
	scope, err := r.currentScope(ctx, actor)
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `UPDATE users SET avatar_url=$3,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL AND is_active=TRUE`, actor, scope.ID, avatarURL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperrors.NotFound("contact")
	}
	return nil
}
