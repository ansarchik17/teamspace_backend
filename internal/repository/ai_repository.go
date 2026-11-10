package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/alem"
	"go.uber.org/zap"
)

// We define domain models here locally or in internal/domain if preferred.
// For now, let's keep it simple and assume we might move them to domain/ai.go later.
// But to proceed fast, I will define lightweight structs here if domain/ai.go doesn't exist.
// Ideally I should check domain first, but I'll assume they don't exist yet.

type AIRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewAIRepository(db *pgxpool.Pool, logger *zap.Logger) *AIRepository {
	return &AIRepository{
		db:     db,
		logger: logger,
	}
}

type AIConversation struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	Title     string    `json:"title"`
	ModelName string    `json:"model_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AIMessage struct {
	DataCards      []alem.DataCard   `json:"data_cards,omitempty"`
	DataSources    []alem.DataSource `json:"data_sources,omitempty"`
	ID             string            `json:"id"`
	ConversationID string            `json:"conversation_id"`
	Role           string            `json:"role"`
	Content        string            `json:"content"`
	FileURL        *string           `json:"file_url"`
	AttachmentName *string           `json:"attachment_name,omitempty"`
	AttachmentMime *string           `json:"attachment_mime_type,omitempty"`
	ExtractedText  *string           `json:"-"`
	TokensUsage    int               `json:"tokens_usage"`
	CreatedAt      time.Time         `json:"created_at"`
}

func (r *AIRepository) CreateConversation(ctx context.Context, userID int64, model, title string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO ai_conversations (user_id, model_name, title)
		VALUES ($1, $2, $3)
		RETURNING id
	`, userID, model, title).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("failed to create ai conversation: %w", err)
	}
	return id, nil
}

func (r *AIRepository) GetUserConversations(ctx context.Context, userID int64) ([]AIConversation, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, title, model_name, created_at, updated_at
		FROM ai_conversations
		WHERE user_id = $1
		ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []AIConversation
	for rows.Next() {
		var c AIConversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.ModelName, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, nil
}

func (r *AIRepository) GetConversation(ctx context.Context, id string) (*AIConversation, error) {
	var c AIConversation
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, title, model_name, created_at, updated_at
		FROM ai_conversations
		WHERE id = $1
	`, id).Scan(&c.ID, &c.UserID, &c.Title, &c.ModelName, &c.CreatedAt, &c.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil // Not found
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateConversationModel updates the model used for a specific chat
func (r *AIRepository) UpdateConversationModel(ctx context.Context, chatID string, newModel string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE ai_conversations 
		SET model_name = $1, updated_at = NOW() 
		WHERE id = $2
	`, newModel, chatID)

	if err != nil {
		return fmt.Errorf("failed to update chat model: %w", err)
	}
	return nil
}

