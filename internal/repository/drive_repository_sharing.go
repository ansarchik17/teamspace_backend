package repository

import (
	"context"
	"fmt"

	"github.com/west6ide/workspace-backend/internal/domain"
)

// ListSharingUsers возвращает список уникальных пользователей, которые предоставили доступ к своим файлам или папкам текущему пользователю
func (r *DriveRepository) ListSharingUsers(ctx context.Context, userID int64) ([]domain.UserBasic, error) {
	query := `
		SELECT DISTINCT u.id, u.username, u.first_name, u.last_name, u.email, u.avatar_url
		FROM permissions p
		LEFT JOIN files fl ON p.resource_type = 'file' AND p.resource_id = fl.id
		LEFT JOIN folders fd ON p.resource_type = 'folder' AND p.resource_id = fd.id
		JOIN users u ON (
			(fl.owner_id = u.id AND fl.deleted_at IS NULL) OR 
			(fd.owner_id = u.id AND fd.deleted_at IS NULL)
		)
		WHERE p.user_id = $1 AND u.id != $1
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list sharing users: %w", err)
	}
	defer rows.Close()

	var users []domain.UserBasic
	for rows.Next() {
		var u domain.UserBasic
		if err := rows.Scan(&u.ID, &u.Username, &u.FirstName, &u.LastName, &u.Email, &u.AvatarURL); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, nil
}
