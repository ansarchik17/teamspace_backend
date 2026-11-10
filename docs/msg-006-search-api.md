# MSG-006 — Поиск по переписке и вложениям

Документ описывает backend-контракт для глобального поиска по сообщениям и вложениям с учетом прав доступа.

## Общие правила

- Base URL: `/api/v1`.
- Все HTTP-запросы требуют `Authorization: Bearer <access_token>`.
- UUID передаются строкой.
- Поиск учитывает права доступа пользователя и момент вступления/последней очистки истории.
- Глобальный поиск включает личные чаты, группы и каналы, где пользователь является активным участником/подписчиком.
- Глобально удалённые и удалённые только для текущего пользователя сообщения не возвращаются.

---

## 1. Глобальный поиск по сообщениям

```http
GET /api/v1/search/messages/global?q={query}&limit={limit}&offset={offset}
```

Поиск по всем доступным пользователю чатам (личные, группы, каналы).

### Query Parameters

| Параметр | Тип | Обязательный | Описание |
|---|---|---|---|
| `q` | `string` | Да | Поисковый запрос (от 2 до 256 Unicode-символов) |
| `limit` | `int` | Нет | Количество результатов (по умолчанию 20, макс 100) |
| `offset` | `int` | Нет | Смещение для пагинации (по умолчанию 0, максимум 10000) |
| `chat_type` | `string` | Нет | Фильтр по типу чата: `personal`, `group`, `channel` |
| `chat_id` | `uuid` | Нет | Поиск в конкретном чате |
| `attachment_type` | `string` | Нет | Фильтр по типу вложения: `image`, `video`, `audio`, `file`, `voice` |
| `from_date` | `string` | Нет | Фильтр по дате "от" (ISO 8601: `2026-01-01T00:00:00Z`) |
| `to_date` | `string` | Нет | Фильтр по дате "до" (ISO 8601: `2026-12-31T23:59:59Z`) |
| `sender_id` | `int64` | Нет | Фильтр по отправителю |

### Ответ `200`

```json
{
  "query": "важный документ",
  "results": [
    {
      "id": "11111111-1111-1111-1111-111111111111",
      "conversation_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      "chat_type": "group",
      "chat_name": "Команда проекта",
      "sender_id": 42,
      "sender_username": "ivanov",
      "sender_full_name": "Иван Иванов",
      "sender_avatar": "https://...",
      "content": "Отправляю важный документ по проекту",
      "highlight": "Отправляю ⟦важный⟧ ⟦документ⟧ по проекту",
      "type": "file",
      "attachment_url": "https://.../document.pdf",
      "attachment_type": "file",
      "attachment_name": "requirements.pdf",
      "attachment_size": 1024000,
      "created_at": "2026-09-23T10:30:00Z",
      "relevance_score": 0.95
    }
  ],
  "total_count": 156,
  "has_more": true
}
```

### Поля результата

| Поле | Тип | Описание |
|---|---|---|
| `id` | `uuid` | ID сообщения или канального поста |
| `conversation_id` | `uuid` | ID чата |
| `chat_type` | `string` | Тип чата: `personal`, `group`, `channel` |
| `chat_name` | `string` | Название чата/группы/канала |
| `sender_*` | - | Информация об отправителе |
| `content` | `string` | Текст сообщения |
| `highlight` | `string` | Текст с подсветкой найденных слов (`<mark>...</mark>`) |
| `type` | `string` | Тип сообщения |
| `attachment_*` | - | Информация о вложении (если есть) |
| `created_at` | `timestamp` | Дата создания |
| `relevance_score` | `float` | Релевантность (0.0 - 1.0) |

`highlight` содержит текстовые маркеры `⟦` и `⟧`, а не HTML. Frontend должен разобрать эти маркеры и отрисовать подсветку безопасными UI-компонентами; не вставляйте значение через `innerHTML`.

---

## 2. Поиск вложений

```http
GET /api/v1/search/attachments?attachment_type={type}&limit={limit}&offset={offset}
```

Поиск всех вложений в доступных чатах с фильтрацией по типу.

