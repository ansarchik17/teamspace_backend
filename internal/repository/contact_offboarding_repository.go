package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) ContactOffboarding(ctx context.Context, actor, target int64) (*domain.ContactOffboardingResult, error) {
	org, err := r.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	var encoded []byte
	var status string
	err = r.db.QueryRow(ctx, `SELECT result,identity_sync_status FROM employee_offboardings WHERE user_id=$1 AND organization_id=$2`, target, org).Scan(&encoded, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.NotFound("offboarding")
	}
	if err != nil {
		return nil, err
	}
	var result domain.ContactOffboardingResult
	if err = json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	result.IdentitySyncStatus = status
	return &result, nil
}

func (r *OrganizationRepository) OffboardContact(ctx context.Context, actor, target int64, req domain.ContactOffboardingRequest, dryRun bool) (*domain.ContactOffboardingResult, error) {
	org, err := r.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	if actor == target || target == req.SuccessorID {
		return nil, apperrors.Validation("employee, successor and acting administrator must be distinct from employee")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='30s'`); err != nil {
		return nil, err
	}
	// Same ordering as import/structure operations. Table locks give a bounded, atomic
	// snapshot of all transferred work; ownership triggers guard queued writers afterwards.
	for _, salt := range []int{1005, 1002, 1003} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,$2))`, org.String(), salt); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724619203)`); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE boards,board_members,projects,folders,files,file_versions,permissions,drive_shared_links IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM users WHERE id=ANY($1::bigint[]) ORDER BY id FOR UPDATE`, []int64{actor, target, req.SuccessorID})
	if err != nil {
		return nil, err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	scoped := &OrganizationRepository{db: txOrganizationDB{tx}, logger: r.logger}
	current, err := scoped.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	if current != org {
		return nil, apperrors.Conflict("organization changed")
	}
	existing, e := scoped.ContactOffboarding(ctx, actor, target)
	if e == nil {
		if existing.SuccessorID != req.SuccessorID {
			return nil, apperrors.Conflict("employee already offboarded to another successor")
		}
		return existing, nil
	}
	if app := apperrors.ToAppError(e); app.StatusCode != 404 {
		return nil, e
	}
	var active bool
	var role string
	var manager *int64
	var owner bool
	err = tx.QueryRow(ctx, `SELECT u.is_active,u.role,(SELECT m.id FROM users m WHERE m.id=u.manager_id AND m.id<>u.id AND m.organization_id=u.organization_id AND m.is_active=TRUE AND m.deleted_at IS NULL),EXISTS(SELECT 1 FROM organizations WHERE owner_id=u.id) FROM users u WHERE u.id=$1 AND u.organization_id=$2 AND u.deleted_at IS NULL`, target, org).Scan(&active, &role, &manager, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.NotFound("employee")
	}
	if err != nil {
		return nil, err
	}
	if owner || role == "admin" || target == domain.SupportUserID {
		return nil, apperrors.Forbidden("organization owners, platform administrators and support cannot be offboarded")
	}
	// Already inactive employees may still need a handover, but protected audit state is immutable.
	err = tx.QueryRow(ctx, `SELECT is_active FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL AND role<>'guest'`, req.SuccessorID, org).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return nil, apperrors.Validation("successor must be an active non-guest employee in the same company")
	}
	if err != nil {
		return nil, err
	}
	var foreign bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects p JOIN boards b ON b.id=p.board_id LEFT JOIN users u ON u.id=b.owner_id WHERE p.owner_id=$1 AND u.organization_id IS DISTINCT FROM $2::uuid)`, target, org).Scan(&foreign)
	if err != nil {
		return nil, err
	}
	if foreign {
		return nil, apperrors.Conflict("employee owns projects on a board outside the company; resolve ownership first")
	}
	result := &domain.ContactOffboardingResult{UserID: target, SuccessorID: req.SuccessorID, ActorID: actor, Reason: req.Reason, DryRun: dryRun, Applied: !dryRun, IdentitySyncStatus: "pending"}
	manifest := map[string]any{}
	collect := func(name, query string, args ...any) ([]uuid.UUID, error) {
		rs, e := tx.Query(ctx, query, args...)
		if e != nil {
			return nil, e
		}
		defer rs.Close()
		ids := []uuid.UUID{}
		for rs.Next() {
			var id uuid.UUID
			if e = rs.Scan(&id); e != nil {
				return nil, e
			}
			ids = append(ids, id)
		}
		manifest[name] = ids
		return ids, rs.Err()
	}
	boards, err := collect("boards", `SELECT id FROM boards WHERE owner_id=$1 ORDER BY id`, target)
	if err != nil {
		return nil, err
	}
	result.Boards = len(boards)
	projects, err := collect("projects", `UPDATE projects SET owner_id=$2,updated_at=NOW() WHERE owner_id=$1 RETURNING id`, target, req.SuccessorID)
	if err != nil {
		return nil, err
	}
	result.Projects = len(projects)
	_, err = tx.Exec(ctx, `INSERT INTO board_members(board_id,user_id,role)
 SELECT id,$2::bigint,'owner' FROM boards WHERE id=ANY($1::uuid[])
 ON CONFLICT(board_id,user_id) DO UPDATE SET role='owner'`, boards, req.SuccessorID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO board_members(board_id,user_id,role)
 SELECT DISTINCT board_id,$2::bigint,'editor' FROM projects WHERE id=ANY($1::uuid[])
 ON CONFLICT(board_id,user_id) DO UPDATE SET role=CASE WHEN board_members.role IN ('owner','admin') THEN board_members.role ELSE 'editor' END`, projects, req.SuccessorID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE board_members SET role='member' WHERE board_id=ANY($1::uuid[]) AND user_id=$2`, boards, target)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE boards SET owner_id=$2,updated_at=NOW() WHERE id=ANY($1::uuid[])`, boards, req.SuccessorID)
	if err != nil {
		return nil, err
	}
	files, err := collect("files", `SELECT id FROM files WHERE owner_id=$1 AND scope='drive' ORDER BY id`, target)
	if err != nil {
		return nil, err
	}
	result.Files = len(files)
	folders, err := collect("folders", `SELECT id FROM folders WHERE owner_id=$1 ORDER BY id`, target)
	if err != nil {
		return nil, err
	}
	result.Folders = len(folders)
	if len(files)+len(folders) > 0 {
		container := uuid.New()
		result.HandoverFolderID = &container
		_, err = tx.Exec(ctx, `INSERT INTO folders(id,owner_id,name,created_at,updated_at,is_public) VALUES($1,$2,$3,NOW(),NOW(),FALSE)`, container, req.SuccessorID, fmt.Sprintf("Передано от сотрудника %d", target))
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE folders SET owner_id=$2,is_public=FALSE,parent_id=CASE WHEN parent_id=ANY($1::uuid[]) THEN parent_id ELSE $3 END,updated_at=NOW() WHERE id=ANY($1::uuid[])`, folders, req.SuccessorID, container)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE files SET owner_id=$2,is_public=FALSE,folder_id=CASE WHEN folder_id=ANY($4::uuid[]) THEN folder_id ELSE $3 END,onlyoffice_key=gen_random_uuid()::text,updated_at=NOW() WHERE id=ANY($1::uuid[])`, files, req.SuccessorID, container, folders)
		if err != nil {
			return nil, err
		}
		// Explicit owner ACLs must match the new owner. Historical events are untouched.
		_, err = tx.Exec(ctx, `DELETE FROM permissions WHERE user_id=$1 AND ((resource_type='file' AND resource_id=ANY($2::uuid[])) OR (resource_type='folder' AND resource_id=ANY($3::uuid[])))`, target, files, folders)
		if err != nil {
			return nil, err
		}
		for _, resource := range []struct {
			kind string
			ids  []uuid.UUID
		}{{"file", files}, {"folder", folders}} {
			_, err = tx.Exec(ctx, `INSERT INTO permissions(resource_type,resource_id,user_id,role,created_at) SELECT $1::text,unnest($2::uuid[]),$3::bigint,'owner',NOW() ON CONFLICT(resource_type,resource_id,user_id) DO UPDATE SET role='owner'`, resource.kind, resource.ids, req.SuccessorID)
			if err != nil {
				return nil, err
			}
			// Retain link records and authorship, but expire old bearer URLs.
			_, err = tx.Exec(ctx, `UPDATE drive_shared_links SET expires_at=LEAST(COALESCE(expires_at,NOW()),NOW()) WHERE resource_type=$1 AND resource_id=ANY($2::uuid[])`, resource.kind, resource.ids)
			if err != nil {
				return nil, err
			}
		}
	}
	// Preserve reporting structure without creating a cycle when the successor was a report.
	tag, err := tx.Exec(ctx, `UPDATE users SET manager_id=$3,updated_at=NOW() WHERE organization_id=$1 AND manager_id=$2 AND id<>$2 AND deleted_at IS NULL`, org, target, manager)
	if err != nil {
		return nil, err
	}
	result.DirectReports = int(tag.RowsAffected())
	units, err := collect("units", `UPDATE teams SET manager_id=$3,updated_at=NOW() WHERE organization_id=$1 AND manager_id=$2 RETURNING id`, org, target, req.SuccessorID)
	if err != nil {
		return nil, err
	}
	result.Units = len(units)
	_, err = tx.Exec(ctx, `UPDATE users SET is_active=FALSE,availability_status='offline',updated_at=NOW() WHERE id=$1`, target)
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT NOW()`).Scan(&result.OffboardedAt); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	resources, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_offboardings(user_id,organization_id,successor_id,actor_id,reason,result,resources) VALUES($1,$2,$3,$4,$5,$6,$7)`, target, org, req.SuccessorID, actor, req.Reason, encoded, resources)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return result, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Lease pending identity work before network IO. Failed processes become retryable in one minute.
func (r *OrganizationRepository) ClaimOffboardingIdentity(ctx context.Context) (int64, error) {
	var user int64
	err := r.db.QueryRow(ctx, `WITH pending AS (SELECT user_id FROM employee_offboardings WHERE identity_sync_status='pending' AND identity_sync_next_at<=NOW() ORDER BY identity_sync_next_at LIMIT 1 FOR UPDATE SKIP LOCKED)
 UPDATE employee_offboardings e SET identity_sync_attempts=identity_sync_attempts+1,identity_sync_next_at=NOW()+INTERVAL '1 minute' FROM pending p WHERE e.user_id=p.user_id RETURNING e.user_id`).Scan(&user)
	return user, err
}
func (r *OrganizationRepository) CompleteOffboardingIdentity(ctx context.Context, user int64) error {
	_, err := r.db.Exec(ctx, `UPDATE employee_offboardings SET identity_sync_status='complete' WHERE user_id=$1`, user)
	return err
}

// OffboardingFileIDs is internal metadata for refreshing the existing optional search index.
func (r *OrganizationRepository) OffboardingFileIDs(ctx context.Context, user int64) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `SELECT jsonb_array_elements_text(resources->'files')::uuid FROM employee_offboardings WHERE user_id=$1`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
