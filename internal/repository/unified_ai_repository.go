package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type UnifiedAIRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewUnifiedAIRepository(db *pgxpool.Pool, logger *zap.Logger) *UnifiedAIRepository {
	return &UnifiedAIRepository{
		db:     db,
		logger: logger,
	}
}

type UnifiedAIChat struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	Title     string    `json:"title"`
	ModelName string    `json:"model_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Avatar    string    `json:"avatar"`
}

type UnifiedAIMessage struct {
	ID          string    `json:"id"`
	ChatID      string    `json:"chat_id"`
	Role        string    `json:"role"`
	Content     string    `json:"content"`
	FileURL     *string   `json:"file_url,omitempty"`
	TokensUsage int       `json:"tokens_usage"`
	CreatedAt   time.Time `json:"created_at"`
}

// GetOrCreateChat implements singleton pattern - gets existing or creates new AI chat for user
func (r *UnifiedAIRepository) GetOrCreateChat(ctx context.Context, userID int64, initialModel string) (*UnifiedAIChat, error) {
	// Try to get existing chat first
	var chat UnifiedAIChat
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, title, model_name, created_at, updated_at
		FROM unified_ai_chats
		WHERE user_id = $1
	`, userID).Scan(&chat.ID, &chat.UserID, &chat.Title, &chat.ModelName, &chat.CreatedAt, &chat.UpdatedAt)

	// Hardcoded Avatar for AI
	chat.Avatar = "http://92.38.48.9:18080/api/v1/files/download?object_name=image/0/1768049062_16d320e5-38c8-4e50-9163-a5faba791e00.png"

	if err == nil {
		// Chat exists, return it
		chat.Title = "TeamAI" // Enforce title
		r.logger.Info("Found existing unified AI chat", zap.String("chat_id", chat.ID), zap.Int64("user_id", userID))
		return &chat, nil
	}

	// Check if it's a "no rows" error (chat doesn't exist)
	if err.Error() == "no rows in result set" || err == sql.ErrNoRows {
		// Chat doesn't exist, create it
		if initialModel == "" {
			initialModel = "google/gemma-3-27b"
		}

		err = r.db.QueryRow(ctx, `
			INSERT INTO unified_ai_chats (user_id, model_name, title)
			VALUES ($1, $2, 'AlemAI')
			RETURNING id, user_id, title, model_name, created_at, updated_at
		`, userID, initialModel).Scan(&chat.ID, &chat.UserID, &chat.Title, &chat.ModelName, &chat.CreatedAt, &chat.UpdatedAt)

		if err != nil {
			return nil, fmt.Errorf("failed to create unified AI chat: %w", err)
		}

		chat.Avatar = "http://92.38.48.9:18080/api/v1/files/download?object_name=image/0/1768049062_16d320e5-38c8-4e50-9163-a5faba791e00.png"
		chat.Title = "TeamAI" // Enforce title

		r.logger.Info("Created new unified AI chat", zap.String("chat_id", chat.ID), zap.Int64("user_id", userID))
		return &chat, nil
	}

	// Real error occurred
	return nil, fmt.Errorf("failed to get unified AI chat: %w", err)
}

