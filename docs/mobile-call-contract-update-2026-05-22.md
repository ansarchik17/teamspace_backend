# Mobile Call Contract Update

Дата: `2026-05-22`

Этот документ описывает изменения в backend/LiveKit/notifications, которые нужно учесть в mobile-клиенте после последних правок по звонкам.

## Коротко

Что изменилось на backend:

- Добавлены атомарные переходы статуса звонка, чтобы `accept`, `decline`, `timeout` не гонялись между несколькими устройствами одного пользователя.
- Добавлен multi-device fan-out:
  - при `accept` на одном устройстве остальные устройства того же пользователя получают `call_answered_elsewhere`
  - при `decline` на одном устройстве остальные устройства того же пользователя получают `call_declined`
  - при `timeout` receiver получает `call_ended` с `reason=missed`
- Контракт payload для call events приведён к typed JSON:
  - `type` -> integer
  - `is_video` -> boolean
  - `sent_at_unix` -> integer
  - `from_user_id`, `caller_id`, `receiver_id` -> integer
- Важно: `FCM data` по ограничению Firebase всё равно приходит как `string -> string`. Typed JSON гарантируется для:
  - WebSocket `content`
  - APNS custom payload

## Сломается ли mobile

Может сломаться, если mobile сейчас:

- ожидает `type: "incoming_call"` вместо numeric call type
- ожидает `type` как string enum, а не `0 | 1`
- ожидает `is_video` как строку `"true"` / `"false"` в `WS` или `APNS custom payload`
- не обрабатывает новые события:
  - `call_declined`
  - `call_answered_elsewhere`
  - `call_ended`
  - `call_cancelled`
  - `call_busy`
- не закрывает incoming/ringing UI по `call_id`
- не различает transport payload (`FCM data`, где всё string) и JSON payload (`WS/APNS custom`, где типы уже native)

Если mobile уже умеет:

- читать `event` / `action` / `notification_type` / `call_event`
- парсить `type` как `0 | 1`
- закрывать текущий входящий экран по `call_id` на stop/sync events

то адаптация будет небольшой.

## Значение полей

- `type`
  - `0` = audio call
  - `1` = video call
- `event`
  - основное имя события, по нему лучше всего маршрутизировать клиентскую логику
- `action`
  - дублирует `event` для backward compatibility
- `notification_type`
  - дублирует `event` для backward compatibility
- `call_event`
  - дублирует `event` для backward compatibility
- `call_id`
  - главный идентификатор звонка; mobile должен закрывать/обновлять UI именно по нему
- `room_id` / `room_name`
  - room LiveKit
- `is_video`
  - `true` / `false`
- `reason`
  - причина stop/sync события

## Новый event contract

### 1. Incoming call

Событие входящего звонка:

- `event = incoming_call`
- `type = 0 | 1`

Пример typed JSON payload:

```json
{
  "type": 1,
  "event": "incoming_call",
  "action": "incoming_call",
  "notification_type": "incoming_call",
  "call_event": "incoming_call",
  "payload_type": "incoming_call",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_id": "call_101_202_1779444000",
  "room_name": "call_101_202_1779444000",
  "chat_id": "chat-1",
  "from_user_id": 101,
  "from_name": "Ivan Ivanov",
  "from_full_name": "Ivan Ivanov",
  "caller_id": 101,
  "caller_name": "Ivan Ivanov",
  "caller_avatar": "",
  "livekit_url": "wss://livekit.example.com",
  "is_video": true,
  "sent_at": "2026-05-22T10:00:00Z",
  "sent_at_unix": 1779444000,
  "created_at": "2026-05-22T10:00:00Z",
  "title": "Входящий звонок",
  "body": "Ivan Ivanov звонит вам",
  "priority": "high"
}
```

### 2. Answered on another device

Когда пользователь принял звонок на одном своём устройстве, другие его устройства получают:

