# Ограничение частоты и одновременных ИИ-генераций (Задача 27)

## 1. Назначение и контекст

Механизм регулирования частоты и параллелизма предназначен для защиты от лавины платных вызовов, перегрузки провайдеров и финансовых потерь. Он обеспечивает:
1. **Rate Limiting**: ограничение количества запросов к генерациям на пользователя в скользящем окне времени (sliding window, по умолчанию 20 запросов в минуту).
2. **Concurrency Limiting**: ограничение количества одновременно выполняемых платных генераций на пользователя (по умолчанию максимум 2 параллельных слота).
3. **Multi-Instance Coordination**: согласованная работа между несколькими экземплярами API-сервера через общее распределённое хранилище Redis. Локальные `sync.Mutex` или `sync.Map` не используются как единственная защита.
4. **Pre-Reservation Enforcement**: ограничения проверяются **строго до** создания резерва в базе данных и **строго до** вызова провайдера. При отказе по лимиту база данных не нагружается созданием пустых резервов, а обращение к платным внешним API блокируется.
5. **Fail-Closed Availability Policy**: при недоступности общего хранилища (Redis недоступен/таймаут) система по умолчанию работает в режиме `FailClosed = true`, отклоняя платные вызовы контролируемой серверной ошибкой `503 Service Unavailable` (`AI_LIMITER_UNAVAILABLE`), чтобы исключить неконтролируемый шквал платных генераций в обход лимитов при падении инфраструктуры.
6. **Lease Lifecycle & Crash Recovery**:
   - Параллельный слот оформляется как аренда (Lease) с уникальным идентификатором владельца (`leaseID` UUID).
   - Разрыв клиентского HTTP-соединения не освобождает слот немедленно: слот удерживается до фактического завершения работы генератора и записи расхода в базу данных.
   - Фоновый процесс (heartbeat) регулярно продлевает активную аренду.
   - При падении процесса (краш/OOM) слот автоматически освобождается по истечении `LeaseTTL` (по умолчанию 45 секунд).
   - Запоздалое освобождение слота владельцем (late release после истечения TTL) безопасно: оно удаляет строго токен своего владельца и никогда не затрагивает слот, уже занятый другим запросом.
   - Истечение аренды слота во время генерации не отменяет фиксацию реального расхода провайдера (`SettleSuccess`).

---

## 2. Архитектура компонентов

```
HTTP Request (Gin)
      │
      ▼
AlemHandler / UnifiedAIHandler / NotebookHandler / SmartAgentHandler
      │  (извлечение аутентифицированного пользователя из контекста)
      ▼
alemPaidChat (CreateChatCompletion)
      │
      ▼
AIPaidCallerService (ExecutePaidCall / ExecutePaidCallTyped)
      │
      ├─► 1. Pre-Reservation Enforcement (AIGenerationLimiter.Acquire)
      │        │
      │        ├─► Проверка Rate Limit (Redis Sorted Set, sliding window)
      │        ├─► Очистка истёкших слотов (Lease TTL pruning)
      │        ├─► Проверка Concurrency Limit (< MaxConcurrentPerUser)
      │        └─► Атомарный захват слота (Lua script: ZADD leaseID)
      │
      ├─► [Если отказ]: 429 Too Many Requests (без записи в БД, без вызова провайдера)
      │
      ├─► [Если успех]: Запуск Lease Heartbeat (фоновое продление каждые 15 сек)
      │        │
      │        ├─► 2. Резервирование в БД (store.Reserve)
      │        ├─► 3. Atomic MarkSent (store.MarkSent)
      │        ├─► 4. Вызов провайдера (dispatch)
      │        ├─► 5. Списание расхода (store.SettleSuccess) в detached context
      │        │
      │        └─► [defer]: Остановка Heartbeat + Безопасный Release слота (только leaseID)
```

---

## 3. Распределённые структуры данных в Redis

Для каждого аутентифицированного пользователя `userID` используются два ключа:

