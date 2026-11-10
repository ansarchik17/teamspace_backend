package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type unifiedAIRepoStub struct {
	chat               *repository.UnifiedAIChat
	chatErr            error
	lastMessages       []repository.UnifiedAIMessage
	createMessageErr   error
	createAssistantErr error
	createdMessages    []repository.UnifiedAIMessage
	updatedModels      []string
	messageIDCounter   int
}

func (r *unifiedAIRepoStub) GetOrCreateChat(context.Context, int64, string) (*repository.UnifiedAIChat, error) {
	return r.chat, r.chatErr
}

func (r *unifiedAIRepoStub) GetChat(context.Context, string, int64) (*repository.UnifiedAIChat, error) {
	return r.chat, r.chatErr
}

func (r *unifiedAIRepoStub) UpdateModel(_ context.Context, _ string, _ int64, newModel string) error {
	r.updatedModels = append(r.updatedModels, newModel)
	if r.chat != nil {
		r.chat.ModelName = newModel
	}
	return nil
}

func (r *unifiedAIRepoStub) UpdateTitle(context.Context, string, int64, string) error {
	return nil
}

func (r *unifiedAIRepoStub) GetMessages(context.Context, string, int64) ([]repository.UnifiedAIMessage, error) {
	return r.createdMessages, nil
}

func (r *unifiedAIRepoStub) GetLastMessages(context.Context, string, int) ([]repository.UnifiedAIMessage, error) {
	if r.lastMessages != nil {
		return r.lastMessages, nil
	}
	return r.createdMessages, nil
}

func (r *unifiedAIRepoStub) DeleteAllMessages(context.Context, string, int64) error {
	r.createdMessages = nil
	return nil
}

func (r *unifiedAIRepoStub) DeleteChat(context.Context, string, int64) error {
	return nil
}

func (r *unifiedAIRepoStub) DeleteMessage(context.Context, string, string, int64) error {
	return nil
}

func (r *unifiedAIRepoStub) CreateMessage(ctx context.Context, msg repository.UnifiedAIMessage) error {
	_, err := r.CreateMessageWithID(ctx, msg)
	return err
}

func (r *unifiedAIRepoStub) CreateMessageWithID(_ context.Context, msg repository.UnifiedAIMessage) (string, error) {
	if msg.Role == "user" && r.createMessageErr != nil {
		return "", r.createMessageErr
	}
	if msg.Role == "assistant" && r.createAssistantErr != nil {
		return "", r.createAssistantErr
	}
	r.messageIDCounter++
	id := uuid.NewString()
	msg.ID = id
	msg.CreatedAt = time.Now()
	r.createdMessages = append(r.createdMessages, msg)
	return id, nil
}

func newTestUnifiedAIService(repo unifiedAIRepo, store AIAccountingStore, payerUsers aiPayerUsers, client aiCompletionClient) *UnifiedAIService {
	svc := &UnifiedAIService{
		repo:             repo,
		logger:           zap.NewNop(),
		defaultModel:     DefaultAlemModel,
		completionClient: client,
	}
	if store != nil && payerUsers != nil {
		svc.SetPaidAccounting(NewAIPaidCallerService(store), payerUsers)
	}
	return svc
}

