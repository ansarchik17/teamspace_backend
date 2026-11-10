package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type FolderRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewFolderRepository(db *pgxpool.Pool, logger *zap.Logger) *FolderRepository {
	return &FolderRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "folder")),
	}
}

// CreateFolder creates a new folder
func (r *FolderRepository) CreateFolder(ctx context.Context, folder *domain.ChatFolder) error {
	query := `
		INSERT INTO chat_folders (id, name, user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`

	if folder.ID == uuid.Nil {
		folder.ID = uuid.New()
	}
	now := time.Now()
	folder.CreatedAt = now
	folder.UpdatedAt = now

	err := r.db.QueryRow(ctx, query,
		folder.ID,
		folder.Name,
		folder.UserID,
		folder.CreatedAt,
		folder.UpdatedAt,
	).Scan(&folder.ID)

	if err != nil {
		r.logger.Error("Failed to create folder", zap.Error(err))
		return fmt.Errorf("create folder: %w", err)
	}

	return nil
}

// GetUserFolders gets all folders for a user with their items
func (r *FolderRepository) GetUserFolders(ctx context.Context, userID int64) ([]*domain.ChatFolderView, error) {
	queryFolders := `
		SELECT id, name, user_id, created_at, updated_at
		FROM chat_folders
		WHERE user_id = $1
		ORDER BY created_at ASC
	`

	rows, err := r.db.Query(ctx, queryFolders, userID)
	if err != nil {
		return nil, fmt.Errorf("get folders: %w", err)
	}
	defer rows.Close()

	var folders []*domain.ChatFolderView
	folderMap := make(map[uuid.UUID]*domain.ChatFolderView)

	for rows.Next() {
		f := &domain.ChatFolderView{}
		if err := rows.Scan(
			&f.ID, &f.Name, &f.UserID, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, err
		}
		f.Items = []domain.ChatFolderItem{}
		folders = append(folders, f)
		folderMap[f.ID] = f
	}

	if len(folders) == 0 {
		return folders, nil
	}

	queryItems := `
        SELECT folder_id, chat_id, chat_type, created_at
        FROM chat_folder_items
        WHERE folder_id IN (SELECT id FROM chat_folders WHERE user_id = $1)
    `
	itemRows, err := r.db.Query(ctx, queryItems, userID)
	if err != nil {
		return nil, fmt.Errorf("get items: %w", err)
	}
	defer itemRows.Close()

	for itemRows.Next() {
		var item domain.ChatFolderItem
		if err := itemRows.Scan(&item.FolderID, &item.ChatID, &item.ChatType, &item.CreatedAt); err != nil {
			return nil, err
		}
		if f, ok := folderMap[item.FolderID]; ok {
			f.Items = append(f.Items, item)
		}
	}

	return folders, nil
}

// GetFolder gets a single folder view
func (r *FolderRepository) GetFolder(ctx context.Context, folderID uuid.UUID, userID int64) (*domain.ChatFolderView, error) {
	query := `
		SELECT id, name, user_id, created_at, updated_at
		FROM chat_folders
		WHERE id = $1 AND user_id = $2
	`
	f := &domain.ChatFolderView{}
	err := r.db.QueryRow(ctx, query, folderID, userID).Scan(
		&f.ID, &f.Name, &f.UserID, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("folder not found")
		}
		return nil, fmt.Errorf("get folder: %w", err)
	}

	queryItems := `
		SELECT folder_id, chat_id, chat_type, created_at
		FROM chat_folder_items
		WHERE folder_id = $1
	`
	rows, err := r.db.Query(ctx, queryItems, folderID)
	if err != nil {
		return nil, fmt.Errorf("get folder items: %w", err)
	}
	defer rows.Close()

	f.Items = []domain.ChatFolderItem{}
	for rows.Next() {
		var item domain.ChatFolderItem
		if err := rows.Scan(&item.FolderID, &item.ChatID, &item.ChatType, &item.CreatedAt); err != nil {
			return nil, err
		}
		f.Items = append(f.Items, item)
	}

	return f, nil
}

// UpdateFolder updates folder name
func (r *FolderRepository) UpdateFolder(ctx context.Context, folder *domain.ChatFolder) error {
	query := `
		UPDATE chat_folders
		SET name = $1, updated_at = $2
		WHERE id = $3 AND user_id = $4
	`
	folder.UpdatedAt = time.Now()
	result, err := r.db.Exec(ctx, query, folder.Name, folder.UpdatedAt, folder.ID, folder.UserID)
	if err != nil {
		return fmt.Errorf("update folder: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}

// DeleteFolder deletes a folder
func (r *FolderRepository) DeleteFolder(ctx context.Context, folderID uuid.UUID, userID int64) error {
	query := `DELETE FROM chat_folders WHERE id = $1 AND user_id = $2`
	result, err := r.db.Exec(ctx, query, folderID, userID)
	if err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}

// AddChatToFolder adds a chat to a folder
func (r *FolderRepository) AddChatToFolder(ctx context.Context, item *domain.ChatFolderItem) error {
	query := `
        INSERT INTO chat_folder_items (folder_id, chat_id, chat_type, created_at)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (folder_id, chat_id, chat_type) DO NOTHING
    `
	item.CreatedAt = time.Now()
	_, err := r.db.Exec(ctx, query, item.FolderID, item.ChatID, item.ChatType, item.CreatedAt)
	if err != nil {
		return fmt.Errorf("add chat to folder: %w", err)
	}
	return nil
}

// RemoveChatFromFolder removes a chat from a folder
func (r *FolderRepository) RemoveChatFromFolder(ctx context.Context, folderID, chatID uuid.UUID) error {
	query := `
        DELETE FROM chat_folder_items
        WHERE folder_id = $1 AND chat_id = $2
    `
	_, err := r.db.Exec(ctx, query, folderID, chatID)
	if err != nil {
		return fmt.Errorf("remove chat from folder: %w", err)
	}
	return nil
}
