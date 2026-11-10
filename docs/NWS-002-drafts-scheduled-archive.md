# NWS-002: Черновики, отложенная публикация и архив

## 1. Обзор задачи

Реализация серверной части (Go / PostgreSQL) для задачи **NWS-002**:
* **Черновики (Drafts)**:
  - Создание публикаций в статусе `draft` для предварительного редактирования без видимости в общей ленте.
  - Редактирование, просмотр и мягкое удаление черновиков.
  - Мгновенная публикация черновика (`POST /api/v1/publications/:id/publish`).
  - Снятие с публикации обратно в черновик (`POST /api/v1/publications/:id/unpublish`).
  - Фильтрация в ленте: `GET /api/v1/publications?status=draft` возвращает только собственные черновики автора (или все черновики для роли `SuperAdmin`).
* **Отложенная публикация (Scheduled Publishing)**:
  - Назначение будущего времени выхода через поле `scheduled_at`.
  - Фоновый воркер (`RunScheduledPublications`) каждые 15 секунд автоматически переводит наступившие публикации (`scheduled_at <= NOW()`) в статус `published`.
  - Автоматическая рассылка широковещательных уведомлений (`BroadcastPublication`) для важных/срочных новостей (`urgent` / `important`) сразу после наступления публикации по таймеру.
  - Публикации с будущим `scheduled_at` не отображаются в публичной ленте до наступления времени.
* **Архив и автоматический перевод в архив (Archive & Expiration)**:
  - Ручной перевод в архив (`POST /api/v1/publications/:id/archive`) и восстановление из архива (`POST /api/v1/publications/:id/unarchive`).
  - Назначение срока действия публикации через поле `archive_at` (дата автоматической архивации).
  - Фоновый воркер каждые 15 секунд автоматически переводит устаревшие публикации (`archive_at <= NOW()`) в статус `archived` с установкой `archived_at = NOW()`.
  - Автоматическое снятие закрепления (`is_pinned = false`) и отключение баннера (`is_banner = false`) при архивации.
  - Массовая архивация устаревших постов по возрасту или дате отсечки (`POST /api/v1/publications/archive/obsolete`) для администраторов.
  - Просмотр архива: `GET /api/v1/publications?status=archived` (доступен автору и SuperAdmin).

---

## 2. База данных и миграции

Миграция: `db/migrations/20261001000100_publications_drafts_scheduled_archive.sql`

### Изменения схемы:
```sql
ALTER TABLE publications
    ADD COLUMN IF NOT EXISTS archive_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_publications_archive_at
    ON publications (archive_at)
    WHERE status = 'published' AND deleted_at IS NULL AND archive_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_publications_status_archived
    ON publications (status, archived_at DESC)
    WHERE status = 'archived' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_publications_status_author
    ON publications (author_id, status, updated_at DESC)
    WHERE deleted_at IS NULL;
```

---

## 3. Фоновый планировщик (Scheduler Worker)

Файл: `internal/service/publication_schedule.go`
Фоновый процесс запускается при старте API-сервера в `cmd/api/main.go` с интервалом 15 секунд.

### Логика работы:
1. **Публикация по таймеру (`PublishDueWithIDs`)**:
   - Находит все новости со статусом `scheduled` и `scheduled_at <= NOW()`.
   - В транзакции переводит статус в `published` и фиксирует фактическое время `published_at = NOW()`.
   - Ставит в очередь широковещательные уведомления (`PendingBroadcasts`) для новостей с важностью `urgent` или `important`.
2. **Автоматическая архивация по таймеру (`ArchiveDueWithIDs`)**:
   - Находит опубликованные новости со статусом `published` и `archive_at <= NOW()`.
   - Переводит статус в `archived`, устанавливает `archived_at = NOW()`, сбрасывает `is_pinned = false` и `is_banner = false`.
3. **Реальное время (Real-time Filtering)**:
   - В эндпоинтах выборки (`List`, `ListBanners`) и проверке прав (`publicationVisible`) встроено условие `(archive_at IS NULL OR archive_at > NOW())`.
   - Даже если фоновый воркер ещё не успел сработать (в рамках 15-секундного тика), публикация с истекшим `archive_at` немедленно скрывается из публичной ленты.

---

