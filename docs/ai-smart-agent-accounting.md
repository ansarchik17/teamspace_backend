# Учёт платных генераций Smart Agent: задача 25

Подключён чат Smart Agent (`POST /agent/chat`) через `SmartAgentService` и `SmartAgentHandler` к общему механизму учёта бюджетов ИИ-токенов (`AIPaidCallerService`). Устранены прямые и нерезервированные HTTP-вызовы в обход системы учёта. Личность пользователя и организация определяются исключительно на сервере.

## 1. Выявление и устранение нерезервированных вызовов

Ранее в `internal/service/smart_agent_service.go` метод `callOpenAI` выполнял прямой HTTP POST на `https://api.openai.com/v1/chat/completions` без context, без предварительного резервирования и без фиксации расхода в БД.
В `internal/handler/smart_agent_handler.go` при ошибке `ProcessLegacy` происходил молчаливый fallback на v2 (`h.service.Chat`), что могло маскировать отказы по квоте или отсутствие плательщика.

**Внесённые изменения**:
- Прямой вызов в `callOpenAI` заменён на единый адаптер `alemPaidChat` с интерфейсом `aiCompletionClient`.
- Реализован `openAICompletionClient`, поддерживающий отмену через `context.Context` и передающий сырой usage провайдера в `alem.ChatCompletionResponse.AccountingUsage`.
- Добавлена функция `aiProviderForModel`: для модели `gpt-4o` автоматически назначается `ProviderID: "openai-direct"`, а для моделей NITEC — `ProviderID: "nitec"`, что обеспечивает корректную тарификацию по утверждённой политике v1.
- В `SmartAgentHandler.Chat` бюджетные ошибки (`domain.AIReservationDenied`, `domain.ErrAIPaidCallMissingContext`, `domain.ErrAIPaidCallInvalidCaller`, `domain.ErrAIPaidCallContextMismatch`, `domain.ErrAIReservationConflict`) перехватываются и возвращаются клиенту со статусами 429 / 403 / 502 через `writeAlemChatError`. Обход квот через fallback на v2 заблокирован.

## 2. Идентичность плательщика и безопасность компании

- Личность пользователя извлекается из проверенного токена аутентификации (`middleware.MustGetUserID(c)`).
- Проверяется принадлежность пользователя к компании, статус активности (`IsActive=true`) и отсутствие отметки удаления (`DeletedAt == nil`).
- Создаётся неизменяемый контекст `domain.AIPaidCaller` (`domain.WithAIPaidCaller`).
- Попытки передать чужого плательщика в контексте пресекаются (`domain.ErrAIPaidCallContextMismatch`).
- Ни модель, ни аргументы инструментов не могут переопределить плательщика, организацию, модель или тариф.

## 3. Цикл инструментов (Tool Loop) и раздельный учёт генераций

- Внутри цикла инструментов (до 5 итераций) каждый вызов LLM учитывается как отдельный шаг:
  `smart_agent:<sessionID>:1`, `smart_agent:<sessionID>:2`, ...
- Вызов инструментов (`s.executeTool`):
  - Передаёт проверенный контекст `paidCtx` и аутентифицированный `userID`.
  - Все действия инструментов (`search_users`, `get_user_events`, `create_calendar_event`, `create_kanban_task` и др.) являются неплатными операциями с локальной БД и не списывают ИИ-кредиты.
- При получении ответа модели с `finish_reason == "tool_calls"`:
  - Шаг 1 завершается и списывается в БД (`settled`).
  - Инструменты выполняются (0 кредитов).
  - На следующем шаге модель получает результаты инструментов. Следующая генерация резервирует и списывает кредиты под шагом 2.
- Если на следующем шаге квота пользователя исчерпана (`domain.AIReservationDenied`), следующий вызов блокируется, провайдер не вызывается, а расход за предыдущий шаг остаётся зафиксированным в БД.
- Если инструмент возвращает ошибку, сообщение об ошибке передаётся модели на следующем шаге; проведённый расход за предыдущие шаги не аннулируется.
- При отмене контекста (`context.Canceled`) или таймауте проведённые до этого шаги остаются сохранёнными со статусом `settled`.

## 4. Смена провайдера и модели (Fallback)

- Если целевая модель (например, `gpt-4o` или настроенная в `s.defaultModel`) возвращает ошибку доступа к модели (`isModelAccessDenied`), первая попытка фиксируется в журнале со статусом `unknown`.
- Smart Agent автоматически переключается на резервную модель `DefaultAlemModel` (`openai/gpt-oss-120b`).
- Вторая попытка регистрирует собственный резерв с тарифом `DefaultAlemModel` и шагом 2.
- В журнале фиксируются обе попытки без скрытых повторов.

## 5. Идемпотентность и защита от повторов (Replay Protection)

- Клиент может передавать `operation_id` в теле запроса либо в HTTP-заголовках `X-Operation-ID` / `Idempotency-Key`.
- При повторной доставке операции (`!reservation.Created`) адаптер возвращает `domain.ErrAIReservationConflict`. Повторное обращение к провайдеру блокируется.

## 6. Выполненные проверки

1. **Unit-тесты (`internal/service/smart_agent_accounting_test.go`)**:
   - `TestSmartAgentMissingPayerContext`: отказ при ненастроенном учёте, неактивном пользователе, пользователе без компании, несовпадении контекста плательщика (0 обращений к провайдеру, 0 резерваций).
   - `TestSmartAgentQuotaDeniedBlocksFirstStep`: отказ по квоте до первого шага (0 обращений к провайдеру).
   - `TestSmartAgentQuotaDeniedBlocksNextStep`: шаг 1 оплачен и сохранён, отказ по квоте на шаге 2 блокирует провайдера.
   - `TestSmartAgentToolLoopMultipleGenerations`: цикл из 2 генераций и выполнения инструмента (ровно 2 списания, инструмент не тратит кредиты).
   - `TestSmartAgentToolErrorPreservesPreviousExpense`: ошибка инструмента не отменяет оплату шага 1.
   - `TestSmartAgentContextCancelledAfterStep1`: отмена контекста после шага 1 сохраняет проведённый расход.
   - `TestSmartAgentModelFallback`: отказ доступа к первой модели фиксируется как `unknown`, fallback на `DefaultAlemModel` успешно списывается (`settled`).
   - `TestSmartAgentReplayProtection`: повторная доставка операции возвращает конфликт без обращения к провайдеру.
2. **HTTP Handler тесты (`internal/handler/smart_agent_chat_error_test.go`)**:
   - `TestSmartAgentHandlerBudgetErrorsDoNotFallbackToV2`: ошибки лимитов и квот возвращают 429 / 403 / 502 без обхода через fallback на v2.
   - `TestSmartAgentHandlerPassesOperationIDHeaders`: извлечение `OperationID` из заголовков `X-Operation-ID` и `Idempotency-Key`.
3. **Интеграционные тесты с Postgres (`internal/service/smart_agent_accounting_integration_test.go`)**:
   - Проверка записи реальных транзакций в БД для сценариев: `tool_loop_success`, `model_fallback`, `quota_denied`, `replay_conflict`.