```json
{
  "type": 1,
  "event": "call_answered_elsewhere",
  "action": "call_answered_elsewhere",
  "notification_type": "call_answered_elsewhere",
  "call_event": "call_answered_elsewhere",
  "payload_type": "call_answered_elsewhere",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_id": "call_101_202_1779444000",
  "room_name": "call_101_202_1779444000",
  "reason": "answered_on_another_device",
  "receiver_id": 202,
  "is_video": true,
  "sent_at": "2026-05-22T10:00:05Z",
  "sent_at_unix": 1779444005
}
```

Ожидаемая mobile-реакция:

- найти текущий incoming/ringing UI по `call_id`
- закрыть его
- не пытаться повторно делать `accept`

### 3. Declined on another device

Когда receiver отклонил звонок на одном своём устройстве, остальные его устройства получают:

```json
{
  "type": 1,
  "event": "call_declined",
  "action": "call_declined",
  "notification_type": "call_declined",
  "call_event": "call_declined",
  "payload_type": "call_declined",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_id": "call_101_202_1779444000",
  "room_name": "call_101_202_1779444000",
  "reason": "declined_on_another_device",
  "receiver_id": 202,
  "is_video": true,
  "sent_at": "2026-05-22T10:00:07Z",
  "sent_at_unix": 1779444007
}
```

Ожидаемая mobile-реакция:

- закрыть incoming UI по `call_id`

### 4. Ringing call ended / missed

Когда звонок истёк по timeout:

```json
{
  "type": 1,
  "event": "call_ended",
  "action": "call_ended",
  "notification_type": "call_ended",
  "call_event": "call_ended",
  "payload_type": "call_ended",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_id": "call_101_202_1779444000",
  "room_name": "call_101_202_1779444000",
  "reason": "missed",
  "receiver_id": 202,
  "is_video": true,
  "sent_at": "2026-05-22T10:00:25Z",
  "sent_at_unix": 1779444025
}
```

Ожидаемая mobile-реакция:

- закрыть incoming UI по `call_id`
- показать missed state, если это предусмотрено UX

### 5. Cancelled / busy / ringing stop

Backend также шлёт stop events другой стороне:

- `call_declined`
- `call_cancelled`
- `call_busy`
- `call_ended`

Примеры `reason`:

- `declined`
- `declined_on_another_device`
- `canceled`
- `busy`
- `ended`
- `missed`
- `answered_on_another_device`

## Transport-specific notes

## FCM Data

Это важно: в `FCM data` значения всё равно строковые, потому что Firebase Admin SDK требует `map[string]string`.

Пример того же `incoming_call` в FCM transport:

```json
{
  "type": "1",
  "event": "incoming_call",
  "action": "incoming_call",
  "notification_type": "incoming_call",
  "call_event": "incoming_call",
  "payload_type": "incoming_call",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_id": "call_101_202_1779444000",
  "room_name": "call_101_202_1779444000",
  "chat_id": "chat-1",
  "from_user_id": "101",
  "from_name": "Ivan Ivanov",
  "from_full_name": "Ivan Ivanov",
  "caller_id": "101",
  "caller_name": "Ivan Ivanov",
  "caller_avatar": "",
  "livekit_url": "wss://livekit.example.com",
  "is_video": "true",
  "sent_at": "2026-05-22T10:00:00Z",
  "sent_at_unix": "1779444000",
  "created_at": "2026-05-22T10:00:00Z",
  "title": "Входящий звонок",
  "body": "Ivan Ivanov звонит вам",
  "priority": "high"
}
```

Вывод для mobile:

- если читаете `Firebase RemoteMessage.data`, парсите строки сами
- если читаете `APNS custom payload` или `WS content`, там уже native JSON types

## WebSocket contract

Для `incoming_call`, `call_declined`, `call_answered_elsewhere`, `call_ended` backend теперь старается отправлять `content` в typed JSON contract.

Особенно важно:

- `SendIncomingCallWS(...)` больше не хардкодит `is_video=true`
- теперь туда пробрасывается реальный `is_video`

## APNS contract

Для iOS custom payload теперь значимые поля в `CustomData` идут typed-значениями:

- `type` -> integer
- `is_video` -> boolean
- `sent_at_unix` -> integer

Но если iOS-клиент читает payload через слой, который уже всё приводит к string, это нужно учесть отдельно в клиентском коде.

## HTTP API changes

Ниже только те ручки, которые нужны mobile для 1:1 call flow.

### POST /calls/start

Назначение:

- caller инициирует новый звонок
- backend создаёт call history
- caller получает LiveKit token
- callee получает incoming push/ws

Request:

```json
{
  "callee_id": 202,
  "chat_id": "chat-1",
  "priority": "high",
  "is_video": true
}
```

Curl:

```bash
curl -X POST "$BASE_URL/calls/start" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "callee_id": 202,
    "chat_id": "chat-1",
    "priority": "high",
    "is_video": true
  }'
```

Success `200`:

```json
{
  "token": "<livekit_token>",
  "url": "wss://livekit.example.com",
  "room_name": "call_101_202_1779444000",
  "call_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

Conflict `409` если пользователь уже в звонке:

```json
{
  "error": "call_busy",
  "message": "User is already in a call",
  "reason": "busy"
}
```

Conflict `409` если уже существует активный/висящий call между этими пользователями:

```json
{
  "error": "call_busy",
  "message": "Call already exists for these users",
  "reason": "busy",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_name": "call_101_202_1779444000",
  "status": "initiated",
  "from_user_id": 202,
  "to_user_id": 101,
  "incoming_call": true
}
```

Что должен сделать mobile на `incoming_call=true`:

- не показывать generic error
- открыть/обновить экран входящего звонка по `call_id`

### POST /calls/accept

Назначение:

- receiver принимает звонок
- backend atomically переводит call из `initiated` в `accepted`
- текущему устройству возвращается LiveKit token
- остальным устройствам этого receiver уходит `call_answered_elsewhere`

Request:

```json
{
  "room_name": "call_101_202_1779444000",
  "call_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

Curl:

```bash
curl -X POST "$BASE_URL/calls/accept" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "room_name": "call_101_202_1779444000",
    "call_id": "550e8400-e29b-41d4-a716-446655440000"
  }'
```

Success `200`:

```json
{
  "token": "<livekit_token>",
  "url": "wss://livekit.example.com",
  "call_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

Conflict `409` если уже приняли на другом устройстве:

```json
{
  "error": {
    "code": "CONFLICT",
    "message": "call already accepted on another device",
    "user_message": "Что-то пошло не так. Попробуйте позже."
  }
}
```

Conflict `409` если звонок уже финализирован:

```json
{
  "error": {
    "code": "CONFLICT",
    "message": "call is already declined"
  }
}
```

Что должен сделать mobile:

- на `409 call already accepted on another device` закрыть локальный ringing UI
- не ретраить `accept`

### POST /calls/decline

Назначение:

- receiver отклоняет входящий звонок
- или caller отменяет исходящий звонок
- backend atomically завершает call state

Request receiver decline:

```json
{
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "reason": "declined",
  "room_name": "call_101_202_1779444000"
}
```

Request busy:

```json
{
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "reason": "busy",
  "room_name": "call_101_202_1779444000"
}
```

Caller cancel:

```json
{
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "reason": "canceled",
  "room_name": "call_101_202_1779444000"
}
```

Curl:

```bash
curl -X POST "$BASE_URL/calls/decline" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "550e8400-e29b-41d4-a716-446655440000",
    "reason": "declined",
    "room_name": "call_101_202_1779444000"
  }'
```

Success `200`:

```json
{
  "message": "ok"
}
```

Idempotent success `200` если состояние уже финальное:

```json
{
  "status": "ok",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "call_status": "declined",
  "idempotent": true
}
```

Что должен сделать mobile:

- считать `200 idempotent=true` успешным завершением
- не показывать ошибку пользователю

### POST /calls/rejoin

Назначение:

- пере-вход в активный звонок без создания нового call

Request:

```json
{
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_name": "call_101_202_1779444000"
}
```

Curl:

```bash
curl -X POST "$BASE_URL/calls/rejoin" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "550e8400-e29b-41d4-a716-446655440000",
    "room_name": "call_101_202_1779444000"
  }'
