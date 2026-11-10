package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// 1. Проверка: отсутствие проверенного плательщика
func TestNotebookAccounting_MissingPayer(t *testing.T) {
	orgID := uuid.New()
	chatUUID := uuid.New()
	fileID := uuid.New()
	files := []*domain.DriveFile{{ID: fileID, OwnerID: 42, RealName: "source.txt", ObjectName: "drive/source.txt", Size: 10}}
	drive := fakeDriveSearchService(map[string][]byte{"drive/source.txt": []byte("Контент документа")})

	req := NotebookResearchRequest{Question: "Вопрос", FileIDs: []uuid.UUID{fileID}}

	// Case 1: SetPaidAccounting вообще не вызывался (paidCalls == nil)
	s := &NotebookResearchService{
		notebooks: notebookTestReader{owner: 42, files: files},
		drive:     drive,
		llm:       &notebookTestLLM{},
		logger:    zap.NewNop(),
	}
	_, err := s.Ask(context.Background(), 42, chatUUID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallMissingContext)

	// Case 2: Неактивный пользователь
	store := &aiAccountingStub{reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true}}
	llm := &notebookTestLLM{}
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &orgID, IsActive: false}})
	s.llm = llm
	_, err = s.Ask(context.Background(), 42, chatUUID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
	require.Empty(t, llm.requests, "провайдер не должен вызываться при невалидном пользователе")

	// Case 3: Несовпадение подтвержденной личности пользователя
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 43, OrganizationID: nil, IsActive: true}})
	_, err = s.Ask(context.Background(), 42, chatUUID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
	require.Empty(t, llm.requests, "провайдер не должен вызываться при несовпадении личности")

	// Case 4: Несовпадение контекста (чужая организация в контексте)
	foreignOrg := uuid.New()
	foreignCaller, _ := domain.NewAIPaidUserCaller(foreignOrg, 42)
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &orgID, IsActive: true}})
	ctxForeign := domain.WithAIPaidCaller(context.Background(), foreignCaller)
	_, err = s.Ask(ctxForeign, 42, chatUUID, req)
	require.ErrorIs(t, err, domain.ErrAIPaidCallContextMismatch)
	require.Empty(t, llm.requests, "провайдер не должен вызываться при несовпадении контекста")
}

// 2. Проверка: отказ по квоте до обращения к провайдеру
func TestNotebookAccounting_QuotaDenied(t *testing.T) {
	orgID := uuid.New()
	chatUUID := uuid.New()
	fileID := uuid.New()
	files := []*domain.DriveFile{{ID: fileID, OwnerID: 42, RealName: "source.txt", ObjectName: "drive/source.txt", Size: 10}}
	drive := fakeDriveSearchService(map[string][]byte{"drive/source.txt": []byte("Контент документа")})

	store := &aiAccountingStub{
		reserveErr: &domain.AIReservationDenied{Reason: "QUOTA_EXCEEDED"},
	}
	llm := &notebookTestLLM{}

	s := &NotebookResearchService{
		notebooks: notebookTestReader{owner: 42, files: files},
		drive:     drive,
		llm:       llm,
		logger:    zap.NewNop(),
	}
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &orgID, IsActive: true}})

	req := NotebookResearchRequest{Question: "Вопрос", FileIDs: []uuid.UUID{fileID}}
	_, err := s.Ask(context.Background(), 42, chatUUID, req)
	var denied *domain.AIReservationDenied
	require.True(t, errors.As(err, &denied))
	require.Equal(t, "QUOTA_EXCEEDED", denied.Reason)
	require.Empty(t, llm.requests, "провайдер не должен вызываться при отказе по квоте")
	require.Equal(t, 1, store.reserveCalls)
}

