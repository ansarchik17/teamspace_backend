# NWS-006: Важные оповещения — Закреплённые объявления и уведомления о важных публикациях

## 1. Обзор задачи

Реализация серверной части (Go / PostgreSQL / MinIO / Push & Email) для задачи **NWS-006: Важные оповещения** («Баннеры важных новостей и обязательные push/email рассылки»):

* **Закрепление публикаций (Pinning)**:
  - Возможность закрепить важную публикацию вверху ленты новостей (`is_pinned = true`).
  - Опциональный срок действия закрепления (`pinned_until`). Публикация автоматически возвращается в стандартную хронологическую сортировку после истечения срока без необходимости фоновых демонов благодаря динамическому выражению в SQL:
    `ORDER BY (is_pinned AND (pinned_until IS NULL OR pinned_until > NOW())) DESC, published_at DESC NULLS LAST, created_at DESC, id DESC`.
  - Информация о том, кто и когда закрепил публикацию (`pinned_at`, `pinned_by`).

* **Баннеры важных объявлений (Banners)**:
  - Режим отображения новости в виде сквозного баннера/уведомления поверх страниц системы (`is_banner = true`).
  - Настраиваемые параметры:
    - Срок показа баннера (`banner_until`).
    - Текст кнопки действия (`banner_action_text`), например «Ознакомиться» или «Принять участие».
    - Целевая ссылка перехода (`banner_action_url`).
    - Цветовая схема / уровень важности (`banner_color`: `urgent`, `warning`, `info`, `primary`).
  - Возможность скрытия/закрытия баннера пользователем (`POST /publications/:id/banner/dismiss`), если новость не требует обязательного подтверждения ознакомления.
  - Если для новости включен флаг обязательного ознакомления (`requires_acknowledgement = true`), баннер отображается до тех пор, пока пользователь явно не нажмет кнопку «Ознакомлен» (`POST /publications/:id/acknowledge`).

* **Обязательные рассылки и нотификации (Broadcasts)**:
  - Автоматическая рассылка при публикации важных/срочных новостей (`importance = "important"` или `"urgent"`).
  - Ручная инициация рассылки модератором/автором через `POST /publications/:id/broadcast`.
  - Push-уведомления: рассылка через существующий `NotificationService` (WebSocket + Push/FCM + сохранение в БД) с типом `publication_alert` и иконками/префиксами срочности (`🚨 ВАЖНО: ` / `📢 `).
  - Email-рассылки: при `importance = "urgent"` или явном флаге `send_email: true` через `EmailService` с адаптивным HTML-шаблоном письма и ссылкой прямо на публикацию.
  - Фильтрация целевой аудитории: `target_audience` (`all`, `ministry`) с автоматической выборкой реципиентов. `subordinate_organizations` и `selected_roles` пока не реализованы.
  - Автоматические отправки сохраняются в очереди PostgreSQL и повторяются после ошибок; успешные доставки по каждому каналу записываются отдельно. Параллельные запуски сериализуются; повторная отправка в течение 5 минут блокируется, если не передан флаг `force: true`.

* **Статистика и отчетность по рассылкам и ознакомлениям**:
  - `GET /publications/:id/broadcast/stats`: общая статистика по доставке push, email, количеству и проценту пользователей, подтвердивших ознакомление, а также детальная история запусков рассылок.
  - `GET /publications/:id/acknowledgements`: постраничный список сотрудников, подтвердивших ознакомление с датой и временем.

---

## 2. База данных и миграции

Миграция: `db/migrations/20260930001400_publications_pinned_banners_broadcasts.sql`

### Новые поля таблицы `publications`:
- `pinned_at TIMESTAMP WITH TIME ZONE NULL` — время закрепления публикации.
- `pinned_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL` — кто закрепил публикацию.
- `pinned_until TIMESTAMP WITH TIME ZONE NULL` — дата/время автоматического снятия закрепления.
- `is_banner BOOLEAN NOT NULL DEFAULT FALSE` — флаг показа в виде сквозного баннера.
- `banner_until TIMESTAMP WITH TIME ZONE NULL` — срок показа баннера.
- `banner_action_text VARCHAR(100) NULL` — текст кнопки действия (CTA).
- `banner_action_url VARCHAR(500) NULL` — HTTPS-ссылка или внутренний путь.
- `banner_color VARCHAR(50) NOT NULL DEFAULT 'urgent'` — цвет/тип баннера (`urgent`, `warning`, `info`, `primary`).
- `notification_sent_at TIMESTAMP WITH TIME ZONE NULL` — отметка последней отправки рассылки.

### Созданные таблицы:

