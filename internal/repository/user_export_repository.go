package repository

import (
	"context"
	"fmt"

	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func (r *UserRepository) ExportMinistryUsers(
	ctx context.Context,
	ministryID int64,
	excludeStateBodyIDs []int64,
	excludeDepartmentIDs []int64,
	excludeDivisionIDs []int64,
) ([]domain.MinistryUserExportRow, error) {
	query := `
		SELECT
			u.id,
			COALESCE(
				NULLIF(TRIM(u.full_name_local), ''),
				NULLIF(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''),
				NULLIF(TRIM(u.username), ''),
				u.email
			) AS fio,
			u.email,
			u.username,
			u.last_login_at,
			m.id,
			m.name,
			u.state_body_id,
			COALESCE(sb.name, ''),
			COALESCE(u.department_id, div.department_id),
			COALESCE(dep_direct.name, dep_from_div.name, ''),
			u.division_id,
			COALESCE(div.name, ''),
			COALESCE(NULLIF(TRIM(u.position), ''), ''),
			u.is_active
		FROM users u
		JOIN ministries m ON m.id = u.ministry_id
		LEFT JOIN state_bodies sb ON sb.id = u.state_body_id
		LEFT JOIN departments dep_direct ON dep_direct.id = u.department_id
		LEFT JOIN divisions div ON div.id = u.division_id
		LEFT JOIN departments dep_from_div ON dep_from_div.id = div.department_id
		WHERE u.deleted_at IS NULL
		  AND u.is_active = TRUE
		  AND u.ministry_id = $1
		  AND NOT (
			(COALESCE(array_length($2::bigint[], 1), 0) > 0 AND (
				u.state_body_id = ANY($2::bigint[])
				OR dep_direct.state_body_id = ANY($2::bigint[])
				OR dep_from_div.state_body_id = ANY($2::bigint[])
				OR div.state_body_id = ANY($2::bigint[])
			))
			OR
			(COALESCE(array_length($3::bigint[], 1), 0) > 0 AND (
				u.department_id = ANY($3::bigint[])
				OR div.department_id = ANY($3::bigint[])
			))
			OR
			(COALESCE(array_length($4::bigint[], 1), 0) > 0 AND (
				u.division_id = ANY($4::bigint[])
			))
		  )
		ORDER BY u.last_login_at DESC NULLS LAST, fio ASC
	`

	rows, err := r.db.Query(ctx, query, ministryID, excludeStateBodyIDs, excludeDepartmentIDs, excludeDivisionIDs)
	if err != nil {
		r.logger.Error("Failed to export ministry users", zap.Error(err), zap.Int64("ministry_id", ministryID))
		return nil, fmt.Errorf("export ministry users: %w", err)
	}
	defer rows.Close()

	result := make([]domain.MinistryUserExportRow, 0, 256)
	for rows.Next() {
		var row domain.MinistryUserExportRow
		if err := rows.Scan(
			&row.UserID,
			&row.FIO,
			&row.Email,
			&row.Username,
			&row.LastLoginAt,
			&row.MinistryID,
			&row.MinistryName,
			&row.StateBodyID,
			&row.StateBodyName,
			&row.DepartmentID,
			&row.DepartmentName,
			&row.DivisionID,
			&row.DivisionName,
			&row.PositionName,
			&row.IsActive,
		); err != nil {
			return nil, fmt.Errorf("scan ministry export row: %w", err)
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ministry export rows: %w", err)
	}

	return result, nil
}

func (r *UserRepository) GetMinistryName(ctx context.Context, ministryID int64) (string, error) {
	const query = `SELECT name FROM ministries WHERE id = $1`

	var name string
	if err := r.db.QueryRow(ctx, query, ministryID).Scan(&name); err != nil {
		return "", fmt.Errorf("get ministry name: %w", err)
	}

	return name, nil
}
