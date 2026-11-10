package service

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type AlemService struct {
	client       *alem.Client
	repo         *repository.AIRepository
	documentRepo *repository.AIDocumentRepository
	fileSvc      *FileService
	extractor    *ContentExtractionService
	logger       *zap.Logger
	MinIO        config.MinIOConfig
	defaultModel string
	userData     AIUserDataReader
	chatAgent    *SmartAgentService
	paidCalls    AIPaidCallerService
	payerUsers   aiPayerUsers
}

const DefaultAlemModel = "openai/gpt-oss-120b"

func NewAlemService(cfg *config.Config, repo *repository.AIRepository, fileSvc *FileService, extractor *ContentExtractionService, logger *zap.Logger) *AlemService {
	defaultModel := strings.TrimSpace(cfg.Alem.DefaultModel)
	if defaultModel == "" || !isChatModel(defaultModel) {
		defaultModel = DefaultAlemModel
	}
	return &AlemService{
		client:       alem.NewClient(cfg.Alem.APIKey, cfg.Alem.RequestTimeout, cfg.Alem.MaxRetries),
		repo:         repo,
		fileSvc:      fileSvc,
		extractor:    extractor,
		logger:       logger,
		MinIO:        cfg.MinIO,
		defaultModel: defaultModel,
	}
}

func (s *AlemService) SetDocumentRepository(repo *repository.AIDocumentRepository) {
	s.documentRepo = repo
}
func (s *AlemService) SetUserDataReader(reader AIUserDataReader) { s.userData = reader }
func (s *AlemService) SetChatAgent(agent *SmartAgentService)     { s.chatAgent = agent }

// UploadAIFile uploads a file to MinIO "ai-uploads" prefix and returns the PUBLIC URL.
// MinIO Lifecycle policies should calculate expiration on this prefix.
// We're doing a trick here to reuse FileService logic but place it in a specific "folder" structure that we can target with Lifecycle rules.
func (s *AlemService) UploadAIFile(ctx context.Context, userID int64, file *multipart.FileHeader) (string, error) {
	// Open the file
	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	// We can't easily force FileService to change bucket or prefix without modifying it.
	// However, FileService generates object name based on fileType argument in UploadFile.
	// public func UploadFile(ctx context.Context, userID int64, filename string, fileType string, size int64, reader io.Reader)
	// format: "%s/%d/%d_%s%s" -> fileType/userID/timestamp_uuid.ext

	// If we pass "ai-uploads" as fileType, the object name will start with "ai-uploads/..."
	// Then we can set a lifecycle rule on "ai-uploads/" prefix in the bucket.

	objectName, err := s.fileSvc.UploadFile(ctx, userID, file.Filename, "ai-uploads", file.Size, src)
	if err != nil {
		return "", err
	}

	// Construct public URL directly for Alem
	// {publicBaseURL}/{bucket}/{objectName} usually, or just {publicBaseURL}/{objectName} depending on setup.
	// Looking at config: PublicBaseURL: "http://localhost:9000/messenger" (includes bucket)
	// So simply appending objectName is correct.

	// BUT, FileService.GetFileURL returns a PROXY URL (/api/v1/files/download).
	// Alem Cloud cannot reach localhost / internal proxy. It needs a real public URL if we are local,
	// OR it needs the MinIO URL if MinIO is public.
	// Assuming MinIO PublicBaseURL is reachable by Alem, we construct it manually.

	// Clean way:
	baseURL := strings.TrimRight(strings.TrimSpace(s.MinIO.PublicBaseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("AI file storage public URL is not configured")
	}
	fullURL := fmt.Sprintf("%s/%s", baseURL, objectName)

	// Check if PublicBaseURL ends with slash
	return fullURL, nil
}

func (s *AlemService) CreateChat(ctx context.Context, userID int64, model string, title string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = s.defaultModel
	}
	if !isChatModel(model) {
		return "", fmt.Errorf("model %q is not a supported chat model", model)
	}
	if strings.TrimSpace(title) == "" {
		title = "New Chat"
	}
	return s.repo.CreateConversation(ctx, userID, model, strings.TrimSpace(title))
}

