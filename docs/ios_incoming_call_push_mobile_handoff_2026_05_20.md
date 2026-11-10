# iOS Incoming Call Push Handoff

Актуальная памятка для mobile dev по изменениям backend для входящих звонков на iOS.

Дата изменений backend: `2026-05-20`

## 1. Что изменилось на backend

- `POST /api/v1/devices` теперь принимает не только `fcm_token` и `device_type`, но и `voip_token`.
- Для personal incoming call backend теперь отправляет platform-aware push:
- `android` устройства: FCM с `high` priority.
- `ios` устройства: FCM + APNS alert config для incoming call.
- Для iOS incoming-call push backend теперь выставляет:
- `content-available: 1`
- `mutable-content: 1`
- `apns-push-type: alert`
- `apns-priority: 10`
- `apns-expiration`
- `sound: default`
- `category: INCOMING_CALL`
- В payload personal incoming call добавлены и выровнены поля, нужные клиенту:
- `room_name`
- `from_full_name`
- `is_video`
- `event`
- `action`

Важно:
- Сейчас backend уже хранит `voip_token`, но пока не шлёт отдельный прямой VoIP push через PushKit/APNS `.voip`.
- Текущий рабочий путь для iOS: принять FCM push с APNs alert/data payload через Firebase Messaging и на клиенте показать CallKit UI.

## 2. Что должен сделать mobile iOS

1. При регистрации устройства отправлять:
- `fcm_token`
- `device_type = "ios"`
- `voip_token`, если клиент уже получает его через PushKit

2. На iOS уметь обрабатывать incoming-call push в background/data сценарии.

3. На входящем push поднимать CallKit экран на основе payload ниже.

4. Если `voip_token` уже есть на клиенте, хранить и обновлять его через тот же endpoint `POST /devices`.

5. Не завязываться только на `title/body`; основная логика должна опираться на:
- `type`
- `call_id`
- `room_id` или `room_name`
- `from_user_id`
- `from_name`
- `chat_id`

## 3. Регистрация устройства

Endpoint:
- `POST /api/v1/devices`

Headers:
- `Authorization: Bearer <ACCESS_TOKEN>`
- `Content-Type: application/json`

### 3.1 iOS с FCM и PushKit token

```bash
curl -X POST "$BASE_URL/devices" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "fcm_token": "fcm_token_from_firebase",
    "device_type": "ios",
    "voip_token": "voip_token_from_pushkit"
  }'
```

Успех:

```json
{
  "message": "device registered successfully"
}
```

### 3.2 iOS только с FCM token

```bash
curl -X POST "$BASE_URL/devices" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "fcm_token": "fcm_token_from_firebase",
    "device_type": "ios"
  }'
```

Успех:

```json
{
  "message": "device registered successfully"
}
```

### 3.3 Возможные ошибки

Пример ошибки валидации:

```json
{
  "error": {
    "code": "validation_error",
    "message": "invalid request body"
  }
}
```

Пример ошибки токена:

```json
{
  "error": {
    "code": "bad_request",
    "message": "invalid fcm_token: empty"
  }
}
```

Также возможны:
- `invalid fcm_token: placeholder`
- `invalid fcm_token: contains spaces/newlines`
- `invalid fcm_token: too short`
- `invalid voip_token: placeholder`
- `invalid voip_token: contains spaces/newlines`

## 4. Incoming call push payload

Тип события:
- `type = "incoming_call"`

Пример data payload, который теперь приходит для personal call:

```json
{
  "type": "incoming_call",
  "event": "incoming_call",
  "action": "incoming_call",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_id": "call_11111_22345_1709121500",
  "room_name": "call_11111_22345_1709121500",
  "chat_id": "conversation-uuid",
  "from_user_id": "11111",
  "from_name": "Alice",
  "from_full_name": "Alice",
  "is_video": "true",
  "sent_at": "2026-05-20T11:00:00Z",
  "sent_at_unix": "1779274800",
  "title": "Входящий звонок",
  "body": "Alice звонит вам",
  "priority": "high"
}
```

