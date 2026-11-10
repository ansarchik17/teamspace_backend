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

type NotebookRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewNotebookRepository(db *pgxpool.Pool, logger *zap.Logger) *NotebookRepository {
	return &NotebookRepository{
		db:     db,
		logger: logger,
	}
}

// CreateChat creates a new notebook chat session
func (r *NotebookRepository) CreateChat(ctx context.Context, userID int64, title string) (*domain.NotebookChat, error) {
	chat := &domain.NotebookChat{
		ID:        uuid.New(),
		UserID:    userID,
		Title:     title,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	query := `
		INSERT INTO notebook_chats (id, user_id, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.Exec(ctx, query, chat.ID, chat.UserID, chat.Title, chat.CreatedAt, chat.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create notebook chat: %w", err)
	}

	return chat, nil
}

// GetUserChats returns all chats for a user
func (r *NotebookRepository) GetUserChats(ctx context.Context, userID int64) ([]*domain.NotebookChat, error) {
	query := `
		SELECT id, user_id, title, created_at, updated_at
		FROM notebook_chats
		WHERE user_id = $1
		ORDER BY updated_at DESC
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []*domain.NotebookChat
	for rows.Next() {
		c := &domain.NotebookChat{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, nil
}

// GetChatMessages retrieves all messages for a specific chat
func (r *NotebookRepository) GetChatMessages(ctx context.Context, chatID uuid.UUID) ([]*domain.NotebookMessage, error) {
	query := `
		SELECT id, chat_id, role, type, content, created_at
		FROM notebook_messages
		WHERE chat_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.db.Query(ctx, query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*domain.NotebookMessage
	for rows.Next() {
		m := &domain.NotebookMessage{}
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Type, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}

// AddMessage adds a single message to a chat (defaults to type "text")
func (r *NotebookRepository) AddMessage(ctx context.Context, chatID uuid.UUID, role, content string) error {
	return r.AddMessageWithType(ctx, chatID, role, domain.NotebookMessageTypeText, content)
}

// AddMessageWithType adds a single message to a chat with a specific type
func (r *NotebookRepository) AddMessageWithType(ctx context.Context, chatID uuid.UUID, role, msgType, content string) error {
	msgID := uuid.New()
	createdAt := time.Now()

	if msgType == "" {
		msgType = domain.NotebookMessageTypeText
	}

	query := `
		INSERT INTO notebook_messages (id, chat_id, role, type, content, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.db.Exec(ctx, query, msgID, chatID, role, msgType, content, createdAt)
	if err != nil {
		return err
	}

	// Update chat timestamp
	_, _ = r.db.Exec(ctx, "UPDATE notebook_chats SET updated_at = $1 WHERE id = $2", createdAt, chatID)

	return nil
}

// DeleteChat deletes a chat and its messages (CASCADE expected in DB)
func (r *NotebookRepository) DeleteChat(ctx context.Context, chatID uuid.UUID, userID int64) error {
	query := "DELETE FROM notebook_chats WHERE id = $1 AND user_id = $2"
	_, err := r.db.Exec(ctx, query, chatID, userID)
	return err
}

// GetChatByID verifies ownership and returns chat
func (r *NotebookRepository) GetChatByID(ctx context.Context, chatID uuid.UUID) (*domain.NotebookChat, error) {
	query := "SELECT id, user_id, title, created_at, updated_at FROM notebook_chats WHERE id = $1"
	chat := &domain.NotebookChat{}
	err := r.db.QueryRow(ctx, query, chatID).Scan(&chat.ID, &chat.UserID, &chat.Title, &chat.CreatedAt, &chat.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return chat, nil
}

func (r *NotebookRepository) UpdateChatTitle(ctx context.Context, chatID uuid.UUID, title string) error {
	query := "UPDATE notebook_chats SET title = $1, updated_at = $2 WHERE id = $3"
	_, err := r.db.Exec(ctx, query, title, time.Now(), chatID)
	return err
}

func (r *NotebookRepository) AddChatDocument(ctx context.Context, chatID, docID uuid.UUID) error {
	query := "INSERT INTO notebook_chat_documents (chat_id, document_id) VALUES ($1, $2) ON CONFLICT DO NOTHING"
	_, err := r.db.Exec(ctx, query, chatID, docID)
	return err
}

func (r *NotebookRepository) RemoveChatDocument(ctx context.Context, chatID, docID uuid.UUID) error {
	query := "DELETE FROM notebook_chat_documents WHERE chat_id = $1 AND document_id = $2"
	_, err := r.db.Exec(ctx, query, chatID, docID)
	return err
}

func (r *NotebookRepository) SaveChatDocuments(ctx context.Context, chatID uuid.UUID, docIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Clear existing documents for this chat
	if _, err := tx.Exec(ctx, "DELETE FROM notebook_chat_documents WHERE chat_id = $1", chatID); err != nil {
		return err
	}

	// 2. Insert new documents
	if len(docIDs) > 0 {
		query := "INSERT INTO notebook_chat_documents (chat_id, document_id) VALUES ($1, $2)"
		for _, docID := range docIDs {
			if _, err := tx.Exec(ctx, query, chatID, docID); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

func (r *NotebookRepository) GetChatDocuments(ctx context.Context, chatID uuid.UUID) ([]*domain.DriveFile, error) {
	query := `
		SELECT f.id, f.owner_id, f.folder_id, f.real_name, f.object_name, f.source_system_code, f.source_system_name,
		       f.external_message_id, f.size, f.mime_type, f.version, f.onlyoffice_key, f.created_at, f.updated_at, f.deleted_at
		FROM files f
		JOIN notebook_chat_documents nd ON nd.document_id = f.id
		WHERE nd.chat_id = $1 AND f.deleted_at IS NULL
		ORDER BY f.real_name ASC
	`
	rows, err := r.db.Query(ctx, query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*domain.DriveFile
	for rows.Next() {
		f := &domain.DriveFile{}
		if err := rows.Scan(
			&f.ID, &f.OwnerID, &f.FolderID, &f.RealName, &f.ObjectName, &f.SourceSystemCode, &f.SourceSystemName,
			&f.ExternalMessageID, &f.Size, &f.MimeType, &f.Version, &f.Key, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
		); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}