## 4. Контроль доступа и видимость (Security & Permissions)

| Статус | Обычный пользователь | Автор новости | SuperAdmin |
|---|---|---|---|
| `published` (активная) | ✅ Видит | ✅ Видит | ✅ Видит |
| `draft` | ❌ Не видит | ✅ Видит | ✅ Видит |
| `scheduled` (до наступления срока) | ❌ Не видит | ✅ Видит | ✅ Видит |
| `scheduled` (после наступления срока) | ✅ Видит | ✅ Видит | ✅ Видит |
| `archived` | ❌ Не видит | ✅ Видит | ✅ Видит |
| `published` (с `archive_at <= NOW()`) | ❌ Не видит (считается устаревшей) | ✅ Видит | ✅ Видит |

---

## 5. API Контракты

### 5.1. Публикация новости (немедленно или с отложенной публикацией/архивацией)
`POST /api/v1/publications/:id/publish`

**Тело запроса (JSON, опционально):**
```json
{
  "scheduled_at": "2026-10-15T09:00:00Z",
  "archive_at": "2026-11-15T09:00:00Z"
}
```

* Если `scheduled_at` не указан или находится в прошлом: новость публикуется немедленно (`status = 'published'`, `published_at = NOW()`).
* Если `scheduled_at` в будущем: новость переходит в статус `scheduled` до наступления времени.
* Поле `archive_at` задает срок жизни публикации: по истечении времени она автоматически переходит в архив.

---

### 5.2. Снятие с публикации обратно в черновик
`POST /api/v1/publications/:id/unpublish`

Переводит новость из любого статуса (`published`, `scheduled`, `archived`) обратно в `draft`.

---

### 5.3. Ручной перевод новости в архив
`POST /api/v1/publications/:id/archive`

Переводит новость в статус `archived`, проставляет `archived_at = NOW()`, сбрасывает закрепление и баннер.

**Ответ (200 OK):**
```json
{
  "id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "title": "Уведомление об архивации",
  "status": "archived",
  "archived_at": "2026-10-01T11:45:00Z"
}
```

---

### 5.4. Восстановление новости из архива
`POST /api/v1/publications/:id/unarchive`

**Тело запроса (JSON, опционально):**
```json
{
  "target_status": "draft"
}
```
* `target_status`: `"draft"` (по умолчанию) или `"published"`.

---

### 5.5. Массовая архивация устаревших постов (SuperAdmin)
`POST /api/v1/publications/archive/obsolete`

Позволяет администратору в один запрос перевести в архив все устаревшие публикации, например старше 90 дней.

**Тело запроса:**
```json
{
  "older_than_days": 90,
  "types": ["news", "announcement"]
}
```
*или с точной датой отсечки:*
```json
{
  "before_date": "2026-06-01T00:00:00Z"
}
```

**Ответ (200 OK):**
```json
{
  "archived_count": 42,
  "cutoff_date": "2026-07-03T11:45:00Z"
}
```

---

### 5.6. Фильтрация списка публикаций по статусу
`GET /api/v1/publications?status={status}&page=1&limit=20`

* `?status=published` (по умолчанию) — только актуальные опубликованные новости (`published_at <= NOW() AND archive_at > NOW()`).
* `?status=draft` — черновики автора (или все для SuperAdmin), отсортированные по `updated_at DESC`.
* `?status=scheduled` — запланированные новости автора (или все для SuperAdmin), отсортированные по `scheduled_at ASC`.
* `?status=archived` — архивные новости автора (или все для SuperAdmin), отсортированные по `COALESCE(archived_at, updated_at) DESC`.
* `?status=all` — все опубликованные новости плюс собственные публикации автора во всех статусах.

---

## 6. Тестирование

- Модульные тесты видимости и сериализации: `internal/service/publication_nws002_test.go`
  - `TestNWS002_PublicationVisibilityWithArchive`
  - `TestNWS002_DraftsAndArchiveJSONSerialization`
- Модульные тесты маршрутов и валидации: `internal/handler/publication_nws002_test.go`
  - `TestNWS002_HandlerRoutesAndValidation`
- Запуск всех тестов:
  ```bash
  go test -v ./internal/domain/... ./internal/service/... ./internal/handler/... ./internal/repository/...
  ```