### 3.1. Rate Limit: `ai:ratelimit:user:<userID>`
- **Тип**: Redis Sorted Set (ZSet).
- **Member**: `<timestamp_nano>:<lease_id>`.
- **Score**: `<timestamp_nano>` (время поступления запроса в наносекундах).
- **TTL ключа**: `ceil(RateLimitWindow) + 1` секунда.
- **Алгоритм**:
  1. `ZREMRANGEBYSCORE key -inf (now - window)` — удаляет события старше скользящего окна.
  2. `ZCARD key` — текущее количество запросов в окне.
  3. Если `current >= maxReqs` -> отказ `rate_limit_exceeded` (`domain.ErrAIRateLimitExceeded`).
  4. Иначе `ZADD key now now:leaseID` и `EXPIRE key rateExpireSec`.

### 3.2. Concurrency Limit: `ai:concurrency:user:<userID>`
- **Тип**: Redis Sorted Set (ZSet).
- **Member**: `lease_id` (уникальный UUID попытки).
- **Score**: `expire_at_nano` (`now + LeaseTTL`).
- **TTL ключа**: `LeaseTTL + 120` секунд.
- **Алгоритм**:
  1. `ZREMRANGEBYSCORE key -inf now` — автоматическая очистка истёкших слотов от упавших экземпляров API (crash recovery).
  2. `ZCARD key` — количество реально активных параллельных генераций.
  3. Если `active >= maxConcurrent` -> отказ `concurrency_limit_exceeded` (`domain.ErrAIConcurrencyLimitExceeded`).
  4. Иначе `ZADD key expireAt leaseID` и `EXPIRE key concurExpireSec`.

Все эти шаги выполняются **в едином атомарном Redis Lua-скрипте (`luaAcquire`)**, что полностью исключает race conditions между параллельными инстансами API.

---

## 4. Жизненный цикл аренды (Lease Lifecycle)

### 4.1. Захват слота (Acquire)
- Вызывается до обращения к БД и до отправки запроса провайдеру в `aiPaidCallerServiceImpl.ExecutePaidCall`.
- Если лимит исчерпан: возвращается ошибка `ErrAIRateLimitExceeded` или `ErrAIConcurrencyLimitExceeded`. Метод завершается, **не создавая резерва в БД**.

### 4.2. Продление аренды (Heartbeat / Renew)
- При успешном получении слота запускается горутина `runLeaseHeartbeat(lease, interval, stop, done)`.
- Каждые `HeartbeatInterval` (по умолчанию 15 секунд) отправляется команда `Renew`.
- В Redis Lua-скрипте `luaRenew` проверяется `ZSCORE key leaseID`. Если слот активен, его score обновляется на `now + LeaseTTL`.
- Контекст для `Renew` отвязан от клиентского HTTP-запроса (`context.Background()` с таймаутом 5с), что гарантирует удержание слота даже если пользователь закрыл вкладку или прервал соединение.

### 4.3. Безопасное освобождение (Safe Release)
- Вызывается в `defer` функции `ExecutePaidCall` строго после того, как завершился вызов провайдера и завершились все необходимые действия учёта (`SettleSuccess` или запись ошибки в БД).
- Используется отвязанный контекст `context.WithoutCancel(ctx)`.
- В Redis выполняется атомарный `ZREM key leaseID`.
- **Защита от освобождения чужого слота (Late Release)**:
  Если запрос задержался дольше `LeaseTTL`, аренда истекла, и другой запрос занял слот с новым `leaseID_2`, то поздний вызов `lease1.Release()` удаляет ровно `leaseID_1` (которого уже нет в ZSet) и **ни при каких условиях не затрагивает `leaseID_2`**.

### 4.4. Принцип сохранения расхода провайдера
Истечение аренды слота во время выполнения запроса (например, при сетевой задержке обновления TTL) **не считается доказательством отсутствия расхода**.
Если провайдер вернул ответ с использованными токенами, `ExecutePaidCall` передаёт доказательства в `store.SettleSuccess`. Расход фиксируется в полном объёме.

---

## 5. Конфигурация (`AILimiterConfig`)

| Переменная окружения | По умолчанию | Описание |
|----------------------|--------------|----------|
| `AI_LIMITER_ENABLED` | `true` | Включение/выключение лимитера генераций |
| `AI_LIMITER_MAX_CONCURRENT` | `2` | Максимальное количество одновременных генераций на пользователя |
| `AI_LIMITER_RATE_REQUESTS` | `20` | Лимит запросов на генерацию в скользящем окне |
| `AI_LIMITER_RATE_WINDOW` | `1m` | Длительность скользящего окна rate limit |
| `AI_LIMITER_LEASE_TTL` | `45s` | Время жизни аренды слота (авто-восстановление после краша) |
| `AI_LIMITER_HEARTBEAT_INTERVAL` | `15s` | Интервал фонового продления аренды активным владельцем |
| `AI_LIMITER_FAIL_CLOSED` | `true` | Поведение при сбое Redis: `true` — отклонять запросы (fail-closed); `false` — пропускать |

