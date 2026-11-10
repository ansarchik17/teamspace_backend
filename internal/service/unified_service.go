package service

import (
	"context"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type UnifiedService struct {
	chatRepo    *repository.ChatRepository
	groupRepo   *repository.GroupRepository
	pinnedRepo  *repository.PinnedRepository
	aiRepo      *repository.UnifiedAIRepository
	channelRepo *repository.ChannelRepository
	hub         *websocket.Hub
	logger      *zap.Logger
}

func NewUnifiedService(
	chatRepo *repository.ChatRepository,
	groupRepo *repository.GroupRepository,
	pinnedRepo *repository.PinnedRepository,
	aiRepo *repository.UnifiedAIRepository,
	channelRepo *repository.ChannelRepository,
	hub *websocket.Hub,
	logger *zap.Logger,
) *UnifiedService {
	return &UnifiedService{
		chatRepo:    chatRepo,
		groupRepo:   groupRepo,
		pinnedRepo:  pinnedRepo,
		aiRepo:      aiRepo,
		channelRepo: channelRepo,
		hub:         hub,
		logger:      logger.With(zap.String("service", "unified")),
	}
}

// UnifiedChatsTiming contains durations only, never identifiers or query text.
// Source durations run concurrently and must not be added together.
type UnifiedChatsTiming struct {
	Personal time.Duration
	Groups   time.Duration
	Channels time.Duration
	Pinned   time.Duration
	Fetch    time.Duration
	Total    time.Duration
}

func (s *UnifiedService) GetUnifiedChats(ctx context.Context, userID int64, limit, offset int) ([]*domain.ConversationView, error) {
	chats, _, err := s.GetUnifiedChatsTimed(ctx, userID, limit, offset)
	return chats, err
}

func (s *UnifiedService) GetUnifiedChatsTimed(ctx context.Context, userID int64, limit, offset int) ([]*domain.ConversationView, UnifiedChatsTiming, error) {
	var timing UnifiedChatsTiming
	chats, err := s.getUnifiedChats(ctx, userID, limit, offset, &timing)
	return chats, timing, err
}

func (s *UnifiedService) getUnifiedChats(ctx context.Context, userID int64, limit, offset int, timing *UnifiedChatsTiming) ([]*domain.ConversationView, error) {
	start := time.Now()
	defer func() { timing.Total = time.Since(start) }()
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}

	fetchLimit := 0
	if limit > 0 {
		fetchLimit = limit + offset
	}

	var (
		pinnedChats   []*domain.PinnedChat
		conversations []*domain.ConversationView
		groups        []*domain.GroupView
		channels      []domain.ChannelView

		pinnedErr error
		convErr   error
		groupErr  error
		chErr     error
	)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		started := time.Now()
		pinnedChats, pinnedErr = s.pinnedRepo.GetPinnedChats(ctx, userID)
		timing.Pinned = time.Since(started)
	}()
	go func() {
		defer wg.Done()
		started := time.Now()
		conversations, convErr = s.chatRepo.GetUserConversations(ctx, userID, fetchLimit, 0)
		timing.Personal = time.Since(started)
	}()
	go func() {
		defer wg.Done()
		started := time.Now()
		groups, groupErr = s.groupRepo.GetUserGroups(ctx, userID, fetchLimit, 0)
		timing.Groups = time.Since(started)
	}()
	if s.channelRepo != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			channels, chErr = s.channelRepo.GetUserChannels(ctx, userID)
			timing.Channels = time.Since(started)
		}()
	}
	wg.Wait()
	fetchDone := time.Now()
	timing.Fetch = fetchDone.Sub(start)

	if pinnedErr != nil {
		return nil, apperrors.InternalWithErr("failed to get pinned chats", pinnedErr)
	}
	if convErr != nil {
		return nil, apperrors.InternalWithErr("failed to get conversations", convErr)
	}
	if groupErr != nil {
		return nil, apperrors.InternalWithErr("failed to get groups", groupErr)
	}

	pinnedMap := make(map[string]bool)
	missingPinned := make(map[string]bool)

	for _, pc := range pinnedChats {
		chatType := normalizeChatType(pc.ChatType)
		key := chatType + ":" + pc.ChatID.String()
		pinnedMap[key] = true
		missingPinned[key] = true
	}

	// aiChat, err := s.aiRepo.GetOrCreateChat(ctx, userID, "google/gemma-3-27b")
	// if err != nil {
	// 	s.logger.Warn("Failed to get AI chat, excluding from unified view", zap.Error(err))
	// 	aiChat = nil
	// }

	var allChats []*domain.ConversationView
	seenChat := make(map[string]bool)
	supportChatFound := false

	// AI чат
	// if aiChat != nil {
	// 	aiChatID, parseErr := uuid.Parse(aiChat.ID)
	// 	if parseErr == nil {
	// 		chatKey := "ai:" + aiChat.ID
	// 		seenChat[chatKey] = true
	//
	// 		aiConv := &domain.ConversationView{
	// 			ID:         aiChatID,
	// 			Type:       "ai",
	// 			AIModel:    &aiChat.ModelName,
	// 			AITitle:    &aiChat.Title,
	// 			PeerAvatar: &aiChat.Avatar,
	// 			CreatedAt:  aiChat.CreatedAt,
	// 			UpdatedAt:  aiChat.UpdatedAt,
	// 			IsPinned:   true,
	// 		}
	// 		allChats = append(allChats, aiConv)
	// 	}
	// }

	// Personal (включая support)
	for _, conv := range conversations {
		chatKey := "personal:" + conv.ID.String()

		if seenChat[chatKey] {
			continue
		}
		seenChat[chatKey] = true

		if pinnedMap[chatKey] {
			conv.IsPinned = true
			delete(missingPinned, chatKey)
		}

		if conv.Type == "support" && userID != domain.SupportUserID {
			conv.IsPinned = true
			supportChatFound = true
		}

		allChats = append(allChats, conv)
	}

	// Groups
	for _, grp := range groups {
		chatKey := "group:" + grp.ID.String()
		if seenChat[chatKey] {
			continue
		}
		seenChat[chatKey] = true

		conv := &domain.ConversationView{
			ID:           grp.ID,
			Type:         "group",
			GroupID:      &grp.ID,
			GroupName:    &grp.Name,
			GroupAvatar:  grp.AvatarURL,
			CreatedAt:    grp.CreatedAt,
			UpdatedAt:    grp.UpdatedAt,
			UnreadCount:  grp.UnreadCount,
			MentionCount: grp.MentionCount,
			IsMuted:      grp.IsMuted,
			LastMessage:  grp.LastMessage,
		}

		if pinnedMap[chatKey] {
			conv.IsPinned = true
			delete(missingPinned, chatKey)
		}

		allChats = append(allChats, conv)
	}

	// Channels — Telegram-style
	if s.channelRepo != nil {
		if chErr != nil {
			s.logger.Warn("Failed to get user channels for unified", zap.Error(chErr))
		} else {
			for _, ch := range channels {
				chatKey := "channel:" + ch.ID.String()
				if seenChat[chatKey] {
					continue
				}
				seenChat[chatKey] = true

				chID := ch.ID
				chName := ch.Name
				conv := &domain.ConversationView{
					ID:            chID,
					Type:          "channel",
					IsChannel:     true,
					ChannelID:     &chID,
					ChannelName:   &chName,
					ChannelAvatar: ch.AvatarURL,
					IsMuted:       ch.IsMuted,
					UnreadCount:   ch.UnreadCount,
					CreatedAt:     ch.CreatedAt,
					UpdatedAt:     ch.UpdatedAt,
				}
				if ch.MyRole != nil {
					roleStr := string(*ch.MyRole)
					conv.MyChannelRole = &roleStr
				}
				if ch.LastPost != nil {
					conv.LastPost = ch.LastPost
					conv.LastMessage = channelPostToMessageView(ch.LastPost)
					conv.UpdatedAt = ch.LastPost.CreatedAt
				}
				allChats = append(allChats, conv)
			}
		}
	}

	// Ensure support chat (если не нашли в списке)
	if !supportChatFound && userID != domain.SupportUserID {
		supportView, err := s.ensureSupportChat(ctx, userID, pinnedMap, missingPinned, seenChat)
		if err != nil {
			s.logger.Warn("Failed to ensure support chat", zap.Error(err))
		} else if supportView != nil {
			allChats = append(allChats, supportView)
		}
	}

	// Update last seen for online users
	if s.hub != nil {
		now := time.Now()
		for _, chat := range allChats {
			if chat.Type == "personal" || chat.Type == "support" {
				if chat.PeerID != nil && s.hub.IsUserOnline(*chat.PeerID) {
					chat.PeerLastSeenAt = &now
				}
			}
		}
	}

	// Догружаем закрепы, которые не попали в основной fetch (missingPinned)
	for key := range missingPinned {
		var chatType, chatIDStr string

		switch {
		case len(key) > 6 && key[:6] == "group:":
			chatType = "group"
			chatIDStr = key[6:]
		case len(key) > 9 && key[:9] == "personal:":
			chatType = "personal"
			chatIDStr = key[9:]
		default:
			continue
		}

		chatID, parseErr := uuid.Parse(chatIDStr)
		if parseErr != nil {
			continue
		}

		switch chatType {
		case "personal":
			conv, err := s.chatRepo.GetConversationView(ctx, chatID, userID)
			if err == nil && conv != nil {
				conv.IsPinned = true
				if conv.PeerID != nil && *conv.PeerID == domain.SupportUserID && userID != domain.SupportUserID {
					conv.Type = "support"
				}
				allChats = append(allChats, conv)
			}
		case "group":
			grp, err := s.groupRepo.GetGroupView(ctx, chatID, userID)
			if err == nil && grp != nil {
				conv := &domain.ConversationView{
					ID:          grp.ID,
					Type:        "group",
					GroupID:     &grp.ID,
					GroupName:   &grp.Name,
					GroupAvatar: grp.AvatarURL,
					CreatedAt:   grp.CreatedAt,
					UpdatedAt:   grp.UpdatedAt,
					UnreadCount: grp.UnreadCount,
					LastMessage: grp.LastMessage,
					IsPinned:    true,
				}
				allChats = append(allChats, conv)
			}
		}
	}

	for _, chat := range allChats {
		signConversationView(chat)
		if chat.LastMessage != nil && chat.LastMessage.Content != nil {
			cleanContent := s.formatMessageContent(chat.LastMessage.Type, *chat.LastMessage.Content)
			chat.LastMessage.Content = &cleanContent
		}
		if chat.LastPost != nil && chat.LastPost.Content != nil {
			msgType := "text"
			if chat.LastPost.AttachmentType != nil {
				msgType = *chat.LastPost.AttachmentType
			}
			cleanContent := s.formatMessageContent(msgType, *chat.LastPost.Content)
			chat.LastPost.Content = &cleanContent
		}
	}

	// Сортировка
	sort.SliceStable(allChats, func(i, j int) bool {
		c1, c2 := allChats[i], allChats[j]

		if c1.Type == "ai" && c2.Type != "ai" {
			return true
		}
		if c2.Type == "ai" && c1.Type != "ai" {
			return false
		}

		if c1.Type == "support" && c2.Type != "support" {
			return true
		}
		if c2.Type == "support" && c1.Type != "support" {
			return false
		}

		if c1.IsPinned != c2.IsPinned {
			return c1.IsPinned
		}

		t1 := c1.UpdatedAt
		if c1.LastMessage != nil {
			t1 = c1.LastMessage.CreatedAt
		} else if c1.LastPost != nil {
			t1 = c1.LastPost.CreatedAt
		}
		t2 := c2.UpdatedAt
		if c2.LastMessage != nil {
			t2 = c2.LastMessage.CreatedAt
		} else if c2.LastPost != nil {
			t2 = c2.LastPost.CreatedAt
		}

		return t1.After(t2)
	})

	if offset >= len(allChats) {
		s.logger.Info("unified_chats_built",
			zap.Int64("user_id", userID),
			zap.Int("limit", limit),
			zap.Int("offset", offset),
			zap.Int("total_count", len(allChats)),
			zap.Int("result_count", 0),
			zap.Int64("fetch_ms", fetchDone.Sub(start).Milliseconds()),
			zap.Int64("total_ms", time.Since(start).Milliseconds()),
		)
		return []*domain.ConversationView{}, nil
	}
	if limit == 0 {
		s.logger.Info("unified_chats_built",
			zap.Int64("user_id", userID),
			zap.Int("limit", limit),
			zap.Int("offset", offset),
			zap.Int("total_count", len(allChats)),
			zap.Int("result_count", len(allChats[offset:])),
			zap.Int64("fetch_ms", fetchDone.Sub(start).Milliseconds()),
			zap.Int64("total_ms", time.Since(start).Milliseconds()),
		)
		return allChats[offset:], nil
	}

	end := offset + limit
	if end > len(allChats) {
		end = len(allChats)
	}

	result := allChats[offset:end]
	s.logger.Info("unified_chats_built",
		zap.Int64("user_id", userID),
		zap.Int("limit", limit),
		zap.Int("offset", offset),
		zap.Int("total_count", len(allChats)),
		zap.Int("result_count", len(result)),
		zap.Int64("fetch_ms", fetchDone.Sub(start).Milliseconds()),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	return result, nil
}