// 1. Тест: отсутствие проверенного плательщика
func TestUnifiedAIChatMissingPayer(t *testing.T) {
	org := uuid.New()
	chatID := uuid.NewString()

	baseRepo := func() *unifiedAIRepoStub {
		return &unifiedAIRepoStub{
			chat: &repository.UnifiedAIChat{ID: chatID, UserID: 42, ModelName: DefaultAlemModel},
		}
	}

	// Case 1: SetPaidAccounting вообще не вызывался (paidCalls == nil)
	svc := newTestUnifiedAIService(baseRepo(), nil, nil, nil)
	_, err := svc.SendMessage(context.Background(), chatID, 42, "Привет", nil)
	require.ErrorIs(t, err, domain.ErrAIPaidCallMissingContext)

	// Case 2: Неактивный пользователь (IsActive = false)
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	svc = newTestUnifiedAIService(baseRepo(), store, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: false}}, nil)
	_, err = svc.SendMessage(context.Background(), chatID, 42, "Привет", nil)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)

	// Case 3: Несовпадение подтвержденной личности пользователя
	svc = newTestUnifiedAIService(baseRepo(), store, alemPayerStub{user: &domain.User{ID: 43, OrganizationID: nil, IsActive: true}}, nil)
	_, err = svc.SendMessage(context.Background(), chatID, 42, "Привет", nil)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)

	// Case 4: Конфликт контекста плательщика (в context передан чужой caller)
	otherOrg := uuid.New()
	foreignCaller, _ := domain.NewAIPaidUserCaller(otherOrg, 42)
	ctxWithForeignCaller := domain.WithAIPaidCaller(context.Background(), foreignCaller)

	svc = newTestUnifiedAIService(baseRepo(), store, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}, nil)
	_, err = svc.SendMessage(ctxWithForeignCaller, chatID, 42, "Привет", nil)
	require.ErrorIs(t, err, domain.ErrAIPaidCallContextMismatch)
}

// 2. Тест: отказ по квоте до обращения к провайдеру
func TestUnifiedAIChatQuotaDenied(t *testing.T) {
	org := uuid.New()
	chatID := uuid.NewString()
	deniedErr := &domain.AIReservationDenied{Reason: "WEEKLY_EXHAUSTED", Scope: "weekly"}

	store := &aiAccountingStub{
		reserveErr: deniedErr,
	}
	repo := &unifiedAIRepoStub{
		chat: &repository.UnifiedAIChat{ID: chatID, UserID: 42, ModelName: DefaultAlemModel},
	}
	calls := 0
	client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		return nil, nil
	})

	svc := newTestUnifiedAIService(repo, store, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}, client)
	_, err := svc.SendMessage(context.Background(), chatID, 42, "Привет", nil)

	require.ErrorIs(t, err, deniedErr)
	require.Zero(t, calls, "провайдер не должен вызываться при отказе по квоте")
	require.Zero(t, store.markCalls)
	require.Empty(t, store.completed)
}

// 3. Тест: ошибка провайдера с неизвестным расходом
func TestUnifiedAIChatProviderErrorUnknown(t *testing.T) {
	org := uuid.New()
	chatID := uuid.NewString()
	attemptID := uuid.New()

	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: attemptID, Created: true},
	}
	repo := &unifiedAIRepoStub{
		chat: &repository.UnifiedAIChat{ID: chatID, UserID: 42, ModelName: DefaultAlemModel},
	}

	providerErr := errors.New("upstream provider connection reset")
	calls := 0
	client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		return nil, providerErr
	})

	svc := newTestUnifiedAIService(repo, store, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}, client)
	_, err := svc.SendMessage(context.Background(), chatID, 42, "Вопрос к ИИ", nil)

	require.ErrorIs(t, err, providerErr)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, store.markCalls)
	require.Empty(t, store.completed)
	require.Len(t, store.failures, 1)
	require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)

	// Ответ ассистента НЕ должен быть сохранен
	for _, m := range repo.createdMessages {
		require.NotEqual(t, "assistant", m.Role)
	}
}

