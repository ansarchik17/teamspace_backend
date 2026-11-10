# iOS Incoming Call Push Fix Handoff

Дата: `2026-05-21`

Этот документ для iOS-разработчика после исправлений backend по LiveKit + Firebase incoming call push.

## Коротко

Что было:

- Android получал входящий звонок корректно.
- iOS не получал входящий звонок или не показывал его как ожидается.

Что исправлено на backend:

- backend теперь корректно нормализует iOS platform;
- iOS token участвует в выборке active устройств для звонка;
- для iOS incoming call теперь отправляется APNs-compatible payload через Firebase;
- в payload добавлены поля звонка, которые нужны клиенту;
- ошибки Firebase/APNs логируются подробнее;
- logout/logout-all корректно деактивируют токены, а повторная регистрация устройства снова их активирует.

## Что именно исправлено на backend

### 1. Нормализация platform

Backend теперь приводит такие значения к `ios`:

- `ios`
- `iOS`
- `IOS`
- `iphone`
- `ipad`
- `apple`
- `apns`

И к `android`:

- `android`

Поэтому iOS-клиенту всё равно лучше отправлять ровно:

```json
"device_type": "ios"
```

### 2. iOS токены теперь реально участвуют в call push

При вызове backend ищет все активные устройства пользователя:

```sql
WHERE user_id = ?
  AND active = true
  AND logged_out_at IS NULL
```

То есть iOS больше не выпадает из выборки только из-за platform filter.

### 3. Изменён payload для iOS звонка

Раньше для iOS использовался фактически background-style push.

Теперь для iOS incoming call backend отправляет через Firebase payload с APNs alert config:

- `apns-push-type: alert`
- `apns-priority: 10`
- `sound: default`
- `content-available: 1`
- `mutable-content: 1`
- `category: INCOMING_CALL`

Это важно, потому что для iPhone обычный silent/background push недостаточен для корректного входящего звонка.

### 4. Добавлены поля звонка в payload

Backend теперь отправляет в data payload:

- `type`
- `event`
- `action`
- `call_id`
- `room_id`
- `room_name`
- `chat_id`
- `from_user_id`
- `from_name`
- `from_full_name`
- `caller_id`
- `caller_name`
- `caller_avatar`
- `livekit_url`
- `created_at`
- `sent_at`
- `sent_at_unix`
- `is_video`
- `title`
- `body`

### 5. Улучшено логирование

В логах backend теперь видно:

- `receiver_id`
- `call_id`
- `room_name`
- `platform`
- `masked_token`
- `firebase_message_id`
- `firebase_error_code`

Если проблема останется, по логам уже можно понять, это:

- bad payload;
- invalid token;
- APNs credentials issue;
- Firebase project mismatch;
- iOS client handling issue.

## Что должен сделать iOS-клиент

## 1. При регистрации устройства отправлять `device_type=ios`

Endpoint:

`POST /api/v1/devices`

Обязательно передавать:

- `fcm_token`
- `device_type = "ios"`
- `device_id`

Опционально:

- `voip_token`, если приложение уже использует PushKit

### Пример curl

```bash
curl --location 'http://localhost:8080/api/v1/devices' \
  --header 'Authorization: Bearer <ACCESS_TOKEN>' \
  --header 'Content-Type: application/json' \
  --header 'X-Device-ID: ios-device-001' \
  --data '{
    "device_id": "ios-device-001",
    "device_type": "ios",
    "fcm_token": "fcm_token_from_firebase",
    "voip_token": "optional_pushkit_voip_token"
  }'
```

### Успешный ответ

```json
{
  "message": "device registered successfully"
}
```

### Возможные ошибки

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "invalid fcm_token: too short"
  }
}
```

или

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "invalid request body"
  }
}
```

## 2. После логина всегда заново регистрировать устройство

Даже если `fcm_token` не изменился, после нового логина приложение должно снова вызвать:

`POST /api/v1/devices`

Зачем:

- backend мог деактивировать token после logout;
- повторная регистрация снова делает token активным.

## 3. При logout отправлять `device_id`

Endpoint:

`POST /api/v1/auth/logout`

### Пример curl

```bash
curl --location 'http://localhost:8080/api/v1/auth/logout' \
  --header 'Authorization: Bearer <ACCESS_TOKEN>' \
  --header 'Content-Type: application/json' \
  --header 'X-Device-ID: ios-device-001' \
  --data '{
    "refresh_token": "<REFRESH_TOKEN>",
    "device_id": "ios-device-001"
  }'
```

### Успешный ответ

```json
{
  "message": "logged out successfully"
}
```

Это отключает push только для текущего устройства.

## 4. Обрабатывать incoming call по data + APNs alert payload

### Что теперь приходит на iOS

