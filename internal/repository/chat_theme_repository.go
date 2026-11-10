package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type ChatThemeRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewChatThemeRepository(db *pgxpool.Pool, logger *zap.Logger) *ChatThemeRepository {
	return &ChatThemeRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "chat_theme")),
	}
}

// Upsert creates or updates a global chat theme for a specific user
func (r *ChatThemeRepository) Upsert(ctx context.Context, theme *domain.ChatTheme) error {
	query := `
		INSERT INTO chat_themes (
			user_id, message_color_hex, background_type, background_color_hex, background_image_url, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, NOW(), NOW()
		)
		ON CONFLICT (user_id) DO UPDATE SET
			message_color_hex = EXCLUDED.message_color_hex,
			background_type = EXCLUDED.background_type,
			background_color_hex = EXCLUDED.background_color_hex,
			background_image_url = EXCLUDED.background_image_url,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRow(ctx, query,
		theme.UserID, theme.MessageColorHex,
		theme.BackgroundType, theme.BackgroundColorHex, theme.BackgroundImageURL,
	).Scan(&theme.ID, &theme.CreatedAt, &theme.UpdatedAt)

	if err != nil {
		r.logger.Error("Failed to upsert chat theme", zap.Error(err))
		return fmt.Errorf("failed to upsert chat theme: %w", err)
	}

	return nil
}

// Get finds a chat theme by user
func (r *ChatThemeRepository) Get(ctx context.Context, userID int64) (*domain.ChatTheme, error) {
	query := `
		SELECT id, user_id, message_color_hex, background_type, background_color_hex, background_image_url, created_at, updated_at
		FROM chat_themes
		WHERE user_id = $1
	`

	var t domain.ChatTheme
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&t.ID, &t.UserID,
		&t.MessageColorHex, &t.BackgroundType, &t.BackgroundColorHex, &t.BackgroundImageURL,
		&t.CreatedAt, &t.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // Not found is not an error here, means default theme
		}
		r.logger.Error("Failed to get chat theme", zap.Error(err))
		return nil, fmt.Errorf("failed to get chat theme: %w", err)
	}

	return &t, nil
}

// Delete removes a chat theme, reverting to default
func (r *ChatThemeRepository) Delete(ctx context.Context, userID int64) error {
	query := `
		DELETE FROM chat_themes
		WHERE user_id = $1
	`

	tag, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to delete chat theme", zap.Error(err))
		return fmt.Errorf("failed to delete chat theme: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("theme not found")
	}

	return nil
}
