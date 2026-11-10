# Push Tokens And Logout For Mobile

## Что изменилось

Backend больше не отправляет push-уведомления на устройство после logout.

Новое поведение:

- logout на одном устройстве отключает только push token текущего устройства;
- logout на всех устройствах отключает все активные push token-ы пользователя;
- при повторном логине и повторной регистрации device token запись снова активируется;
- при ошибках Firebase для невалидных token-ов backend автоматически отключает такие token-ы.

## Что должен делать мобильный клиент

### 1. После логина зарегистрировать push token

После успешного логина приложение должно вызвать `POST /api/v1/devices`.

Нужно передавать:

- `Authorization: Bearer <access_token>`
- `X-Device-ID: <stable_device_id>` или `device_id` в body
- `fcm_token`
- `device_type`
- `voip_token` для iOS, если используется

`device_id` должен быть стабильным для одного и того же устройства. Это важно, потому что logout отключает token именно по `device_id`.

### 2. Перед logout передать device_id текущего устройства

При `POST /api/v1/auth/logout` мобильный клиент должен передавать `device_id` текущего устройства:

- либо в JSON body;
- либо в header `X-Device-ID`.

Рекомендуемый вариант: передавать и в body, и в header одинаковое значение.

### 3. После повторного входа заново зарегистрировать token

Если пользователь снова вошёл в аккаунт, приложение должно снова вызвать `POST /api/v1/devices`, даже если `fcm_token` не изменился.

Это реактивирует token на backend.

## Endpoint: Register Device

`POST /api/v1/devices`

### Пример curl Android

```bash
curl --location 'http://localhost:8080/api/v1/devices' \
  --header 'Authorization: Bearer <ACCESS_TOKEN>' \
  --header 'Content-Type: application/json' \
  --header 'X-Device-ID: android-device-001' \
  --data '{
    "device_id": "android-device-001",
    "device_type": "android",
    "fcm_token": "fcm_token_from_firebase_very_long_value",
    "voip_token": ""
  }'
```

### Пример curl iOS

```bash
curl --location 'http://localhost:8080/api/v1/devices' \
  --header 'Authorization: Bearer <ACCESS_TOKEN>' \
  --header 'Content-Type: application/json' \
  --header 'X-Device-ID: ios-device-001' \
  --data '{
    "device_id": "ios-device-001",
    "device_type": "ios",
    "fcm_token": "fcm_token_from_firebase_very_long_value",
    "voip_token": "optional_voip_token_for_ios"
  }'
```

### Успешный ответ

```json
{
  "message": "device registered successfully"
}
```

### Ошибки

Невалидный body:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "invalid request body"
  }
}
```

Невалидный `fcm_token`:

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "invalid fcm_token: too short"
  }
}
```

## Endpoint: Logout Current Device

`POST /api/v1/auth/logout`

Этот endpoint:

- завершает logout пользователя;
- деактивирует push token только текущего устройства;
- не трогает token-ы других устройств пользователя.

### Рекомендуемый curl

```bash
curl --location 'http://localhost:8080/api/v1/auth/logout' \
  --header 'Authorization: Bearer <ACCESS_TOKEN>' \
  --header 'Content-Type: application/json' \
  --header 'X-Device-ID: android-device-001' \
  --data '{
    "refresh_token": "<REFRESH_TOKEN>",
    "device_id": "android-device-001"
  }'
```

### Успешный ответ

```json
{
  "message": "logged out successfully"
}
```

### Если refresh token не передан

```json
{
  "error": {
    "code": "UNAUTHORIZED",
    "message": "refresh token not found"
  }
}
```

## Endpoint: Logout All Devices

`POST /api/v1/auth/logout-all`

Этот endpoint:

- завершает все пользовательские сессии;
- деактивирует все активные push token-ы пользователя на всех устройствах.

### Пример curl

```bash
curl --location 'http://localhost:8080/api/v1/auth/logout-all' \
  --header 'Authorization: Bearer <ACCESS_TOKEN>' \
  --header 'Content-Type: application/json' \
  --data '{}'
```

### Успешный ответ

```json
{
  "message": "logged out from all devices"
}
```

### Если пользователь не определён

```json
{
  "error": {
    "code": "UNAUTHORIZED",
    "message": "authentication required"
  }
}
```

## Ожидаемый mobile flow

### Обычный login

1. Пользователь логинится.
2. Приложение получает `access_token` и `refresh_token`.
3. Приложение вызывает `POST /api/v1/devices`.
4. Устройство начинает получать push.

### Logout только на текущем устройстве

1. Пользователь нажимает logout.
2. Приложение вызывает `POST /api/v1/auth/logout` с `refresh_token` и `device_id`.
3. Backend отключает token только этого устройства.
4. Это устройство больше не должно получать push.
5. Другие устройства пользователя продолжают получать push.

### Повторный login на том же устройстве

1. Пользователь снова логинится.
2. Приложение снова вызывает `POST /api/v1/devices`.
3. Backend реактивирует token.
4. Устройство снова получает push.

### Logout all devices

1. Пользователь вызывает logout all.
2. Backend отключает все активные token-ы пользователя.
3. Push не должны приходить ни на одно устройство, пока они заново не зарегистрируются после нового логина.

## Важные замечания для mobile

- Не меняйте `device_id` на каждом запуске приложения.
- После каждого успешного логина вызывайте `POST /api/v1/devices`.
- Если Firebase выдал новый `fcm_token`, отправьте его повторно через `POST /api/v1/devices`.
- Если logout вызывается без `device_id`, backend не сможет гарантированно отключить push только для текущего устройства.
- Для iOS можно передавать `voip_token`, но основная логика отключения после logout сейчас завязана на запись устройства и `fcm_token`.

## Коротко для mobile-разработчика

- login -> `POST /devices`
- logout current device -> `POST /auth/logout` + обязательно `device_id`
- logout all -> `POST /auth/logout-all`
- relogin -> снова `POST /devices`