Валидация конфигурации гарантирует:
- `MaxConcurrentPerUser >= 1`;
- `RateLimitRequests >= 1`;
- `LeaseTTL > HeartbeatInterval` (аренда всегда живёт дольше интервала heartbeat);
- Все временные интервалы строго положительны и лежат в допустимых границах.

---

## 6. Коды ошибок API и локализация

| Ошибка домена | HTTP Статус | Код ошибки | Локализация (RU / KK) |
|---------------|-------------|------------|-----------------------|
| `ErrAIRateLimitExceeded` | `429 Too Many Requests` | `AI_RATE_LIMIT` | Превышен лимит запросов к ИИ. Пожалуйста, подождите немного перед следующим запросом. / ЖИ-ге сұраныс лимиті асып кетті. Келесі сұраныс алдында біраз күте тұрыңыз. |
| `ErrAIConcurrencyLimitExceeded` | `429 Too Many Requests` | `AI_CONCURRENCY_LIMIT` | У вас уже выполняется генерация ответа. Дождитесь её завершения. / Сізде ЖИ жауабын жасау әрекеті жүріп жатыр. Оның аяқталуын күтіңіз. |
| `ErrAILimiterUnavailable` | `503 Service Unavailable` | `AI_LIMITER_UNAVAILABLE` | Сервис контроля генераций временно недоступен. Попробуйте позже. / ЖИ женерацияларын бақылау қызметі уақытша қолжетімсіз. Кейінірек қайталап көріңіз. |

---

## 7. Доказательства и тесты

Реализация покрыта автоматическими тестами:
- `internal/config/ai_limiter_test.go`:
  - `TestAILimiterConfig`: проверка валидации границ и загрузки параметров из окружения.
- `internal/service/ai_limiter_test.go`:
  - `TestAILimiter_TwoInstances_ConcurrencyEnforcement`: координация двух инстансов API через общий стор, проверка параллельных слотов на пользователя и изоляции разных пользователей.
  - `TestAILimiter_RateLimit_SlidingWindow`: точное соблюдение скользящего окна частоты запросов.
  - `TestAILimiter_CrashRecovery_ExpiredLeasePruned`: падение владельца слота приводит к автоматической очистке истёкшей аренды через `LeaseTTL` без вечной блокировки.
  - `TestAILimiter_SafeRelease_LateReleaseDoesNotTouchOtherOwnerSlot`: запоздалое освобождение чужого слота не затрагивает аренду нового владельца.
  - `TestAILimiter_FailClosed_WhenStoreUnavailable`: безопасный fail-closed при падении хранилища.
  - `TestAILimiter_RenewHeartbeat`: продление времени жизни активной аренды.
  - `TestAILimiter_ConcurrentRace`: защита от race conditions при одновременных запросах.
- `internal/service/ai_paid_caller_limiter_test.go`:
  - `TestAIPaidCall_FrequentRejectionsDoNotCreateReservations`: доказано, что при исчерпании лимитов БД не создаёт ни одного резерва (`reserveCalls == 0`), а вызовы провайдера блокируются.
  - `TestAIPaidCall_ClientCancellationDoesNotPrematurelyReleaseSlot`: доказано, что разрыв HTTP-соединения не освобождает слот, пока генерация и фиксация в БД продолжаются.
  - `TestAIPaidCall_LeaseExpirationDoesNotDropProviderExpense`: доказано, что истечение аренды слота не отменяет списание расхода у провайдера (`SettleSuccess` подтверждён).
  - `TestAIPaidCall_TwoInstances_MultiNodeCoordination`: согласованность полного цикла `ExecutePaidCall` на двух независимых узлах API.
- `internal/handler/alem_chat_error_test.go`:
  - `TestAlemChatAccountingErrorsAreLocalized`: проверка безопасных локализованных сообщений ошибок на русском и казахском языках.