Для iOS client считать обязательными для call flow:
- `type`
- `call_id`
- `room_id`
- `room_name`
- `from_user_id`
- `from_name`
- `chat_id`

Желательно использовать дополнительно:
- `from_full_name`
- `is_video`
- `sent_at`

## 5. Что делать на клиенте при incoming_call

Рекомендуемая логика:

1. Получить data push от Firebase Messaging.
2. Проверить `type == "incoming_call"`.
3. Извлечь:
- `call_id`
- `room_name` или `room_id`
- `from_name`
- `from_user_id`
- `chat_id`
4. Поднять CallKit экран.
5. По `Accept` выполнить текущий flow rejoin/accept звонка.
6. По `Decline` выполнить текущий backend decline flow.

Если на клиенте уже есть общий обработчик и для `call_invite`, и для `incoming_call`, лучше унифицировать маппинг:
- `room_name = payload.room_name ?? payload.room_id`
- caller display name = `from_full_name ?? from_name`

## 6. Related REST API for call flow

База:

```bash
BASE_URL=http://92.38.48.9:18080/api/v1
```

### 6.1 Принять звонок

```bash
curl -X POST "$BASE_URL/calls/accept" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "room_name": "call_11111_22345_1709121500",
    "call_id": "550e8400-e29b-41d4-a716-446655440000"
  }'
```

Успех:

```json
{
  "token": "eyJ...",
  "url": "wss://alem-workspace.gov.kz/livekit",
  "call_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

### 6.2 Rejoin звонка

```bash
curl -X POST "$BASE_URL/calls/rejoin" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "550e8400-e29b-41d4-a716-446655440000",
    "room_name": "call_11111_22345_1709121500"
  }'
```

Успех:

```json
{
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "room_name": "call_11111_22345_1709121500",
  "token": "eyJ...",
  "url": "wss://alem-workspace.gov.kz/livekit"
}
```

### 6.3 Отклонить звонок

```bash
curl -X POST "$BASE_URL/calls/decline" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "550e8400-e29b-41d4-a716-446655440000",
    "reason": "declined"
  }'
```

Успех:

```json
{
  "message": "ok"
}
```

## 7. iOS-specific notes

Что уже подготовил backend:
- background APNS delivery через Firebase для incoming call
- хранение `voip_token`
- раздельную отправку по платформам

Что нужно на клиенте:
- включённый background mode для remote notifications
- корректный Firebase Messaging background handler
- вызов CallKit при получении `incoming_call`

Что пока не включено на backend:
- отдельная прямая отправка VoIP push через APNS `voip`
- использование `voip_token` как отдельного delivery channel

Это значит:
- если у клиента уже есть PushKit token, его нужно уже сейчас отправлять и обновлять
- но фактическая доставка пока идёт через FCM/APNS background push

## 8. Checklist для mobile dev

- Добавить `voip_token` в request регистрации устройства на iOS.
- Отправлять `device_type: "ios"` явно.
- Убедиться, что iOS background/data push доходит в killed/background state настолько, насколько это позволяет текущий FCM path.
- Обрабатывать `incoming_call` по полям `call_id`, `room_name`, `from_name`, `chat_id`.
- Поднимать CallKit UI из background push.
- Сохранять `voip_token` и обновлять его при изменении.
- Не ожидать пока отдельного backend VoIP APNS endpoint.

## 9. Короткий итог для реализации

Минимум, который должен сделать mobile iOS прямо сейчас:
- при старте/обновлении токенов вызвать `POST /devices`
- передать `fcm_token`, `device_type=ios`, `voip_token` если есть
- на входящем `type=incoming_call` показать системный экран звонка
- по accept/decline использовать текущие endpoints `/calls/accept`, `/calls/rejoin`, `/calls/decline`