```

Success `200`:

```json
{
  "token": "<livekit_token>",
  "url": "wss://livekit.example.com",
  "room_name": "call_101_202_1779444000",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "ongoing",
  "chat_id": "chat-1",
  "participants_count": 2
}
```

## Error response format

Часть ручек возвращает structured app error:

```json
{
  "error": {
    "code": "CONFLICT",
    "message": "call already accepted on another device",
    "user_message": "Что-то пошло не так. Попробуйте позже.",
    "details": {}
  }
}
```

Но некоторые legacy branches всё ещё возвращают plain JSON вида:

```json
{
  "error": "call_busy",
  "message": "User is already in a call",
  "reason": "busy"
}
```

Вывод для mobile:

- в обработке ошибок поддерживать обе формы

## Что mobile developer должен поменять

### 1. Unified parser для call payload

Нужен единый parser для:

- FCM `data`
- APNS custom payload
- WebSocket `content`

Правило:

- сначала брать `event`
- fallback: `action`
- fallback: `notification_type`
- fallback: `call_event`

### 2. Парсинг типов

Нужно поддержать:

- `type: 0 | 1` как integer
- `type: "0" | "1"` как string only для FCM `data`
- `is_video: true | false` как boolean
- `is_video: "true" | "false"` как string only для FCM `data`

Рекомендуемый mapping:

- `0` -> audio
- `1` -> video

### 3. Закрытие UI по sync/stop events

На событиях ниже нужно искать текущий звонок по `call_id` и закрывать incoming/ringing UI:

- `call_declined`
- `call_cancelled`
- `call_ended`
- `call_answered_elsewhere`
- `call_busy`

### 4. Multi-device behavior

Mobile должен считать нормальным сценарием:

- пользователь открыл звонок на двух устройствах
- одно устройство приняло звонок
- второе устройство получает `call_answered_elsewhere` и закрывает экран

И аналогично для `call_declined`.

### 5. Idempotent decline

Если `decline` вернул:

```json
{
  "status": "ok",
  "idempotent": true
}
```

это success, а не ошибка.

## Рекомендуемая клиентская модель

Рекомендуется нормализовать любой входящий transport payload в такую локальную модель:

```json
{
  "event": "incoming_call",
  "callId": "550e8400-e29b-41d4-a716-446655440000",
  "roomId": "call_101_202_1779444000",
  "roomName": "call_101_202_1779444000",
  "chatId": "chat-1",
  "callerId": 101,
  "callerName": "Ivan Ivanov",
  "receiverId": 202,
  "isVideo": true,
  "callType": 1,
  "reason": "answered_on_another_device",
  "sentAtUnix": 1779444000
}
```

## Regression checklist for mobile

1. Один user, два device, входящий звонок приходит на оба устройства.
2. На device A нажать `decline`.
3. На device B должен прийти `call_declined` с тем же `call_id`.
4. Device B должен закрыть incoming UI без ошибки.
5. Снова повторить сценарий, но на device A нажать `accept`.
6. На device B должен прийти `call_answered_elsewhere`.
7. Device B должен закрыть incoming UI без повторного `accept`.
8. Дождаться timeout без действий.
9. Device должен получить `call_ended` с `reason=missed`.
10. Проверить iOS payload: `type` не должен быть `"incoming_call"`.
11. Проверить, что `WS/APNS custom payload` парсятся как typed JSON.
12. Проверить, что `FCM data` корректно конвертируется из string payload в локальную typed model.

## Итог

Главное изменение для mobile: backend больше не гарантирует старый "везде всё string" контракт для call JSON payload. Теперь нужно различать:

- transport-level `FCM data`: строки
- application-level call payload (`WS/APNS custom`): typed JSON

И обязательно обработать новые multi-device события:

- `call_declined`
- `call_answered_elsewhere`
- `call_ended`

