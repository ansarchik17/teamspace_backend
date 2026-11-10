# NWS-005: Вовлеченность — Реакции, комментарии и управление обсуждением

## 1. Обзор задачи

Реализация серверной части (Go / PostgreSQL / MinIO) для задачи **NWS-005**:
* **Эмодзи-реакции на публикации**:
  - Пользователи могут ставить любые эмодзи-реакции (👍, ❤️, 🔥, 👏, 🎉 и др.) на опубликованные новости.
  - Повторный клик или запрос на снятие реакции удаляет её.
  - Сохранена обратная совместимость: установка/снятие реакций `"like"` или `"👍"` автоматически синхронизирует легаси счетчики `likes_count` и флаг `is_liked`.
  - Предоставление детального списка пользователей для каждой реакции с превью аватарок.
* **Ветки комментариев (Comment Threads)**:
  - Поддержка иерархических ответов: указание `parent_id` (ID родительского комментария) и `reply_to_user_id` (кому адресован ответ).
  - Вложенность ответов возвращается в виде дерева (`replies []CommentResponse` и `replies_count`).
  - Закрепление важных комментариев (`is_pinned`, `pinned_at`, `pinned_by`) автором новости или модератором. Закрепленные комментарии выводятся первыми в ленте.
  - Эмодзи-реакции на отдельные комментарии (`/comments/:comment_id/reactions`).
* **Модерация обсуждения**:
  - Управление комментариями: скрытие (`hide`), восстановление (`restore`) и удаление (`delete`) модератором.
  - Статусы комментария: `active`, `hidden`, `deleted_by_moderator`.
  - Маскирование контента: для обычных пользователей скрытые комментарии отображаются как `[Комментарий скрыт модератором]`, а причина скрытия (`moderation_reason`) не раскрывается.
  - Для автора публикации и модераторов (роли `superadmin`, `manager`) оригинальный текст комментария и причина модерации остаются доступными для аудита.
  - Блокировка/закрытие ветки обсуждения: возможность закрыть или открыть комментирование новости (`PUT /comments/lock`) с сохранением причины. При закрытом обсуждении добавление новых комментариев блокируется (доступно только модераторам).

---

## 2. База данных и миграции

Миграция: `db/migrations/20260929001300_publications_reactions_threads_moderation.sql`

### Созданные таблицы:
1. `publication_reactions`:
   - `publication_id UUID REFERENCES publications(id) ON DELETE CASCADE`
   - `user_id BIGINT REFERENCES users(id) ON DELETE CASCADE`
   - `reaction VARCHAR(32) NOT NULL`
   - `created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP`
   - `PRIMARY KEY (publication_id, user_id, reaction)`
   - Индексы: `idx_publication_reactions_pub_id`, `idx_publication_reactions_pub_rx`, `idx_publication_reactions_user`.

2. `publication_comment_reactions`:
   - `comment_id UUID REFERENCES publication_comments(id) ON DELETE CASCADE`
   - `user_id BIGINT REFERENCES users(id) ON DELETE CASCADE`
   - `reaction VARCHAR(32) NOT NULL`
   - `created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP`
   - `PRIMARY KEY (comment_id, user_id, reaction)`
   - Индексы: `idx_comment_reactions_comment_id`, `idx_comment_reactions_comment_rx`, `idx_comment_reactions_user`.

### Модификация таблицы `publication_comments`:
- `parent_id UUID NULL REFERENCES publication_comments(id) ON DELETE CASCADE` — ссылка на родительский комментарий ветки.
- `reply_to_user_id BIGINT NULL REFERENCES users(id) ON DELETE SET NULL` — пользователь, которому адресован ответ.
- `is_pinned BOOLEAN NOT NULL DEFAULT FALSE` — флаг закрепления комментария вверху списка.
- `pinned_at TIMESTAMP WITH TIME ZONE NULL` — время закрепления.
- `pinned_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL` — идентификатор пользователя, закрепившего комментарий.
- `status VARCHAR(50) NOT NULL DEFAULT 'active'` (`active`, `hidden`, `deleted_by_moderator`).
- `is_hidden BOOLEAN NOT NULL DEFAULT FALSE` — флаг скрытия комментария модератором.
- `moderation_reason TEXT NULL` — причина модерации.
- `moderated_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL` — ID модератора.
- `moderated_at TIMESTAMP WITH TIME ZONE NULL` — время модерации.
- Индексы:
  - `idx_publication_comments_parent_id`
  - `idx_publication_comments_pinned`
  - `idx_publication_comments_status`

---

## 3. Модели предметной области (Domain)

Файл: `internal/domain/publication.go`

### Статусы и действия модерации:
```go
const (
	CommentStatusActive             = "active"
	CommentStatusHidden             = "hidden"
	CommentStatusDeletedByModerator = "deleted_by_moderator"

	ModerationActionHide    = "hide"
	ModerationActionRestore = "restore"
	ModerationActionDelete  = "delete"
)
```

### Структуры данных:
- `ReactionSummary`:
  ```go
  type ReactionSummary struct {
      Reaction string         `json:"reaction"`
      Count    int            `json:"count"`
      Reacted  bool           `json:"reacted"`
      Users    []ReactionUser `json:"users,omitempty"`
  }
  ```
- `CreateCommentRequest`:
  ```go
  type CreateCommentRequest struct {
      Content       string     `json:"content" binding:"required"`
      ParentID      *uuid.UUID `json:"parent_id,omitempty"`
      ReplyToUserID *int64     `json:"reply_to_user_id,omitempty"`
  }
  ```
