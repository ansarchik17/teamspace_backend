# Сверка инвентаризации ИИ-обращений и статус учёта: задача 26

> Обновление 20 октября: таблица ниже фиксирует историческое состояние 19 октября. Неподключённые production-границы Drive, adaptive web search, 2slides, удалённого DocumentParse, AxisMail и внешних legacy POST закрыты. Актуальные ограничения, доказательства и команды ручной сверки: [ai-production-readiness.md](ai-production-readiness.md). Эти пути не объявляются интегрированными или бесплатными.

**Основание**: Полная инвентаризация задачи 01 (`workspace-front/docs/ai-provider-call-inventory.md`).  
**Дата аудита**: 19 октября 2026 года.  
**Исследованные кодовые базы**: `workspace-backend` и `workspace-front`.

---

## 1. Сводная матрица всех точек инвентаризации

| ID | Точка / Исходник | Используется | Кто платит | Провайдер / Модель | Единица расхода | Контракт usage | Механизм резервирования | Статус подключения | Проверяющий тест |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **B01** | Основной чат Alem (`alem_service.go`) | Да | Проверенный пользователь (`domain.AIPaidCaller`) | NITEC (`gpt-oss-120b`, `gemma-3-27b`, `Kimi-K2-Thinking` и др.) | Микрокредиты (токены) | Подтверждён (`AccountingUsage` JSON) | `alemPaidChat` (`alem:<turnID>:1`) | **Подключено** (задача 22) | `internal/service/alem_accounting_test.go`, `internal/handler/alem_chat_error_test.go` |
| **B02** | Контекстный цикл Alem (`ai_context_chat.go`) | Да | Проверенный пользователь B01 | NITEC, модель запроса | Микрокредиты (токены) | Подтверждён (`AccountingUsage`) | `alemPaidChat` (до 9 раундов `alem:<turnID>:<step>`) | **Подключено** (задача 22) | `internal/service/ai_context_chat_test.go` |
| **B03** | Проверка/публикация Alem (`ai_user_answer.go`) | Да | Проверенный пользователь B01 | NITEC, переданная модель | Микрокредиты (токены) | Подтверждён (`AccountingUsage`) | `alemPaidChat` (`alem:<turnID>:<step>`) | **Подключено** (задача 22) | `internal/service/alem_accounting_test.go` |
| **B04** | Автонаименование чата в фоне (`alem_service.go:626`) | Да | Проверенный пользователь B01 (`WithoutCancel`) | NITEC, `google/gemma-3-27b` | Микрокредиты (токены) | Подтверждён (`AccountingUsage`) | `alemPaidChat` (`alem:<turnID>:title`) | **Подключено** (задача 22) | `internal/service/alem_accounting_test.go` |
| **B05** | Unified AI чат (`unified_ai_service.go`) | Да | Проверенный пользователь (`domain.AIPaidCaller`) | NITEC (`DefaultAIModel` / запрошенная) | Микрокредиты (токены) | Подтверждён (`AccountingUsage`) | `newUnifiedAIPaidChat` (`unified_ai:<turnID>:1`, fallback :2) | **Подключено** (задача 23) | `internal/service/unified_ai_accounting_test.go`, `internal/handler/unified_ai_chat_error_test.go` |
| **B06** | Proxy embeddings (`alem_service.go`, `unified_ai_service.go`) | Нет | Не определён | NITEC, `BAAI/bge-m3` | Векторы | **Не подтверждён** | Отсутствует | **Заблокировано enforcement'ом** (HTTP 403 `AI_CHAT_ACCESS`) | `internal/handler/proxy_enforcement_test.go` |
| **B07** | Proxy rerank (`alem_service.go`, `unified_ai_service.go`) | Нет | Не определён | NITEC, `BAAI/bge-reranker-v2-m3` | Скоры | **Не подтверждён** | Отсутствует | **Заблокировано enforcement'ом** (HTTP 403 `AI_CHAT_ACCESS`) | `internal/handler/proxy_enforcement_test.go` |
| **B08** | Notebook: основной ответ (`notebook_research_service.go`) | Да | Проверенный пользователь (`domain.AIPaidCaller`) | NITEC, fast (`gpt-oss-120b`) / thinking (`Kimi-K2`, `Qwen3.5`, `DeepSeek-V3.2`) | Микрокредиты (токены) | Подтверждён (`AccountingUsage`) | `newNotebookPaidChat` (`notebook:<opID>:1`, fallback :2) | **Подключено** (задача 24) | `internal/service/notebook_accounting_test.go`, `internal/handler/notebook_chat_error_test.go` |
| **B09** | Notebook: проверка ответа (`notebook_verification.go`) | Да | Проверенный пользователь B08 | NITEC, модель вызова B08 | Микрокредиты (токены) | Подтверждён (`AccountingUsage`) | `newNotebookPaidChat` (`notebook:<opID>:<step>`) | **Подключено** (задача 24) | `internal/service/notebook_accounting_test.go` |
| **B10** | Drive: rewrite поискового запроса (`drive_search_service.go`) | Да | Пользователь поиска | NITEC, `Qwen/Qwen3-8B` | Токены | Совместим | Не подключён | **Выделена задача** (`docs/ai-task-drive-search-accounting.md`) | Блокируется неподтверждённым контрактом embeddings/rerank для поиска |
| **B11** | Drive: HyDE генерация (`drive_search_service.go`) | Да | Пользователь поиска | NITEC, `Qwen/Qwen3-8B` | Токены | Совместим | Не подключён | **Выделена задача** (`docs/ai-task-drive-search-accounting.md`) | См. B10 |
| **B12** | Drive: query embedding (`drive_search_service.go`) | Да | Пользователь поиска | NITEC, `BAAI/bge-m3` | Векторы | **Не подтверждён** | Не подключён | **Выделена задача** (`docs/ai-task-drive-search-accounting.md`) | Блокируется отсутствием подтверждённого контракта usage embeddings |
| **B13** | Drive: rerank результатов (`drive_search_service.go`) | Да | Пользователь поиска | NITEC, `BAAI/bge-reranker-v2-m3` | Скоры | **Не подтверждён** | Не подключён | **Выделена задача** (`docs/ai-task-drive-search-accounting.md`) | Блокируется отсутствием подтверждённого контракта usage rerank |
| **B14** | Drive: chunk embeddings при индексации (`drive_search_service.go`) | Да | Не определён (фоновый воркер `IndexFileAsync`) | NITEC, `BAAI/bge-m3` | Векторы | **Не подтверждён** | Не подключён | **Выделена задача** (`docs/ai-task-drive-search-accounting.md`) | Блокируется отсутствием атрибуции плательщика в фоне и контракта embeddings |
| **B15** | Smart Agent (`smart_agent_service.go`) | Да | Проверенный пользователь (`domain.AIPaidCaller`) | OpenAI `gpt-4o` (`openai-direct`) / NITEC (`DefaultAlemModel`) | Микрокредиты (токены) | Подтверждён (`AccountingUsage`) | `newSmartAgentPaidChat` (`smart_agent:<turnID>:<step>`) | **Подключено** (задача 25) | `internal/service/smart_agent_accounting_test.go`, `internal/handler/smart_agent_chat_error_test.go` |
| **B16** | Adaptive web search (`adaptive_web_search.go`) | Да | Пользователь агента | Внутренний microservice `/v1/chat/ask_adaptive` | Единицы расхода не подтверждены (внутренний RAG) | Отсутствует | Включён в шаг Smart Agent | **Не подтверждено: нужен контракт внутреннего сервиса и отдельный учёт** | `internal/agent/adaptive_web_search_test.go` |
| **B17** | 2slides презентации (`twoslides_service.go`) | Да | Пользователь Notebook | Внешний сервис 2slides | **Нетокенная услуга** (задания на презентацию) | **Нетокенный** (`jobId`) | Не подключён | **Выделена задача** (`docs/ai-task-twoslides-accounting.md`) | Блокируется отсутствием job-based тарификации в `credits-v1` |
| **M01** | Почтовый воркер AxisMail (`workers.ts`) | Да | Пользователь почты (`user_id`, `tenant_id`) | Внешний LLM (`AI_BASE_URL`) | Токены | Usage игнорируется | Локальный лимит 30 заданий/сутки | **Выделена задача** (`docs/ai-task-mail-worker-accounting.md`) | Блокируется отсутствием межсервисного API учёта и маппинга tenant->company |
| **F01–F03** | Браузерный клиент `llm.js` | Нет | — | NITEC (прямой из браузера) | Токены | — | — | **Доказано отсутствие использования** (`getLlmApiKey()` возвращает `""`, вызовы из UI отсутствуют) | `src/presentation/pages/alemdrive/drive-src/api/llm.js:25` |
| **X01–X05** | Внешние `/api/tools/...` (`HttpAlemAiRepository.ts`) | Нет в Go API | — | Внешние mock/edge шлюзы | STT секунды / токены | Неизвестен | — | **Выделена задача** (`docs/ai-task-external-tools-accounting.md`) | Маршруты не зарегистрированы в Go бэкенде |
| **X06** | DocumentParse (`content_extraction_service.go`) | Да | Пользователь документа | Внешний HTTP парсер | Единицы расхода не подтверждены (OCR/парсинг страниц) | Текстовые чанки | — | **Стоимость не подтверждена: нужен контракт парсера** | `internal/service/content_extraction_service.go:281` |
| **Audio/STT** | `services/notes-stt`, NTS-004 | Да | — | MinIO Storage | Байт-хранилище | — | — | **Доказано отсутствие платного использования** (хранение mp3/wav и Range-стриминг) | `db/migrations/20260930000100_notes_audio_recording.sql` |
| **Doc Worker** | `AIDocumentService` | Да | — | Локальный парсинг | Текст чанков | — | — | **Доказано отсутствие платного использования** (нет вызовов к LLM) | `internal/service/ai_document_service.go` |
| **Assistant** | `AIAssistantService` | Да | — | Локальная агрегация | Данные календаря/задач | — | — | **Доказано отсутствие платного использования** (нет вызовов к LLM) | `internal/service/ai_assistant_service.go` |
| **Stream** | `CreateChatCompletionStream` (`methods.go`) | Нет | — | NITEC (Stream) | Токены | — | — | **Доказано отсутствие использования** (0 вызовов в проекте) | `internal/alem/methods.go:89` |

