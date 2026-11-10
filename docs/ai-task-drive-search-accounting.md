# Локальный документ задачи: Подключение Drive AI Search & Indexing к общему учёту бюджетов

**Точки инвентаризации**: B10, B11, B12, B13, B14  
**Дата оформления**: 19 октября 2026 года  
**Статус**: Выделена самостоятельная задача с блокирующими условиями  

---

## 1. Затрагиваемые компоненты и файлы

- `internal/service/drive_search_service.go` — гибридный поиск, `prepareSearchQueryContext`, `rewriteSearchQuery`, `generateHypotheticalDoc`, `embedTexts`, `Rerank`, `IndexFileAsync`, `indexFileInternal`.
- `internal/handler/drive_handler.go` — вызовы `Search` по запросу пользователя.
- `internal/service/drive_service.go` — вызовы фоновой индексации `IndexFileAsync` при загрузке файлов.
- `internal/config/config.go` — параметры `QueryRewriteModel`, `HydeModel`, `EmbeddingModel`, `RerankModel`.
- `cmd/api/main.go` — инициализация `driveSearchService` и подключение к `AIPaidCallerService`.

---

## 2. Плательщик и атрибуция расходов

1. **Операция интерактивного поиска (`Search`)**:
   - Точки: B10 (`rewriteSearchQuery`), B11 (`generateHypotheticalDoc`), B12 (`embedTexts` запроса), B13 (`Rerank`).
   - Плательщик: Авторизованный пользователь поиска (`userID` + `organizationID` из JWT сессии).
   - Единая операция: Все 4 потенциальных вызова в рамках одного поискового запроса должны привязываться к единому стабильному `operationID` поиска с последовательными шагами:
     - `drive_search:<operationID>:rewrite`
     - `drive_search:<operationID>:hyde`
     - `drive_search:<operationID>:embed`
     - `drive_search:<operationID>:rerank`

2. **Фоновая индексация файлов (`IndexFileAsync`, on-demand indexing)**:
   - Точка: B14 (`embedTexts` для чанков документа).
   - Плательщик: В текущей реализации у `IndexFileAsync(fileID)` нет контекста плательщика.
   - Требуется определение продуктового контракта: кто оплачивает индексацию — владелец файла (`file.OwnerID` + его компания) или компания, в которой загружен файл. Для фонового воркера необходимо получать проверенный `domain.AIPaidCaller` владельца файла на момент постановки в очередь.

---

## 3. Модели и контракты usage

| Шаг | Модель | Провайдер | Контракт usage | Блокирующее условие |
| --- | --- | --- | --- | --- |
| B10 Query Rewrite | `Qwen/Qwen3-8B` | NITEC (chat) | `AccountingUsage` (JSON tokens) | Требуется обёртывание в `alemPaidChat` с шагом `drive_search` |
| B11 HyDE | `Qwen/Qwen3-8B` | NITEC (chat) | `AccountingUsage` (JSON tokens) | Требуется обёртывание в `alemPaidChat` с шагом `drive_search` |
| B12 Query Embedding | `BAAI/bge-m3` | NITEC (embeddings) | Векторы `[][]float64` | **Контракт usage embeddings не подтверждён провайдером NITEC**; требуется верификация `prompt_tokens` от провайдера |
| B13 Rerank | `BAAI/bge-reranker-v2-m3` | NITEC (rerank) | Скоры реранка | **Контракт usage rerank не подтверждён провайдером NITEC** |
| B14 Chunk Embeddings | `BAAI/bge-m3` | NITEC (embeddings) | Векторы `[][]float64` | Контракт usage embeddings не подтверждён |

---

## 4. Необходимые изменения в кодовой базе

1. Добавить методы учёта в `DriveSearchService`:
   ```go
   func (s *DriveSearchService) SetPaidAccounting(executor AIPaidCallerService, users aiPayerUsers)
   ```
2. Внедрить адаптер платных вызовов для chat completions (`rewrite` и `hyde`).
3. Для B12, B13, B14: разработать адаптеры верификации `AccountingUsage` для `CreateEmbeddings` и `Rerank` после официального подтверждения провайдером формата отчёта токенов.
4. В `cmd/api/main.go` выполнить инициализацию `driveSearchService.SetPaidAccounting(...)`.

---

## 5. Зависимости и блокирующие условия

1. **Блокирующее условие №1**: Официальное подтверждение провайдером NITEC структуры и семантики `usage` для эндпоинтов `/v1/embeddings` и `/v1/rerank` (сейчас калькулятор `domain.CalculateAICredits` блокирует неподтверждённые семантики).
2. **Блокирующее условие №2**: Продуктовое решение по списанию за поиск: списывать ли микрокредиты с пользователя за каждый поисковый запрос в Drive (пользователь может не ожидать списания при обычном поиске по документам).
3. **Блокирующее условие №3**: Атрибуция фоновой индексации: передача проверенной компании и пользователя через транзакционный outbox / очередь фоновой индексации.

---

## 6. Критерии приёмки

- Ни один вызов к NITEC (rewrite, hyde, embedding, rerank) не выполняется без предварительного резервирования кредитов.
- Отказ по квоте блокирует выполнение платного этапа поиска с безопасным fallback на лексический поиск (BM25/Elasticsearch) без падения всей страницы Drive.
- Повторные поисковые запросы с тем же `OperationID` не вызывают повторной генерации (защита от replay).
- Реализованы unit- и интеграционные тесты с проверкой списания в `ai_reservations`.
