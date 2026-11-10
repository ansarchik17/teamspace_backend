package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

const DefaultAIModel = DefaultAlemModel

// Chat-capable models whitelist (prevents selecting embeddings/rerank/whisper/etc as chat models)
var chatModels = map[string]struct{}{
	"moonshotai/Kimi-K2-Thinking":                   {},
	"alemgpt-intent":                                {},
	"openai/gpt-oss-120b":                           {},
	"google/gemma-3-27b":                            {},
	"deepseek-ai/DeepSeek-V3.2":                     {},
	"zai-org/GLM-4.6":                               {},
	"meta-llama/Llama-4-Maverick-17B-128E-Instruct": {},
	"astanahub/alemllm":                             {},
	"Qwen/Qwen3-8B":                                 {},
	"Qwen/Qwen3-VL-235B-A22B-Instruct":              {},
	"deepseek-ai/DeepSeek-OCR":                      {},
}

func isChatModel(model string) bool {
	_, ok := chatModels[strings.TrimSpace(model)]
	return ok
}

type unifiedAIRepo interface {
	GetOrCreateChat(ctx context.Context, userID int64, initialModel string) (*repository.UnifiedAIChat, error)
	GetChat(ctx context.Context, chatID string, userID int64) (*repository.UnifiedAIChat, error)
	UpdateModel(ctx context.Context, chatID string, userID int64, newModel string) error
	UpdateTitle(ctx context.Context, chatID string, userID int64, title string) error
	GetMessages(ctx context.Context, chatID string, userID int64) ([]repository.UnifiedAIMessage, error)
	GetLastMessages(ctx context.Context, chatID string, limit int) ([]repository.UnifiedAIMessage, error)
	DeleteAllMessages(ctx context.Context, chatID string, userID int64) error
	DeleteChat(ctx context.Context, chatID string, userID int64) error
	DeleteMessage(ctx context.Context, chatID, messageID string, userID int64) error
	CreateMessage(ctx context.Context, msg repository.UnifiedAIMessage) error
	CreateMessageWithID(ctx context.Context, msg repository.UnifiedAIMessage) (string, error)
}

type UnifiedAIService struct {
	client           *alem.Client
	completionClient aiCompletionClient
	repo             unifiedAIRepo
	fileSvc          *FileService
	logger           *zap.Logger
	MinIO            config.MinIOConfig
	defaultModel     string
	paidCalls        AIPaidCallerService
	payerUsers       aiPayerUsers
}

func (s *UnifiedAIService) SetCompletionClient(c aiCompletionClient) {
	s.completionClient = c
}

func NewUnifiedAIService(
	cfg *config.Config,
	repo unifiedAIRepo,
	fileSvc *FileService,
	logger *zap.Logger,
) *UnifiedAIService {
	defaultModel := strings.TrimSpace(cfg.Alem.DefaultModel)
	if defaultModel == "" || !isChatModel(defaultModel) {
		defaultModel = DefaultAIModel
	}
	return &UnifiedAIService{
		client:       alem.NewClient(cfg.Alem.APIKey, cfg.Alem.RequestTimeout, cfg.Alem.MaxRetries),
		repo:         repo,
		fileSvc:      fileSvc,
		logger:       logger.With(zap.String("service", "unified_ai")),
		MinIO:        cfg.MinIO,
		defaultModel: defaultModel,
	}
}

// GetOrCreateChat returns user's singleton AI chat with default model
func (s *UnifiedAIService) GetOrCreateChat(ctx context.Context, userID int64) (*repository.UnifiedAIChat, error) {
	return s.repo.GetOrCreateChat(ctx, userID, s.defaultModel)
}

// GetChat retrieves chat by ID with ownership verification
func (s *UnifiedAIService) GetChat(ctx context.Context, chatID string, userID int64) (*repository.UnifiedAIChat, error) {
	chat, err := s.repo.GetChat(ctx, chatID, userID)
	if err != nil {
		return nil, err
	}
	if chat == nil {
		return nil, fmt.Errorf("chat not found or unauthorized")
	}
	return chat, nil
}