func (s *AlemService) GetUserChats(ctx context.Context, userID int64) ([]repository.AIConversation, error) {
	return s.repo.GetUserConversations(ctx, userID)
}

func (s *AlemService) GetChatHistory(ctx context.Context, chatID string, userID int64) ([]repository.AIMessage, error) {
	if err := s.ensureConversationOwner(ctx, chatID, userID); err != nil {
		return nil, err
	}
	return s.repo.GetMessages(ctx, chatID)
}

func (s *AlemService) DeleteChat(ctx context.Context, chatID string, userID int64) error {
	return s.repo.DeleteConversation(ctx, chatID, userID)
}

// SendMessage handles the full flow: User Msg Save -> AI Context -> Alem Req -> AI Msg Save
func (s *AlemService) SendMessage(ctx context.Context, chatID string, userID int64, content string, fileURL *string, newModel string) (*alem.ChatCompletionResponse, error) {
	return s.sendMessage(ctx, chatID, userID, content, fileURL, "", "", nil, newModel)
}

// SendMessageWithAttachment stores the original file, extracts its text on the
// server, and persists that text next to the user message. This is what makes
// follow-up questions work after the upload request has finished.
func (s *AlemService) SendMessageWithAttachment(ctx context.Context, chatID string, userID int64, content string, file *multipart.FileHeader, newModel string) (*alem.ChatCompletionResponse, error) {
	if file == nil {
		return nil, fmt.Errorf("attachment is required")
	}

	filename := filepath.Base(strings.TrimSpace(file.Filename))
	mimeType := strings.TrimSpace(file.Header.Get("Content-Type"))
	var extractedText *string
	if s.extractor != nil && !isImageFilename(filename, mimeType) {
		text, extractErr := s.extractor.ExtractText(file)
		if extractErr != nil {
			return nil, fmt.Errorf("extract text from %s: %w", filename, extractErr)
		}
		cleaned := strings.TrimSpace(text)
		if cleaned == "" {
			return nil, fmt.Errorf("no readable text found in %s", filename)
		}
		extractedText = &cleaned
	}

	fileURL, err := s.UploadAIFile(ctx, userID, file)
	if err != nil {
		return nil, fmt.Errorf("store AI attachment: %w", err)
	}

	return s.sendMessage(ctx, chatID, userID, content, &fileURL, filename, mimeType, extractedText, newModel)
}

func isImageFilename(filename, mimeType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") ||
		strings.HasPrefix(strings.ToLower(filepath.Ext(filename)), ".jpg") ||
		strings.HasPrefix(strings.ToLower(filepath.Ext(filename)), ".jpeg") ||
		strings.HasPrefix(strings.ToLower(filepath.Ext(filename)), ".png") ||
		strings.HasPrefix(strings.ToLower(filepath.Ext(filename)), ".webp")
}

