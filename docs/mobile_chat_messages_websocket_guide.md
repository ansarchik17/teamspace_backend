# Mobile Chat Messages + WebSocket Guide

Этот документ для мобильного разработчика, который интегрирует:

- сообщения в личных чатах
- сообщения в группах
- realtime через WebSocket
- защиту от дублей своих же сообщений

Все примеры ниже даны для backend из этого репозитория.

## 1. Базовые URL

REST API:

```text
https://YOUR_HOST/api/v1
```

WebSocket:

```text
wss://YOUR_HOST/api/v1/ws
```

Локально обычно это выглядит так:

```text
http://localhost:8080/api/v1
ws://localhost:8080/api/v1/ws
```

## 2. Авторизация

Для всех message endpoints нужен access token:

```http
Authorization: Bearer <ACCESS_TOKEN>
```

Для WebSocket backend поддерживает два варианта:

1. `Authorization: Bearer <ACCESS_TOKEN>` header
2. query param `?token=<ACCESS_TOKEN>`

Если мобильная библиотека неудобно умеет headers в WebSocket handshake, используйте query param.

## 3. Самое важное: как не получить дубль своего сообщения

Backend теперь специально не отправляет `new_message` обратно в ту же клиентскую сессию, которая только что получила HTTP response от `POST /messages`.

Но для этого мобильное приложение обязано передавать один и тот же стабильный идентификатор сессии/устройства:

- и в обычный HTTP запрос на отправку сообщения
- и в WebSocket connection

### Что рекомендуется передавать

Лучший и самый простой вариант для mobile:

```http
X-Device-ID: <STABLE_MOBILE_DEVICE_ID>
```

Пример значения:

```text
a7d1fd82-9d1d-4f2e-b7c6-7c6d52d0b301
```

`X-Device-ID` должен быть:

- стабильным для одной установки приложения или одного device profile
- одинаковым в HTTP и WebSocket
- неизменным после refresh токена

Если access token обновился, `X-Device-ID` менять не надо.

### Что будет, если не передавать стабильный ID

Тогда возможен такой сценарий:

1. мобильный клиент отправляет `POST /messages`
2. backend возвращает `201` с сообщением
3. параллельно backend присылает `new_message` в WebSocket
4. клиент показывает одно и то же сообщение два раза

### Минимальное правило для mobile

На каждом запросе отправки сообщений и при подключении к `/ws` передавайте один и тот же `X-Device-ID`.

## 4. Рекомендуемая стратегия на клиенте

1. При старте приложения получить или создать `deviceId`.
2. Сохранить его в secure storage / local storage приложения.
3. Добавлять `X-Device-ID` во все chat HTTP запросы.
4. Добавлять тот же `X-Device-ID` в WebSocket handshake.
5. После `POST /messages` сразу показывать ответ backend.
6. Любые входящие сообщения дополнительно upsert-ить по `message.id`, а не blindly append.

Даже если backend уже защищает от дубля, клиенту все равно лучше делать upsert по `message.id`.

## 5. Обязательные headers для mobile

Рекомендуемый набор:

```http
Authorization: Bearer <ACCESS_TOKEN>
Content-Type: application/json
X-Device-ID: <STABLE_MOBILE_DEVICE_ID>
```

Опционально можно использовать:

- `X-Session-ID`
- `X-Client-Instance-ID`

Но если нет особой причины, для mobile достаточно одного `X-Device-ID`.

## 6. Личный чат: создать или получить conversation

Endpoint:

```http
POST /api/v1/conversations
```

Body:

```json
{
  "peer_id": 25
}
```

`curl`:

```bash
curl -X POST "https://YOUR_HOST/api/v1/conversations" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Device-ID: $DEVICE_ID" \
  -d '{
    "peer_id": 25
  }'
```

Response example:

```json
{
  "id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "type": "personal",
  "created_by": 10,
  "created_at": "2026-04-01T10:00:00Z",
  "updated_at": "2026-04-01T10:00:00Z"
}
```

## 7. Отправить сообщение в личный чат

Endpoint:

```http
POST /api/v1/conversations/{conversation_id}/messages
```

Поддерживаемые `type`:

- `text`
- `image`
- `video`
- `file`
- `audio`
- `voice`
- `video_message`

Важно:

- поле `content` сейчас обязательно в request body
- это касается не только `text`, но и остальных типов тоже
- для `image/file/video/audio` не отправляйте `content: ""`
- если нужно, передавайте caption, filename или технический placeholder

Body example:

```json
{
  "content": "Привет",
  "content_hash": "sha256:abc123",
  "keys": {
    "device-1": "encrypted-key-for-device-1",
    "device-2": "encrypted-key-for-device-2"
  },
  "type": "text",
  "reply_to_id": null,
  "is_ephemeral": false,
  "ttl_seconds": null
}
```

`curl`:

```bash
curl -X POST "https://YOUR_HOST/api/v1/conversations/$CONVERSATION_ID/messages" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Device-ID: $DEVICE_ID" \
  -d '{
    "content": "Привет",
    "content_hash": "sha256:abc123",
    "keys": {
      "device-1": "encrypted-key-for-device-1"
    },
    "type": "text",
    "is_ephemeral": false
  }'
```

Response:

```json
{
  "id": "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "sender_id": 10,
  "reply_to_id": null,
  "content": "Привет",
  "content_hash": "sha256:abc123",
  "encrypted_keys": {
    "device-1": "encrypted-key-for-device-1"
  },
  "type": "text",
  "metadata": null,
  "is_edited": false,
  "is_forwarded": false,
  "is_deleted": false,
  "created_at": "2026-04-01T10:05:00Z",
  "updated_at": "2026-04-01T10:05:00Z",
  "sender_username": "askar",
  "sender_full_name": "Askar Abylkhaiyrov",
  "sender_avatar": null,
  "reply_to": null,
  "read_by_count": 0,
  "delivered_count": 0,
  "is_read": false,
  "reactions": []
}
```

## 8. Получить историю личного чата

Endpoint:

```http
GET /api/v1/conversations/{conversation_id}/messages?limit=50&offset=0
```

`curl`:

```bash
curl "https://YOUR_HOST/api/v1/conversations/$CONVERSATION_ID/messages?limit=50&offset=0" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "X-Device-ID: $DEVICE_ID"
```

Response:

```json
{
  "messages": [
    {
      "id": "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
      "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
      "sender_id": 10,
      "content": "Привет",
      "type": "text",
      "created_at": "2026-04-01T10:05:00Z",
      "updated_at": "2026-04-01T10:05:00Z",
      "sender_username": "askar",
      "sender_full_name": "Askar Abylkhaiyrov",
      "reactions": []
    }
  ],
  "count": 1
}
```

## 9. Отправить сообщение в группу

Endpoint:

```http
POST /api/v1/groups/{group_id}/messages
```

Body example:

```json
{
  "content": "Всем привет",
  "content_hash": "sha256:def456",
  "type": "text",
  "reply_to_id": null,
  "metadata": "{\"mentions\":[\"john\",\"anna\"]}"
}
```

Важно:

- `metadata` здесь строка, внутри которой лежит JSON
- для mentions backend ожидает поле `mentions`
- поле `content` тоже обязательно

`curl`:

```bash
curl -X POST "https://YOUR_HOST/api/v1/groups/$GROUP_ID/messages" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Device-ID: $DEVICE_ID" \
  -d '{
    "content": "Всем привет",
    "content_hash": "sha256:def456",
    "type": "text",
    "metadata": "{\"mentions\":[\"john\",\"anna\"]}"
  }'
```

## 10. Получить историю группы

Endpoint:

```http
GET /api/v1/groups/{group_id}/messages?limit=50&offset=0
```

`curl`:

```bash
curl "https://YOUR_HOST/api/v1/groups/$GROUP_ID/messages?limit=50&offset=0" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "X-Device-ID: $DEVICE_ID"
```

## 11. Редактировать сообщение

Endpoint:

```http
PUT /api/v1/messages/{message_id}
```

Body:

```json
{
  "content": "Исправленный текст"
}
```

`curl`:

```bash
curl -X PUT "https://YOUR_HOST/api/v1/messages/$MESSAGE_ID" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Device-ID: $DEVICE_ID" \
  -d '{
    "content": "Исправленный текст"
  }'
```

