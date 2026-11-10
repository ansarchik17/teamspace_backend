# NTS-002 — Навигация: Папки, теги, избранное и полнотекстовый поиск

Реализация расширяет функционал заметок на backend (Go) для эффективной навигации, группировки, тегирования, закрепления в избранное и полнотекстового поиска. Изменения выполнены строго в `workspace-backend`.

## 1. Возможности API

### Папки (Folders)
- `GET /api/v1/notes/folders` — список папок пользователя, теперь возвращает `note_count` (количество активных неудалённых заметок в папке).
- `POST /api/v1/notes/folders` — создание новой папки (`name`, `sort_order`, `color`).
- `PATCH /api/v1/notes/folders/{id}` — редактирование папки.
- `DELETE /api/v1/notes/folders/{id}` — удаление папки (заметки открепляются: `folder_id = NULL`).
- `POST /api/v1/notes/{id}/move` — перемещение заметки в другую папку с проверкой версии.
- Фильтрация по папке в `GET /api/v1/notes?folderId={uuid}` или `?folder_id={uuid}`. Для выборки заметок без папки поддерживается `?folderId=none` или `?folderId=root`.

### Теги (Tags)
- Теги хранятся в PostgreSQL как массив `TEXT[]`; фильтр использует `@>` и может применять GIN-индекс `idx_notes_tags`.
- Нормализация: теги автоматически приводятся к нижнему регистру, очищаются от префикса `#` и пробелов, дедуплицируются, ограничиваются длиной до 50 символов (до 50 тегов на заметку).
- Длина считается в Unicode-символах, без повреждения UTF-8. Такая же нормализация применяется к фильтру `tag`: `#GoLang` и `golang` эквивалентны.
- `GET /api/v1/notes/tags` — список всех уникальных тегов пользователя с количеством активных заметок (`[]domain.NoteTagSummary{tag, count}`), отсортированный по частоте использования (count DESC, tag ASC). Удалённые заметки исключаются.
- `GET /api/v1/notes?tag={name}` — быстрая фильтрация заметок по тегу.
- Теги можно указывать при создании (`POST /api/v1/notes`) и обновлении (`PATCH /api/v1/notes/{id}`), а также передавать в multipart-запросах (`tags` как JSON-массив или строка через запятую).

### Избранное (Favorites)
- Поле `is_favorite` (`BOOLEAN NOT NULL DEFAULT FALSE`) в таблице `notes` с частичным B-Tree индексом `idx_notes_user_favorite_updated WHERE is_favorite = TRUE`.
- `PATCH /api/v1/notes/{id}/favorite` — установка/снятие статуса избранного (`{"is_favorite": true, "version": 1}`).
- `POST /api/v1/notes/{id}/favorite` — закрепление в избранное (`{"version": 1}`).
- `POST /api/v1/notes/{id}/unfavorite` — снятие из избранного (`{"version": 1}`).
- `GET /api/v1/notes?isFavorite=true` или `?is_favorite=true` — получение только избранных заметок.
- Поле `is_favorite` также доступно при общем создании (`POST /api/v1/notes`) и частичном обновлении (`PATCH /api/v1/notes/{id}`).

### Полнотекстовый поиск (Full-Text Search)
- `search_vector` типа `tsvector` теперь включает заголовок (`title`), текст содержимого (`content_text`), legacy-контент (`content`) и все теги заметки (`immutable_array_to_string(tags, ' ')`).
- Индексируется GIN-индексом `idx_notes_search_vector_v3`.
- `GET /api/v1/notes/search?q={query}` — выделенный эндпоинт поиска, поддерживающий совместную фильтрацию по папке, тегу, избранному и пагинацию курсором.
- `GET /api/v1/notes?q={query}` — полнотекстовый и ILIKE-поиск в общем списке заметок.

---

## 2. Примеры запросов и ответов

### Создание заметки с тегами и добавлением в избранное
```http
POST /api/v1/notes HTTP/1.1
Content-Type: application/json

{
  "title": "Архитектура модуля навигации",
  "type": "text",
  "content": "Детали реализации полнотекстового поиска и тегирования",
  "folder_id": "4b6bb398-38dc-4f81-9b6f-87ca37ec2e5c",
  "is_favorite": true,
  "tags": ["#Golang", "Backend", "NTS-002"]
}
```

