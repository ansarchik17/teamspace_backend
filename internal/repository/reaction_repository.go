package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type ReactionRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewReactionRepository(db *pgxpool.Pool, logger *zap.Logger) *ReactionRepository {
	return &ReactionRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "reaction")),
	}
}

// AddReaction adds a reaction to a message
func (r *ReactionRepository) AddReaction(ctx context.Context, reaction *domain.MessageReaction) error {
	query := `
		INSERT INTO message_reactions (id, message_id, user_id, reaction, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (message_id, user_id, reaction) DO NOTHING
	`

	if reaction.ID == uuid.Nil {
		reaction.ID = uuid.New()
	}
	if reaction.CreatedAt.IsZero() {
		reaction.CreatedAt = time.Now()
	}

	_, err := r.db.Exec(ctx, query,
		reaction.ID,
		reaction.MessageID,
		reaction.UserID,
		reaction.Reaction,
		reaction.CreatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to add reaction", zap.Error(err))
		return fmt.Errorf("add reaction: %w", err)
	}

	return nil
}

// RemoveReaction removes a reaction from a message
func (r *ReactionRepository) RemoveReaction(ctx context.Context, messageID uuid.UUID, userID int64, reaction string) error {
	query := `
		DELETE FROM message_reactions
		WHERE message_id = $1 AND user_id = $2 AND reaction = $3
	`

	result, err := r.db.Exec(ctx, query, messageID, userID, reaction)
	if err != nil {
		r.logger.Error("Failed to remove reaction", zap.Error(err))
		return fmt.Errorf("remove reaction: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("reaction not found")
	}

	return nil
}

// MessageReactionWithUser includes basic sender info
type MessageReactionWithUser struct {
	domain.MessageReaction
	UserFirstName string
}

// GetReactionsByMessageIDs fetches reactions for multiple messages with user info
func (r *ReactionRepository) GetReactionsByMessageIDs(ctx context.Context, messageIDs []uuid.UUID) (map[uuid.UUID][]domain.MessageReaction, error) {
	if len(messageIDs) == 0 {
		return map[uuid.UUID][]domain.MessageReaction{}, nil
	}

	query := `
		SELECT mr.id, mr.message_id, mr.user_id, mr.reaction, mr.created_at, 
		       CONCAT(u.first_name, ' ', u.last_name) as full_name, u.avatar_url
		FROM message_reactions mr
		JOIN users u ON mr.user_id = u.id
		WHERE mr.message_id = ANY($1)
	`

	rows, err := r.db.Query(ctx, query, messageIDs)
	if err != nil {
		r.logger.Error("Failed to get reactions", zap.Error(err))
		return nil, fmt.Errorf("get reactions: %w", err)
	}
	defer rows.Close()

	reactions := make(map[uuid.UUID][]domain.MessageReaction)
	for rows.Next() {
		var react domain.MessageReaction
		if err := rows.Scan(
			&react.ID,
			&react.MessageID,
			&react.UserID,
			&react.Reaction,
			&react.CreatedAt,
			&react.UserFullName,
			&react.UserAvatar,
		); err != nil {
			r.logger.Error("Failed to scan reaction", zap.Error(err))
			continue
		}
		reactions[react.MessageID] = append(reactions[react.MessageID], react)
	}

	return reactions, nil
}