Пример структуры:

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
  "caller_id": "11111",
  "caller_name": "Alice",
  "caller_avatar": "",
  "livekit_url": "wss://alem-workspace.gov.kz/livekit",
  "is_video": "true",
  "created_at": "2026-05-21T10:00:00Z",
  "sent_at": "2026-05-21T10:00:00Z",
  "sent_at_unix": "1779357600",
  "title": "Входящий звонок",
  "body": "Alice звонит вам",
  "priority": "high"
}
```

### Что нужно использовать на iOS

Минимально клиент должен читать:

- `type`
- `call_id`
- `room_name` или `room_id`
- `caller_name` или `from_name`
- `caller_id` или `from_user_id`
- `livekit_url`

Рекомендуемый маппинг:

- `room = room_name ?? room_id`
- `callerName = caller_name ?? from_full_name ?? from_name`
- `callerId = caller_id ?? from_user_id`

## 5. Что показать на iOS

Если приложение использует Firebase Messaging + CallKit:

1. получить push;
2. проверить `type == "incoming_call"`;
3. поднять CallKit/incoming call UI;
4. сохранить `call_id`, `room_name`, `livekit_url`;
5. по `Accept` выполнить текущий join/accept flow в LiveKit;
6. по `Decline` отправить decline/cancel flow.

## Что нужно проверить в iOS проекте

## 1. Firebase project

Проверить:

- iOS app подключён к тому же Firebase project, что и backend service account;
- bundle id совпадает с конфигурацией Firebase;
- FCM token действительно выпущен этим Firebase project.

Если Android работает, а iOS нет, очень вероятна проблема именно в APNs/Firebase конфигурации.

## 2. APNs credentials в Firebase

В Firebase Console должно быть настроено одно из:

- APNs Auth Key
- или APNs certificate

Если этого нет или оно невалидно, backend будет отправлять push, но iOS не получит его.

Именно для этого backend теперь логирует такие коды ошибок:

- `invalid-apns-credentials`
- `apns-auth-error`
- `third-party-auth-error`
- `mismatched-credential`

## 3. Capabilities на iOS

Проверить, что в iOS app включены нужные capability:

- Push Notifications
- Background Modes

Если нужен приём звонка в фоне:

- Remote notifications

Если используется полноценный системный call flow:

- CallKit

Если нужен настоящий VoIP incoming call в killed state:

- PushKit / VoIP pushes

## Важное ограничение

Сейчас backend исправлен для FCM + APNs alert payload.

Это значит:

- foreground: должно работать;
- background: должно работать при корректной iOS обработке;
- killed state: зависит от того, как именно реализован iOS клиент.

Если продукт хочет поведение как WhatsApp/Telegram с полноценным системным звонком даже при killed app, то одного обычного FCM push может быть недостаточно. Тогда нужно:

- использовать `voip_token`;
- поддержать отдельную VoIP push delivery логику через APNs PushKit channel.

Backend уже умеет хранить `voip_token`, но отдельная отправка VoIP push пока не реализована.

## Проверка сценария end-to-end

## Сценарий 1. Регистрация iPhone

1. Пользователь логинится на iPhone.
2. iOS получает FCM token.
3. iOS вызывает `POST /api/v1/devices`.
4. Backend отвечает:

```json
{
  "message": "device registered successfully"
}
```

## Сценарий 2. Входящий звонок

1. Пользователь A звонит пользователю B.
2. Backend создаёт call/livekit session.
3. Backend выбирает активные устройства пользователя B.
4. Backend отправляет:
   - Android push для Android устройств;
   - APNs-compatible FCM push для iOS устройств.
5. iPhone пользователя B получает push.
6. iOS клиент показывает incoming call UI.

## Сценарий 3. Logout

1. Пользователь B делает logout на iPhone.
2. iOS вызывает `POST /api/v1/auth/logout` с `device_id`.
3. Backend деактивирует token только этого iPhone.
4. На этот iPhone push больше не приходят.

## Сценарий 4. Повторный login

1. Пользователь снова логинится на том же iPhone.
2. iOS снова вызывает `POST /api/v1/devices`.
3. Backend снова активирует token.
4. Входящие звонки снова начинают приходить.

## Что смотреть в логах backend

При проблеме с iOS сначала проверять логи backend по полям:

- `receiver_id`
- `call_id`
- `room_name`
- `platform=ios`
- `masked_token`
- `firebase_error_code`

Если там есть:

- `invalid-registration-token`
- `registration-token-not-registered`

то проблема в token.

Если там есть:

- `invalid-apns-credentials`
- `apns-auth-error`
- `third-party-auth-error`
- `mismatched-credential`

то проблема не в Go-коде, а в Firebase/APNs настройке.

## Рекомендация для iOS команды

Минимум, который нужно сделать сейчас:

1. Всегда слать `device_type: "ios"`.
2. Всегда слать стабильный `device_id`.
3. После каждого логина заново вызывать `POST /devices`.
4. На входящем push обрабатывать `type=incoming_call`.
5. Поднимать CallKit/UI по `call_id`, `room_name`, `caller_name`, `livekit_url`.
6. Проверить Firebase APNs credentials.

## Быстрый чеклист

- `device_type = ios`
- `device_id` стабилен
- `POST /devices` вызывается после логина
- `POST /logout` передаёт `device_id`
- iOS умеет читать `incoming_call`
- iOS поднимает CallKit/UI
- Firebase iOS app в правильном project
- APNs key/cert загружен в Firebase
- bundle id совпадает
- если нужен killed-state system call, отдельно планировать PushKit/VoIP path
