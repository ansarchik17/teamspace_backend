# Messenger mute API

Документ описывает mute-логику для Alem Messenger: личные чаты, группы и каналы можно выключить без звука. Поведение ориентировано на Telegram: обычные сообщения в muted-чате не отправляют push-уведомление, но если пользователя упомянули через mention/tag, уведомление все равно приходит.

## Общее поведение

- Mute хранится персонально для каждого пользователя.
- Mute не удаляет чат, не скрывает чат и не сбрасывает unread.
- Новые сообщения продолжают приходить по WebSocket, чтобы список чатов и открытый чат обновлялись в реальном времени.
- Push/FCM уведомление не отправляется, если чат muted.
- Исключение: если сообщение содержит mention текущего пользователя, push отправляется даже при muted-чате.
- В `GET /chats/unified`, `GET /conversations`, `GET /groups` и `GET /channels` фронт получает `is_muted`.
- Для групп и personal `mention_count` продолжает считаться отдельно от `unread_count`.

Базовый URL:

```text
https://alem-workspace.gov.kz/api/v1
```

Обязательные заголовки:

```http
Authorization: Bearer <access_token>
Accept: application/json
Content-Type: application/json
```

## 1. Personal chat mute

### Endpoint

```http
PUT /api/v1/conversations/{conversation_id}/mute
```

### Path params

| Параметр | Тип | Обязательный | Описание |
| --- | --- | --- | --- |
| `conversation_id` | `uuid` | да | ID личного conversation |

### Body

```json
{
  "mute": true
}
```

| Поле | Тип | Обязательное | Описание |
| --- | --- | --- | --- |
| `mute` | `boolean` | да | `true` выключает push, `false` включает обратно |

### Пример curl

```bash
curl -X PUT "https://alem-workspace.gov.kz/api/v1/conversations/8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5/mute" \
  -H "Accept: application/json" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <access_token>" \
  -d '{"mute":true}'
```

### Success response `200`

```json
{
  "message": "conversation mute setting updated",
  "conversation_id": "8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5",
  "is_muted": true,
  "chat": {
    "id": "8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5",
    "type": "personal",
    "peer_id": 45,
    "peer_username": "john",
    "peer_full_name": "John Doe",
    "unread_count": 3,
    "mention_count": 1,
    "is_pinned": false,
    "is_muted": true,
    "created_at": "2026-05-01T10:00:00Z",
    "updated_at": "2026-05-01T10:20:00Z"
  }
}
```

## 2. Group mute

### Endpoint

```http
PUT /api/v1/groups/{group_id}/mute
```

### Path params

| Параметр | Тип | Обязательный | Описание |
| --- | --- | --- | --- |
| `group_id` | `uuid` | да | ID группы |

### Body

```json
{
  "mute": true
}
```

### Пример curl

```bash
curl -X PUT "https://alem-workspace.gov.kz/api/v1/groups/f7f6601a-d7a4-4c53-a2d7-1f3662faad72/mute" \
  -H "Accept: application/json" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <access_token>" \
  -d '{"mute":true}'
```

### Success response `200`

```json
{
  "message": "group mute setting updated",
  "group_id": "f7f6601a-d7a4-4c53-a2d7-1f3662faad72",
  "is_muted": true,
  "group": {
    "id": "f7f6601a-d7a4-4c53-a2d7-1f3662faad72",
    "name": "Backend Team",
    "type": "private",
    "member_role": "member",
    "member_count": 12,
    "unread_count": 5,
    "mention_count": 1,
    "is_muted": true,
    "created_at": "2026-05-01T10:00:00Z",
    "updated_at": "2026-05-01T10:20:00Z"
  }
}
```

## 3. Channel mute

Для каналов endpoint уже существовал и использует ту же модель.

### Endpoint

```http
PUT /api/v1/channels/{channel_id}/mute
```

### Body

```json
{
  "mute": true
}
```

