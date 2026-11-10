# Единые ошибки исчерпания бюджетов и отказа в доступе (Задача 29)

Документ описывает унифицированную обработку и публичный HTTP-контракт ошибок исчерпания бюджетов, квот, rate-limiting и отказа в доступе к ИИ-функциям платформы (Alem Chat, Notebook Research, Smart Agent, Unified AI) в соответствии со спецификацией задачи 04 (`docs/ai-budget-api-contract.md`, разделы 8 и 9).

---

## 1. Архитектура и точки входа

Все подключённые платные ИИ-операции используют единый механизм трансляции ошибок:
`WriteAICallError(c *gin.Context, err error)` и предикат `IsAICallError(err error) bool` в [internal/handler/ai_error_response.go](file:///c:/Users/Merey/workspace-backend/internal/handler/ai_error_response.go).

### Подключённые обработчики:
1. **Alem Chat**: `AlemHandler.SendMessage` и `AlemHandler.SendMessageWithAttachment` вызывают `WriteAICallError`.
2. **Notebook**: `NotebookHandler.Ask` (метод `researchRequest`) перехватывает все ошибки `h.research.Ask`, проверяет `IsAICallError(err)` и вызывает `WriteAICallError`.
3. **Smart Agent**: `SmartAgentHandler.Chat` при ошибках `h.service.ProcessLegacy` проверяет `IsAICallError(err)` и вызывает `WriteAICallError`, не допуская fallback на v2 при бюджетных ошибках.
4. **Unified AI**: `UnifiedAIHandler.SendMessage` вызывает `WriteAICallError`.

---

## 2. Разграничение статусов и кодов ошибок

| Ситуация / Доменная причина | HTTP-статус | Код ошибки (`code`) | Поля `details` | Поведение заголовка `Retry-After` |
|---|---|---|---|---|
| **Личный 5-часовой лимит исчерпан** (`FIVE_HOUR_EXHAUSTED`) | `429 Too Many Requests` | `AI_QUOTA_EXCEEDED` | `blockers`, `retry_kind: "estimated"`, `retry_at`, `recheck_at`, `server_time` | Выставляется (в секундах до `resets_at`) |
| **Личная недельная квота исчерпана** (`USER_WEEKLY_EXHAUSTED`) | `429 Too Many Requests` | `AI_QUOTA_EXCEEDED` | `blockers`, `retry_kind: "estimated"`, `retry_at`, `recheck_at`, `server_time` | Выставляется (в секундах до `resets_at`) |
| **Бюджет компании исчерпан** (`COMPANY_WEEKLY_EXHAUSTED`) | `429 Too Many Requests` | `AI_QUOTA_EXCEEDED` | `blockers`, `retry_kind: "requires_action"`, `retry_at: null`, `recheck_at: null`, `server_time` | **Не выставляется** (требуется пополнение администратором) |
| **Служебная квота исчерпана** (`SERVICE_WEEKLY_EXHAUSTED`) | `429 Too Many Requests` | `AI_QUOTA_EXCEEDED` | `blockers`, `retry_kind: "requires_action"`, `server_time` | **Не выставляется** |
| **Остаток удерживается активными генерациями** (`RESERVATION_PENDING`) | `429 Too Many Requests` | `AI_QUOTA_EXCEEDED` | `blockers`, `retry_kind: "unknown"`, `retry_at: null`, `recheck_at: null`, `server_time` | **Не выставляется** |
| **Запрос превышает размер периода** (`RESERVATION_TOO_LARGE`) | `429 Too Many Requests` | `AI_QUOTA_EXCEEDED` | `blockers`, `retry_kind: "requires_action"`, `server_time` | **Не выставляется** |
| **Отсутствие настройки квоты/бюджета** (`USER_QUOTA_UNCONFIGURED`, `COMPANY_BUDGET_UNCONFIGURED`) | `403 Forbidden` | `AI_ACCESS_DISABLED` | `reason`, `scope: "account"` / `"company"` | **Не выставляется** |
| **Отключение квоты/бюджета** (`USER_QUOTA_DISABLED`, `COMPANY_BUDGET_DISABLED`, `FIVE_HOUR_DISABLED`, `SERVICE_QUOTA_DISABLED`) | `403 Forbidden` | `AI_ACCESS_DISABLED` | `reason`, `scope: "weekly"` / `"company"` / `"five_hour"` / `"service"` | **Не выставляется** |
| **Блокировка доступа / отсутствие организации** (`AI_ACCESS_DENIED`, `COMPANY_SUSPENDED`, `NO_ORGANIZATION`, `ErrAIPaidCall*`) | `403 Forbidden` | `AI_ACCESS_DISABLED` | `reason`, `scope: "account"` / `"company"` | **Не выставляется** |
| **Превышение частоты запросов** (`ErrAIRateLimitExceeded`) | `429 Too Many Requests` | `AI_RATE_LIMIT` | отсутствуют | **Не выставляется** |
| **Превышение параллельных генераций** (`ErrAIConcurrencyLimitExceeded`) | `429 Too Many Requests` | `AI_CONCURRENCY_LIMIT` | отсутствуют | **Не выставляется** |
| **Конфликт операции / идемпотентность** (`ErrAIReservationConflict`) | `409 Conflict` | `AI_RESERVATION_CONFLICT` | `reason: "OPERATION_CONFLICT"` | **Не выставляется** (предотвращает автоповтор) |
| **Недоступность Redis-лимитера** (`ErrAILimiterUnavailable`) | `503 Service Unavailable` | `AI_LIMITER_UNAVAILABLE` | отсутствуют | **Не выставляется** |
| **Нетарифицированная модель** (`ErrAICreditUnknownModel`) | `503 Service Unavailable` | `AI_MODEL_UNPRICED` | отсутствуют | **Не выставляется** |
| **Сбой сервиса учёта** (`ErrAICreditUnknownPolicy`) | `503 Service Unavailable` | `AI_ACCOUNTING_UNAVAILABLE` | отсутствуют | **Не выставляется** |
| **Провайдер вернул 429** (`alem.APIError{StatusCode: 429}`) | `503 Service Unavailable` | `INTERNAL_ERROR` | отсутствуют | **Не выдаётся за квоту пользователя** |
| **Сетевая ошибка провайдера / 500** (`alem.APIError{StatusCode: 500}`) | `502 Bad Gateway` | `INTERNAL_ERROR` | отсутствуют | **Скрыты внутренние URL и ключи** |
| **Таймаут генерации** (`context.DeadlineExceeded`) | `504 Gateway Timeout` | `INTERNAL_ERROR` | отсутствуют | Безопасное сообщение ожидания |

---

## 3. Требования информационной безопасности

1. **Изоляция корпоративных сумм**:
   В массиве `blockers` объекта `details` при ошибке `429 AI_QUOTA_EXCEEDED` передаются исключительно поля:
   - `reason`: строка (причина блокировки);
   - `scope`: строка (`five_hour`, `weekly`, `company`, `service`);
   - `resets_at`: ISO-строка времени сброса или `null`;
   - `enforced`: булево значение (`true`).
   Суммы лимитов, израсходованные микрокредиты, остатки баланса компании и пользователей **никогда не возвращаются клиенту** в ответах об ошибках.

2. **Защита от кэширования**:
   Все ответы об ошибках содержат заголовок `Cache-Control: no-store`.

3. **Защита от циклических платных повторов**:
   - Заголовок `Retry-After` выставляется **только** при `retry_kind == "estimated"` и положительном времени до сброса.
   - При `unknown`, `requires_action` и конфликте `409` заголовок `Retry-After` строго отсутствует, исключая автоматические спам-повторы клиентскими библиотеками.

---

## 4. Локализация (ru / kk)

Все ошибки снабжены безопасными пользовательскими формулировками в файле [internal/i18n/ai_chat.json](file:///c:/Users/Merey/workspace-backend/internal/i18n/ai_chat.json).
Примеры:
- `USER_WEEKLY_EXHAUSTED`:
  - **ru**: *"Исчерпана недельная квота ИИ-кредитов. Доступ восстановится в следующем периоде."*
  - **kk**: *"Апталық ЖИ-кредиттер квотаңыз таусылды. Қолжетімділік келесі кезеңде қалпына келеді."*
- `COMPANY_SUSPENDED`:
  - **ru**: *"Компания приостановлена. Доступ к ИИ временно недоступен."*
  - **kk**: *"Компания тоқтатылды. ЖИ-ге қолжетімділік уақытша жоқ."*
- `AI_CONCURRENCY_LIMIT`:
  - **ru**: *"У вас уже выполняется генерация ответа. Дождитесь её завершения."*
  - **kk**: *"Сізде ЖИ жауабын жасау әрекеті жүріп жатыр. Оның аяқталуын күтіңіз."*

---

## 5. Проверка и тесты

- `internal/handler/ai_error_response_contract_test.go`:
  - `TestAIErrorResponseContract_AllAccessDisabledReasons`
  - `TestAIErrorResponseContract_QuotaExceededSemanticsAndRetryAfter`
  - `TestAIErrorResponseContract_CallerAndContextErrors`
  - `TestAIErrorResponseContract_RateAndConcurrencyLimits`
  - `TestAIErrorResponseContract_OperationConflict`
  - `TestAIErrorResponseContract_AccountingAndPricingErrors`
  - `TestAIErrorResponseContract_UpstreamProviderErrors`
  - `TestAIErrorResponseContract_IsAICallError`
- `internal/handler/alem_chat_error_test.go`
- `internal/handler/notebook_chat_error_test.go`
- `internal/handler/smart_agent_chat_error_test.go`
- `internal/handler/unified_ai_chat_error_test.go`
- Все тесты проходят успешно (`go test ./internal/handler/...`, `go test ./internal/apperrors/...`, `go test ./internal/service/...`, `go test ./internal/agent/...`).
