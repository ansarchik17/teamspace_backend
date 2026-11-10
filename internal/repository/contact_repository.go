package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
)

const contactNameSQL = `COALESCE(NULLIF(TRIM(u.full_name_local), ''), NULLIF(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''), u.username, u.email, '')`

// ListContacts resolves the tenant from the caller, never from request parameters.
func (r *OrganizationRepository) ListContacts(ctx context.Context, userID int64, f domain.ContactFilter) (*domain.ContactList, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	args := []any{scope.ID}
	where := ` WHERE u.organization_id = $1 AND u.deleted_at IS NULL AND u.is_active = TRUE`
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, q)
		n := len(args)
		where += fmt.Sprintf(` AND (strpos(lower(%s), lower($%d)) > 0 OR strpos(lower(COALESCE(u.email,'')), lower($%d)) > 0 OR strpos(lower(COALESCE(u.username,'')), lower($%d)) > 0)`, contactNameSQL, n, n, n)
	}
	if len(f.IDs) > 0 {
		args = append(args, f.IDs)
		where += fmt.Sprintf(` AND u.id = ANY($%d::bigint[])`, len(args))
	}
	if f.ManagerID != nil {
		args = append(args, *f.ManagerID)
		where += fmt.Sprintf(` AND u.manager_id=$%d AND u.id<>u.manager_id`, len(args))
	}
	if f.UnitID != nil {
		args = append(args, *f.UnitID)
		where += fmt.Sprintf(` AND t.id = $%d`, len(args))
	}
	if p := strings.TrimSpace(f.Position); p != "" {
		args = append(args, p)
		where += fmt.Sprintf(` AND lower(TRIM(u.position)) = lower($%d)`, len(args))
	}
	from := ` FROM users u LEFT JOIN teams t ON t.id = u.team_id AND t.organization_id = u.organization_id AND t.deleted_at IS NULL`
	result := &domain.ContactList{Items: []domain.Contact{}, Limit: f.Limit, Offset: f.Offset}
	// A single SQL statement keeps total and page consistent under concurrent changes,
	// including an offset beyond the last item.
	args = append(args, f.Limit, f.Offset)
	query := fmt.Sprintf(`WITH filtered AS (
 SELECT u.id, %s AS full_name, COALESCE(u.email,'') AS email, COALESCE(u.username,'') AS username,
 u.position,u.avatar_url,u.availability_status,t.id AS unit_id,t.name AS unit_name,
 (SELECT m.id FROM users m WHERE m.id=u.manager_id AND m.organization_id=u.organization_id AND m.deleted_at IS NULL AND m.is_active=TRUE) AS manager_id
 %s %s
 ), page AS (SELECT * FROM filtered ORDER BY lower(full_name),id LIMIT $%d OFFSET $%d)
 SELECT (SELECT count(*) FROM filtered), p.id,p.full_name,p.email,p.username,p.position,p.avatar_url,p.availability_status,p.unit_id,p.unit_name,p.manager_id
 FROM (SELECT 1) anchor LEFT JOIN page p ON TRUE ORDER BY lower(p.full_name),p.id`, contactNameSQL, from, where, len(args)-1, len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id *int64
		var name, email, username *string
		var item domain.Contact
		if err := rows.Scan(&result.Total, &id, &name, &email, &username, &item.Position, &item.AvatarURL, &item.AvailabilityStatus, &item.UnitID, &item.UnitName, &item.ManagerID); err != nil {
			return nil, err
		}
		if id != nil {
			item.ID = *id
			item.FullName = *name
			item.Email = *email
			item.Username = *username
			result.Items = append(result.Items, item)
		}
	}
	return result, rows.Err()
}

func (r *OrganizationRepository) ContactFilters(ctx context.Context, userID int64) (*domain.ContactFilters, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := &domain.ContactFilters{Units: []domain.ContactUnit{}, Positions: []string{}}
	rows, err := r.db.Query(ctx, `SELECT id,parent_team_id,name,unit_type FROM teams WHERE organization_id=$1 AND deleted_at IS NULL ORDER BY display_order,lower(name),id`, scope.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item domain.ContactUnit
		if err = rows.Scan(&item.ID, &item.ParentUnitID, &item.Name, &item.UnitType); err != nil {
			rows.Close()
			return nil, err
		}
		result.Units = append(result.Units, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = r.db.Query(ctx, `SELECT min(TRIM(position)) FROM users WHERE organization_id=$1 AND deleted_at IS NULL AND is_active=TRUE AND NULLIF(TRIM(position),'') IS NOT NULL GROUP BY lower(TRIM(position)) ORDER BY lower(TRIM(position))`, scope.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			return nil, err
		}
		result.Positions = append(result.Positions, p)
	}
	return result, rows.Err()
}
