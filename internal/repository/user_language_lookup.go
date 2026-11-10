package repository

import "context"

func (r *UserRepository) GetLanguage(ctx context.Context, userID int64) (string, error) {
	var language string
	err := r.db.QueryRow(ctx, `SELECT language FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&language)
	return language, err
}