Ответ `201 Created`:
```json
{
  "id": "78a876a8-2da7-4e5a-939e-4e6f477322bf",
  "user_id": 1,
  "folder_id": "4b6bb398-38dc-4f81-9b6f-87ca37ec2e5c",
  "title": "Архитектура модуля навигации",
  "content": "Детали реализации полнотекстового поиска и тегирования",
  "type": "text",
  "is_pinned": false,
  "is_archived": false,
  "is_favorite": true,
  "tags": ["golang", "backend", "nts-002"],
  "is_deleted": false,
  "version": 1,
  "created_at": "2026-09-28T15:40:00Z",
  "updated_at": "2026-09-28T15:40:00Z"
}
```

### Получение списка тегов пользователя
```http
GET /api/v1/notes/tags HTTP/1.1
```

Ответ `200 OK`:
```json
[
  {"tag": "backend", "count": 12},
  {"tag": "golang", "count": 8},
  {"tag": "nts-002", "count": 3},
  {"tag": "architecture", "count": 2}
]
```

### Переключение статуса «Избранное»
```http
PATCH /api/v1/notes/78a876a8-2da7-4e5a-939e-4e6f477322bf/favorite HTTP/1.1
Content-Type: application/json

{
  "is_favorite": false,
  "version": 1
}
```

Ответ `200 OK` (версия инкрементируется до 2):
```json
{
  "id": "78a876a8-2da7-4e5a-939e-4e6f477322bf",
  "is_favorite": false,
  "version": 2
}
```

### Полнотекстовый поиск
```http
GET /api/v1/notes/search?q=архитектура&tag=golang&is_favorite=true HTTP/1.1
```

---

## 3. База данных и миграции

Миграция: `db/migrations/20260929000700_notes_navigation_tags_favorites.sql`:
- Создаёт детерминированную IMMUTABLE-функцию `immutable_array_to_string(text[], text)` для генерации вектора в PostgreSQL STORED column.
- Добавляет `tags TEXT[] NOT NULL DEFAULT '{}'` и `is_favorite BOOLEAN NOT NULL DEFAULT FALSE` в таблицу `notes`.
- Добавляет GIN-индекс `idx_notes_tags` на `tags`.
- Добавляет частичный B-Tree индекс `idx_notes_user_favorite_updated` на `(user_id, is_favorite, updated_at DESC, id DESC) WHERE is_favorite = TRUE`.
- Обновляет `search_vector tsvector GENERATED ALWAYS AS (...) STORED`, добавляя теги в полнотекстовый индекс.
- Добавляет GIN-индекс `idx_notes_search_vector_v3`.

---

## 4. Тестирование

Batch-sync: отсутствующее или `null` поле `is_favorite` сохраняет существующее избранное; явное `false` снимает его. При создании без поля используется `false`. Кэш списка использует однозначную JSON-сериализацию параметров, включая теги и запросы с `|`.

Миграция NTS-002 проверяется настоящим Goose: Up, Down и повторный Up на изолированном PostgreSQL. Down удаляет `search_vector` перед `tags`, поскольку вектор зависит от тегов. Реальные хэндлеры проверяются на некорректные UUID, флаги фильтра и отсутствующую версию.

- **Unit-тесты** (`internal/handler/note_nts002_test.go`):
  - Нормализация тегов (очистка `#`, пробелов, дедупликация, обрезка до 50 символов, лимит 50 тегов).
  - JSON-биндинг `tags`, `is_favorite` в `CreateNoteRequest`, `UpdateNoteRequest`, `FavoriteNoteRequest`.
  - JSON-структуры `NoteTagSummary` и `NoteFolder.NoteCount`.
- **Интеграционные тесты** (`internal/service/note_nts002_integration_test.go`):
  - Создание папок и автоматический подсчёт `note_count`.
  - Создание заметок с тегами и флагом избранного.
  - Агрегация и статистика тегов в `GetUserTags`.
  - Фильтрация по тегу, папке, отсутствию папки (`root`), избранному.
  - Полнотекстовый поиск по тексту, заголовкам и тегам.
  - Оптимистическая блокировка при изменении избранного и тегов.
  - Исключение тегов и пересчёт счётчиков при удалении заметок в корзину.
