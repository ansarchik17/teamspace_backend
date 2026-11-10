# MSG-003 — API интерактивности сообщений

Документ описывает backend-контракт для web/mobile frontend: ответы и дерево ответов, реакции в реальном времени, `@`-упоминания с push и закреплённые сообщения.

## Общие правила

- Base URL: `/api/v1`.
- Все HTTP-запросы требуют `Authorization: Bearer <access_token>`.
- WebSocket: `/ws?token=<access_token>`.
- UUID передаются строкой.
- Для личных чатов и групп используются message endpoints. Для каналов используются channel post endpoints из раздела 2.1.

## 1. Ответ на сообщение

При отправке передайте `reply_to_id`.

Личный чат:

```http
POST /api/v1/conversations/{conversation_id}/messages
Content-Type: application/json
```

Группа:

```http
POST /api/v1/groups/{group_id}/messages
Content-Type: application/json
```

```json
{
  "content": "Согласен",
  "content_hash": "CLIENT_SIDE_HASH",
  "keys": {},
  "type": "text",
  "reply_to_id": "11111111-1111-1111-1111-111111111111"
}
```

Backend проверяет, что исходное сообщение существует и находится в том же чате/группе. В ответе и в событии `new_message` поле `reply_to` содержит краткую модель родительского сообщения.

## 2. Дерево ответов

```http
GET /api/v1/messages/{message_id}/thread?limit=200
```

`message_id` — корневое сообщение. `limit` ограничивает число потомков, допустимо `1..200`, по умолчанию `200`.

```json
{
  "root": {
    "id": "11111111-1111-1111-1111-111111111111",
    "conversation_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    "sender_id": 10,
    "content": "Начальное сообщение",
    "type": "text",
    "reactions": [],
    "created_at": "2026-09-23T10:00:00Z"
  },
  "replies": [
    {
      "message": {
        "id": "22222222-2222-2222-2222-222222222222",
        "reply_to_id": "11111111-1111-1111-1111-111111111111",
        "content": "Ответ первого уровня",
        "reactions": []
      },
      "replies": [
        {
          "message": {
            "id": "33333333-3333-3333-3333-333333333333",
            "reply_to_id": "22222222-2222-2222-2222-222222222222",
            "content": "Вложенный ответ",
            "reactions": []
          },
          "replies": []
        }
      ]
    }
  ],
  "reply_count": 2,
  "truncated": false
}
```

`truncated: true` означает, что достигнут лимит и дерево показано не полностью. Удалённые для пользователя сообщения не включаются.

### 2.1. Ответы на посты канала

Ответ создаётся тем же endpoint, что и пост, но с `reply_to_id`:

```http
POST /api/v1/channels/{channel_id}/posts
Content-Type: application/json

{
  "content": "Ответ на пост канала",
  "reply_to_id": "11111111-1111-1111-1111-111111111111"
}
```

Обычный корневой пост могут публиковать owner/admin. Ответы могут отправлять все активные подписчики канала. Backend проверяет, что родительский пост принадлежит тому же каналу.

Получить дерево:

```http
GET /api/v1/channels/posts/{post_id}/thread?limit=200
```

Ответ имеет ту же структуру дерева, но узел содержит поле `post` вместо `message`:

```json
{
  "root": {"id":"ROOT_POST_UUID","channel_id":"CHANNEL_UUID"},
  "replies": [
    {
      "post": {
        "id":"REPLY_POST_UUID",
        "channel_id":"CHANNEL_UUID",
        "reply_to_id":"ROOT_POST_UUID",
        "content":"Ответ"
      },
      "replies": []
    }
  ],
  "reply_count": 1,
  "truncated": false
}
```

## 3. Реакции

Добавить реакцию:

```http
POST /api/v1/messages/{message_id}/reactions
Content-Type: application/json

{"reaction":"👍"}
```

Удалить свою реакцию (emoji обязательно URL-encode):

```http
DELETE /api/v1/messages/{message_id}/reactions/%F0%9F%91%8D
```

Оба endpoint возвращают актуальный агрегат:

```json
{
  "message": "reaction added",
  "reactions": [
    {
      "reaction": "👍",
      "count": 2,
      "users": [
        {"id": 10, "full_name": "User One", "avatar": null}
      ],
      "reacted": true
    }
  ]
}
```