## 12. Удалить одно сообщение

Endpoint:

```http
DELETE /api/v1/messages/{message_id}
```

`curl`:

```bash
curl -X DELETE "https://YOUR_HOST/api/v1/messages/$MESSAGE_ID" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "X-Device-ID: $DEVICE_ID"
```

## 13. Массово удалить сообщения

Endpoint:

```http
DELETE /api/v1/messages
```

Body:

```json
{
  "message_ids": [
    "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
    "be5d63a7-c841-46f6-a1f5-6c95e2b8e8c6"
  ]
}
```

`curl`:

```bash
curl -X DELETE "https://YOUR_HOST/api/v1/messages" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Device-ID: $DEVICE_ID" \
  -d '{
    "message_ids": [
      "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
      "be5d63a7-c841-46f6-a1f5-6c95e2b8e8c6"
    ]
  }'
```

## 14. Отметить сообщение как прочитанное

Endpoint:

```http
POST /api/v1/messages/{message_id}/read
```

`curl`:

```bash
curl -X POST "https://YOUR_HOST/api/v1/messages/$MESSAGE_ID/read" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "X-Device-ID: $DEVICE_ID"
```

Response:

```json
{
  "message": "marked as read"
}
```

## 15. Переслать сообщения

Endpoint:

```http
POST /api/v1/messages/forward
```

Body:

```json
{
  "message_ids": [
    "8dcb44a4-a370-4f12-95b3-cb2ff754a42d"
  ],
  "target_chat_ids": [
    "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c"
  ]
}
```

`curl`:

```bash
curl -X POST "https://YOUR_HOST/api/v1/messages/forward" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Device-ID: $DEVICE_ID" \
  -d '{
    "message_ids": [
      "8dcb44a4-a370-4f12-95b3-cb2ff754a42d"
    ],
    "target_chat_ids": [
      "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c"
    ]
  }'
```

## 16. WebSocket подключение

Endpoint:

```text
wss://YOUR_HOST/api/v1/ws
```

### Вариант 1. Через query params

Рекомендуемый fallback, если библиотека WebSocket неудобно умеет headers:

```text
wss://YOUR_HOST/api/v1/ws?token=<ACCESS_TOKEN>&device_id=<DEVICE_ID>
```

### Вариант 2. Через headers

Рекомендуемый вариант:

```http
Authorization: Bearer <ACCESS_TOKEN>
X-Device-ID: <DEVICE_ID>
```

### Пример через `wscat`

Через query:

```bash
npx wscat -c "wss://YOUR_HOST/api/v1/ws?token=$ACCESS_TOKEN&device_id=$DEVICE_ID"
```

Через headers:

```bash
npx wscat -c "wss://YOUR_HOST/api/v1/ws" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "X-Device-ID: $DEVICE_ID"
```

## 17. Что mobile отправляет в WebSocket

Backend на вход принимает следующие client events:

### 17.1 Typing start

```json
{
  "type": "typing",
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "content": {
    "is_typing": true
  }
}
```

### 17.2 Typing stop

```json
{
  "type": "typing",
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "content": {
    "is_typing": false
  }
}
```

### 17.3 Ping

```json
{
  "type": "ping"
}
```

### Важно про read

Не используйте raw WebSocket `read` как основной источник истины.

Для mark as read используйте REST:

```http
POST /api/v1/messages/{message_id}/read
```

И уже после этого слушайте server event `read_receipt`.

## 18. Что mobile получает из WebSocket

Ниже основные server events, которые нужно поддержать на мобильном клиенте.

### 18.1 `new_message`

Приходит при новом сообщении.

Пример:

```json
{
  "type": "new_message",
  "from": 0,
  "to": 25,
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "message_id": "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
  "is_mention": false,
  "timestamp": 1775037900,
  "content": {
    "id": "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
    "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
    "sender_id": 10,
    "content": "Привет",
    "type": "text",
    "created_at": "2026-04-01T10:05:00Z",
    "updated_at": "2026-04-01T10:05:00Z",
    "sender_username": "askar",
    "sender_full_name": "Askar Abylkhaiyrov",
    "reply_to": null,
    "reactions": []
  }
}
```

