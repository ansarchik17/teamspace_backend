package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// The browser proxy needs only current identity/access. The full directory is
// pulled once by iForm, rather than twice for every browser request.
func (r *OrganizationRepository) IFormIdentity(ctx context.Context, actor int64) (*domain.IFormContext, error) {
	out := &domain.IFormContext{Actor: domain.IFormSubject(actor)}
	var permissions []string
	var owner bool
	err := r.db.QueryRow(ctx, `SELECT o.id,o.name,COALESCE(p.permissions,'{}'::text[]),(o.owner_id=u.id) IS TRUE
 FROM users u JOIN organizations o ON o.id=u.organization_id
 JOIN organization_access_apps app ON app.organization_id=o.id AND app.application='iform' AND app.enabled
 LEFT JOIN organization_access_assignments assignment ON assignment.user_id=u.id AND assignment.organization_id=u.organization_id
 LEFT JOIN organization_position_permissions p ON p.organization_id=u.organization_id AND p.position_id=assignment.position_id AND p.application='iform'
 JOIN organization_modules module ON module.organization_id=o.id AND module.application='iform' AND module.available AND module.enabled
 WHERE u.id=$1 AND workspace_user_can_login(u.id) AND u.deleted_at IS NULL AND o.status='active'`, actor).Scan(&out.OrganizationID, &out.Name, &permissions, &owner)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && len(domain.IFormRoles(permissions, owner)) == 0 {
		return nil, apperrors.Forbidden("Компания, сотрудник или доступ к iForm отключены")
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// One consistent snapshot, always scoped through the actor's current membership.
// expected is an assertion to check, never a selector for another organization.
func (r *OrganizationRepository) IFormContext(ctx context.Context, actor int64, expected uuid.UUID) (*domain.IFormContext, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out := &domain.IFormContext{Actor: domain.IFormSubject(actor), Members: []domain.IFormMember{}, Units: []domain.IFormUnit{}, Positions: []domain.IFormPosition{}}
	err = tx.QueryRow(ctx, `SELECT o.id,o.name FROM users u JOIN organizations o ON o.id=u.organization_id
 JOIN organization_access_apps a ON a.organization_id=o.id AND a.application='iform' AND a.enabled
 JOIN organization_modules module ON module.organization_id=o.id AND module.application='iform' AND module.available AND module.enabled
 WHERE u.id=$1 AND workspace_user_can_login(u.id) AND u.deleted_at IS NULL AND o.status='active'`, actor).Scan(&out.OrganizationID, &out.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.Forbidden("Компания, сотрудник или доступ к iForm отключены")
	}
	if err != nil {
		return nil, err
	}
	if expected != uuid.Nil && expected != out.OrganizationID {
		return nil, apperrors.Forbidden("Компания пользователя изменилась")
	}
	rows, err := tx.Query(ctx, `SELECT u.id,COALESCE(NULLIF(u.full_name_local,''),CONCAT_WS(' ',u.first_name,u.last_name)),
 COALESCE(pos.name,u.position,''),COALESCE(pos.id::text,''),COALESCE(t.id::text,''),COALESCE(manager.id,0),COALESCE(p.permissions,'{}'::text[]),
 workspace_user_can_login(u.id),(o.owner_id=u.id) IS TRUE
 FROM users u JOIN organizations o ON o.id=u.organization_id
 LEFT JOIN teams t ON t.id=u.team_id AND t.organization_id=u.organization_id AND t.deleted_at IS NULL
 LEFT JOIN users manager ON manager.id=u.manager_id AND manager.organization_id=u.organization_id AND manager.is_active AND manager.deleted_at IS NULL AND manager.id<>u.id
 LEFT JOIN organization_access_assignments assignment ON assignment.user_id=u.id AND assignment.organization_id=u.organization_id
 LEFT JOIN organization_access_positions pos ON pos.organization_id=assignment.organization_id AND pos.id=assignment.position_id
 LEFT JOIN organization_position_permissions p ON p.organization_id=u.organization_id AND p.position_id=pos.id AND p.application='iform'
 WHERE u.organization_id=$1 AND u.deleted_at IS NULL ORDER BY u.id`, out.OrganizationID)
	if err != nil {
		return nil, err
	}
	actorAllowed := false
	for rows.Next() {
		var m domain.IFormMember
		var id, manager int64
		var perms []string
		var owner bool
		if err = rows.Scan(&id, &m.Name, &m.Position, &m.PositionID, &m.UnitID, &manager, &perms, &m.Active, &owner); err != nil {
			rows.Close()
			return nil, err
		}
		m.ID = domain.IFormSubject(id)
		if manager > 0 {
			m.ManagerID = domain.IFormSubject(manager)
		}
		m.Roles = domain.IFormRoles(perms, owner)
		if !m.Active {
			m.Roles = []string{}
		}
		if id == actor {
			actorAllowed = m.Active && len(m.Roles) > 0
		}
		out.Members = append(out.Members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !actorAllowed {
		return nil, apperrors.Forbidden("Должности сотрудника не назначены права iForm")
	}
	rows, err = tx.Query(ctx, `SELECT t.id::text,t.name,COALESCE(parent.id::text,''),COALESCE(manager.id,0)
 FROM teams t
 LEFT JOIN teams parent ON parent.id=t.parent_team_id AND parent.organization_id=t.organization_id AND parent.deleted_at IS NULL
 LEFT JOIN users manager ON manager.id=t.manager_id AND manager.organization_id=t.organization_id AND manager.is_active AND manager.deleted_at IS NULL
 WHERE t.organization_id=$1 AND t.deleted_at IS NULL ORDER BY t.id`, out.OrganizationID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var unit domain.IFormUnit
		var manager int64
		if err = rows.Scan(&unit.ID, &unit.Name, &unit.ParentID, &manager); err != nil {
			rows.Close()
			return nil, err
		}
		if manager > 0 {
			unit.ManagerID = domain.IFormSubject(manager)
		}
		out.Units = append(out.Units, unit)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	positionRows, err := tx.Query(ctx, `SELECT id::text,name FROM organization_access_positions WHERE organization_id=$1 ORDER BY name,id`, out.OrganizationID)
	if err != nil {
		return nil, err
	}
	for positionRows.Next() {
		var position domain.IFormPosition
		if err = positionRows.Scan(&position.ID, &position.Name); err != nil {
			positionRows.Close()
			return nil, err
		}
		out.Positions = append(out.Positions, position)
	}
	err = positionRows.Err()
	positionRows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