// UpdateModel changes the AI model for the chat
func (s *UnifiedAIService) UpdateModel(ctx context.Context, chatID string, userID int64, newModel string) error {
	// Optional: you can validate here too, to block saving non-chat models
	if strings.TrimSpace(newModel) == "" {
		return fmt.Errorf("model is empty")
	}
	if !isChatModel(newModel) {
		return fmt.Errorf("model %q is not a chat model", newModel)
	}
	return s.repo.UpdateModel(ctx, chatID, userID, newModel)
}

// UpdateTitle changes the chat title
func (s *UnifiedAIService) UpdateTitle(ctx context.Context, chatID string, userID int64, title string) error {
	return s.repo.UpdateTitle(ctx, chatID, userID, title)
}

// GetMessages retrieves all messages for a chat
func (s *UnifiedAIService) GetMessages(ctx context.Context, chatID string, userID int64) ([]repository.UnifiedAIMessage, error) {
	return s.repo.GetMessages(ctx, chatID, userID)
}

// ClearHistory deletes all messages from the chat
func (s *UnifiedAIService) ClearHistory(ctx context.Context, chatID string, userID int64) error {
	return s.repo.DeleteAllMessages(ctx, chatID, userID)
}

// DeleteChat deletes the entire chat (allows user to reset and create new one)
func (s *UnifiedAIService) DeleteChat(ctx context.Context, chatID string, userID int64) error {
	return s.repo.DeleteChat(ctx, chatID, userID)
}

// DeleteMessage deletes a specific message from chat
func (s *UnifiedAIService) DeleteMessage(ctx context.Context, chatID, messageID string, userID int64) error {
	return s.repo.DeleteMessage(ctx, chatID, messageID, userID)
}