// UpdateConversationTitle updates the title of a chat
func (r *AIRepository) UpdateConversationTitle(ctx context.Context, chatID string, title string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE ai_conversations 
		SET title = $1, updated_at = NOW() 
		WHERE id = $2
	`, title, chatID)

	if err != nil {
		return fmt.Errorf("failed to update chat title: %w", err)
	}
	return nil
}

func (r *AIRepository) CreateMessage(ctx context.Context, msg AIMessage) error {
	_, err := r.CreateMessageWithID(ctx, msg)
	return err
}

func (r *AIRepository) CreateMessageWithID(ctx context.Context, msg AIMessage) (string, error) {
	if msg.DataCards == nil {
		msg.DataCards = []alem.DataCard{}
	}
	if msg.DataSources == nil {
		msg.DataSources = []alem.DataSource{}
	}
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO ai_messages (
			conversation_id, role, content, file_url, attachment_name,
			attachment_mime_type, extracted_text, tokens_usage, data_cards, data_sources
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, msg.ConversationID, msg.Role, msg.Content, msg.FileURL, msg.AttachmentName, msg.AttachmentMime, msg.ExtractedText, msg.TokensUsage, msg.DataCards, msg.DataSources).Scan(&id)

	if err != nil {
		return "", fmt.Errorf("failed to save message: %w", err)
	}

	// Update conversation updated_at
	_, _ = r.db.Exec(ctx, `UPDATE ai_conversations SET updated_at = NOW() WHERE id = $1`, msg.ConversationID)

	return id, nil
}

// DeleteUnacceptedUserMessage removes only the exact turn rejected before any
// provider dispatch. The caller must retain turns with unknown/paid outcomes.
func (r *AIRepository) DeleteUnacceptedUserMessage(ctx context.Context, conversationID, messageID string, userID int64) error {
	_, err := r.db.Exec(ctx, `DELETE FROM ai_messages m USING ai_conversations c
		WHERE m.id=$1 AND m.conversation_id=$2 AND m.role='user'
		AND c.id=m.conversation_id AND c.user_id=$3`, messageID, conversationID, userID)
	return err
}

func (r *AIRepository) GetMessages(ctx context.Context, conversationID string) ([]AIMessage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, conversation_id, role, content, file_url, attachment_name,
		       attachment_mime_type, extracted_text, tokens_usage, created_at, data_cards, data_sources
		FROM ai_messages
		WHERE conversation_id = $1
		ORDER BY created_at ASC
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []AIMessage
	for rows.Next() {
		var m AIMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.FileURL,
			&m.AttachmentName, &m.AttachmentMime, &m.ExtractedText, &m.TokensUsage, &m.CreatedAt, &m.DataCards, &m.DataSources); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// GetLastMessages retrieves the last N messages for context building
func (r *AIRepository) GetLastMessages(ctx context.Context, conversationID string, limit int) ([]AIMessage, error) {
	query := `
		SELECT id, conversation_id, role, content, file_url, attachment_name,
		       attachment_mime_type, extracted_text, tokens_usage, created_at, data_cards, data_sources
		FROM (
			SELECT * FROM ai_messages
			WHERE conversation_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		) sub
		ORDER BY created_at ASC
	`
	rows, err := r.db.Query(ctx, query, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []AIMessage
	for rows.Next() {
		var m AIMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.FileURL,
			&m.AttachmentName, &m.AttachmentMime, &m.ExtractedText, &m.TokensUsage, &m.CreatedAt, &m.DataCards, &m.DataSources); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// DeleteConversation deletes a conversation and all its messages
func (r *AIRepository) DeleteConversation(ctx context.Context, chatID string, userID int64) error {
	// First check if the conversation belongs to the user
	var ownerID int64
	err := r.db.QueryRow(ctx, `
		SELECT user_id FROM ai_conversations WHERE id = $1
	`, chatID).Scan(&ownerID)

	if err == sql.ErrNoRows {
		return fmt.Errorf("conversation not found")
	}
	if err != nil {
		return fmt.Errorf("failed to check conversation ownership: %w", err)
	}

	if ownerID != userID {
		return fmt.Errorf("unauthorized: conversation does not belong to user")
	}

	// Delete messages first (due to foreign key constraint)
	_, err = r.db.Exec(ctx, `DELETE FROM ai_messages WHERE conversation_id = $1`, chatID)
	if err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}

	// Then delete the conversation
	_, err = r.db.Exec(ctx, `DELETE FROM ai_conversations WHERE id = $1`, chatID)
	if err != nil {
		return fmt.Errorf("failed to delete conversation: %w", err)
	}

	r.logger.Info("Deleted AI conversation", zap.String("chat_id", chatID), zap.Int64("user_id", userID))
	return nil
}

// ReadChatAttachments is scoped in SQL to both the actor and current chat.
// Text is paged in PostgreSQL rather than loading every attachment into RAM.
func (r *AIRepository) ReadChatAttachments(ctx context.Context, userID int64, chatID, messageID string, page int) ([]map[string]any, error) {
	query := `SELECT m.id,m.attachment_name,m.attachment_mime_type,m.created_at,
 COALESCE(length(m.extracted_text),0),substring(m.extracted_text from $4 for 3500)
 FROM ai_messages m JOIN ai_conversations c ON c.id=m.conversation_id
 WHERE c.user_id=$1 AND c.id=$2 AND m.role='user' AND m.attachment_name IS NOT NULL AND m.id=$3`
	var rows pgx.Rows
	var err error
	if messageID == "" {
		rows, err = r.db.Query(ctx, `SELECT m.id,m.attachment_name,m.attachment_mime_type,m.created_at,
   COALESCE(length(m.extracted_text),0),NULL::text FROM ai_messages m
   JOIN ai_conversations c ON c.id=m.conversation_id
   WHERE c.user_id=$1 AND c.id=$2 AND m.role='user' AND m.attachment_name IS NOT NULL
   ORDER BY m.created_at DESC LIMIT 26 OFFSET $3`, userID, chatID, (page-1)*25)
	} else {
		rows, err = r.db.Query(ctx, query, userID, chatID, messageID, (page-1)*3500+1)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id string
		var name, mime, text *string
		var created time.Time
		var chars int
		if err = rows.Scan(&id, &name, &mime, &created, &chars, &text); err != nil {
			return nil, err
		}
		data := map[string]any{"id": id, "name": name, "mime_type": mime, "created_at": created, "total_chars": chars, "text_available": chars > 0}
		if messageID != "" {
			data["text"] = text
			data["page"] = page
			data["has_more"] = page*3500 < chars
			data["next_page"] = page + 1
		}
		result = append(result, data)
	}
	return result, rows.Err()
}
