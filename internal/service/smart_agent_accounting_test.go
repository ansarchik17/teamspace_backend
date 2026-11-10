package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type smartAgentUserRepoStub struct {
	user *domain.User
	err  error
}

func (r *smartAgentUserRepoStub) SearchVisibleTo(ctx context.Context, requesterID int64, q string, limit, offset int) ([]*domain.User, error) {
	if r.user != nil {
		return []*domain.User{r.user}, nil
	}
	return nil, nil
}

func (r *smartAgentUserRepoStub) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return r.user, r.err
}

func newTestSmartAgentService(userRepo UserRepository, store AIAccountingStore, payerUsers aiPayerUsers, completer aiCompletionClient) *SmartAgentService {
	svc := &SmartAgentService{
		logger:     zap.NewNop(),
		userRepo:   userRepo,
		completer:  completer,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	if store != nil && payerUsers != nil {
		svc.SetPaidAccounting(NewAIPaidCallerService(store), payerUsers)
	}
	return svc
}

func accountedSmartAgentResponse(t *testing.T, model, role, content string, toolCalls []alem.ToolCall, finishReason, usage string) *alem.ChatCompletionResponse {
	t.Helper()
	var choice alem.Choice
	choice.FinishReason = finishReason
	choice.Message = alem.Message{
		Role:      role,
		Content:   content,
		ToolCalls: toolCalls,
	}
	var resp alem.ChatCompletionResponse
	resp.ID = "smart-agent-resp-" + uuid.NewString()
	resp.Model = model
	resp.Choices = []alem.Choice{choice}
	if usage != "" {
		resp.AccountingUsage = json.RawMessage(usage)
		_ = json.Unmarshal(resp.AccountingUsage, &resp.Usage)
	}
	return &resp
}

// 1. Тест: без проверенного плательщика провайдер не вызывается
func TestSmartAgentMissingPayerContext(t *testing.T) {
	org := uuid.New()
	userID := int64(101)

	baseUser := &domain.User{
		ID:             userID,
		FirstName:      "Alikhan",
		LastName:       "Bokeikhan",
		Email:          "alikhan@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	providerCalls := 0
	completer := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		return nil, nil
	})

	req := &SmartAgentChatRequest{
		Message:   "Какое расписание на сегодня?",
		SessionID: uuid.NewString(),
	}

	// Case 1: SetPaidAccounting вообще не вызывался (paidCalls == nil)
	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: baseUser}, nil, nil, completer)
	_, err := svc.ProcessChat(context.Background(), userID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallMissingContext)
	require.Zero(t, providerCalls)

	// Case 2: Неактивный пользователь (IsActive = false)
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	inactiveUser := *baseUser
	inactiveUser.IsActive = false
	svc = newTestSmartAgentService(&smartAgentUserRepoStub{user: &inactiveUser}, store, alemPayerStub{user: &inactiveUser}, completer)
	_, err = svc.ProcessChat(context.Background(), userID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
	require.Zero(t, providerCalls)
	require.Zero(t, store.reserveCalls)

	// Case 3: Несовпадение подтвержденной личности пользователя
	noOrgUser := *baseUser
	noOrgUser.ID = userID + 1
	svc = newTestSmartAgentService(&smartAgentUserRepoStub{user: &noOrgUser}, store, alemPayerStub{user: &noOrgUser}, completer)
	_, err = svc.ProcessChat(context.Background(), userID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
	require.Zero(t, providerCalls)
	require.Zero(t, store.reserveCalls)

	// Case 4: Конфликт контекста плательщика (чужой caller в context)
	otherOrg := uuid.New()
	foreignCaller, _ := domain.NewAIPaidUserCaller(otherOrg, userID)
	ctxWithForeignCaller := domain.WithAIPaidCaller(context.Background(), foreignCaller)

	svc = newTestSmartAgentService(&smartAgentUserRepoStub{user: baseUser}, store, alemPayerStub{user: baseUser}, completer)
	_, err = svc.ProcessChat(ctxWithForeignCaller, userID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallContextMismatch)
	require.Zero(t, providerCalls)
	require.Zero(t, store.reserveCalls)
}

// 2. Тест: отказ по квоте до обращения к провайдеру на первом шаге
func TestSmartAgentQuotaDeniedBlocksFirstStep(t *testing.T) {
	org := uuid.New()
	userID := int64(102)

	user := &domain.User{
		ID:             userID,
		FirstName:      "Dinmukhamed",
		LastName:       "Kunayev",
		Email:          "kunayev@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	deniedErr := &domain.AIReservationDenied{Reason: "DAILY_EXHAUSTED", Scope: "daily"}
	store := &aiAccountingStub{reserveErr: deniedErr}

	providerCalls := 0
	completer := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		return nil, nil
	})

	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel(DefaultAlemModel)

	req := &SmartAgentChatRequest{
		Message:   "Создай задачу в канбане",
		SessionID: uuid.NewString(),
	}

	_, err := svc.ProcessChat(context.Background(), userID, req)
	require.Error(t, err)

	var actualDenied *domain.AIReservationDenied
	require.True(t, errors.As(err, &actualDenied))
	require.Equal(t, "DAILY_EXHAUSTED", actualDenied.Reason)
	require.Zero(t, providerCalls)
	require.Equal(t, 1, store.reserveCalls)
	require.Empty(t, store.completed)
}