func signConversationView(chat *domain.ConversationView) {
	if chat == nil {
		return
	}

	chat.PeerAvatar = signLegacyFileDownloadURL(chat.PeerAvatar)
	chat.GroupAvatar = signLegacyFileDownloadURL(chat.GroupAvatar)
	chat.ChannelAvatar = signLegacyFileDownloadURL(chat.ChannelAvatar)

	if chat.LastMessage != nil {
		chat.LastMessage.SenderAvatar = signLegacyFileDownloadURL(chat.LastMessage.SenderAvatar)
		if chat.LastMessage.ReplyTo != nil {
			chat.LastMessage.ReplyTo.SenderAvatar = signLegacyFileDownloadURL(chat.LastMessage.ReplyTo.SenderAvatar)
		}
	}
}

func (s *UnifiedService) ensureSupportChat(
	ctx context.Context,
	userID int64,
	pinnedMap map[string]bool,
	missingPinned map[string]bool,
	seenChat map[string]bool,
) (*domain.ConversationView, error) {

	supportConv, err := s.chatRepo.GetConversationByParticipants(ctx, userID, domain.SupportUserID)
	if err != nil {
		s.logger.Warn("Failed to get support conversation", zap.Error(err))
		return nil, nil
	}

	var convID uuid.UUID

	if supportConv != nil {
		convID = supportConv.ID
	} else {
		newC, err := s.chatRepo.CreateConversation(ctx, userID)
		if err != nil {
			s.logger.Warn("Failed to create support conversation", zap.Error(err))
			return nil, nil
		}
		_ = s.chatRepo.AddParticipant(ctx, newC.ID, userID)
		if err := s.chatRepo.AddParticipant(ctx, newC.ID, domain.SupportUserID); err != nil {
			s.logger.Warn("Support user participant not added", zap.Error(err))
		}
		convID = newC.ID
	}

	chatKey := "personal:" + convID.String()
	if seenChat[chatKey] {
		return nil, nil
	}

	view, err := s.chatRepo.GetConversationView(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, nil
	}

	// Защита: помечаем support только если peer реально support
	if view.PeerID == nil || *view.PeerID != domain.SupportUserID {
		s.logger.Warn("ensureSupportChat: conversation peer is not support, skipping",
			zap.String("conversation_id", convID.String()),
			zap.Any("peer_id", view.PeerID),
		)
		return nil, nil
	}

	view.Type = "support"
	view.IsPinned = true

	if pinnedMap[chatKey] {
		delete(missingPinned, chatKey)
	}

	seenChat[chatKey] = true
	return view, nil
}