// 4. Тест: успешная генерация и запись
func TestUnifiedAIChatSuccessfulGenerationAndSave(t *testing.T) {
	org := uuid.New()
	chatID := uuid.NewString()
	attemptID := uuid.New()

	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: attemptID, Created: true},
	}
	repo := &unifiedAIRepoStub{
		chat: &repository.UnifiedAIChat{ID: chatID, UserID: 42, ModelName: DefaultAlemModel},
	}

	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		return accountedAlemResponse(t, req.Model, `{"prompt_tokens":12,"completion_tokens":6,"total_tokens":18}`), nil
	})

	svc := newTestUnifiedAIService(repo, store, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}, client)
	resp, err := svc.SendMessage(context.Background(), chatID, 42, "Расскажи о планах", nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, store.markCalls)
	require.Len(t, store.completed, 1)
	require.Equal(t, org, store.completed[0].OrganizationID)
	require.Equal(t, attemptID, store.completed[0].AttemptID)
	require.Equal(t, DefaultAlemModel, store.completed[0].ActualModel)

	// Проверяем сохранение сообщений в историю
	require.Len(t, repo.createdMessages, 2)
	require.Equal(t, "user", repo.createdMessages[0].Role)
	require.Equal(t, "Расскажи о планах", repo.createdMessages[0].Content)
	require.Equal(t, "assistant", repo.createdMessages[1].Role)
	require.Equal(t, "answer", repo.createdMessages[1].Content)
}

// 5. Тест: успешная генерация, затем ошибка записи ответа
// Платный вызов остаётся оплаченным; повторная генерация НЕ выполняется!
func TestUnifiedAIChatSuccessWithAssistantSaveError(t *testing.T) {
	org := uuid.New()
	chatID := uuid.NewString()
	attemptID := uuid.New()

	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: attemptID, Created: true},
	}
	saveErr := errors.New("database disk full: failed to insert message")
	repo := &unifiedAIRepoStub{
		chat:               &repository.UnifiedAIChat{ID: chatID, UserID: 42, ModelName: DefaultAlemModel},
		createAssistantErr: saveErr,
	}

	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		return accountedAlemResponse(t, req.Model, `{"prompt_tokens":15,"completion_tokens":8,"total_tokens":23}`), nil
	})

	svc := newTestUnifiedAIService(repo, store, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}, client)
	resp, err := svc.SendMessage(context.Background(), chatID, 42, "Сгенерируй отчет", nil)

	// Метод возвращает ошибку сохранения
	require.Error(t, err)
	require.True(t, errors.Is(err, saveErr) || strings.Contains(err.Error(), "failed to save assistant response"))
	require.Nil(t, resp)

	// Платный вызов ровно 1 (никаких повторных генераций!)
	require.Equal(t, 1, calls, "не должно быть повторной генерации ради восстановления записи истории")

	// Расход зафиксирован и оплачен!
	require.Len(t, store.completed, 1)
	require.Equal(t, attemptID, store.completed[0].AttemptID)
	require.Empty(t, store.failures)
}

// 6. Тест: сменяемая модель и fallback с раздельными попытками учёта
func TestUnifiedAIChatModelFallbackSeparateAccountingAttempts(t *testing.T) {
	org := uuid.New()
	chatID := uuid.NewString()
	customModel := "moonshotai/Kimi-K2-Thinking"

	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}
	repo := &unifiedAIRepoStub{
		chat: &repository.UnifiedAIChat{ID: chatID, UserID: 42, ModelName: customModel},
	}

	calls := 0
	client := completionFunc(func(_ context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			require.Equal(t, customModel, req.Model)
			return nil, &alem.APIError{StatusCode: 403, Details: alem.ErrorDetails{Message: "model access denied"}}
		}
		require.Equal(t, DefaultAlemModel, req.Model)
		return accountedAlemResponse(t, req.Model, `{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}`), nil
	})

	svc := newTestUnifiedAIService(repo, store, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}, client)
	resp, err := svc.SendMessage(context.Background(), chatID, 42, "Вопрос для fallback", nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 2, calls)

	// Первая попытка зафиксирована как ошибка (unknown), вторая как успешное списание
	require.Len(t, store.failures, 1)
	require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)
	require.Len(t, store.completed, 1)
	require.Equal(t, DefaultAlemModel, store.completed[0].ActualModel)

	// Модель в репозитории обновлена на fallback
	require.Contains(t, repo.updatedModels, DefaultAlemModel)
}
