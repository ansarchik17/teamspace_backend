# NWS-001: Публикация — Создание, редактирование и публикация новостей

## 1. Обзор задачи

### Ограничения и проверки безопасности

- Чужие черновики, архивные и будущие публикации недоступны по ID; та же проверка применяется к комментариям, реакциям, подтверждению и распространению. Автор и SuperAdmin могут просматривать черновики.
- HTML очищается по разрешённому набору тегов при сохранении и чтении. Обработчики событий, скрипты, встроенные объекты и опасные схемы ссылок не возвращаются клиенту.
- JSON создания/редактирования ограничен 4 МиБ. Запрос публикации ограничен 16 КиБ; ошибки JSON или даты отклоняются, включая тело неизвестной длины.
- Multipart-обложка ограничена 10 МиБ, права проверяются до разбора и загрузки. Формат определяется по данным изображения (JPEG/PNG/GIF/WebP), размеры ограничены 40 миллионами пикселей.
- Для подписываемой обложки допустимы только ключи `image/<author_id>/...`. Произвольные приватные ключи других пользователей не подписываются, в том числе из ранее сохранённых URL.
- Фоновая задача каждые 15 секунд переводит наступившие `scheduled` новости в `published`. Лента показывает наступившие новости и до выполнения задачи. Ручная публикация устанавливает актуальную дату.
- Для работы необходима миграция `20260929001000_publications_news_cover_status_rich.sql` до запуска новой версии API.

Реализация серверной части (Go / PostgreSQL / MinIO) для задачи **NWS-001**:
* **Создание и редактирование новостей/публикаций** (поддержка черновиков, запланированной публикации и мгновенного выпуска).
* **Визуальный редактор (Rich Content)**: хранение структурированного документа (`content_doc` JSONB) и/или форматированного HTML (`content_html` TEXT), а также автоматическая синхронизация текстового превью (`content` TEXT) для списков, карточек и мобильных уведомлений.
* **Обложка публикации (Cover Image)**: хранение `cover_image_url`, поддержка прямой загрузки (через multipart form-data) или передачи ссылки, а также удаление обложки.
* **Жизненный цикл и статусы**:
  - `draft` — черновик (виден только автору и администраторам).
  - `published` — опубликовано (доступно в общей ленте новостей).
  - `scheduled` — запланировано на будущее время (`scheduled_at`).
  - `archived` — архивная публикация.

---

## 2. База данных и миграции

Миграция: `db/migrations/20260929001000_publications_news_cover_status_rich.sql`

Добавлены поля в таблицу `publications`:
- `cover_image_url TEXT NULL` — ссылка на изображение обложки (поддерживает MinIO / S3 и внешние URL с автоматической подписью ссылок).
- `status VARCHAR(50) NOT NULL DEFAULT 'published'` — статус жизненного цикла публикации с проверкой `CHECK (status IN ('draft', 'published', 'scheduled', 'archived'))`.
- `content_doc JSONB NOT NULL DEFAULT '{}'::jsonb` — структурированный документ визуального редактора (блочная модель).
- `content_html TEXT NOT NULL DEFAULT ''` — HTML-разметка, сформированная визуальным редактором.
- Индекс: `idx_publications_status_published_at` по `(status, published_at DESC) WHERE deleted_at IS NULL` для быстрой выборки ленты опубликованных новостей.

---

## 3. Модели предметной области (Domain)

Файл: `internal/domain/publication.go`

### Статусы:
```go
const (
	PublicationStatusDraft     = "draft"
	PublicationStatusPublished = "published"
	PublicationStatusScheduled = "scheduled"
	PublicationStatusArchived  = "archived"
)
```

### Основные структуры:
- `domain.Publication`: содержит поля `CoverImageURL`, `Status`, `ContentDoc`, `ContentHTML`.
- `domain.CreatePublicationRequest`:
  - `CoverImageURL *string` (необязательно)
  - `Status string` (необязательно, по умолчанию `'published'` для полной обратной совместимости)
  - `ContentDoc *ContentDoc` (структурированный документ)
  - `ContentHTML string` (HTML-разметка)
  - `ScheduledAt *time.Time` (для отложенной публикации)