func channelPostToMessageView(post *domain.ChannelPost) *domain.MessageView {
	if post == nil {
		return nil
	}

	msgType := "text"
	if post.AttachmentType != nil && *post.AttachmentType != "" {
		msgType = *post.AttachmentType
	}

	return &domain.MessageView{
		Message: domain.Message{
			ID:             post.ID,
			ConversationID: post.ChannelID,
			SenderID:       post.AuthorID,
			Content:        post.Content,
			Type:           msgType,
			AttachmentURL:  post.AttachmentURL,
			AttachmentType: post.AttachmentType,
			AttachmentSize: post.AttachmentSize,
			Metadata:       post.Metadata,
			IsEdited:       post.IsEdited,
			IsForwarded:    post.IsForwarded,
			IsDeleted:      post.IsDeleted,
			CreatedAt:      post.CreatedAt,
			UpdatedAt:      post.UpdatedAt,
			EditedAt:       post.EditedAt,
		},
	}
}

func normalizeChatType(t string) string {
	switch t {
	case "conversation":
		return "personal"
	default:
		return t
	}
}

// formatMessageContent оставляет от ссылки только имя файла
func (s *UnifiedService) formatMessageContent(msgType string, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return content
	}

	switch msgType {
	case "image":
		return "📷 Фото"
	case "video":
		return "📹 Видео"
	case "audio":
		return "🎵 Аудио"
	case "voice":
		return "🎤 Голосовое"
	case "video_message":
		return "🎥 Видеосообщение"
	}

	if name, ok := extractFileName(content); ok {
		if isUUIDFileName(name) {
			if label := fileTypeLabelByExt(name); label != "" {
				return label
			}
			return "📄 Файл"
		}
		return name
	}

	if msgType == "text" {
		return content
	}

	if label := fileTypeLabelByExt(content); label != "" {
		return label
	}

	return "📄 Файл"
}

