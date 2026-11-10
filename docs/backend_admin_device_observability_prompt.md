# ТЗ для backend: полная админка по аккаунтам, устройствам и активности

Документ актуализирован под текущий backend-код в `/Users/kkabyl/Documents/Projects/Alem_Back_Front/alem-workspace-backend`.

Цель: не придумывать новую систему с нуля, а расширить уже существующие backend-сущности и API так, чтобы админка могла полноценно показывать:

- какой аккаунт активен или был активен недавно
- через что зашел пользователь: `web`, `android`, `ios`
- тип устройства: `phone`, `tablet`, `desktop`
- список устройств пользователя
- последнее время активности
- время логина
- online/offline
- историю активности
- возможность завершать устройства и сессии

---

## Что уже есть в backend

По текущему коду backend уже существуют полезные части, на которые нужно опереться:

- `users` уже хранит `last_login_at` и `last_seen_at`
- есть `GET /api/v1/users`
- есть `GET /api/v1/users/online`
- есть `POST /api/v1/devices`
- есть таблица `user_devices`
- есть таблица `login_logs`

### Уже существующие данные в `users`

Сейчас в `users` уже есть:

- `id`
- `email`
- `username`
- `first_name`
- `last_name`
- `last_login_at`
- `last_seen_at`
- орг-структура

### Уже существующие данные в `user_devices`

Сейчас в `user_devices` уже есть:

- `id`
- `user_id`
- `fcm_token`
- `device_type`
- `platform`
- `device_id`
- `voip_token`
- `active`
- `last_used_at`
- `logged_out_at`
- `created_at`
- `updated_at`

### Уже существующие ограничения

На текущий момент backend уже может отдать:

- пользователя
- `last_login_at`
- `last_seen_at`
- привязанные устройства
- `platform`
- `device_type`
- `device_id`
- признаки активного push-устройства

Но backend пока не хранит полноценно:

- `manufacturer`
- `model`
- `brand`
- `os_name`
- `os_version`
- `app_version`
- `build_number`
- `browser_name`
- `browser_version`
- `locale`
- `timezone`
- `last_ip`
- `last_user_agent`

То есть для полной админки эти поля нужно добавить.

---

## Главный принцип реализации

Не нужно делать все через одну API.

Для админки нужно сделать несколько специализированных admin endpoint-ов, которые опираются на уже существующие таблицы:

- `users`
- `user_devices`
- `login_logs`

И при необходимости расширяются новыми полями и новыми таблицами.

Рекомендуемый backend scope:

1. Расширить текущую модель `user_devices`
2. Добавить 2-3 admin endpoint-а для админки
3. Добавить session/activity слой там, где реально нужен отдельный контроль

---

## Что нужно сделать

### 1. Расширить `user_devices`

Нужно не создавать параллельную сущность `devices`, а расширить уже существующую таблицу `user_devices`.

Добавить поля:

- `device_name`
- `manufacturer`
- `model`
- `brand`
- `os_name`
- `os_version`
- `app_version`
- `build_number`
- `browser_name`
- `browser_version`
- `locale`
- `timezone`
- `is_emulator`
- `last_ip`
- `last_user_agent`
- `trusted`
- `last_seen_at`

Если часть полей неактуальна для mobile или web, допускаются `NULL`.

### 2. Не менять текущие клиентские API

Текущий `POST /api/v1/devices` уже используется клиентом для регистрации устройства и push token.

Важно:

- не менять контракт существующего `POST /api/v1/devices`
- не переиспользовать его для новой админской телеметрии
- не ломать текущую мобильную интеграцию

Вся новая логика должна добавляться через новые API.

Если backend-команда хочет собирать расширенные device metadata, это нужно делать через новый endpoint, а не через изменение старого контракта.

### 3. Добавить admin API

Вместо одной супер-ручки нужно сделать минимум 3 ручки.

---

## Новые admin endpoint-ы

### 1. `GET /api/v1/admin/users`

Назначение: список пользователей для таблицы админки.

Должен возвращать:

- `user_id`
- `full_name`
- `email`
- `username`
- `last_login_at`
- `last_seen_at`
- `is_online`
- `devices_count`
- `active_devices_count`
- `last_platform`
- `last_device_type`
- `position_name`
- `organization_name`