func (s *AlemService) sendMessage(ctx context.Context, chatID string, userID int64, content string, fileURL *string, attachmentName string, attachmentMime string, extractedText *string, newModel string) (_ *alem.ChatCompletionResponse, sendErr error) {
	// 1. Get Chat details (for model)
	chat, err := s.repo.GetConversation(ctx, chatID)
	if err != nil {
		return nil, fmt.Errorf("chat not found: %w", err)
	}
	if chat == nil {
		return nil, fmt.Errorf("chat %s does not exist", chatID)
	}
	if chat.UserID != userID {
		return nil, fmt.Errorf("unauthorized: conversation does not belong to user")
	}
	ctx, err = s.paidUserContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	chatUUID, err := uuid.Parse(chatID)
	if err != nil {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	newModel = strings.TrimSpace(newModel)
	if newModel != "" && !isChatModel(newModel) {
		return nil, domain.ErrAICreditUnknownModel
	}

	// 2. Handle Model Switching
	targetModel := chat.ModelName
	if newModel != "" && newModel != chat.ModelName {
		// User wants to switch model
		s.logger.Info("Switching model for chat", zap.String("chatID", chatID), zap.String("old", chat.ModelName), zap.String("new", newModel))
		if err := s.repo.UpdateConversationModel(ctx, chatID, newModel); err != nil {
			s.logger.Error("Failed to update chat model setting, proceeding with old one", zap.Error(err))
		} else {
			targetModel = newModel
		}
	}

	// 3. Save User Message
	userMsg := repository.AIMessage{
		ConversationID: chatID,
		Role:           "user",
		Content:        content,
		FileURL:        fileURL,
		AttachmentName: aiOptionalString(attachmentName),
		AttachmentMime: aiOptionalString(attachmentMime),
		ExtractedText:  extractedText,
	}
	turnID, err := s.repo.CreateMessageWithID(ctx, userMsg)
	if err != nil {
		return nil, err
	}
	paidClient := &alemPaidChat{executor: s.paidCalls, client: alemSingleAttempt{s.client}, chatID: chatUUID, turnID: turnID}
	defer func() {
		if paidClient.dispatched || !aiTurnRejectedBeforeDispatch(sendErr) {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.repo.DeleteUnacceptedUserMessage(cleanupCtx, chatID, turnID, userID); err != nil {
			s.logger.Error("Failed to remove unaccepted AI message", zap.Error(err))
		}
	}()

	// 4. Build conversation context. Contextual chat retains more follow-ups;
	// the transformed history is bounded before it enters the provider request.
	// NOTE: Not using system message because models like google/gemma-3-27b don't support it
	// and it causes "Conversation roles must alternate user/assistant" error
	alemMessages := []alem.Message{}

	historyLimit := 10
	if s.userData != nil {
		historyLimit = 40
	}
	history, err := s.repo.GetLastMessages(ctx, chatID, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	var documentChunks []repository.AIDocumentChunk
	if s.userData == nil && s.documentRepo != nil && strings.TrimSpace(content) != "" {
		documentChunks, err = s.documentRepo.SearchChunks(ctx, userID, chatID, content, nil, 8)
		if err != nil {
			s.logger.Warn("document search failed; trying ready-document fallback", zap.Error(err), zap.String("chat_id", chatID))
		}
		if len(documentChunks) == 0 {
			fallbackChunks, fallbackErr := s.documentRepo.ListReadyChunks(ctx, userID, chatID, nil, 8)
			if fallbackErr != nil {
				s.logger.Warn("ready-document fallback failed; continuing without document context", zap.Error(fallbackErr), zap.String("chat_id", chatID))
			} else {
				documentChunks = fallbackChunks
				s.logger.Info("using ready-document fallback context", zap.String("chat_id", chatID), zap.Int("chunks", len(documentChunks)))
			}
		}
	}

	// Find the last message with an image to determine which one gets vision treatment
	lastImageIndex := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].FileURL != nil && *history[i].FileURL != "" {
			lastImageIndex = i
			break
		}
	}

	// Transform history
	var lastRole string
	for i, m := range history {
		// Skip if same role appears consecutively (database anomaly protection)
		if m.Role == lastRole {
			s.logger.Warn("Skipping message with duplicate consecutive role",
				zap.String("chat_id", chatID),
				zap.String("role", m.Role),
				zap.String("prev_role", lastRole),
				zap.Int("index", i),
			)
			// Preserve the latest question after a provider failure left a
			// previous user turn without a reply. Do not silently resend it.
			if m.Role == "user" && len(alemMessages) > 0 {
				alemMessages = alemMessages[:len(alemMessages)-1]
			} else {
				continue
			}
		}
		lastRole = m.Role

		isVisionMsg := m.FileURL != nil && *m.FileURL != ""
		isVisionModel := isModelMultimodal(targetModel)
		isLastImage := i == lastImageIndex // Only the latest image gets full vision treatment

		// Debug decision
		if isVisionMsg {
			s.logger.Info("Message processing",
				zap.String("model", targetModel),
				zap.Bool("has_file", true),
				zap.Bool("model_supports_vision", isVisionModel),
				zap.Bool("is_last_image", isLastImage),
				zap.String("decision", func() string {
					if isVisionModel && isLastImage {
						return "send_vision_structure"
					}
					return "downgrade_to_text"
				}()),
				zap.String("file_url", *m.FileURL),
			)
		}

		// Only send vision structure for the LAST image in a vision-capable model
		if isVisionMsg && isVisionModel && isLastImage {
			// Vision request structure
			alemMessages = append(alemMessages, alem.Message{
				Role: m.Role,
				Content: []alem.ContentPart{
					{Type: "text", Text: m.Content},
					{Type: "image_url", ImageURL: &alem.ImageURL{URL: *m.FileURL}},
				},
			})
		} else {
			// Text only (append URL reference if file exists but not using vision)
			content := m.Content
			if i == len(history)-1 && m.Role == "user" && len(documentChunks) > 0 {
				content += buildDocumentPrompt(documentChunks)
			}
			if m.ExtractedText != nil && strings.TrimSpace(*m.ExtractedText) != "" {
				label := "документ"
				if m.AttachmentName != nil && strings.TrimSpace(*m.AttachmentName) != "" {
					label = *m.AttachmentName
				}
				if s.userData != nil {
					content = fmt.Sprintf("%s\n\n[Вложение: %s, message_id=%s. Полный текст доступен через chat_attachments.]", content, label, m.ID)
				} else {
					content = fmt.Sprintf("%s\n\n[Содержимое документа %s — используй его как источник ответа]\n%s", content, label, selectDocumentContext(m.Content, *m.ExtractedText))
				}
			} else if isVisionMsg {
				if s.userData != nil {
					label := "вложение"
					if m.AttachmentName != nil {
						label = *m.AttachmentName
					}
					content = fmt.Sprintf("%s\n\n[Вложение: %s, message_id=%s. Открытый текст отсутствует; не описывай содержимое без поддерживаемого источника.]", content, label, m.ID)
				} else {
					content = fmt.Sprintf("%s\n\n[Previous attachment: %s]", content, *m.FileURL)
				}
			}

			alemMessages = append(alemMessages, alem.Message{
				Role:    m.Role,
				Content: content,
			})
		}
	}

	if s.userData != nil {
		alemMessages = limitAIConversationHistory(alemMessages)
	}

	// Log final message sequence for debugging
	s.logger.Info("Final message sequence",
		zap.String("chat_id", chatID),
		zap.Int("count", len(alemMessages)),
		zap.String("roles", func() string {
			roles := make([]string, len(alemMessages))
			for i, msg := range alemMessages {
				roles[i] = msg.Role
			}
			return fmt.Sprintf("[%s]", strings.Join(roles, ", "))
		}()),
	)

	// Ensure we have messages and the last one is from user
	if len(alemMessages) == 0 {
		return nil, fmt.Errorf("no valid messages to send to AI")
	}

	// The last message MUST be from user (as we just saved it)
	// If not, something is wrong with the message history
	if alemMessages[len(alemMessages)-1].Role != "user" {
		s.logger.Error("Last message is not from user, fixing sequence",
			zap.String("chat_id", chatID),
			zap.String("last_role", alemMessages[len(alemMessages)-1].Role),
		)
		// This shouldn't happen, but as a safeguard, we'll return an error
		return nil, fmt.Errorf("invalid message sequence: last message must be from user")
	}

	// 5. Call Alem
	req := alem.ChatCompletionRequest{
		Model:       targetModel,
		Messages:    alemMessages,
		MaxTokens:   1000, // Reasonable default
		Temperature: 0.7,
	}

	// DEBUG: Log what we're sending to Alem API
	s.logger.Info("Sending request to Alem API",
		zap.String("model", targetModel),
		zap.Int("message_count", len(alemMessages)),
	)
	for i, msg := range alemMessages {
		if parts, ok := msg.Content.([]alem.ContentPart); ok {
			s.logger.Info("Message with vision content",
				zap.Int("index", i),
				zap.String("role", msg.Role),
				zap.Int("parts", len(parts)),
			)
			for j, part := range parts {
				if part.Type == "image_url" && part.ImageURL != nil {
					s.logger.Info("Image URL in message",
						zap.Int("msg_index", i),
						zap.Int("part_index", j),
						zap.String("url", part.ImageURL.URL),
					)
				}
			}
		}
	}

	complete := func() (*alem.ChatCompletionResponse, error) {
		if s.userData != nil {
			identity, ok := ctx.Value(aiIdentityKey{}).(AIDataIdentity)
			if !ok || identity.UserID != userID {
				return nil, fmt.Errorf("authenticated AI data identity is required")
			}
			// The selected model uses native tools or the strict JSON read-plan
			// protocol. Every protocol/model fallback gets its own reservation.
			req.Model = targetModel
			if decision, ok := ctx.Value(aiActionDecisionKey{}).(aiActionDecision); ok && s.chatAgent != nil {
				return s.chatAgent.resolveChatAction(ctx, identity, decision), nil
			}
			return completeWithUserData(ctx, paidClient, s.userData, identity, req, s.chatAgent)
		}
		return paidClient.CreateChatCompletion(ctx, req)
	}
	resp, err := complete()
	if err != nil && targetModel != s.defaultModel && isModelAccessDenied(err) {
		// Provider access can change independently from the application. Recover
		// old chats created with a retired/restricted model and persist the
		// working model so subsequent requests do not repeat the failure.
		s.logger.Warn("AI model is not available for the provider team; falling back",
			zap.String("chat_id", chatID),
			zap.String("from_model", targetModel),
			zap.String("to_model", s.defaultModel),
		)
		targetModel = s.defaultModel
		req.Model = targetModel
		if updateErr := s.repo.UpdateConversationModel(ctx, chatID, targetModel); updateErr != nil {
			s.logger.Warn("failed to persist fallback AI model", zap.Error(updateErr), zap.String("chat_id", chatID))
		}
		resp, err = complete()
	}
	if paidClient.quotaCutoff != nil {
		final, cutoffErr := s.completeFinalQuotaTurn(ctx, paidClient, resp)
		if cutoffErr != nil {
			return nil, errors.Join(err, cutoffErr)
		}
		if final != nil {
			return final, nil
		}
	}
	if err != nil {
		return nil, err
	}

	// 6. Save Assistant Response
	if len(resp.Choices) > 0 {
		resp.DocumentSources = make([]alem.DocumentSource, 0, len(documentChunks))
		for rank, chunk := range documentChunks {
			resp.DocumentSources = append(resp.DocumentSources, alem.DocumentSource{
				DocumentID: chunk.DocumentID, Name: chunk.Name, ChunkID: chunk.ID, Locator: chunk.Locator, Rank: rank + 1,
			})
		}
		assistantMsg := repository.AIMessage{
			ConversationID: chatID,
			Role:           "assistant",
			Content:        fmt.Sprintf("%v", resp.Choices[0].Message.Content), // Content is interface{}, usually string
			TokensUsage:    resp.Usage.TotalTokens,
			DataCards:      resp.DataCards,
			DataSources:    resp.DataSources,
		}
		// Handle if content is complex
		if strContent, ok := resp.Choices[0].Message.Content.(string); ok {
			assistantMsg.Content = strContent
		} else {
			assistantMsg.Content = "Received complex content from AI"
		}

		assistantMessageID, err := s.repo.CreateMessageWithID(ctx, assistantMsg)
		if err != nil {
			s.logger.Error("Failed to save AI response", zap.Error(err))
		} else if s.documentRepo != nil && len(documentChunks) > 0 {
			if err := s.documentRepo.SaveAnswerSources(ctx, assistantMessageID, documentChunks); err != nil {
				s.logger.Error("Failed to save AI answer sources", zap.Error(err))
			}
		}

		// IMPROVEMENT: Auto-Title generation in background if this is the start of convo
		if len(history) <= 2 && resp.Choices[0].FinishReason != "length" {
			go s.GenerateTitle(context.WithoutCancel(ctx), chatID, turnID, content)
		}
	}

	return resp, nil
}

func aiTurnRejectedBeforeDispatch(err error) bool {
	if err == nil {
		return false
	}
	var denied *domain.AIReservationDenied
	return errors.As(err, &denied) ||
		errors.Is(err, domain.ErrAIConcurrencyLimitExceeded) ||
		errors.Is(err, domain.ErrAIRateLimitExceeded) ||
		errors.Is(err, domain.ErrAILimiterUnavailable) ||
		errors.Is(err, domain.ErrAIAccountingDisabled) ||
		errors.Is(err, domain.ErrAIReservationConflict)
}

func buildDocumentPrompt(chunks []repository.AIDocumentChunk) string {
	const maxChars = 16000
	var builder strings.Builder
	builder.WriteString("\n\n[Проверяемые источники из готовых документов. Используй только эти данные; если ответа нет, скажи об этом прямо.]\n")
	for index, chunk := range chunks {
		if builder.Len()+len(chunk.Text) > maxChars {
			break
		}
		builder.WriteString(fmt.Sprintf("\nИсточник %d — %s, фрагмент %d:\n%s\n", index+1, chunk.Name, chunk.Ordinal+1, chunk.Text))
	}
	return builder.String()
}

func (s *AlemService) ensureConversationOwner(ctx context.Context, chatID string, userID int64) error {
	chat, err := s.repo.GetConversation(ctx, chatID)
	if err != nil {
		return fmt.Errorf("chat not found: %w", err)
	}
	if chat == nil {
		return fmt.Errorf("chat %s does not exist", chatID)
	}
	if chat.UserID != userID {
		return fmt.Errorf("unauthorized: conversation does not belong to user")
	}
	return nil
}

func aiOptionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// selectDocumentContext keeps prompts bounded while preserving the parts of a
// document that are most relevant to the current question. It is intentionally
// deterministic: the document is never silently replaced by a model guess.
func selectDocumentContext(question, text string) string {
	const maxDocumentChars = 32000
	const chunkChars = 2400

	text = strings.TrimSpace(text)
	if len([]rune(text)) <= maxDocumentChars {
		return text
	}

	chunks := splitDocumentChunks(text, chunkChars)
	questionTerms := documentTerms(question)
	type scoredChunk struct {
		index int
		score int
		text  string
	}
	scored := make([]scoredChunk, 0, len(chunks))
	for index, chunk := range chunks {
		score := 0
		chunkTerms := documentTerms(chunk)
		for term := range questionTerms {
			if _, ok := chunkTerms[term]; ok {
				score++
			}
		}
		scored = append(scored, scoredChunk{index: index, score: score, text: chunk})
	}
	// Stable insertion sort avoids changing equal-score document order.
	for i := 1; i < len(scored); i++ {
		item := scored[i]
		j := i - 1
		for j >= 0 && scored[j].score < item.score {
			scored[j+1] = scored[j]
			j--
		}
		scored[j+1] = item
	}

	selected := make([]scoredChunk, 0, 12)
	total := 0
	for _, item := range scored {
		if len(selected) >= 12 || total+len([]rune(item.text)) > maxDocumentChars {
			continue
		}
		selected = append(selected, item)
		total += len([]rune(item.text))
	}
	if len(selected) == 0 {
		return string([]rune(text)[:maxDocumentChars])
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].index < selected[j].index })
	parts := make([]string, 0, len(selected))
	for _, item := range selected {
		parts = append(parts, item.text)
	}
	return strings.Join(parts, "\n\n…\n\n")
}