// 3. Тест: отказ по квоте блокирует следующий шаг (шаг 1 оплачен, шаг 2 заблокирован)
func TestSmartAgentQuotaDeniedBlocksNextStep(t *testing.T) {
	org := uuid.New()
	userID := int64(103)

	user := &domain.User{
		ID:             userID,
		FirstName:      "Kanysh",
		LastName:       "Satbayev",
		Email:          "satbayev@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	step := 0
	providerCalls := 0
	deniedErr := &domain.AIReservationDenied{Reason: "INSUFFICIENT_CREDITS", Scope: "organization"}

	store := &aiAccountingStub{}
	store.reservation = domain.AIReservation{AttemptID: uuid.New(), Created: true}

	completer := completionFunc(func(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		step++
		if step == 1 {
			// Шаг 1 возвращает tool_call
			return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "", []alem.ToolCall{
				{
					ID:   "call-search-1",
					Type: "function",
					Function: alem.ToolFunction{
						Name:      "search_users",
						Arguments: `{"query":"Алихан"}`,
					},
				},
			}, "tool_calls", `{"prompt_tokens":120,"completion_tokens":25,"total_tokens":145}`), nil
		}
		// На шаге 2 провайдер не должен быть вызван, т.к. резерв отклонен
		return nil, errors.New("provider should not be called on step 2")
	})

	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel(DefaultAlemModel)

	// На шаге 1 резерв проходит, а перед шагом 2 активируем отказ по квоте
	origReserve := store.reservation
	_ = origReserve

	// Используем кастомный store для перехвата шага 2
	stepCounter := 0
	customStore := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}
	customStoreHook := func() error {
		stepCounter++
		if stepCounter >= 2 {
			return deniedErr
		}
		return nil
	}

	// Оборачиваем вызовы Reserve
	conditionalStore := &conditionalAccountingStub{
		aiAccountingStub: customStore,
		onReserve:        customStoreHook,
	}

	svc = newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, conditionalStore, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel(DefaultAlemModel)

	req := &SmartAgentChatRequest{
		Message:   "Найди Алихана",
		SessionID: uuid.NewString(),
	}

	_, err := svc.ProcessChat(context.Background(), userID, req)
	require.Error(t, err)

	var actualDenied *domain.AIReservationDenied
	require.True(t, errors.As(err, &actualDenied))
	require.Equal(t, "INSUFFICIENT_CREDITS", actualDenied.Reason)

	// Провайдер вызван ровно 1 раз (для шага 1)
	require.Equal(t, 1, providerCalls)
	// Шаг 1 остался зафиксированным (settled)
	require.Len(t, conditionalStore.completed, 1)
	require.Equal(t, DefaultAlemModel, conditionalStore.completed[0].ActualModel)
	require.EqualValues(t, 120, *conditionalStore.completed[0].ReportedInputTokens)
	require.EqualValues(t, 25, *conditionalStore.completed[0].ReportedOutputTokens)
}

type conditionalAccountingStub struct {
	*aiAccountingStub
	onReserve func() error
}