Допустимые фильтры:

- `q`
- `platform`
- `device_type`
- `is_online`
- `app_version`
- `limit`
- `offset`

Важно:

- это не должна быть тяжелая detail-ручка
- список должен быть быстрым
- детальные устройства не нужно возвращать здесь целиком

### 2. `GET /api/v1/admin/users/:userId/devices`

Назначение: детальная карточка устройств конкретного пользователя.

Должен возвращать:

- `id`
- `device_id`
- `device_name`
- `platform`
- `device_type`
- `manufacturer`
- `model`
- `brand`
- `os_name`
- `os_version`
- `app_version`
- `build_number`
- `browser_name`
- `browser_version`
- `locale`
- `timezone`
- `active`
- `trusted`
- `has_fcm_token`
- `has_voip_token`
- `last_used_at`
- `last_seen_at`
- `logged_out_at`
- `last_ip`
- `last_user_agent`
- `created_at`
- `updated_at`

### 3. `GET /api/v1/admin/users/:userId/activity`

Назначение: история активности пользователя.

MVP-вариант можно собрать из уже существующих данных:

- `login_logs`
- `users.last_login_at`
- `users.last_seen_at`
- `user_devices.last_used_at`
- `user_devices.logged_out_at`

Возвращать:

- `recent_logins`
- `recent_devices`
- `last_seen_at`
- `last_login_at`
- `online_status`

Если backend-команда готова сделать лучше, можно добавить отдельную таблицу `user_activity_events` и писать туда:

- `login`
- `logout`
- `device_registered`
- `device_deactivated`
- `push_token_refreshed`
- `web_session_started`

---

## Новые client endpoint-ы

Чтобы не менять существующие API, новые данные нужно принимать отдельными ручками.

### 1. `POST /api/v1/client/devices/metadata`

Назначение: сохранить расширенную информацию об устройстве без изменения старого `POST /api/v1/devices`.

Должен принимать:

- `device_id`
- `platform`
- `device_type`
- `device_name`
- `manufacturer`
- `model`
- `brand`
- `os_name`
- `os_version`
- `app_version`
- `build_number`
- `browser_name`
- `browser_version`
- `locale`
- `timezone`
- `is_emulator`

Важно:

- endpoint новый
- старый `POST /api/v1/devices` остается как есть
- backend может связывать metadata с уже зарегистрированным устройством по `user_id + device_id`

### 2. `POST /api/v1/client/devices/heartbeat`

Назначение: обновлять `last_seen_at` и operational-данные устройства.

Должен принимать:

- `device_id`
- `last_seen_at`
- `app_state`
- `screen_name` опционально
- `ip` или вычислять IP на backend
- `user_agent` для web

### 3. `POST /api/v1/client/devices/logout`

Назначение: явно помечать конкретное устройство как завершенное.

Должен принимать:

- `device_id`

Ожидаемое поведение:

- обновить `logged_out_at`
- при необходимости `active = false`
- не менять контракт старых auth/logout flow, если они уже используются

---

## Опциональные admin actions

Если готовы делать полноценную управляемую админку, дополнительно нужны:

### 4. `POST /api/v1/admin/users/:userId/devices/:deviceId/deactivate`

Назначение: деактивировать конкретное устройство пользователя.

Ожидаемое поведение:

- `user_devices.active = false`
- выставить `logged_out_at`
- опционально очистить push token

### 5. `POST /api/v1/admin/users/:userId/devices/:deviceId/trust`

Назначение: пометить устройство как доверенное.

### 6. `POST /api/v1/admin/users/:userId/devices/:deviceId/untrust`

Назначение: снять доверенность с устройства.

### 7. `POST /api/v1/admin/users/:userId/logout-all-devices`

Назначение: завершить все устройства пользователя.

---

## Как backend должен определять online/offline

На текущем backend уже есть `GET /api/v1/users/online`, который опирается на Keycloak active sessions.

Для admin UI логика должна быть такой:

- `is_online = true`, если у пользователя есть активная backend/auth session
- дополнительно можно учитывать свежий `last_seen_at`

Рекомендованное отображение:

- если есть активная Keycloak session: `online`
- если сессии нет, но `last_seen_at` свежий: `recently_active`
- если давно ничего нет: `offline`

Если backend-команда не хочет вводить третий статус, можно оставить:

- `online`
- `offline`

а `last_seen_at` использовать как supporting field.

---

## Что нужно принимать от клиента в новых API

Текущий `POST /api/v1/devices` не меняем.

Он как и раньше продолжает принимать только текущие поля:

- `fcm_token`
- `device_type`
- `device_id`
- `voip_token`

Расширенные данные устройство должно отправлять в новый endpoint `POST /api/v1/client/devices/metadata`.

Пример payload:

```json
{
  "fcm_token": "optional",
  "voip_token": "optional",
  "device_id": "stable-device-id",
  "device_type": "tablet",
  "platform": "ios",
  "device_name": "iPad Air",
  "manufacturer": "Apple",
  "model": "iPad Air",
  "brand": "Apple",
  "os_name": "iOS",
  "os_version": "18.5",
  "app_version": "1.1.95",
  "build_number": "195",
  "browser_name": null,
  "browser_version": null,
  "locale": "ru-KZ",
  "timezone": "Asia/Almaty",
  "is_emulator": false
}
```

Для web-клиента:

```json
{
  "fcm_token": null,
  "voip_token": null,
  "device_id": "web-chrome-macbook-slot-1",
  "device_type": "desktop",
  "platform": "web",
  "device_name": "Chrome on macOS",
  "manufacturer": null,
  "model": "MacBook",
  "brand": "Apple",
  "os_name": "macOS",
  "os_version": "15.0",
  "app_version": "web-2026.06.18",
  "build_number": "20260618",
  "browser_name": "Chrome",
  "browser_version": "137",
  "locale": "ru-KZ",
  "timezone": "Asia/Almaty",
  "is_emulator": false
}
```

---

## Что должно быть в admin response

### Ответ `GET /api/v1/admin/users`