// SendMessage handles the full AI interaction flow.
// IMPORTANT CHANGE: now it uses model from DB (user-selected), with DefaultAIModel fallback,
// and blocks non-chat models from being used in chat.
func (s *UnifiedAIService) SendMessage(
	ctx context.Context,
	chatID string,
	userID int64,
	content string,
	fileURL *string,
) (*alem.ChatCompletionResponse, error) {
	// 1. Get and verify chat ownership
	chat, err := s.repo.GetChat(ctx, chatID, userID)
	if err != nil {
		return nil, err
	}
	if chat == nil {
		return nil, fmt.Errorf("chat not found or unauthorized")
	}

	ctx, err = s.paidUserContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	chatUUID, err := uuid.Parse(chatID)
	if err != nil {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}

	// 2. Use model from DB, fallback to default if empty
	targetModel := strings.TrimSpace(chat.ModelName)
	if targetModel == "" || !isChatModel(targetModel) {
		targetModel = s.defaultModel
	}

	// 2.1 Validate it is chat-capable
	if !isChatModel(targetModel) {
		return nil, domain.ErrAICreditUnknownModel
	}

	// 3. Save User Message
	userMsg := repository.UnifiedAIMessage{
		ChatID:  chatID,
		Role:    "user",
		Content: content,
		FileURL: fileURL,
	}
	turnID, err := s.repo.CreateMessageWithID(ctx, userMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to save user message: %w", err)
	}

	var singleClient aiCompletionClient = alemSingleAttempt{s.client}
	if s.completionClient != nil {
		singleClient = s.completionClient
	}
	paidClient := newUnifiedAIPaidChat(s.paidCalls, singleClient, chatUUID, turnID)

	// 4. Build Context (Last 10 messages)
	var alemMessages []alem.Message

	history, err := s.repo.GetLastMessages(ctx, chatID, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	// Find the last message with an image
	lastImageIndex := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].FileURL != nil && strings.TrimSpace(*history[i].FileURL) != "" {
			lastImageIndex = i
			break
		}
	}

	// Transform history ensuring role alternation
	var lastRole string
	for i, m := range history {
		if m.Role == lastRole {
			s.logger.Warn("Skipping message with duplicate consecutive role",
				zap.String("chat_id", chatID),
				zap.String("role", m.Role),
				zap.String("prev_role", lastRole),
				zap.Int("index", i),
			)
			continue
		}
		lastRole = m.Role

		isVisionMsg := m.FileURL != nil && strings.TrimSpace(*m.FileURL) != ""
		isVisionModel := isModelMultimodal(targetModel)
		isLastImage := i == lastImageIndex

		// Only send vision structure for the LAST image in a vision-capable model
		if isVisionMsg && isVisionModel && isLastImage {
			alemMessages = append(alemMessages, alem.Message{
				Role: m.Role,
				Content: []alem.ContentPart{
					{Type: "text", Text: m.Content},
					{Type: "image_url", ImageURL: &alem.ImageURL{URL: strings.TrimSpace(*m.FileURL)}},
				},
			})
		} else {
			// Text only (append URL reference if file exists but not using vision)
			msgContent := m.Content
			if isVisionMsg {
				msgContent = fmt.Sprintf("%s\n\n[Previous attachment: %s]", msgContent, strings.TrimSpace(*m.FileURL))
			}

			alemMessages = append(alemMessages, alem.Message{
				Role:    m.Role,
				Content: msgContent,
			})
		}
	}

	// Ensure we have messages and the last one is from user
	if len(alemMessages) == 0 {
		return nil, fmt.Errorf("no valid messages to send to AI")
	}
	if alemMessages[len(alemMessages)-1].Role != "user" {
		s.logger.Error("Last message is not from user",
			zap.String("chat_id", chatID),
			zap.String("last_role", alemMessages[len(alemMessages)-1].Role),
		)
		return nil, fmt.Errorf("invalid message sequence: last message must be from user")
	}

	// 5. Call Alem API
	req := alem.ChatCompletionRequest{
		Model:       targetModel,
		Messages:    alemMessages,
		MaxTokens:   1000,
		Temperature: 0.7,
	}

	s.logger.Info("Sending request to Alem API",
		zap.String("model", targetModel),
		zap.Int("message_count", len(alemMessages)),
	)

	resp, err := paidClient.CreateChatCompletion(ctx, req)
	if err != nil && targetModel != s.defaultModel && isModelAccessDenied(err) {
		s.logger.Warn("AI model is not available for the provider team; falling back",
			zap.String("chat_id", chatID),
			zap.String("from_model", targetModel),
			zap.String("to_model", s.defaultModel),
		)
		targetModel = s.defaultModel
		req.Model = targetModel
		if updateErr := s.repo.UpdateModel(ctx, chatID, userID, targetModel); updateErr != nil {
			s.logger.Warn("failed to persist fallback AI model", zap.Error(updateErr), zap.String("chat_id", chatID))
		}
		resp, err = paidClient.CreateChatCompletion(ctx, req)
	}
	if err != nil {
		return nil, err
	}

	// 6. Save Assistant Response
	if len(resp.Choices) > 0 {
		var assistantContent string
		if strContent, ok := resp.Choices[0].Message.Content.(string); ok {
			assistantContent = strContent
		} else {
			assistantContent = "Received complex content from AI"
		}

		assistantMsg := repository.UnifiedAIMessage{
			ChatID:      chatID,
			Role:        "assistant",
			Content:     assistantContent,
			TokensUsage: resp.Usage.TotalTokens,
		}

		if saveErr := s.repo.CreateMessage(ctx, assistantMsg); saveErr != nil {
			s.logger.Error("Failed to save AI response", zap.Error(saveErr))
			return nil, fmt.Errorf("failed to save assistant response: %w", saveErr)
		}
	}

	return resp, nil
}

// ListModels proxy to Alem API
func (s *UnifiedAIService) ListModels(ctx context.Context) (*alem.ModelListResponse, error) {
	return s.client.ListModels(ctx)
}

// CreateEmbeddings proxy to Alem API
func (s *UnifiedAIService) CreateEmbeddings(ctx context.Context, req alem.EmbeddingRequest) (*alem.EmbeddingResponse, error) {
	return s.client.CreateEmbeddings(ctx, req)
}

// Rerank proxy to Alem API
func (s *UnifiedAIService) Rerank(ctx context.Context, req alem.RerankRequest) (*alem.RerankResponse, error) {
	return s.client.Rerank(ctx, req)
}
