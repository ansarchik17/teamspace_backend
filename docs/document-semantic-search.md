# Гибридный поиск по документам

Вложения AI-чата: PostgreSQL full-text (`simple` для кодов и точных терминов,
`russian` для словоформ) + cosine similarity pgvector, объединённые RRF.
Поиск фильтрует владельца, владельца чата, выбранные документы, `ready` и
`deleted_at` до векторного ранжирования. Неверный UUID выбора возвращает ошибку,
а не расширяет поиск на все документы. Инструмент AI `chat_documents` принимает
`params.query`; можно ограничить поиск одним документом через `id`.

В блокноте короткие источники по-прежнему читаются целиком. Для длинных файлов,
которые не помещаются в общий контекст 150 000 символов, векторное ранжирование
блоков дополняет выбор строк по словам. Сохраняются исходные номера строк,
ревизия, квота каждого выбранного источника и флаг неполного чтения. Перед
извлечением текста проверяются доступ к блокноту и ACL каждого файла Drive.
Глобальный поиск по содержимому Drive теперь использует общий адаптер; архитектура и включение описаны в `file-retrieval-architecture.md`. Поиск списка файлов по имени остаётся отдельной операцией.

Embeddings рассчитывает локальная модель Ollama: запросы к платным провайдерам
и их ограничения учёта не меняются. Тексты передаются настроенному серверу
Ollama, поэтому URL должен указывать на собственный доверенный сервис.

## Включение на сервере

Код не меняет образ общей БД и не перезапускает инфраструктуру автоматически.
Обычный запуск без `DOCUMENT_EMBEDDING_URL` работает без pgvector. Если URL
задан, но таблица embeddings не подготовлена, процесс останавливается с явной
ошибкой конфигурации.

1. Определить major PostgreSQL (`SHOW server_version;`), реальный образ,
   PGDATA, тома и способ деплоя shared-infra. Подготовить резервную копию.
   Установить pgvector в образ той же major-версии и с совместимыми путями
   PGDATA. Изменение общей БД выполнять через её штатный деплой. Нельзя просто
   менять `postgres:alpine` на произвольный образ с другим layout данных.
2. От имени администратора БД выполнить в базе `workspace` файл
   `deploy/production/document-search.sql`. Если API работает под отдельной
   ролью, выдать ей SELECT, INSERT, UPDATE на `public.ai_text_embeddings`.
   Обычная миграция также создаёт эту таблицу, если расширение уже включено;
   ручной SQL подходит и после того, как миграция была применена без pgvector.
3. Разместить `compose.document-search.yml` на сервере и запустить отдельный
   сервис модели (желательно закрепить протестированный digest образа через
   `DOCUMENT_OLLAMA_IMAGE`):

   ```bash
   docker compose -f /путь/compose.document-search.yml up -d
   docker compose -f /путь/compose.document-search.yml exec embeddings ollama pull bge-m3
   docker compose -f /путь/compose.document-search.yml exec embeddings ollama cp bge-m3 workspace-bge-m3-v1
   ```

   По умолчанию используется уже существующая сеть `workspace_workspace`,
   общая для API и worker в исследованном развёртывании. На другом стенде
   задать `DOCUMENT_EMBEDDING_NETWORK`. Порт модели не публикуется на хост.
   Для контейнера ограничены CPU и RAM; проверить свободные ресурсы сервера
   и время ответа на реальных документах. На CPU первая обработка медленнее.
4. В `/opt/workspace/.env` добавить одинаковые настройки API и worker:

   ```dotenv
   DOCUMENT_EMBEDDING_URL=http://workspace-embeddings:11434
   DOCUMENT_EMBEDDING_MODEL=workspace-bge-m3-v1
   DOCUMENT_EMBEDDING_DIMS=1024
   DOCUMENT_EMBEDDING_TIMEOUT=30s
   ```

   Не перезаписывать модель под уже использованным именем: для новых весов
   создать новый локальный tag и обновить настройку. Ключ кэша включает tag,
   размерность и SHA-256 точного текста, поэтому разные модели не смешиваются.
5. Выпустить новый backend штатным CI/deploy: обновить и API, и
   `ai-document-worker`. Учесть, что shared-compose использует также
   переменные из `.shared-infra.env`; одного `-f` без env-файлов недостаточно.
   Worker заполняет embeddings старых и новых готовых фрагментов партиями
   до 16, без повторной загрузки файлов. Блокноты кэшируют embeddings при
   первом чтении длинного источника; изменение текста создаёт новый ключ.

## Проверка

```sql
SELECT model, count(*) FILTER (WHERE embedding IS NOT NULL) AS indexed,
       count(*) FILTER (WHERE embedding IS NULL) AS pending
FROM ai_text_embeddings GROUP BY model;

SELECT count(*) AS chunks_without_embeddings
FROM ai_document_chunks c
JOIN ai_documents d ON d.id=c.document_id
WHERE d.status='ready' AND d.deleted_at IS NULL
AND NOT EXISTS (
  SELECT 1 FROM ai_text_embeddings e
  WHERE e.model='workspace-bge-m3-v1:1024'
  AND e.content_hash=encode(sha256(convert_to(c.text,'UTF8')),'hex')
  AND e.embedding IS NOT NULL
);
```

Сравнить набор реальных вопросов до и после включения: перефразировки,
точные коды, русские словоформы, ссылки на источник. Например, найти порядок
«расторжения трудового договора» вопросом «как уйти из компании». Повторить
с другим пользователем и с ограниченным выбором документов. Сам факт
успешного embedding не доказывает улучшение качества ответов.

При недоступности модели запросы используют лексические результаты, worker
делает паузу 5 минут для неудачных фрагментов. Общая попытка backfill ограничена
45 секундами; семантическая обработка всех источников одного запроса блокнота
тоже ограничена 45 секундами и 512 блоками на файл, затем остаётся обычный
выбор фрагментов. Статус `ready` означает успешное извлечение текста, а не
готовность embeddings; для последних используется запрос выше.

Для текущего объёма применяется точный cosine search после фильтрации,
приближённый индекс HNSW не нужен. При росте измерить latency и recall с ACL
перед добавлением ANN. Кэш не хранит текст; его строки остаются после удаления
документов и требуют отдельной политики retention при длительной эксплуатации.

Отключение: убрать URL из env и перевыпустить API/worker. Останется улучшенный
полнотекстовый поиск; документы и фрагменты удалять не нужно.

## Тесты

```bash
go test -p 1 ./internal/documentsearch ./internal/repository ./internal/service ./internal/config ./cmd/api
TEST_DOCUMENT_SEARCH_DATABASE_URL='postgres://.../disposable_db' \
  go test -p 1 ./internal/repository -run TestDocumentSearchScopeAndHybridIntegration -v
```

Для полного SQL-прогона используйте `./scripts/verify-file-retrieval.sh` с отдельной тестовой БД. Интеграционный тест создаёт временную схему и удаляет её. Без установленного
расширения проверяет только лексический поиск и изоляцию; для полного прогона
в тестовой базе заранее включить pgvector. HTTP-тесты используют фиктивную
локальную модель, не скачивают веса и не подтверждают качество реальных embeddings.

API модели: <https://docs.ollama.com/api/embed>.
Векторный поиск: <https://github.com/pgvector/pgvector>.