func splitDocumentChunks(text string, chunkChars int) []string {
	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)+chunkChars-1)/chunkChars)
	for start := 0; start < len(runes); start += chunkChars {
		end := start + chunkChars
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[start:end])))
	}
	return chunks
}

func documentTerms(value string) map[string]struct{} {
	terms := make(map[string]struct{})
	var current []rune
	flush := func() {
		if len(current) >= 3 {
			terms[strings.ToLower(string(current))] = struct{}{}
		}
		current = current[:0]
	}
	for _, r := range []rune(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
		} else {
			flush()
		}
	}
	flush()
	return terms
}

// GenerateTitle asks AI to summarize the conversation into a title
func (s *AlemService) GenerateTitle(parent context.Context, chatID, turnID, firstUserMessage string) {
	// Use a background context because main request context might cancel
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	s.logger.Info("Generating auto-title", zap.String("id", chatID), zap.String("first_msg", firstUserMessage))

	// Use a simpler model for title generation to avoid "thinking" overhead or errors
	titleModel := "google/gemma-3-27b"

	s.logger.Info("Generating auto-title", zap.String("id", chatID), zap.String("first_msg", firstUserMessage))

	req := alem.ChatCompletionRequest{
		Model: titleModel,
		Messages: []alem.Message{
			{Role: "user", Content: fmt.Sprintf("Сгенерируй короткое название (максимум 5 слов) для этого чата на основе сообщения пользователя: \"%s\". Не используй кавычки. Выведи ТОЛЬКО название на русском языке.", firstUserMessage)},
		},
		MaxTokens: 200,
	}

	chatUUID, err := uuid.Parse(chatID)
	if err != nil {
		s.logger.Warn("Invalid title chat identity")
		return
	}
	paidClient := &alemPaidChat{executor: s.paidCalls, client: alemSingleAttempt{s.client}, chatID: chatUUID, turnID: turnID + ":title"}
	resp, err := paidClient.CreateChatCompletion(ctx, req)
	if err != nil {
		s.logger.Warn("Failed to generate auto-title: API error", zap.Error(err))
		return
	}

	if len(resp.Choices) > 0 {
		title, ok := resp.Choices[0].Message.Content.(string)
		if !ok || title == "" {
			s.logger.Warn("Failed to generate auto-title: Empty or invalid content", zap.Any("content", resp.Choices[0].Message.Content))
			return
		}

		err := s.repo.UpdateConversationTitle(ctx, chatID, title)
		if err != nil {
			s.logger.Warn("Failed to save auto-title: DB error", zap.Error(err))
		} else {
			s.logger.Info("Auto-generated title saved", zap.String("id", chatID), zap.String("title", title))
		}
	} else {
		s.logger.Warn("Failed to generate auto-title: No choices returned")
	}
}

