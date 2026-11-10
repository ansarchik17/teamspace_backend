package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type FolderService struct {
	folderRepo *repository.FolderRepository
	chatRepo   *repository.ChatRepository
	groupRepo  *repository.GroupRepository
	logger     *zap.Logger
}

func NewFolderService(
	folderRepo *repository.FolderRepository,
	chatRepo *repository.ChatRepository,
	groupRepo *repository.GroupRepository,
	logger *zap.Logger,
) *FolderService {
	return &FolderService{
		folderRepo: folderRepo,
		chatRepo:   chatRepo,
		groupRepo:  groupRepo,
		logger:     logger.With(zap.String("service", "folder")),
	}
}

func (s *FolderService) CreateFolder(ctx context.Context, userID int64, name string) (*domain.ChatFolder, error) {
	folder := &domain.ChatFolder{
		Name:   name,
		UserID: userID,
	}

	if err := s.folderRepo.CreateFolder(ctx, folder); err != nil {
		return nil, err
	}

	return folder, nil
}

func (s *FolderService) GetUserFolders(ctx context.Context, userID int64) ([]*domain.ChatFolderView, error) {
	start := time.Now()
	folders, err := s.folderRepo.GetUserFolders(ctx, userID)
	if err != nil {
		return nil, err
	}
	fetchDone := time.Now()

	totalItems := 0
	for _, folder := range folders {
		totalItems += len(folder.Items)
	}

	const maxConcurrentFolderChatLoads = 8
	sem := make(chan struct{}, maxConcurrentFolderChatLoads)
	var wg sync.WaitGroup

	type folderChatLoad struct {
		folder   *domain.ChatFolderView
		chatView []*domain.ConversationView
	}

	loads := make([]folderChatLoad, 0, len(folders))

	// Keep the existing per-chat view queries, but avoid paying their latency sequentially.
	for _, folder := range folders {
		folder.Chats = make([]domain.ConversationView, 0, len(folder.Items))
		chatViews := make([]*domain.ConversationView, len(folder.Items))
		loads = append(loads, folderChatLoad{folder: folder, chatView: chatViews})

		for i, item := range folder.Items {
			wg.Add(1)
			go func(folderID uuid.UUID, idx int, item domain.ChatFolderItem, results []*domain.ConversationView) {
				defer wg.Done()

				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}

				var chatView *domain.ConversationView

				switch item.ChatType {
				case "conversation", "personal":
					cv, err := s.chatRepo.GetConversationView(ctx, item.ChatID, userID)
					if err == nil {
						chatView = cv
					} else {
						s.logger.Warn("failed to get conversation view for folder",
							zap.String("folder_id", folderID.String()),
							zap.String("chat_id", item.ChatID.String()),
							zap.Error(err),
						)
					}
				case "group":
					gv, err := s.groupRepo.GetGroupView(ctx, item.ChatID, userID)
					if err == nil {
						chatView = &domain.ConversationView{
							ID:          gv.ID,
							Type:        "group",
							GroupID:     &gv.ID,
							GroupName:   &gv.Name,
							GroupAvatar: gv.AvatarURL,
							CreatedAt:   gv.CreatedAt,
							UpdatedAt:   gv.UpdatedAt,
							UnreadCount: gv.UnreadCount,
							LastMessage: gv.LastMessage,
						}
					} else {
						s.logger.Warn("failed to get group view for folder",
							zap.String("folder_id", folderID.String()),
							zap.String("chat_id", item.ChatID.String()),
							zap.Error(err),
						)
					}
				}

				results[idx] = chatView
			}(folder.ID, i, item, chatViews)
		}
	}

	wg.Wait()

	for _, load := range loads {
		for _, chatView := range load.chatView {
			if chatView != nil {
				load.folder.Chats = append(load.folder.Chats, *chatView)
			}
		}
	}

	s.logger.Info("chat_folders_built",
		zap.Int64("user_id", userID),
		zap.Int("folders_count", len(folders)),
		zap.Int("items_count", totalItems),
		zap.Int64("folders_fetch_ms", fetchDone.Sub(start).Milliseconds()),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	return folders, nil
}