---

## 2. Реализованный Enforcement для неподтверждённых контрактов

В соответствии с правилом:
> *«При неподтверждённом контракте enforcement должен явно запрещать этот путь либо использовать уже согласованный режим наблюдения — без бесплатного обхода.»*

1. **Proxy Embeddings (`B06`) и Proxy Rerank (`B07`)**:
   - Эндпоинты `POST /api/v1/ai/embeddings`, `POST /api/v1/ai/rerank`, `POST /api/v1/unified-ai/embeddings`, `POST /api/v1/unified-ai/rerank` блокируются enforcement'ом с возвратом HTTP 403 `ErrCodeAIChatAccess` ("Платный вызов временно недоступен: контракт учёта расхода не подтверждён").
   - Закрыты только прямые proxy endpoints B06/B07. Drive и другие перечисленные ниже пути ещё не покрыты общим учётом.
   - Проверено тестом [`internal/handler/proxy_enforcement_test.go`](file:///c:/Users/Merey/workspace-backend/internal/handler/proxy_enforcement_test.go).

2. **Запрет подмены тарифных единиц**:
   - Нетокенные услуги (секунды аудиозаписей, задания 2slides, файлы DocumentParse) не выдаются за токены. До подтверждения официальных контрактов эти сервисы не списывают ИИ-кредиты фиктивными значениями.

---

## 3. Выделенные самостоятельные задачи с блокирующими условиями

Для оставшихся неподключённых используемых путей сформированы детальные локальные документы спецификаций с указанием точных файлов, плательщика, контракта, изменений, зависимостей и критериев приёмки:
1. [`docs/ai-task-drive-search-accounting.md`](file:///c:/Users/Merey/workspace-backend/docs/ai-task-drive-search-accounting.md) — Drive AI Search & Indexing (B10, B11, B12, B13, B14).
2. [`docs/ai-task-twoslides-accounting.md`](file:///c:/Users/Merey/workspace-backend/docs/ai-task-twoslides-accounting.md) — 2slides генерация презентаций (B17).
3. [`docs/ai-task-mail-worker-accounting.md`](file:///c:/Users/Merey/workspace-backend/docs/ai-task-mail-worker-accounting.md) — Почтовый AI воркер AxisMail (M01).
4. [`docs/ai-task-external-tools-accounting.md`](file:///c:/Users/Merey/workspace-backend/docs/ai-task-external-tools-accounting.md) — Внешние интерфейсы X01–X05.

---

## 4. Итог аудита

- **Подключены к общему учёту с подтверждёнными тестами**: B01, B02, B03, B04, B05, B08, B09, B15.
- **Закрыты enforcement'ом для предотвращения бесплатного обхода**: B06, B07.
- **Доказано отсутствие платного использования**: F01–F03, AIDocumentService, AIAssistantService, CreateChatCompletionStream.
- **Не подтверждены и требуют завершения интеграции**: B16 (adaptive web search), X06 (DocumentParse), внешний STT worker. Наличие HTTP-вызова без usage не доказывает отсутствие платного расхода.
- **Оформлены локальные задачи с блокирующими условиями**: B10–B14 (Drive Search), B17 (2slides), M01 (AxisMail), X01–X05 (External Tools).

## Ограничение готовности

Задача 26 не завершена: отдельные документы не подключают работающие Drive, 2slides и внешние workers к бюджету. До общего включения ограничений необходимы их интеграция либо явно согласованное отключение. Контракт usage внешних сервисов и исходники внешних workers отсутствуют в этих репозиториях; фиктивное списание не применяется.