func extractFileName(content string) (string, bool) {
	s := strings.TrimSpace(content)
	if s == "" {
		return "", false
	}

	lines := splitNonEmptyLines(s)
	for _, ln := range lines {
		if name, ok := extractFromURL(ln); ok {
			return name, true
		}
	}

	for i := len(lines) - 1; i >= 0; i-- {
		if name, ok := extractFromFileLike(lines[i]); ok {
			return name, true
		}
	}

	if name, ok := extractFromURL(s); ok {
		return name, true
	}
	if name, ok := extractFromFileLike(s); ok {
		return name, true
	}

	return "", false
}

func splitNonEmptyLines(s string) []string {
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func extractFromURL(s string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}

	objName := u.Query().Get("object_name")
	if objName != "" {
		_, filename := path.Split(objName)
		filename = strings.TrimSpace(filename)
		if filename != "" {
			if parts := strings.SplitN(filename, "_", 2); len(parts) == 2 && parts[1] != "" {
				return parts[1], true
			}
			return filename, true
		}
	}

	_, filename := path.Split(u.Path)
	filename = strings.TrimSpace(filename)
	if filename != "" && filename != "download" {
		return filename, true
	}

	return "📄 Файл", true
}

func extractFromFileLike(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}

	last := s
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 && i+1 < len(s) {
		last = s[i+1:]
	}
	last = strings.TrimSpace(last)

	lower := strings.ToLower(last)
	exts := []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".rar", ".7z", ".png", ".jpg", ".jpeg", ".mp4", ".mov", ".mp3", ".wav"}
	for _, e := range exts {
		if strings.HasSuffix(lower, e) {
			return last, true
		}
	}

	if strings.Count(last, "-") >= 3 && len(last) >= 20 {
		return "📄 Файл", true
	}

	return "", false
}

func fileTypeLabelByExt(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}

	if i := strings.LastIndex(n, "."); i >= 0 {
		n = n[i:]
	}

	switch n {
	case ".pdf":
		return "📄 PDF"
	case ".doc", ".docx":
		return "📄 Документ (DOCX)"
	case ".xls", ".xlsx":
		return "📊 Excel (XLSX)"
	case ".ppt", ".pptx":
		return "📑 PowerPoint (PPTX)"
	case ".zip", ".rar", ".7z":
		return "🗜️ Архив"
	case ".png", ".jpg", ".jpeg":
		return "🖼️ Изображение"
	case ".mp4", ".mov":
		return "📹 Видео"
	case ".mp3", ".wav":
		return "🎵 Аудио"
	default:
		return ""
	}
}

func isUUIDFileName(name string) bool {
	s := strings.TrimSpace(name)
	if s == "" {
		return false
	}

	if i := strings.LastIndex(s, "."); i > 0 {
		s = s[:i]
	}

	if len(s) != 36 {
		return false
	}

	for i, ch := range s {
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
				continue
			}
			return false
		}
	}

	return true
}