### Query Parameters

| Параметр | Тип | Обязательный | Описание |
|---|---|---|---|
| `attachment_type` | `string` | Нет | Тип вложения: `image`, `video`, `audio`, `file`, `voice`, `all` (по умолчанию `all`) |
| `q` | `string` | Нет | Поиск по имени файла (максимум 256 Unicode-символов) |
| `limit` | `int` | Нет | Количество результатов (по умолчанию 20, макс 100) |
| `offset` | `int` | Нет | Смещение для пагинации (максимум 10000) |
| `chat_id` | `uuid` | Нет | Фильтр по конкретному чату |
| `from_date` | `string` | Нет | Фильтр по дате "от" |
| `to_date` | `string` | Нет | Фильтр по дате "до" |

### Ответ `200`

```json
{
  "attachments": [
    {
      "id": "11111111-1111-1111-1111-111111111111",
      "conversation_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      "chat_type": "group",
      "chat_name": "Команда проекта",
      "sender_id": 42,
      "sender_full_name": "Иван Иванов",
      "attachment_url": "https://.../photo.jpg",
      "attachment_type": "image",
      "attachment_name": "screenshot.jpg",
      "attachment_size": 2048000,
      "created_at": "2026-09-23T10:30:00Z"
    }
  ],
  "total_count": 47,
  "has_more": true
}
```

---

## 3. Статистика по вложениям в чате

```http
GET /api/v1/chats/{chat_id}/attachments/stats
```

Получить статистику по типам вложений в конкретном чате.

### Ответ `200`

```json
{
  "chat_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  "stats": {
    "image": 127,
    "video": 23,
    "file": 56,
    "audio": 12,
    "voice": 89
  },
  "total_files": 307,
  "total_size_bytes": 1567890123
}
```

---

## 4. Особенности реализации

### Полнотекстовый поиск

- Использует PostgreSQL Full-Text Search с `tsvector` и GIN индексом
- Поддержка русского языка (`russian` конфигурация)
- Подсветка найденных слов безопасными текстовыми маркерами `⟦...⟧` (`ts_headline`)
- Ранжирование результатов по релевантности (`ts_rank`)
- Индексируются текст сообщения и имя вложения; поиск по коротким/стоп-словам имеет безопасный substring fallback

### Права доступа

Поиск возвращает только сообщения из чатов, где пользователь:
- Является участником личного чата (`conversation_participants`)
- Является членом группы (`group_members`)
- Подписан на канал (`channel_members`, активная запись без `left_at`)

Дополнительно учитываются `joined_at`, `last_cleared_at`, глобальное удаление и удаление сообщения для конкретного пользователя. Фильтр `chat_id` не расширяет права: недоступный UUID возвращает пустой результат.

### Производительность

- Индексы: `idx_messages_search_vector` (GIN), `idx_messages_conversation_search`
- Лимиты: максимум 100 результатов за запрос
- Пагинация через `offset` для больших результатов
- Параметры SQL передаются только через placeholders; пользовательский ввод не конкатенируется в SQL

---

## 5. Ошибки

| HTTP | Код | Причина |
|---|---|---|
| 400 | `BAD_REQUEST` / `VALIDATION_ERROR` | Неверный UUID, диапазон дат, запрос длиннее 256 символов или offset больше 10000 |
| 401 | `UNAUTHORIZED` | Отсутствует или недействителен токен |
| 403 | `FORBIDDEN` | Запрос статистики для недоступного чата |

---

## 6. Примеры использования

### Поиск всех изображений в чатах

```http
GET /api/v1/search/attachments?attachment_type=image&limit=50
```

### Поиск сообщений в конкретной группе

```http
GET /api/v1/search/messages/global?q=встреча&chat_id=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
```

### Поиск файлов за последний месяц

```http
GET /api/v1/search/attachments?attachment_type=file&from_date=2026-08-23T00:00:00Z&to_date=2026-09-23T23:59:59Z
```

### Поиск по отправителю

```http
GET /api/v1/search/messages/global?q=отчет&sender_id=42
```