func (s *conditionalAccountingStub) Reserve(ctx context.Context, req domain.AIReserveRequest) (domain.AIReservation, error) {
	if s.onReserve != nil {
		if err := s.onReserve(); err != nil {
			return domain.AIReservation{}, err
		}
	}
	return s.aiAccountingStub.Reserve(ctx, req)
}

// 4. Тест: цикл инструментов (2 генерации + 1 инструмент). Инструмент не тратит токены.
func TestSmartAgentToolLoopMultipleGenerations(t *testing.T) {
	org := uuid.New()
	userID := int64(104)

	user := &domain.User{
		ID:             userID,
		FirstName:      "Akhmet",
		LastName:       "Baitursynov",
		Email:          "akhmet@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	step := 0
	providerCalls := 0

	completer := completionFunc(func(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		step++
		if step == 1 {
			// Шаг 1: вызов инструмента
			return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "", []alem.ToolCall{
				{
					ID:   "call-search-user",
					Type: "function",
					Function: alem.ToolFunction{
						Name:      "search_users",
						Arguments: `{"query":"Ахмет"}`,
					},
				},
			}, "tool_calls", `{"prompt_tokens":200,"completion_tokens":30,"total_tokens":230}`), nil
		}
		// Шаг 2: финальный ответ
		return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "Пользователь Ахмет найден.", nil, "stop", `{"prompt_tokens":350,"completion_tokens":40,"total_tokens":390}`), nil
	})

	sessionID := uuid.NewString()
	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel(DefaultAlemModel)

	req := &SmartAgentChatRequest{
		Message:   "Найди Ахмета",
		SessionID: sessionID,
	}

	resp, err := svc.ProcessChat(context.Background(), userID, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "Пользователь Ахмет найден.", resp.Reply)

	// Ровно 2 вызова провайдера
	require.Equal(t, 2, providerCalls)
	// Ровно 2 списания в журнале
	require.Len(t, store.completed, 2)
	require.Equal(t, 2, store.reserveCalls)

	// Проверяем первый шаг
	require.EqualValues(t, 200, *store.completed[0].ReportedInputTokens)
	require.EqualValues(t, 30, *store.completed[0].ReportedOutputTokens)

	// Проверяем второй шаг
	require.EqualValues(t, 350, *store.completed[1].ReportedInputTokens)
	require.EqualValues(t, 40, *store.completed[1].ReportedOutputTokens)
}

// 5. Тест: ошибка инструмента после оплаченного шага сохраняет выполненный расход
func TestSmartAgentToolErrorPreservesPreviousExpense(t *testing.T) {
	org := uuid.New()
	userID := int64(105)

	user := &domain.User{
		ID:             userID,
		FirstName:      "Mukhtar",
		LastName:       "Auezov",
		Email:          "auezov@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	step := 0
	providerCalls := 0

	completer := completionFunc(func(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		step++
		if step == 1 {
			// Шаг 1: вызов неизвестного инструмента
			return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "", []alem.ToolCall{
				{
					ID:   "call-nonexistent",
					Type: "function",
					Function: alem.ToolFunction{
						Name:      "nonexistent_tool",
						Arguments: `{}`,
					},
				},
			}, "tool_calls", `{"prompt_tokens":150,"completion_tokens":20,"total_tokens":170}`), nil
		}
		// Шаг 2: ответ с пояснением
		return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "Такой инструмент не поддерживается.", nil, "stop", `{"prompt_tokens":220,"completion_tokens":25,"total_tokens":245}`), nil
	})

	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel(DefaultAlemModel)

	req := &SmartAgentChatRequest{
		Message:   "Запусти неизвестное действие",
		SessionID: uuid.NewString(),
	}

	resp, err := svc.ProcessChat(context.Background(), userID, req)
	require.NoError(t, err)
	require.Equal(t, "Такой инструмент не поддерживается.", resp.Reply)

	// Шаг 1 и шаг 2 зафиксированы
	require.Equal(t, 2, providerCalls)
	require.Len(t, store.completed, 2)
	require.EqualValues(t, 150, *store.completed[0].ReportedInputTokens)
}

