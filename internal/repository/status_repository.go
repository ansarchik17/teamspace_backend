package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type StatusRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewStatusRepository(db *pgxpool.Pool, logger *zap.Logger) *StatusRepository {
	return &StatusRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "status")),
	}
}

// GetInterestedUsers returns active colleagues in the user's company.
// Presence delivery is scoped by organization and does not depend on chats.
func (r *StatusRepository) GetInterestedUsers(ctx context.Context, userID int64) ([]int64, error) {
	query := `
		SELECT colleague.id
		FROM users subject
		JOIN users colleague ON colleague.organization_id = subject.organization_id
		WHERE subject.id = $1
		  AND subject.organization_id IS NOT NULL
		  AND subject.is_active = true
		  AND colleague.is_active = true
		  AND colleague.id <> subject.id
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to get interested users", zap.Error(err), zap.Int64("user_id", userID))
		return nil, err
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			r.logger.Error("Failed to scan user ID", zap.Error(err))
			continue
		}
		userIDs = append(userIDs, id)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating rows", zap.Error(err))
		return nil, err
	}

	r.logger.Debug("Found interested users",
		zap.Int64("user_id", userID),
		zap.Int("count", len(userIDs)),
	)

	return userIDs, nil
}
