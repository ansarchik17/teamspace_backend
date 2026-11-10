package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

// UpdateLanguage only changes the preference, so concurrent profile or identity
// updates cannot overwrite it with a stale copy of the user.
func (r *UserRepository) UpdateLanguage(ctx context.Context, userID int64, language string) error {
	if language != "ru" && language != "kk" {
		return apperrors.BadRequest("language must be ru or kk")
	}
	tag, err := r.db.Exec(ctx, `UPDATE users SET language = $2, updated_at = NOW()
        WHERE id = $1 AND deleted_at IS NULL`, userID, language)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