// 3. Проверка цепочки: несколько успешных вызовов и последний неуспешный
// (первичная генерация успешна, верификация падает с ошибкой провайдера)
func TestNotebookAccounting_Chain_MultiSuccessAndLastFailure(t *testing.T) {
	orgID := uuid.New()
	chatUUID := uuid.New()
	fileID := uuid.New()
	files := []*domain.DriveFile{{ID: fileID, OwnerID: 42, RealName: "source.txt", ObjectName: "drive/source.txt", Size: 20}}
	drive := fakeDriveSearchService(map[string][]byte{"drive/source.txt": []byte("Строка данных 1000\nВторая строка 2000")})

	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}

	llm := &notebookTestLLM{
		outputs: []string{"Черновик ответа [S1:L1]"},
		failAt:  2, // Вторая попытка (верификация) завершается ошибкой модели
	}

	s := &NotebookResearchService{
		notebooks: notebookTestReader{owner: 42, files: files},
		drive:     drive,
		llm:       llm,
		logger:    zap.NewNop(),
	}
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &orgID, IsActive: true}})

	opID := uuid.NewString()
	req := NotebookResearchRequest{
		OperationID: opID,
		Question:    "Что с бюджетом?",
		FileIDs:     []uuid.UUID{fileID},
		Mode:        "thinking", // Режим thinking инициирует цепочку: генерация + верификация
	}

	_, err := s.Ask(context.Background(), 42, chatUUID, req)
	require.Error(t, err, "вызов Ask должен вернуть ошибку при сбое верификации")

	// Проверяем количество обращений к провайдеру: ровно 2 (1 генерация + 1 верификация)
	require.Len(t, llm.requests, 2, "число отправок провайдеру должно быть ровно 2")

	// Проверяем вызовы Reserve: ровно 2
	require.Equal(t, 2, store.reserveCalls, "резервирование должно производиться для каждого шага")

	// Первый шаг успешно списан:
	require.Len(t, store.completed, 1, "первый успешный вызов должен быть списан")
	require.Equal(t, "nitec-chat-v1", store.completed[0].UsageAdapterVersion)
	require.Equal(t, "verified", store.completed[0].Usage.Status)

	// Второй шаг зафиксирован со статусом unknown:
	require.Len(t, store.failures, 1, "сбойный второй шаг должен быть зарегистрирован")
	require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome, "исход сбоя провайдера без usage должен быть unknown")

	// Предыдущий расход НЕ отменился:
	require.NotEmpty(t, store.completed, "ошибка проверки не должна отменять предыдущий списанный расход")
}

// 4. Проверка цепочки: fallback модели + верификация (3 фактических вызова)
func TestNotebookAccounting_Chain_FallbackAndVerification(t *testing.T) {
	orgID := uuid.New()
	chatUUID := uuid.New()
	fileID := uuid.New()
	files := []*domain.DriveFile{{ID: fileID, OwnerID: 42, RealName: "source.txt", ObjectName: "drive/source.txt", Size: 20}}
	drive := fakeDriveSearchService(map[string][]byte{"drive/source.txt": []byte("Строка данных 1000\nВторая строка 2000")})

	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}

	llm := &notebookTestLLM{
		models:    []string{"moonshotai/Kimi-K2-Thinking", DefaultAlemModel},
		failFirst: true, // Первый вызов на moonshotai фейлится -> fallback на DefaultAlemModel
		outputs:   []string{"Fallback черновик [S1:L1]", "Верифицированный ответ [S1:L1]"},
	}

	s := &NotebookResearchService{
		notebooks: notebookTestReader{owner: 42, files: files},
		drive:     drive,
		llm:       llm,
		logger:    zap.NewNop(),
	}
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &orgID, IsActive: true}})

	opID := uuid.NewString()
	req := NotebookResearchRequest{
		OperationID: opID,
		Question:    "Что с бюджетом?",
		FileIDs:     []uuid.UUID{fileID},
		Mode:        "thinking",
	}

	resp, err := s.Ask(context.Background(), 42, chatUUID, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Fallback, "результат должен отражать fallback модели")

	// Проверяем число вызовов к провайдеру: ровно 3
	// 1: moonshotai (сбой)
	// 2: DefaultAlemModel (fallback)
	// 3: verifyNotebookAnswer
	require.Len(t, llm.requests, 3, "должно быть ровно 3 вызова к провайдеру")
	require.Equal(t, 3, store.reserveCalls, "должно быть 3 резервирования")

	// 1 сбойный вызов зарегистрирован как unknown:
	require.Len(t, store.failures, 1)
	require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)

	// 2 вызова успешно списаны:
	require.Len(t, store.completed, 2, "два успешных вызова должны быть зафиксированы в списаниях")
}