func (s *FolderService) UpdateFolder(ctx context.Context, folderID uuid.UUID, userID int64, name string) error {
	folder := &domain.ChatFolder{
		ID:     folderID,
		UserID: userID,
		Name:   name,
	}
	return s.folderRepo.UpdateFolder(ctx, folder)
}

func (s *FolderService) DeleteFolder(ctx context.Context, folderID uuid.UUID, userID int64) error {
	return s.folderRepo.DeleteFolder(ctx, folderID, userID)
}

func (s *FolderService) AddChatToFolder(ctx context.Context, folderID uuid.UUID, userID int64, chatID uuid.UUID, chatType string) error {
	// Check ownership
	_, err := s.folderRepo.GetFolder(ctx, folderID, userID)
	if err != nil {
		return err
	}

	if chatType != "conversation" && chatType != "group" && chatType != "personal" {
		return fmt.Errorf("invalid chat type")
	}

	item := &domain.ChatFolderItem{
		FolderID: folderID,
		ChatID:   chatID,
		ChatType: chatType,
	}

	return s.folderRepo.AddChatToFolder(ctx, item)
}

func (s *FolderService) RemoveChatFromFolder(ctx context.Context, folderID uuid.UUID, userID int64, chatID uuid.UUID) error {
	// Check ownership
	_, err := s.folderRepo.GetFolder(ctx, folderID, userID)
	if err != nil {
		return err
	}

	return s.folderRepo.RemoveChatFromFolder(ctx, folderID, chatID)
}

func (s *FolderService) GetFolder(ctx context.Context, folderID uuid.UUID, userID int64) (res *domain.ChatFolderView, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Panic in GetFolder", zap.Any("recover", r), zap.Stack("stack"))
			err = fmt.Errorf("internal server error: panic recovered")
		}
	}()

	folder, err := s.folderRepo.GetFolder(ctx, folderID, userID)
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, fmt.Errorf("folder is nil somehow")
	}

	// Enrich with chat details
	folder.Chats = make([]domain.ConversationView, 0, len(folder.Items))
	for _, item := range folder.Items {
		var chatView *domain.ConversationView

		switch item.ChatType {
		case "conversation", "personal":
			// Personal chat
			cv, err := s.chatRepo.GetConversationView(ctx, item.ChatID, userID)
			if err == nil {
				chatView = cv
			} else {
				s.logger.Warn("failed to get conversation view for folder", zap.String("chat_id", item.ChatID.String()), zap.Error(err))
			}
		case "group":
			// Group chat
			gv, err := s.groupRepo.GetGroupView(ctx, item.ChatID, userID)
			if err == nil {
				// Convert GroupView to ConversationView
				chatView = &domain.ConversationView{
					ID:          gv.ID,
					Type:        "group",
					GroupID:     &gv.ID,
					GroupName:   &gv.Name,
					GroupAvatar: gv.AvatarURL,
					CreatedAt:   gv.CreatedAt,
					UpdatedAt:   gv.UpdatedAt,
					UnreadCount: gv.UnreadCount,
					LastMessage: gv.LastMessage,
				}
			} else {
				s.logger.Warn("failed to get group view for folder", zap.String("chat_id", item.ChatID.String()), zap.Error(err))
			}
		}

		if chatView != nil {
			folder.Chats = append(folder.Chats, *chatView)
		}
	}

	// Sort chats by LastMessage timestamp descending
	sort.Slice(folder.Chats, func(i, j int) bool {
		t1 := folder.Chats[i].UpdatedAt
		if folder.Chats[i].LastMessage != nil {
			t1 = folder.Chats[i].LastMessage.CreatedAt
		}

		t2 := folder.Chats[j].UpdatedAt
		if folder.Chats[j].LastMessage != nil {
			t2 = folder.Chats[j].LastMessage.CreatedAt
		}

		return t1.After(t2)
	})

	return folder, nil
}