```json
{
  "items": [
    {
      "user_id": 123,
      "full_name": "Ivan Ivanov",
      "email": "ivan@company.kz",
      "username": "ivanov",
      "last_login_at": "2026-06-18T08:00:00Z",
      "last_seen_at": "2026-06-18T12:30:00Z",
      "is_online": true,
      "devices_count": 3,
      "active_devices_count": 2,
      "last_platform": "ios",
      "last_device_type": "tablet",
      "position_name": "Advisor",
      "organization_name": "Some Ministry"
    }
  ],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

### Ответ `GET /api/v1/admin/users/:userId/devices`

```json
[
  {
    "id": "uuid",
    "device_id": "mobile-ios-001",
    "device_name": "iPad Air",
    "platform": "ios",
    "device_type": "tablet",
    "manufacturer": "Apple",
    "model": "iPad Air",
    "brand": "Apple",
    "os_name": "iOS",
    "os_version": "18.5",
    "app_version": "1.1.95",
    "build_number": "195",
    "browser_name": null,
    "browser_version": null,
    "active": true,
    "trusted": true,
    "has_fcm_token": true,
    "has_voip_token": true,
    "last_used_at": "2026-06-18T12:29:00Z",
    "last_seen_at": "2026-06-18T12:30:00Z",
    "logged_out_at": null,
    "last_ip": "masked-or-raw",
    "last_user_agent": "optional",
    "created_at": "2026-05-01T10:00:00Z",
    "updated_at": "2026-06-18T12:30:00Z"
  }
]
```

### Ответ `GET /api/v1/admin/users/:userId/activity`

```json
{
  "user_id": 123,
  "last_login_at": "2026-06-18T08:00:00Z",
  "last_seen_at": "2026-06-18T12:30:00Z",
  "is_online": true,
  "recent_logins": [
    {
      "login_time": "2026-06-18T08:00:00Z",
      "email": "ivan@company.kz"
    }
  ],
  "recent_devices": [
    {
      "device_id": "mobile-ios-001",
      "platform": "ios",
      "device_type": "tablet",
      "last_used_at": "2026-06-18T12:29:00Z",
      "active": true
    }
  ]
}
```

---

## Что нужно сделать по БД

### Обязательно

Нужно добавить migration для расширения `user_devices`.

Новые поля:

- `device_name TEXT`
- `manufacturer TEXT`
- `model TEXT`
- `brand TEXT`
- `os_name TEXT`
- `os_version TEXT`
- `app_version TEXT`
- `build_number TEXT`
- `browser_name TEXT`
- `browser_version TEXT`
- `locale TEXT`
- `timezone TEXT`
- `is_emulator BOOLEAN DEFAULT FALSE`
- `trusted BOOLEAN DEFAULT FALSE`
- `last_seen_at TIMESTAMPTZ`
- `last_ip TEXT`
- `last_user_agent TEXT`

### Индексы

Добавить индексы:

- `(user_id, active)`
- `(user_id, last_used_at DESC)`
- `(platform)`
- `(device_type)`
- `(app_version)`
- `(last_seen_at DESC)`

---

## MVP

### В MVP обязательно сделать

1. Расширить `user_devices`
2. Добавить `POST /api/v1/client/devices/metadata`
3. Добавить `POST /api/v1/client/devices/heartbeat`
4. Сделать `GET /api/v1/admin/users`
5. Сделать `GET /api/v1/admin/users/:userId/devices`
6. Сделать `GET /api/v1/admin/users/:userId/activity`

### В MVP можно не делать отдельную таблицу `sessions`

На текущем этапе можно опереться на:

- `users.last_login_at`
- `users.last_seen_at`
- `login_logs`
- `user_devices`
- active sessions из Keycloak

---

## V2

Во второй итерации можно добавить:

- отдельную таблицу `user_sessions`
- отдельную таблицу `user_activity_events`
- хранение `termination_reason`
- admin audit log
- suspicious login detection
- geo/IP anomaly detection
- forced session revoke через backend + Keycloak

---

## Edge cases

Backend должен корректно обрабатывать:

- один пользователь на нескольких устройствах
- один пользователь одновременно на web и mobile
- переустановку приложения
- обновление `fcm_token`
- logout на одном устройстве
- logout all devices
- inactive devices
- web-device без push token
- mobile-device только с `voip_token`
- старые записи `user_devices`, где нет новых полей

---

## Готовый промт для backend-команды или LLM

Ты работаешь с уже существующим Go backend.

Нельзя проектировать систему с нуля. Нужно расширить текущую реализацию.

### Уже есть:

- таблица `users` с `last_login_at` и `last_seen_at`
- таблица `user_devices` с `user_id`, `device_type`, `platform`, `device_id`, `fcm_token`, `voip_token`, `active`, `last_used_at`, `logged_out_at`
- таблица `login_logs`
- endpoint `POST /api/v1/devices`
- endpoint `GET /api/v1/users`
- endpoint `GET /api/v1/users/online`

### Нужно:

1. Расширить `user_devices`, а не создавать новую параллельную таблицу устройств
2. Не менять контракт текущего `POST /api/v1/devices`
3. Добавить admin API:
   - `GET /api/v1/admin/users`
   - `GET /api/v1/admin/users/:userId/devices`
   - `GET /api/v1/admin/users/:userId/activity`
4. Добавить новые client API:
   - `POST /api/v1/client/devices/metadata`
   - `POST /api/v1/client/devices/heartbeat`
   - опционально `POST /api/v1/client/devices/logout`
5. Опционально добавить:
   - `POST /api/v1/admin/users/:userId/devices/:deviceId/deactivate`
   - `POST /api/v1/admin/users/:userId/devices/:deviceId/trust`
   - `POST /api/v1/admin/users/:userId/devices/:deviceId/untrust`
   - `POST /api/v1/admin/users/:userId/logout-all-devices`

### Важно:

- не пытайся запихнуть все в один endpoint
- не менять существующие клиентские API по контракту
- admin list endpoint должен быть легким
- devices detail endpoint должен отдавать полную информацию по устройствам
- activity endpoint должен собирать логины и последние активности
- используй текущие репозитории и структуры по максимуму
- если для части данных таблиц пока нет, предложи точечные migrations

### Что хочу получить в ответ:

1. точный plan изменений
2. SQL migration для `user_devices`
3. request/response DTO
4. список новых handler/service/repository методов
5. SQL queries для admin list, user devices, user activity
6. что сделать в MVP и что отложить на V2