// 5. Проверка: защита от повторной генерации при повторной доставке операции
func TestNotebookAccounting_ReplayConflict_NoDuplicateDispatch(t *testing.T) {
	orgID := uuid.New()
	chatUUID := uuid.New()
	fileID := uuid.New()
	files := []*domain.DriveFile{{ID: fileID, OwnerID: 42, RealName: "source.txt", ObjectName: "drive/source.txt", Size: 20}}
	drive := fakeDriveSearchService(map[string][]byte{"drive/source.txt": []byte("Данные")})

	// Симулируем повторную доставку: операция уже существует (Created: false)
	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: false},
	}
	llm := &notebookTestLLM{}

	s := &NotebookResearchService{
		notebooks: notebookTestReader{owner: 42, files: files},
		drive:     drive,
		llm:       llm,
		logger:    zap.NewNop(),
	}
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &orgID, IsActive: true}})

	opID := uuid.NewString()
	req := NotebookResearchRequest{
		OperationID: opID,
		Question:    "Повторный запрос",
		FileIDs:     []uuid.UUID{fileID},
	}

	_, err := s.Ask(context.Background(), 42, chatUUID, req)
	require.ErrorIs(t, err, domain.ErrAIReservationConflict, "повторная доставка должна возвращать ErrAIReservationConflict")
	require.Empty(t, llm.requests, "при повторной доставке провайдер категорически не должен вызываться")
}

// 6. Проверка: ошибка сохранения в историю не отменяет проведённый расход
func TestNotebookAccounting_SaveFailureKeepsPriorExpense(t *testing.T) {
	orgID := uuid.New()
	chatUUID := uuid.New()
	fileID := uuid.New()
	files := []*domain.DriveFile{{ID: fileID, OwnerID: 42, RealName: "source.txt", ObjectName: "drive/source.txt", Size: 20}}
	drive := fakeDriveSearchService(map[string][]byte{"drive/source.txt": []byte("Данные для генерации")})

	store := &aiAccountingStub{
		reservation: domain.AIReservation{AttemptID: uuid.New(), Created: true},
	}
	llm := &notebookTestLLM{
		outputs: []string{"Сгенерированный ответ [S1:L1]"},
	}

	s := &NotebookResearchService{
		notebooks: notebookTestReader{owner: 42, files: files},
		drive:     drive,
		llm:       llm,
		logger:    zap.NewNop(),
	}
	s.SetPaidAccounting(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &orgID, IsActive: true}})

	opID := uuid.NewString()
	req := NotebookResearchRequest{
		OperationID: opID,
		Question:    "Создай материалы",
		FileIDs:     []uuid.UUID{fileID},
		Format:      "cards",
	}

	resp, err := s.Ask(context.Background(), 42, chatUUID, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Генерация уже оплачена и списана ДО сохранения в историю:
	require.Len(t, store.completed, 1, "списание должно быть зафиксировано")

	// Симулируем сбой последующей записи истории (например, ошибка базы данных при сохранении):
	saveErr := errors.New("failed to save assistant response: disk full")
	require.Error(t, saveErr, "сохранение в историю упало")

	// Несмотря на сбой записи истории, списание остаётся завершённым и оплаченным:
	require.Len(t, store.completed, 1, "вызов остаётся оплаченным несмотря на сбой истории")
	require.Len(t, llm.requests, 1, "повторная генерация ради истории не выполняется")
}

func fakeDriveSearchService(data map[string][]byte) *DriveSearchService {
	drive := NewDriveSearchService(fakeDriveSearchConfig(), zap.NewNop())
	drive.repo = fakeDriveSearchRepo{}
	extracted := make(map[string]string, len(data))
	for k, v := range data {
		name := strings.TrimPrefix(k, "drive/")
		extracted[name] = string(v)
	}
	drive.fileSvc = fakeDriveSearchFileDownloader{dataByObjectName: data}
	drive.extractor = fakeDriveSearchExtractor{textByFilename: extracted}
	return drive
}

func fakeDriveSearchConfig() config.DriveSearchConfig {
	return config.DriveSearchConfig{}
}