После добавления или удаления всем онлайн-участникам приходит WebSocket snapshot:

```json
{
  "type": "message_reactions_updated",
  "conversation_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  "message_id": "11111111-1111-1111-1111-111111111111",
  "content": {
    "message_id": "11111111-1111-1111-1111-111111111111",
    "reactions": [
      {"reaction": "👍", "count": 2, "users": [], "reacted": true}
    ]
  },
  "timestamp": 1790157600
}
```

`reacted` персонализирован для получателя события. Frontend заменяет реакции конкретного сообщения массивом из `content.reactions`; отдельный refetch не нужен.

## 4. `@`-упоминания и push

Frontend показывает пользователю `@username`, но вместе с сообщением обязательно передаёт usernames в `metadata`. Сейчас `metadata` — JSON-строка, не объект.

```json
{
  "content": "@alex посмотри документ",
  "content_hash": "CLIENT_SIDE_HASH",
  "keys": {},
  "type": "text",
  "metadata": "{\"mentions\":[\"alex\"]}"
}
```

Backend:

1. разрешает usernames в user IDs;
2. оставляет только участников текущего чата/группы;
3. сохраняет упоминания;
4. отправляет WebSocket `new_message` с `is_mention: true` конкретному упомянутому пользователю;
5. отправляет push даже если чат/группа muted.

Push data для личного чата:

```json
{
  "kind": "chat_message",
  "conversation_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  "message_id": "11111111-1111-1111-1111-111111111111",
  "sender_id": "10",
  "type": "text",
  "is_mention": "true"
}
```

Push data для группы содержит `group_id`, `message_id`, `type`, `is_mention`.

Для поста или ответа в канале используется тот же `metadata`-контракт:

```json
{
  "content": "@alex проверь ответ",
  "reply_to_id": "ROOT_POST_UUID",
  "metadata": "{\"mentions\":[\"alex\"]}"
}
```

Упомянуть можно только активного подписчика канала. Упоминание сохраняется в `channel_post_mentions`, WebSocket `new_channel_post` приходит этому пользователю с `is_mention: true`, а push отправляется независимо от `is_muted`.

Channel push data:

```json
{
  "kind": "channel_mention",
  "channel_id": "CHANNEL_UUID",
  "post_id": "POST_UUID",
  "author_id": "10",
  "is_mention": "true"
}
```

## 5. Закрепление сообщений

Закрепить:

```http
POST /api/v1/messages/pin
Content-Type: application/json

{
  "chat_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  "chat_type": "group",
  "message_id": "11111111-1111-1111-1111-111111111111"
}
```

`chat_type`: `personal`, `group` или `channel`. В группе и канале закреплять могут owner/admin.

Открепить:

```http
POST /api/v1/messages/{message_id}/unpin?chat_id={chat_id}&chat_type=group
```

Получить закреплённые:

```http
GET /api/v1/messages/pinned?chat_id={chat_id}&chat_type=group
```

После закрепления/открепления участникам приходит:

```json
{
  "type": "pinned_messages_updated",
  "conversation_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  "content": {"chat_type":"group"},
  "timestamp": 1790157600
}
```

После события frontend повторно вызывает `GET /messages/pinned`.

## 6. Событие нового сообщения

Ответы и упоминания доставляются через обычное событие:

```json
{
  "type": "new_message",
  "conversation_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  "message_id": "11111111-1111-1111-1111-111111111111",
  "category": "chat",
  "is_mention": true,
  "content": {
    "id": "11111111-1111-1111-1111-111111111111",
    "reply_to_id": "00000000-0000-0000-0000-000000000000",
    "reply_to": {},
    "reactions": []
  },
  "timestamp": 1790157600
}
```

Frontend должен дедуплицировать сообщения по `content.id`, поскольку HTTP-ответ отправителю и WebSocket-синхронизация других его сессий могут приходить почти одновременно.

## 7. Ошибки

Используется общий формат ошибок backend:

```json
{
  "error": {
    "code": "FORBIDDEN",
    "message": "you are not a participant of this conversation",
    "user_message": "Недостаточно прав для выполнения операции."
  }
}
```

Основные статусы: `400` — неверный UUID/payload/limit, `401` — нет токена, `403` — нет доступа, `404` — сообщение не найдено.
