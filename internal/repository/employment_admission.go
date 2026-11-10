package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func employmentAdmission(ctx context.Context, db userRowQuerier, company, request uuid.UUID) (*domain.EmploymentAdmission, error) {
	out := &domain.EmploymentAdmission{OrganizationID: company, RequestID: request}
	err := db.QueryRow(ctx, `SELECT a.actor_id,a.employee_id,a.start_date::text,a.cancelled_at IS NOT NULL,a.revision,COALESCE(s.timezone,'Asia/Almaty'),workspace_user_can_login(a.employee_id)
	 FROM employee_admissions a LEFT JOIN organization_employment_settings s ON s.organization_id=a.organization_id WHERE a.organization_id=$1 AND a.request_id=$2`, company, request).Scan(&out.ActorID, &out.EmployeeID, &out.StartDate, &out.Cancelled, &out.Revision, &out.Timezone, &out.Allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.NotFound("Employment admission not found")
	}
	return out, err
}
func (r *UserRepository) GetEmploymentAdmission(ctx context.Context, company, request uuid.UUID) (*domain.EmploymentAdmission, error) {
	return employmentAdmission(ctx, r.db, company, request)
}

// This action is reached through the authenticated iForm bridge, after a live HR
// membership check. Cancellation creates a tombstone before account creation too.
func (r *OrganizationRepository) IFormEmploymentAccess(ctx context.Context, actor int64, company, request uuid.UUID, action, start string, revision int64) (*domain.EmploymentAdmission, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "employment:"+company.String()+":"+request.String())
	if err != nil {
		return nil, err
	}
	if action == "cancel" {
		result, e := tx.Exec(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,cancelled_at) VALUES($1,$2,$3,now())
		 ON CONFLICT(organization_id,request_id) DO UPDATE SET cancelled_at=COALESCE(employee_admissions.cancelled_at,EXCLUDED.cancelled_at),revision=employee_admissions.revision+CASE WHEN employee_admissions.cancelled_at IS NULL THEN 1 ELSE 0 END,updated_at=now()
		 WHERE employee_admissions.actor_id=EXCLUDED.actor_id`, company, request, actor)
		if e != nil {
			return nil, e
		}
		if result.RowsAffected() != 1 {
			return nil, apperrors.Forbidden("Admission belongs to another HR")
		}
	}
	out, err := employmentAdmission(ctx, tx, company, request)
	var missing *apperrors.AppError
	if errors.As(err, &missing) && missing.StatusCode == 404 && action != "cancel" {
		// A legacy onboarding account stays closed until HR explicitly supplies
		// a verified date. Only its original successful creation receipt may link it.
		out = &domain.EmploymentAdmission{OrganizationID: company, RequestID: request, ActorID: actor}
		err = tx.QueryRow(ctx, `SELECT u.id,COALESCE(s.timezone,'Asia/Almaty') FROM users u
		 LEFT JOIN organization_employment_settings s ON s.organization_id=u.organization_id
		 JOIN iform_operation_receipts receipt ON receipt.organization_id=u.organization_id AND receipt.request_id=$2 AND receipt.actor_id=$3
		 AND receipt.operation='workspace.employee.create' AND receipt.response->>'state'='SUCCEEDED' AND receipt.response->>'employee_id'=u.id::text
		 WHERE u.organization_id=$1 AND u.username='onb.'||replace($2::text,'-','') AND u.deleted_at IS NULL LIMIT 1`, company, request, actor).Scan(&out.EmployeeID, &out.Timezone)
		if err != nil {
			return nil, apperrors.NotFound("Employment admission not found")
		}
	}
	if err != nil {
		return nil, err
	}
	if out.ActorID != actor {
		return nil, apperrors.Forbidden("Admission belongs to another HR")
	}
	if action == "update" {
		if !domain.ValidEmploymentDate(start) {
			return nil, apperrors.New("EMPLOYMENT_DATE_INVALID", "invalid employment date", 400)
		}
		if out.Cancelled {
			return nil, apperrors.Conflict("Cancelled admission cannot be resumed")
		}
		if out.Revision == 0 {
			if revision != 0 {
				return nil, apperrors.Conflict("Employment admission changed")
			}
			_, err = tx.Exec(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,employee_id,start_date) VALUES($1,$2,$3,$4,$5)`, company, request, actor, out.EmployeeID, start)
			if err != nil {
				return nil, err
			}
			out, err = employmentAdmission(ctx, tx, company, request)
			if err != nil {
				return nil, err
			}
		}
		// Same desired date is a safe replay even after a lost update response.
		if out.StartDate == nil || *out.StartDate != start {
			if out.Revision != revision {
				return nil, apperrors.Conflict("Employment admission changed")
			}
			_, err = tx.Exec(ctx, `UPDATE employee_admissions SET start_date=$3,revision=revision+1,updated_at=now() WHERE organization_id=$1 AND request_id=$2`, company, request, start)
			if err != nil {
				return nil, err
			}
		}
		out, err = employmentAdmission(ctx, tx, company, request)
		if err != nil {
			return nil, err
		}
	}
	if action != "get" {
		snapshot, _ := json.Marshal(map[string]any{"start_date": out.StartDate, "cancelled": out.Cancelled, "revision": out.Revision})
		if err = writeAdministrationAudit(ctx, tx, company, actor, "employment."+action, "employment_admission", request.String(), nil, snapshot); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *OrganizationRepository) EmploymentTimezone(ctx context.Context, actor int64, organization *uuid.UUID, timezone *string, revision int64) (map[string]any, error) {
	scope, err := r.accessScope(ctx, actor, organization)
	if err != nil {
		return nil, err
	}
	if timezone != nil {
		if !domain.ValidCompanyTimezone(*timezone) {
			return nil, apperrors.New("EMPLOYMENT_TIMEZONE_INVALID", "invalid IANA company timezone", 400)
		}
		err = auditOrganizationMutationError(ctx, r, actor, scope.ID, organization != nil, "employment.timezone.update", "employment_timezone", scope.ID.String(), func(inner *OrganizationRepository) error {
			result, e := inner.db.Exec(ctx, `INSERT INTO organization_employment_settings(organization_id,timezone) SELECT $1,$2 WHERE $3=0
     ON CONFLICT(organization_id) DO NOTHING`, scope.ID, *timezone, revision)
			if e != nil {
				return e
			}
			if result.RowsAffected() == 1 {
				return nil
			}
			result, e = inner.db.Exec(ctx, `UPDATE organization_employment_settings SET timezone=$2,revision=revision+1,updated_at=now() WHERE organization_id=$1 AND revision=$3`, scope.ID, *timezone, revision)
			if e != nil {
				return e
			}
			if result.RowsAffected() != 1 {
				return apperrors.Conflict("Company timezone changed")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	var zone string
	var rev int64
	err = r.db.QueryRow(ctx, `SELECT COALESCE(s.timezone,'Asia/Almaty'),COALESCE(s.revision,0) FROM organizations o LEFT JOIN organization_employment_settings s ON s.organization_id=o.id WHERE o.id=$1`, scope.ID).Scan(&zone, &rev)
	return map[string]any{"timezone": zone, "revision": rev}, err
}