Что делать на клиенте:

- upsert сообщения по `content.id`
- не append без проверки
- обновить превью чата

### 18.2 `typing`

Пример:

```json
{
  "type": "typing",
  "from": 25,
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "timestamp": 1775037901,
  "content": {
    "is_typing": true
  }
}
```

### 18.3 `read_receipt`

Пример:

```json
{
  "type": "read_receipt",
  "from": 25,
  "to": 10,
  "message_id": "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
  "timestamp": 1775037902
}
```

Смысл:

- `from` это пользователь, который прочитал сообщение
- `message_id` это сообщение, которое дочитали

### 18.4 `message_edited`

Пример:

```json
{
  "type": "message_edited",
  "to": 25,
  "timestamp": 1775037903,
  "content": {
    "id": "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
    "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
    "content": "Исправленный текст",
    "is_edited": true
  }
}
```

### 18.5 `message_deleted`

Пример:

```json
{
  "type": "message_deleted",
  "to": 25,
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "message_id": "8dcb44a4-a370-4f12-95b3-cb2ff754a42d",
  "timestamp": 1775037904,
  "content": {
    "is_for_everyone": false,
    "new_last_message": null
  }
}
```

### 18.6 `user_status`

Пример:

```json
{
  "type": "user_status",
  "from": 25,
  "timestamp": 1775037905,
  "content": {
    "user_id": 25,
    "status": "offline",
    "last_seen_at": "2026-04-01T10:10:00Z",
    "is_last_seen": true
  }
}
```

### 18.7 `initial_online_list`

Сразу после подключения backend может прислать список пользователей, которые уже online:

```json
{
  "type": "initial_online_list",
  "to": 10,
  "timestamp": 1775037906,
  "content": [25, 30, 44]
}
```

### 18.8 `chat_hidden`

```json
{
  "type": "chat_hidden",
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "content": {
    "chat_type": "personal"
  }
}
```

### 18.9 `chat_deleted`

```json
{
  "type": "chat_deleted",
  "conversation_id": "f2e5b0f1-f0f9-4f0a-8e3c-5d5f73335c6c",
  "content": {
    "chat_type": "personal"
  }
}
```

### 18.10 `pong`

Ответ на клиентский `ping`.

```json
{
  "type": "pong",
  "timestamp": 1775037907
}
```

## 19. Рекомендованный mobile flow

### При запуске приложения

1. Прочитать `deviceId` из storage.
2. Если его нет, сгенерировать UUID.
3. Подключить WebSocket с тем же `deviceId`.

### При отправке сообщения

1. Отправить HTTP `POST`.
2. Получить `201` и вставить/обновить message по `id`.
3. Не ждать для этого WebSocket.

### При получении `new_message`

1. Проверить `content.id`.
2. Если такого сообщения нет, вставить.
3. Если уже есть, обновить запись.

## 20. Android / iOS смысл в одной фразе

Mobile клиент должен использовать:

- один и тот же `X-Device-ID` для HTTP и WebSocket
- upsert по `message.id`
- REST как source of truth для отправки и mark-as-read
- WebSocket как realtime-канал для обновлений

## 21. Короткий чеклист для мобильного разработчика

- Добавить стабильный `deviceId`.
- Во все chat HTTP запросы добавлять `X-Device-ID`.
- В `/ws` передавать тот же `X-Device-ID`.
- После `POST /messages` рисовать ответ сразу.
- На `new_message` делать upsert по `message.id`.
- Для read использовать `POST /messages/{message_id}/read`.
- Для typing использовать WebSocket event `typing`.

## 22. Если WebSocket библиотека не умеет headers

Используйте query params:

```text
wss://YOUR_HOST/api/v1/ws?token=<ACCESS_TOKEN>&device_id=<DEVICE_ID>
```

Для backend это тоже валидно.

## 23. Если после этого все равно есть дубль

Нужно проверить ровно 3 вещи:

1. В HTTP запросе реально уходит `X-Device-ID`.
2. В WebSocket handshake уходит тот же самый `X-Device-ID`.
3. Клиент не делает `append`, а делает `upsert` по `message.id`.

Если хотя бы одно из этих правил не выполнено, дубль может остаться.