- `domain.UpdatePublicationRequest`: аналогично поддерживает обновление обложки, статуса, `content_doc` и `content_html`.
- `domain.PublicationResponse`: возвращает клиенту все новые поля, причём `cover_image_url` автоматически подписывается presigned URL (если это MinIO/хранилище).

---

## 4. API Эндпоинты

Все эндпоинты зарегистрированы в `/api/v1/publications`:

| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/v1/publications` | Создание новости (черновик, опубликованная или запланированная) |
| `GET` | `/api/v1/publications` | Получение списка новостей с фильтрацией (в т.ч. `?status=draft`, `?status=all`) |
| `GET` | `/api/v1/publications/:id` | Просмотр детальной информации новости |
| `PUT` | `/api/v1/publications/:id` | Редактирование новости |
| `DELETE` | `/api/v1/publications/:id` | Удаление новости (мягкое удаление) |
| `POST` | `/api/v1/publications/:id/publish` | Публикация новости (перевод из черновика в статус `published` или `scheduled`) |
| `POST` | `/api/v1/publications/:id/unpublish` | Снятие с публикации (перевод в статус `draft`) |
| `POST` | `/api/v1/publications/:id/cover` | Установка/загрузка обложки (JSON `{"cover_image_url": "..."}` или multipart file) |
| `DELETE` | `/api/v1/publications/:id/cover` | Удаление обложки новости |

### Примеры запросов:

#### 1. Создание черновика с визуальным редактором и обложкой:
```http
POST /api/v1/publications
Content-Type: application/json
Authorization: Bearer <token>

{
  "source_type": "user",
  "type": "news",
  "title": "Новое открытие филиала",
  "status": "draft",
  "cover_image_url": "https://storage.workspace.kz/publications/covers/hero-1.jpg",
  "content_html": "<p>Мы рады сообщить об открытии <b>нового филиала</b>.</p>",
  "content_doc": {
    "type": "doc",
    "version": 1,
    "blocks": [
      {
        "id": "b-1",
        "type": "paragraph",
        "runs": [
          {"text": "Мы рады сообщить об открытии "},
          {"text": "нового филиала", "marks": ["bold"]},
          {"text": "."}
        ]
      }
    ]
  },
  "target_audience": "all",
  "importance": "normal"
}
```

#### 2. Публикация черновика:
```http
POST /api/v1/publications/550e8400-e29b-41d4-a716-446655440000/publish
Authorization: Bearer <token>
```
*Для отложенной публикации можно передать тело: `{"scheduled_at": "2026-10-01T10:00:00Z"}`*.

#### 3. Снятие с публикации в черновик:
```http
POST /api/v1/publications/550e8400-e29b-41d4-a716-446655440000/unpublish
Authorization: Bearer <token>
```

#### 4. Установка или загрузка обложки:
```http
POST /api/v1/publications/550e8400-e29b-41d4-a716-446655440000/cover
Content-Type: application/json
Authorization: Bearer <token>

{
  "cover_image_url": "https://storage.workspace.kz/images/cover.png"
}
```
*Также поддерживается загрузка через `multipart/form-data` с полем `cover` или `file`.*

#### 5. Получение черновиков автора:
```http
GET /api/v1/publications?status=draft
Authorization: Bearer <token>
```

---

## 5. Безопасность и разграничение прав
- Публиковать, снимать с публикации, менять обложку и удалять новость может только **автор публикации** или **SuperAdmin**.
- Запрос черновиков (`?status=draft`) возвращает только черновики текущего пользователя.
- Запрос общей ленты по умолчанию возвращает только опубликованные материалы (`status = 'published'` и `published_at <= NOW()`).