### Пример curl

```bash
curl -X PUT "https://alem-workspace.gov.kz/api/v1/channels/4de2b8b5-7fb5-4f47-9f64-3cfe9e69a102/mute" \
  -H "Accept: application/json" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <access_token>" \
  -d '{"mute":true}'
```

### Success response `200`

```json
{
  "muted": true
}
```

В списке каналов и unified list канал возвращается с `is_muted`.

## 4. Mentions/tag исключение

Чтобы пользователь получил уведомление даже в muted-чате, отправитель должен передать `metadata` со списком usernames.

Важно: сейчас `metadata` передается строкой JSON.

### Group message with mentions

```http
POST /api/v1/groups/{group_id}/messages
```

```json
{
  "content": "Проверь, пожалуйста, @john",
  "type": "text",
  "metadata": "{\"mentions\":[\"john\"]}"
}
```

### Personal message with mentions

```http
POST /api/v1/conversations/{conversation_id}/messages
```

```json
{
  "content": "Проверь, пожалуйста, @john",
  "type": "text",
  "metadata": "{\"mentions\":[\"john\"]}"
}
```

Backend:

- резолвит usernames в user IDs;
- сохраняет mention в `message_mentions`;
- ставит `is_mention=true` в WebSocket `new_message` для упомянутого пользователя;
- отправляет push упомянутому пользователю даже если чат muted;
- не отправляет push по обычному сообщению, если чат muted.

## 5. Unified chat list

### Endpoint

```http
GET /api/v1/chats/unified?limit=20&offset=0
```

В каждом элементе personal/group/channel фронт должен читать:

```json
{
  "id": "f7f6601a-d7a4-4c53-a2d7-1f3662faad72",
  "type": "group",
  "unread_count": 5,
  "mention_count": 1,
  "is_muted": true,
  "is_pinned": false
}
```

Рекомендация для UI:

- если `is_muted=true`, показывать muted-иконку;
- badge можно показывать по `unread_count`;
- отдельный `@` badge показывать по `mention_count`;
- локальные браузерные/нативные уведомления от WebSocket показывать только если `is_muted=false` или `is_mention=true`.

## 6. WebSocket behavior

Новые сообщения приходят как раньше:

```json
{
  "type": "new_message",
  "conversation_id": "f7f6601a-d7a4-4c53-a2d7-1f3662faad72",
  "message_id": "0c46a2a0-2c9c-4ed8-b7ef-6b3cda2f4c9b",
  "category": "chat",
  "is_mention": true,
  "content": {
    "id": "0c46a2a0-2c9c-4ed8-b7ef-6b3cda2f4c9b",
    "content": "Проверь, пожалуйста, @john",
    "type": "text"
  }
}
```

WebSocket нужен для live-синхронизации, поэтому muted-чаты все равно получают событие. Клиент не должен показывать звук/баннер для muted-чата, если `is_mention=false`.

## 7. Ошибки

Формат ошибок стандартный:

```json
{
  "code": "BAD_REQUEST",
  "message": "invalid group ID",
  "details": null
}
```

Типовые случаи:

| HTTP | Причина |
| --- | --- |
| `400` | Невалидный UUID или body |
| `401` | Нет/просрочен `Authorization` token |
| `403` | Пользователь не участник conversation/group |
| `500` | Ошибка базы или внутреннего сервиса |

## 8. Быстрый сценарий подключения на фронте

1. На экране настроек чата дернуть `PUT /conversations/{id}/mute`, `PUT /groups/{id}/mute` или `PUT /channels/{id}/mute`.
2. После ответа обновить локальное поле `is_muted`.
3. В списке чатов читать `is_muted` из `GET /chats/unified`.
4. При отправке сообщения с tag передавать `metadata: "{\"mentions\":[\"username\"]}"`.
5. При получении WebSocket `new_message` не показывать локальный баннер/звук, если чат muted и `is_mention=false`.