- `ModerateCommentRequest`:
  ```go
  type ModerateCommentRequest struct {
      Action string  `json:"action" binding:"required,oneof=hide restore delete"`
      Reason *string `json:"reason,omitempty"`
  }
  ```
- `LockCommentsRequest`:
  ```go
  type LockCommentsRequest struct {
      AllowComments bool    `json:"allow_comments"`
      Reason        *string `json:"reason,omitempty"`
  }
  ```
- `SetReactionRequest`:
  ```go
  type SetReactionRequest struct {
      Reaction string `json:"reaction" binding:"required,max=32"`
  }
  ```
- `CommentResponse`: расширен полями `ParentID`, `ReplyToUser`, `IsPinned`, `PinnedAt`, `PinnedBy`, `IsHidden`, `Status`, `ModerationReason`, `RepliesCount`, `Replies`, `Reactions`.
- `PublicationResponse`: расширен полями `Reactions []ReactionSummary` и `UserReactions []string`.

---

## 4. REST API Эндпоинты

Все эндпоинты защищены JWT-авторизацией и работают в контексте `/api/v1/publications`:

### Эмодзи-реакции на новость
| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/v1/publications/:id/reactions` | Поставить реакцию на публикацию (тело: `{"reaction": "🔥"}`) |
| `DELETE` | `/api/v1/publications/:id/reactions/:reaction` | Снять реакцию с публикации |
| `GET` | `/api/v1/publications/:id/reactions` | Получить список всех реакций с количеством и пользователями |

### Комментарии и ветки
| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/v1/publications/:id/comments` | Создать комментарий (корневой или ответ в ветку с `parent_id`) |
| `GET` | `/api/v1/publications/:id/comments` | Получить дерево комментариев (с вложенными ответами и реакциями) |
| `GET` | `/api/v1/publications/:id/comments/:comment_id/replies` | Получить плоский список ответов на конкретный комментарий |
| `DELETE` | `/api/v1/publications/:id/comments/:comment_id` | Удалить свой комментарий (или модератором) |

### Реакции на комментарии
| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/v1/publications/:id/comments/:comment_id/reactions` | Поставить реакцию на комментарий |
| `DELETE` | `/api/v1/publications/:id/comments/:comment_id/reactions/:reaction` | Удалить реакцию с комментария |

### Модерация и управление обсуждением
| Метод | Путь | Описание |
|---|---|---|
| `PUT` | `/api/v1/publications/:id/comments/:comment_id/pin` | Закрепить комментарий вверху обсуждения |
| `DELETE` | `/api/v1/publications/:id/comments/:comment_id/pin` | Открепить комментарий |
| `PUT` | `/api/v1/publications/:id/comments/:comment_id/moderate` | Модерировать комментарий (`hide`, `restore`, `delete`) с причиной |
| `PUT` | `/api/v1/publications/:id/comments/lock` | Заблокировать или открыть комментирование публикации |

---

## 5. Примеры запросов и ответов

### 1. Добавление эмодзи-реакции на новость:
```http
POST /api/v1/publications/550e8400-e29b-41d4-a716-446655440000/reactions
Content-Type: application/json
Authorization: Bearer <token>

{
  "reaction": "🔥"
}
```
**Ответ:**
```json
{
  "status": "reaction_added",
  "reactions": [
    {
      "reaction": "🔥",
      "count": 1,
      "reacted": true,
      "users": [
        {
          "id": 42,
          "first_name": "Мерей",
          "last_name": "Смагулов",
          "full_name": "Мерей Смагулов"
        }
      ]
    }
  ]
}
```

### 2. Ответ в ветку комментариев:
```http
POST /api/v1/publications/550e8400-e29b-41d4-a716-446655440000/comments
Content-Type: application/json
Authorization: Bearer <token>

{
  "content": "Полностью согласен с озвученными тезисами!",
  "parent_id": "a3b83ef3-401f-4b09-ba60-038c353dcfb0",
  "reply_to_user_id": 10
}
```

### 3. Модерация комментария (Скрытие с причиной):
```http
PUT /api/v1/publications/550e8400-e29b-41d4-a716-446655440000/comments/a3b83ef3-401f-4b09-ba60-038c353dcfb0/moderate
Content-Type: application/json
Authorization: Bearer <token>

{
  "action": "hide",
  "reason": "Нарушение этики общения и оффтоп"
}
```

### Ограничения реакций и модерации

Допустимые реакции: 👍, 👎, ❤️, ❤, 🔥, 🎉, 😂, 😮, 😢, 👏, 🙏, 😍, 🤔, 💯.
`like` — совместимый псевдоним 👍. Оба API лайков используют одну транзакцию.
`users` содержит максимум 20 пользователей на реакцию; `count` и `reacted`
считаются по всем записям. Причина модерации ограничена 1000 символами.
Максимальная глубина новых веток — 64 уровня. Удаление модератором атомарно
сохраняет статус, причину, модератора и время удаления.

### 4. Закрытие обсуждения новости автором:
```http
PUT /api/v1/publications/550e8400-e29b-41d4-a716-446655440000/comments/lock
Content-Type: application/json
Authorization: Bearer <token>

{
  "allow_comments": false,
  "reason": "Тема исчерпана, обсуждение закрыто автором"
}
```