1. `publication_banner_dismissals`:
   - `publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE`
   - `user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE`
   - `dismissed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP`
   - `PRIMARY KEY (publication_id, user_id)`

2. `publication_broadcasts`:
   - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
   - `publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE`
   - `triggered_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL`
   - `importance VARCHAR(32) NOT NULL DEFAULT 'normal'`
   - `target_audience VARCHAR(64) NOT NULL DEFAULT 'all'`
   - `sent_push_count INT NOT NULL DEFAULT 0`
   - `sent_email_count INT NOT NULL DEFAULT 0`
   - `sent_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP`
   - `status VARCHAR(32) NOT NULL DEFAULT 'completed'`
   - `error_message TEXT NULL`

### Созданные индексы:
- `idx_publications_pinned_until`: `(is_pinned, pinned_until)`
- `idx_publications_banner_until`: `(is_banner, banner_until)`
- `idx_publications_importance`: `(importance)`
- `idx_publication_broadcasts_pub_id`: `(publication_id, sent_at DESC)`

---

## 3. Константы и Модели Domain

Файл: `internal/domain/publication.go`

```go
const (
	PublicationImportanceNormal    = "normal"
	PublicationImportanceImportant = "important"
	PublicationImportanceUrgent    = "urgent"

	BannerColorUrgent  = "urgent"
	BannerColorWarning = "warning"
	BannerColorInfo    = "info"
	BannerColorPrimary = "primary"

	NotificationTypePublicationAlert = "publication_alert"
)
```

### Запросы и Ответы:
- `PinPublicationRequest`: `{"pinned_until": "2026-10-05T12:00:00Z"}`
- `SetBannerRequest`:
  ```json
  {
    "is_banner": true,
    "banner_until": "2026-10-05T12:00:00Z",
    "banner_action_text": "Ознакомиться с регламентом",
    "banner_action_url": "https://workspace.alem.kz/regulations",
    "banner_color": "urgent"
  }
  ```
- `BroadcastPublicationRequest`:
  ```json
  {
    "send_push": true,
    "send_email": true,
    "force": false
  }
  ```
- `PublicationBroadcastStatsResponse`:
  ```json
  {
    "publication_id": "9059f13e-f8ae-4f7f-8547-8cfb177ca0d7",
    "importance": "urgent",
    "total_target_users": 150,
    "acknowledged_count": 92,
    "acknowledgement_percent": 61.33,
    "sent_push_count": 148,
    "sent_email_count": 145,
    "last_broadcast_at": "2026-09-30T10:00:00Z",
    "broadcasts": [...]
  }
  ```

---

## 4. REST API Эндпоинты

Все эндпоинты требуют авторизации `Bearer <JWT>`:

| Метод | Путь | Описание | Доступ |
|---|---|---|---|
| `GET` | `/api/v1/publications/banners` | Список активных баннеров для текущего пользователя (с учетом скрытия и подтверждения) | Все пользователи |
| `POST` | `/api/v1/publications/:id/banner/dismiss` | Скрыть баннер в интерфейсе текущего пользователя | Все пользователи |
| `PUT` | `/api/v1/publications/:id/banner` | Настроить баннер (цвета, CTA, срок) | Автор / Модератор |
| `DELETE` | `/api/v1/publications/:id/banner` | Отключить показ баннера | Автор / Модератор |
| `PUT` | `/api/v1/publications/:id/pin` | Закрепить публикацию вверху ленты | Автор / Модератор |
| `DELETE` | `/api/v1/publications/:id/pin` | Открепить публикацию | Автор / Модератор |
| `POST` | `/api/v1/publications/:id/broadcast` | Отправить Push/Email рассылку по целевой аудитории | Автор / Модератор |
| `GET` | `/api/v1/publications/:id/broadcast/stats` | Получить аналитику доставки и статус подтверждений | Автор / Модератор |
| `GET` | `/api/v1/publications/:id/acknowledgements` | Список ознакомившихся сотрудников (`?page=1&limit=20`) | Автор / Модератор |

---

## 5. Тестирование

- **Domain тесты**: сериализация всех DTO, константы статусов и приоритетов.
- **Service тесты** (`internal/service/publication_nws006_test.go`):
  - `TestNWS006_Constants`
  - `TestNWS006_PinAndBannerJSONSerialization`
  - `TestNWS006_PublicationResponseFields`
  - `TestNWS006_EmailTemplateRendering`
- **Handler тесты** (`internal/handler/publication_nws006_test.go`):
  - `TestNWS006_HandlerValidation`: проверка валидации UUID и JSON payload для всех 9 новых эндпоинтов.
