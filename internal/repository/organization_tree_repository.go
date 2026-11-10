package repository

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// OrganizationTree reads unit metadata and employee counts from one company snapshot.
func (r *OrganizationRepository) OrganizationTree(ctx context.Context, actor int64) (*domain.OrganizationTree, []*domain.OrganizationTreeNode, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return nil, nil, err
	}
	snapshot := &OrganizationRepository{db: txOrganizationDB{tx}, logger: r.logger}
	scope, err := snapshot.currentScope(ctx, actor)
	if err != nil {
		return nil, nil, err
	}
	canManage := true
	if err = authorizeAdministration(ctx, tx, actor, scope.OwnerID, false); err != nil {
		if apperrors.ToAppError(err).StatusCode != 403 {
			return nil, nil, err
		}
		canManage = false
	}
	result := &domain.OrganizationTree{OrganizationID: scope.ID, CanManageStructure: canManage, Items: []*domain.OrganizationTreeNode{}}
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE t.id IS NULL)
 FROM users u LEFT JOIN teams t ON t.id=u.team_id AND t.organization_id=u.organization_id AND t.deleted_at IS NULL
 WHERE u.organization_id=$1 AND u.deleted_at IS NULL AND u.is_active=TRUE`, scope.ID).Scan(&result.ActiveEmployeeCount, &result.UnassignedEmployeeCount)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `WITH counts AS (
 SELECT team_id,count(*)::int AS employees FROM users WHERE organization_id=$1 AND deleted_at IS NULL AND is_active=TRUE GROUP BY team_id
 ) SELECT t.id,t.parent_team_id,t.name,t.unit_type,t.description,t.display_order,
 m.id,COALESCE(NULLIF(TRIM(m.full_name_local),''),NULLIF(TRIM(CONCAT_WS(' ',m.first_name,m.last_name)),''),m.username,m.email,''),m.position,m.avatar_url,COALESCE(c.employees,0)
 FROM teams t LEFT JOIN counts c ON c.team_id=t.id
 LEFT JOIN users m ON m.id=t.manager_id AND m.organization_id=t.organization_id AND m.deleted_at IS NULL AND m.is_active=TRUE
 WHERE t.organization_id=$1 AND t.deleted_at IS NULL
 ORDER BY t.display_order,lower(t.name),t.id`, scope.ID)
	if err != nil {
		return nil, nil, err
	}
	nodes := []*domain.OrganizationTreeNode{}
	for rows.Next() {
		node := &domain.OrganizationTreeNode{Children: []*domain.OrganizationTreeNode{}}
		var managerID *int64
		var manager domain.OrganizationTreeManager
		if err = rows.Scan(&node.ID, &node.ParentUnitID, &node.Name, &node.UnitType, &node.Description, &node.DisplayOrder, &managerID, &manager.FullName, &manager.Position, &manager.AvatarURL, &node.EmployeeCount); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if managerID != nil {
			manager.ID = *managerID
			node.Manager = &manager
		}
		nodes = append(nodes, node)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	result.TotalUnits = len(nodes)
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return result, nodes, nil
}
