# Mobile Device Metadata Integration Guide

Этот документ описывает, как мобильное приложение должно отправлять metadata устройства, чтобы админ видел только пользователей, которые реально вызвали API, и видел по ним платформу, тип устройства, версию ОС, версию приложения и номер сборки.

## Коротко

После успешного логина мобильное приложение должно вызвать:

```http
POST /api/v1/client/devices/metadata
Authorization: Bearer <access_token>
Content-Type: application/json
```

Если запрос успешно выполнен, пользователь появится в админском списке `GET /api/v1/admin/users` с данными устройства.

## Когда вызывать

Вызывать нужно после получения валидного `access_token`.

Обязательные сценарии:

1. Сразу после успешного логина по паролю.
2. Сразу после успешного логина через Keycloak/OAuth.
3. Сразу после успешного входа по PIN, если после PIN-login приложение получает новый `access_token`.
4. При холодном старте приложения, если пользователь уже авторизован и есть валидный `access_token`.

Рекомендация: отправлять один раз за сессию приложения и повторять при смене версии приложения, номера сборки, версии ОС или `device_id`.

Не нужно дергать endpoint на каждом экране или на каждое действие пользователя.

## Endpoint

```http
POST /api/v1/client/devices/metadata
```

Endpoint требует авторизацию. Без `Authorization: Bearer <access_token>` backend вернет `401 Unauthorized`.

## Request Body

```json
{
  "device_id": "3f8c5b9e-1d2a-4c8e-8b9a-7f6e5d4c3b2a",
  "platform": "ios",
  "device_type": "mobile",
  "os_version": "18.5",
  "app_version": "1.1.95",
  "build_number": "195"
}
```

### Поля

| Поле | Тип | Обязательно | Что отправлять |
| --- | --- | --- | --- |
| `device_id` | string | Да | Стабильный уникальный ID установки приложения |
| `platform` | string | Да | `ios` или `android` |
| `device_type` | string | Да | `mobile` или `tablet` |
| `os_version` | string | Нет | Версия ОС, например `18.5` или `14` |
| `app_version` | string | Нет | Версия приложения, например `1.1.95` |
| `build_number` | string | Нет | Номер сборки, например `195` |

## Правила для device_id

`device_id` должен быть стабильным для одной установки приложения.

Требования:

- Не генерировать новый `device_id` при каждом запуске приложения.
- Не генерировать новый `device_id` при каждом логине.
- Не использовать FCM token как `device_id`, потому что push token может меняться.
- Не отправлять модель телефона, IMEI, serial number, MAC address или другие чувствительные идентификаторы.

Рекомендации:

- iOS: сгенерировать UUID один раз и хранить в Keychain.
- Android: сгенерировать UUID один раз и хранить в EncryptedSharedPreferences.

Если пользователь удалил приложение и локальное защищенное хранилище очистилось, допустимо создать новый `device_id`.

## Response

Успешный ответ:

```json
{
  "message": "device metadata saved successfully"
}
```

После этого админ увидит пользователя в списке пользователей с device metadata.

## Ошибки

### 401 Unauthorized

Причина: нет access token, token просрочен или невалиден.

Что делать:

1. Обновить token через refresh flow.
2. Повторить `POST /api/v1/client/devices/metadata` один раз после успешного refresh.
3. Если refresh не удался, отправить пользователя на login.

### 400/422 Validation error

Причина: нет обязательных полей или body невалидный.

Проверьте, что отправляются:

- `device_id`
- `platform`
- `device_type`

### 5xx Server error

Причина: временная ошибка backend.

Что делать:

- Не блокировать пользователя.
- Сохранить флаг, что metadata не отправилась.
- Повторить позже с backoff, например через 30 секунд, 2 минуты, 5 минут.

## Idempotency

Endpoint можно безопасно вызывать повторно с тем же `device_id`.

Backend обновит существующую запись:

- `last_used_at`
- `last_seen_at`
- `os_version`
- `app_version`
- `build_number`

Дубликат устройства для того же `user_id + device_id` создаваться не должен.

## Что увидит админ

Админская ручка:

```http
GET /api/v1/admin/users
Authorization: Bearer <admin_access_token>
```

Показывает только пользователей, которые уже отправляли:

```http
POST /api/v1/client/devices/metadata
```

В ответе по пользователю будут поля:

```json
{
  "user_id": 123,
  "full_name": "Test User",
  "email": "user@example.com",
  "username": "test.user",
  "last_login_at": "2026-06-18T10:00:00Z",
  "last_seen_at": "2026-06-18T10:02:00Z",
  "is_online": true,
  "platform": "ios",
  "device_type": "mobile",
  "os_version": "18.5",
  "app_version": "1.1.95",
  "build_number": "195"
}
```

Если у пользователя несколько устройств, в общем списке показывается последнее устройство, по которому была отправлена metadata.

## Пример последовательности после логина

1. Пользователь логинится.
2. Приложение получает `access_token`.
3. Приложение собирает локальные данные:
   - stable `device_id`
   - `platform`
   - `device_type`
   - `os_version`
   - `app_version`
   - `build_number`
4. Приложение вызывает `POST /api/v1/client/devices/metadata`.
5. Если ответ `200 OK`, больше ничего делать не нужно.
6. Если ошибка временная, повторить позже и не блокировать основной UX.

## iOS Checklist

- `device_id` хранится стабильно в Keychain.
- `platform` всегда `ios`.
- `device_type` определяется как `mobile` или `tablet`.
- `os_version` берется из версии iOS.
- `app_version` берется из `CFBundleShortVersionString`.
- `build_number` берется из `CFBundleVersion`.
- Запрос выполняется только после получения `access_token`.

## Android Checklist

- `device_id` хранится стабильно в EncryptedSharedPreferences.
- `platform` всегда `android`.
- `device_type` определяется как `mobile` или `tablet`.
- `os_version` берется из версии Android.
- `app_version` берется из versionName.
- `build_number` берется из versionCode.
- Запрос выполняется только после получения `access_token`.

## Не отправлять

Для этого endpoint не нужно отправлять:

- геолокацию;
- IP address;
- User-Agent;
- модель телефона;
- производителя устройства;
- IMEI;
- serial number;
- MAC address;
- контакты;
- advertising ID.

Backend ожидает только минимальную техническую metadata, перечисленную в request body.

## Минимальный тест перед релизом

1. Установить приложение на чистое устройство.
2. Залогиниться.
3. Проверить, что приложение вызвало `POST /api/v1/client/devices/metadata`.
4. Проверить, что backend вернул `200 OK`.
5. Открыть админский список пользователей.
6. Убедиться, что пользователь появился в списке.
7. Убедиться, что `platform`, `device_type`, `os_version`, `app_version`, `build_number` заполнены корректно.
8. Перезапустить приложение и проверить, что `device_id` не изменился.