// GetChat retrieves chat by ID and verifies ownership
func (r *UnifiedAIRepository) GetChat(ctx context.Context, chatID string, userID int64) (*UnifiedAIChat, error) {
	var chat UnifiedAIChat
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, title, model_name, created_at, updated_at
		FROM unified_ai_chats
		WHERE id = $1 AND user_id = $2
	`, chatID, userID).Scan(&chat.ID, &chat.UserID, &chat.Title, &chat.ModelName, &chat.CreatedAt, &chat.UpdatedAt)

	chat.Avatar = "http://92.38.48.9:18080/api/v1/files/download?object_name=image/0/1768049062_16d320e5-38c8-4e50-9163-a5faba791e00.png"
	chat.Title = "TeamAI" // Enforce title for everyone

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get chat: %w", err)
	}
	return &chat, nil
}

// UpdateModel updates the AI model for a chat
func (r *UnifiedAIRepository) UpdateModel(ctx context.Context, chatID string, userID int64, newModel string) error {
	result, err := r.db.Exec(ctx, `
		UPDATE unified_ai_chats 
		SET model_name = $1, updated_at = NOW() 
		WHERE id = $2 AND user_id = $3
	`, newModel, chatID, userID)

	if err != nil {
		return fmt.Errorf("failed to update model: %w", err)
	}

	rows := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("chat not found or unauthorized")
	}

	return nil
}

// UpdateTitle updates the title of a chat
func (r *UnifiedAIRepository) UpdateTitle(ctx context.Context, chatID string, userID int64, title string) error {
	result, err := r.db.Exec(ctx, `
		UPDATE unified_ai_chats 
		SET title = $1, updated_at = NOW() 
		WHERE id = $2 AND user_id = $3
	`, title, chatID, userID)

	if err != nil {
		return fmt.Errorf("failed to update title: %w", err)
	}

	rows := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("chat not found or unauthorized")
	}

	return nil
}

// CreateMessageWithID adds a new message and returns its generated ID string
func (r *UnifiedAIRepository) CreateMessageWithID(ctx context.Context, msg UnifiedAIMessage) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO unified_ai_messages (chat_id, role, content, file_url, tokens_usage)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, msg.ChatID, msg.Role, msg.Content, msg.FileURL, msg.TokensUsage).Scan(&id)

	if err != nil {
		return "", fmt.Errorf("failed to save message: %w", err)
	}

	// Update chat updated_at
	_, _ = r.db.Exec(ctx, `UPDATE unified_ai_chats SET updated_at = NOW() WHERE id = $1`, msg.ChatID)

	return id, nil
}

// CreateMessage adds a new message to the chat
func (r *UnifiedAIRepository) CreateMessage(ctx context.Context, msg UnifiedAIMessage) error {
	_, err := r.CreateMessageWithID(ctx, msg)
	return err
}

// GetMessages retrieves all messages for a chat
func (r *UnifiedAIRepository) GetMessages(ctx context.Context, chatID string, userID int64) ([]UnifiedAIMessage, error) {
	// First verify the chat belongs to the user
	var ownerID int64
	err := r.db.QueryRow(ctx, `SELECT user_id FROM unified_ai_chats WHERE id = $1`, chatID).Scan(&ownerID)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("chat not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to verify chat ownership: %w", err)
	}
	if ownerID != userID {
		return nil, fmt.Errorf("unauthorized")
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, chat_id, role, content, file_url, tokens_usage, created_at
		FROM unified_ai_messages
		WHERE chat_id = $1
		ORDER BY created_at ASC
	`, chatID)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer rows.Close()

	var msgs []UnifiedAIMessage
	for rows.Next() {
		var m UnifiedAIMessage
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &m.FileURL, &m.TokensUsage, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// GetLastMessages retrieves the last N messages for context building
func (r *UnifiedAIRepository) GetLastMessages(ctx context.Context, chatID string, limit int) ([]UnifiedAIMessage, error) {
	query := `
		SELECT id, chat_id, role, content, file_url, tokens_usage, created_at
		FROM (
			SELECT * FROM unified_ai_messages
			WHERE chat_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		) sub
		ORDER BY created_at ASC
	`
	rows, err := r.db.Query(ctx, query, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get last messages: %w", err)
	}
	defer rows.Close()

	var msgs []UnifiedAIMessage
	for rows.Next() {
		var m UnifiedAIMessage
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &m.FileURL, &m.TokensUsage, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// DeleteAllMessages deletes all messages from a chat (clear history)
func (r *UnifiedAIRepository) DeleteAllMessages(ctx context.Context, chatID string, userID int64) error {
	// First verify ownership
	var ownerID int64
	err := r.db.QueryRow(ctx, `SELECT user_id FROM unified_ai_chats WHERE id = $1`, chatID).Scan(&ownerID)

	if err == sql.ErrNoRows {
		return fmt.Errorf("chat not found")
	}
	if err != nil {
		return fmt.Errorf("failed to verify chat ownership: %w", err)
	}
	if ownerID != userID {
		return fmt.Errorf("unauthorized")
	}

	_, err = r.db.Exec(ctx, `DELETE FROM unified_ai_messages WHERE chat_id = $1`, chatID)
	if err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}

	r.logger.Info("Deleted all messages from unified AI chat", zap.String("chat_id", chatID), zap.Int64("user_id", userID))
	return nil
}

// DeleteChat deletes the entire chat and all messages (reset AI chat for user)
func (r *UnifiedAIRepository) DeleteChat(ctx context.Context, chatID string, userID int64) error {
	// First verify ownership
	var ownerID int64
	err := r.db.QueryRow(ctx, `SELECT user_id FROM unified_ai_chats WHERE id = $1`, chatID).Scan(&ownerID)

	if err == sql.ErrNoRows {
		return fmt.Errorf("chat not found")
	}
	if err != nil {
		return fmt.Errorf("failed to verify chat ownership: %w", err)
	}
	if ownerID != userID {
		return fmt.Errorf("unauthorized")
	}

	// Messages will be deleted automatically due to CASCADE
	_, err = r.db.Exec(ctx, `DELETE FROM unified_ai_chats WHERE id = $1`, chatID)
	if err != nil {
		return fmt.Errorf("failed to delete chat: %w", err)
	}

	r.logger.Info("Deleted unified AI chat", zap.String("chat_id", chatID), zap.Int64("user_id", userID))
	return nil
}

// DeleteMessage deletes a specific message from the chat
func (r *UnifiedAIRepository) DeleteMessage(ctx context.Context, chatID string, messageID string, userID int64) error {
	// First verify chat ownership. Since it's a personal AI chat, the user owns the chat and all messages within it contextually.
	var ownerID int64
	err := r.db.QueryRow(ctx, `SELECT user_id FROM unified_ai_chats WHERE id = $1`, chatID).Scan(&ownerID)

	if err == sql.ErrNoRows {
		return fmt.Errorf("chat not found")
	}
	if err != nil {
		return fmt.Errorf("verify ownership: %w", err)
	}
	if ownerID != userID {
		return fmt.Errorf("unauthorized")
	}

	// Delete the message
	res, err := r.db.Exec(ctx, `DELETE FROM unified_ai_messages WHERE id = $1 AND chat_id = $2`, messageID, chatID)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}

	if res.RowsAffected() == 0 {
		return fmt.Errorf("message not found")
	}

	return nil
}