// 6. Тест: отмена контекста после шага 1 сохраняет проведённый расход
func TestSmartAgentContextCancelledAfterStep1(t *testing.T) {
	org := uuid.New()
	userID := int64(106)

	user := &domain.User{
		ID:             userID,
		FirstName:      "Shokan",
		LastName:       "Ualikhanov",
		Email:          "ualikhanov@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	ctx, cancel := context.WithCancel(context.Background())
	step := 0

	completer := completionFunc(func(c context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		step++
		if step == 1 {
			// Отменяем контекст сразу после первого вызова
			cancel()
			return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "", []alem.ToolCall{
				{
					ID:   "call-cancel-test",
					Type: "function",
					Function: alem.ToolFunction{
						Name:      "search_users",
						Arguments: `{"query":"Шокан"}`,
					},
				},
			}, "tool_calls", `{"prompt_tokens":180,"completion_tokens":30,"total_tokens":210}`), nil
		}
		return nil, errors.New("should not reach step 2")
	})

	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel(DefaultAlemModel)

	req := &SmartAgentChatRequest{
		Message:   "Найди Шокана",
		SessionID: uuid.NewString(),
	}

	_, err := svc.ProcessChat(ctx, userID, req)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)

	// Шаг 1 остался зафиксированным
	require.Len(t, store.completed, 1)
	require.EqualValues(t, 180, *store.completed[0].ReportedInputTokens)
}

// 7. Тест: fallback модели (первая модель падает -> unknown, fallback на DefaultAlemModel -> settled)
func TestSmartAgentModelFallback(t *testing.T) {
	org := uuid.New()
	userID := int64(107)

	user := &domain.User{
		ID:             userID,
		FirstName:      "Abai",
		LastName:       "Kunanbayev",
		Email:          "abai@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	step := 0
	providerCalls := 0

	completer := completionFunc(func(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		step++
		if step == 1 {
			// Первая модель возвращает 403 model access denied
			return nil, &alem.APIError{
				StatusCode: http.StatusForbidden,
				Details: alem.ErrorDetails{
					Code:    "model_access_denied",
					Message: "access to deepseek model is denied for this account",
				},
			}
		}
		// Вторая модель (fallback) успешна
		return accountedSmartAgentResponse(t, DefaultAlemModel, "assistant", "Ответ от запасной модели.", nil, "stop", `{"prompt_tokens":210,"completion_tokens":35,"total_tokens":245}`), nil
	})

	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel("deepseek-ai/DeepSeek-V3.2")

	req := &SmartAgentChatRequest{
		Message:   "Стихотворение Абая",
		SessionID: uuid.NewString(),
	}

	resp, err := svc.ProcessChat(context.Background(), userID, req)
	require.NoError(t, err)
	require.Equal(t, "Ответ от запасной модели.", resp.Reply)

	// Ровно 2 вызова провайдера: 1 неудачный целевой, 1 успешный fallback
	require.Equal(t, 2, providerCalls)
	require.Equal(t, 2, store.reserveCalls)

	// В журнале: первая попытка зафиксирована как failure (unknown), вторая как settled
	require.Len(t, store.failures, 1)
	require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)

	require.Len(t, store.completed, 1)
	require.Equal(t, DefaultAlemModel, store.completed[0].ActualModel)
}

// 8. Тест: защита от скрытых повторов при повторной доставке операции (replay)
func TestSmartAgentReplayProtection(t *testing.T) {
	org := uuid.New()
	userID := int64(108)

	user := &domain.User{
		ID:             userID,
		FirstName:      "Bauyrzhan",
		LastName:       "Momyshuly",
		Email:          "bauyrzhan@gov.kz",
		IsActive:       true,
		OrganizationID: &org,
	}

	// Симулируем уже существующую операцию (Created = false)
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: false}}
	providerCalls := 0

	completer := completionFunc(func(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		providerCalls++
		return nil, nil
	})

	svc := newTestSmartAgentService(&smartAgentUserRepoStub{user: user}, store, alemPayerStub{user: user}, completer)
	svc.SetDefaultModel(DefaultAlemModel)

	req := &SmartAgentChatRequest{
		Message:     "Команда",
		SessionID:   uuid.NewString(),
		OperationID: "op-fixed-replay-108",
	}

	_, err := svc.ProcessChat(context.Background(), userID, req)
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrAIReservationConflict)

	// Провайдер не вызывался ни разу!
	require.Zero(t, providerCalls)
	require.Empty(t, store.completed)
}