// ListModels proxy with custom filtering and titles
func (s *AlemService) ListModels(ctx context.Context) (*alem.ModelListResponse, error) {
	models := []alem.Model{
		{ID: "moonshotai/Kimi-K2-Thinking", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai", Title: "Kimi K2 Thinking (чат)"},
		{ID: "openai/gpt-oss-120b", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai", Title: "GPT-OSS 120B (чат)"},
		{ID: "google/gemma-3-27b", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai", Title: "Gemma 3 27B (чат/картинки)"},
		{ID: "meta-llama/Llama-4-Maverick-17B-128E-Instruct", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai", Title: "Llama 4 Maverick (чат)"},
		{ID: "Qwen/Qwen3-8B", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai", Title: "Qwen3 8B (чат)"},
		{ID: "Qwen/Qwen3-VL-235B-A22B-Instruct", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai", Title: "Qwen3 VL 235B (картинки)"},
	}

	return &alem.ModelListResponse{Object: "list", Data: models}, nil
}

// Embeddings Proxy
func (s *AlemService) CreateEmbeddings(ctx context.Context, req alem.EmbeddingRequest) (*alem.EmbeddingResponse, error) {
	return s.client.CreateEmbeddings(ctx, req)
}

// Rerank Proxy
func (s *AlemService) Rerank(ctx context.Context, req alem.RerankRequest) (*alem.RerankResponse, error) {
	return s.client.Rerank(ctx, req)
}
