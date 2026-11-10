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

// GetInterestedUsers returns list of user IDs who should receive status updates for the given user
// This includes:
// - Users who have personal conversations with this user
// - Users who are in the same groups as this user
func (r *StatusRepository) GetInterestedUsers(ctx context.Context, userID int64) ([]int64, error) {
	query := `
		SELECT DISTINCT u.user_id
		FROM (
			-- Users from personal conversations
			SELECT cp2.user_id
			FROM conversation_participants cp1
			JOIN conversation_participants cp2 ON cp1.conversation_id = cp2.conversation_id
			JOIN conversations c ON c.id = cp1.conversation_id
			WHERE cp1.user_id = $1 
			  AND cp2.user_id != $1
			  -- AND c.type = 'personal' -- removed to include group chats
			  AND cp1.left_at IS NULL
			  AND cp2.left_at IS NULL
			  AND c.deleted_at IS NULL
			
			UNION
			
			-- Users from group conversations
			SELECT gm2.user_id
			FROM group_members gm1
			JOIN group_members gm2 ON gm1.group_id = gm2.group_id
			JOIN groups g ON g.id = gm1.group_id
			WHERE gm1.user_id = $1
			  AND gm2.user_id != $1
			  AND gm1.left_at IS NULL
			  AND gm2.left_at IS NULL
			  AND g.deleted_at IS NULL
			  AND g.is_active = true
		) u
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
