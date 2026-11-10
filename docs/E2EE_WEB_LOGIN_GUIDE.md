# Web Frontend Guide: Login + E2EE Bootstrap

Актуально для текущего backend flow на `2026-04-05`.

## Base URL

```bash
BASE_URL="http://92.38.48.9:18080/api/v1"
WS_URL="ws://92.38.48.9:18080/api/v1/ws"
```

## Что frontend должен понимать сразу

Для web UI здесь есть 2 разных сценария:

1. `Быстрый старт`
   Новый браузер получает свой новый E2EE key pair и сразу начинает читать/отправлять новые сообщения.

2. `Доступ к старой истории`
   Если нужно расшифровать старую E2EE-историю на новом web device, canonical flow идет через `QR linked-device + history sync / re-wrap`.

Важно:

- `POST /auth/login` только логинит пользователя.
- `POST /e2ee/device/register` обязательно должен вызываться после web login.
- Именно `POST /e2ee/device/register`:
  - привязывает текущий браузер как linked web device
  - сохраняет `ip / country / city / user-agent`
  - может автоматически завершить другие web browser sessions
  - возвращает device-scoped web tokens
  - может сразу вернуть `backup`

Еще важно:

- текущий password-login flow регистрирует новый web device, но сам по себе не открывает старую историю
- для old history restore при `fresh web key` нужен QR linked-device flow
- при ordinary login с restored identity key из backup старая история тоже может быть доступна
- текущий backend честно поддерживает cloud backup, но backup шифруется на клиенте и без master password сервер его не расшифрует
- backup metadata нужно трактовать как recovery contract, а не как implicit разрешение использовать ту же identity на новом web device

---

## Полный flow для frontend

### Шаг 1. Логин по email/password

#### Request

```bash
curl -X POST "$BASE_URL/auth/login" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "StrongPassword123"
  }'
```

#### Response `200 OK`

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.bootstrap-access",
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.bootstrap-refresh",
  "token_type": "bearer",
  "expires_in": 300,
  "expires_at": "2026-04-05T12:05:00Z"
}
```

Что делает frontend:

- сохраняет эти токены временно
- НЕ считает этот шаг завершением web bootstrap
- сразу переходит к подготовке web device

Примечание:

- текущий `/auth/login` не возвращает профиль пользователя
- если нужен профиль сразу, вызовите `/auth/me`

#### Optional: текущий пользователь

```bash
curl "$BASE_URL/auth/me" \
  -H "Authorization: Bearer <ACCESS_TOKEN_FROM_LOGIN>"
```

---

### Шаг 2. Выбрать один из двух frontend сценариев

#### Вариант A. Быстрый старт

Используется если:

- это новый браузер
- старая история не обязательна прямо сейчас
- нужен быстрый вход и работа с новыми сообщениями

Тогда frontend:

- создает стабильный `device_id`
- генерирует новый локальный key pair
- вызывает `/e2ee/device/register`

#### Вариант B. Доступ к старой истории

Используется если:

- пользователь хочет читать старую E2EE-историю
- он подключает новый browser slot
- нужна история, которая была отправлена до появления этого `web_device_id`

Тогда frontend:

1. создает новый локальный `web_device_id` и новый `X25519 key pair`
2. вызывает `/e2ee/device/register` для новых сообщений
3. запускает QR linked-device flow
4. после approve дожидается `history sync / re-wrap`

---

## Вариант A. Быстрый старт

### Шаг 3A. Сгенерировать `device_id` и E2EE key pair

Frontend должен хранить:

- `device_id` стабильно для этого browser slot
- private key только локально
- public key для отправки на backend

Рекомендуемое поведение:

- `device_id` хранить в `localStorage` или `IndexedDB`
- private key хранить в `IndexedDB`
- не хранить private key в `localStorage`

Пример логики:

```ts
function getOrCreateWebDeviceId(): string {
  const key = "web_e2ee_device_id";
  let deviceId = localStorage.getItem(key);
  if (!deviceId) {
    deviceId = crypto.randomUUID();
    localStorage.setItem(key, deviceId);
  }
  return deviceId;
}
```

---

### Шаг 4A. Зарегистрировать текущий браузер как linked web device

Это главный endpoint для web bootstrap.

#### Request

```bash
curl -i -X POST "$BASE_URL/e2ee/device/register" \
  -H "Authorization: Bearer <ACCESS_TOKEN_FROM_LOGIN>" \
  -H "Content-Type: application/json" \
  -H "X-Web-Device-ID: 7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f" \
  -d '{
    "device_id": "7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f",
    "public_key": "BASE64_PUBLIC_KEY_FROM_BROWSER",
    "device_name": "Chrome on macOS",
    "platform": "web",
    "replace_other_web_sessions": true,
    "include_backup": true
  }'
```

#### Response `200 OK`

```json
{
  "device_id": "7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f",
  "message": "device registered successfully",
  "has_backup": true,
  "backup_algo": "pbkdf2-sha256",
  "backup": {
    "user_id": 42,
    "encrypted_private_key": "BASE64_BACKUP_CIPHERTEXT",
    "salt": "BASE64_SALT",
    "iv": "BASE64_IV",
    "kdf_algorithm": "pbkdf2-sha256",
    "kdf_params": {
      "iterations": 310000,
      "hash": "sha256",
      "key_length": 32
    },
    "version": 1,
    "backup_key_type": "x25519",
    "backup_key_format": "pkcs8-der-base64",
    "backup_key_scope": "device_identity",
    "backup_format_version": 1,
    "cipher_algorithm": "aes-256-gcm",
    "cipher_tag_embedded": true,
    "aad_mode": "none",
    "password_processing": "utf8-raw",
    "created_at": "2026-03-20T10:00:00Z",
    "updated_at": "2026-04-01T09:15:00Z"
  },
  "user": {
    "id": 42,
    "email": "user@example.com",
    "username": "askar",
    "first_name": "Askar",
    "last_name": "Abylkhaiyrov"
  },
  "current_login": {
    "ip": "92.38.48.9",
    "country": "KZ",
    "city": "Almaty",
    "user_agent": "Mozilla/5.0 ..."
  },
  "revoked_device_ids": [
    "old-web-device-1",
    "old-web-device-2"
  ],
  "replace_other_web_sessions": true,
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.linked-device-access",
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.linked-device-refresh",
  "token_type": "Bearer",
  "expires_in": 300,
  "expires_at": "2026-04-05T12:10:00Z"
}
```

Backend также ставит cookies:

- `Set-Cookie: access_token=...`
- `Set-Cookie: refresh_token=...`

Что frontend обязан сделать после ответа:

1. заменить bootstrap tokens из `/auth/login` на токены из `/e2ee/device/register`
2. считать именно этот ответ финальным web session bootstrap
3. сохранить `device_id`
4. если `revoked_device_ids` не пустой:
   показать пользователю, что старые браузеры завершены
5. использовать `current_login` для security UI
6. если `has_backup=true`:
   можно показать, что recovery backup существует:
   - restored identity key может открыть old history уже при login
   - fresh web key по-прежнему потребует QR history sync

Что делает backend при `replace_other_web_sessions=true`:

- отзывает другие `web` устройства пользователя
- отзывает их linked web JWT sessions
- создает security notification о новом web login

Поведение старого браузера:

- следующие HTTP запросы начнут получать `401`
- websocket после реконнекта тоже не пройдет авторизацию
- frontend старого браузера должен отправить пользователя на экран логина

---

### Шаг 5A. Подключить websocket уже новым linked-device token

Рекомендуемый вариант:

```text
ws://92.38.48.9:18080/api/v1/ws?token=<ACCESS_TOKEN_FROM_DEVICE_REGISTER>&web_device_id=<DEVICE_ID>
```

Пример:

```bash
echo 'ws://92.38.48.9:18080/api/v1/ws?token=<TOKEN>&web_device_id=7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f'
```

Рекомендация для frontend:

- во все websocket reconnection attempts передавать актуальный `access_token`
- передавать `web_device_id`
- на `401` или failed reconnect очищать локальную сессию и переводить пользователя на login screen

---

### Шаг 6A. Создать или получить личный диалог

#### Request

```bash
curl -X POST "$BASE_URL/conversations" \
  -H "Authorization: Bearer <ACCESS_TOKEN_FROM_DEVICE_REGISTER>" \
  -H "Content-Type: application/json" \
  -H "X-Web-Device-ID: 7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f" \
  -d '{
    "peer_id": 45
  }'
```

#### Response `200 OK`

```json
{
  "id": "8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5",
  "type": "personal",
  "created_by": 42,
  "created_at": "2026-04-05T12:00:00Z",
  "updated_at": "2026-04-05T12:00:00Z"
}
```

---

### Шаг 7A. Получить public keys собеседника и свои

Frontend обычно делает 2 запроса:

1. `GET /e2ee/keys/<peer_id>`
2. `GET /e2ee/keys/<my_user_id>`

#### Request

```bash
curl "$BASE_URL/e2ee/keys/45" \
  -H "Authorization: Bearer <ACCESS_TOKEN_FROM_DEVICE_REGISTER>"
```

#### Response `200 OK`

```json
{
  "user_id": 45,
  "keys": {
    "mobile-device-1": "BASE64_PUBLIC_KEY_A",
    "web-device-1": "BASE64_PUBLIC_KEY_B"
  }
}
```

Что делает frontend:

- генерирует случайный message key
- шифрует plaintext сообщения в `content`
- делает per-device wrapping этого message key
- кладет wrapped keys в `keys`

---

### Шаг 8A. Отправить E2EE сообщение

#### Request

```bash
curl -X POST "$BASE_URL/conversations/8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5/messages" \
  -H "Authorization: Bearer <ACCESS_TOKEN_FROM_DEVICE_REGISTER>" \
  -H "Content-Type: application/json" \
  -H "X-Web-Device-ID: 7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f" \
  -d '{
    "content": "{\"v\":1,\"alg\":\"aes-256-gcm\",\"iv\":\"BASE64_IV\",\"ciphertext\":\"BASE64_CIPHERTEXT\"}",
    "content_hash": "sha256-of-plaintext-or-empty",
    "keys": {
      "mobile-device-1": "{\"v\":1,\"alg\":\"x25519-hkdf-aes256gcm\",\"ciphertext\":\"...\"}",
      "web-device-1": "{\"v\":1,\"alg\":\"x25519-hkdf-aes256gcm\",\"ciphertext\":\"...\"}",
      "7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f": "{\"v\":1,\"alg\":\"x25519-hkdf-aes256gcm\",\"ciphertext\":\"...\"}"
    },
    "type": "text"
  }'
```

#### Response `201 Created`

```json
{
  "id": "e5f847b7-9e8d-4b0d-8be6-3c0c9e2ce500",
  "conversation_id": "8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5",
  "sender_id": 42,
  "content": "{\"v\":1,\"alg\":\"aes-256-gcm\",\"iv\":\"BASE64_IV\",\"ciphertext\":\"BASE64_CIPHERTEXT\"}",
  "content_hash": "sha256-of-plaintext-or-empty",
  "encrypted_keys": {
    "mobile-device-1": "{\"v\":1,\"alg\":\"x25519-hkdf-aes256gcm\",\"ciphertext\":\"...\"}",
    "web-device-1": "{\"v\":1,\"alg\":\"x25519-hkdf-aes256gcm\",\"ciphertext\":\"...\"}",
    "7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f": "{\"v\":1,\"alg\":\"x25519-hkdf-aes256gcm\",\"ciphertext\":\"...\"}"
  },
  "type": "text",
  "is_deleted": false,
  "created_at": "2026-04-05T12:02:00Z",
  "updated_at": "2026-04-05T12:02:00Z"
}
```

---

### Шаг 9A. Получить сообщения и расшифровать их на web

#### Request

```bash
curl "$BASE_URL/conversations/8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5/messages?limit=50&offset=0" \
  -H "Authorization: Bearer <ACCESS_TOKEN_FROM_DEVICE_REGISTER>" \
  -H "X-Web-Device-ID: 7df0f53b-1d8c-4f53-b1b2-1a2b3c4d5e6f"
```

#### Что делает frontend для каждого сообщения

1. берет `encrypted_keys[current_device_id]`
2. расшифровывает wrapped message key своим private key
3. этим message key расшифровывает `content`
4. показывает plaintext

Если `encrypted_keys[current_device_id]` нет:

- это сообщение не было зашифровано для текущего web device
- для нового браузера это нормально для старой истории
- для старой истории нужен QR history sync flow

---

## Вариант B. Доступ к старой истории на новом web device

### Шаг 3B. Зарегистрировать новый web device как отдельное устройство

Сначала web все равно должен пройти обычный quick-start flow:

- создать новый стабильный `device_id`
- сгенерировать новый локальный `X25519 key pair`
- вызвать `POST /e2ee/device/register`

Это нужно для того, чтобы новые сообщения уже начали шифроваться на текущий браузер.

### Шаг 4B. Запустить QR linked-device flow

Для старой истории canonical путь такой:

1. web создает QR pairing session
2. mobile подтверждает pairing
3. mobile делает `history sync / re-wrap`
4. backend начинает возвращать `encrypted_keys[current_web_device_id]` и для старых сообщений

Подробный flow описан в:

- `docs/e2ee_web_pairing_contract.md`
- `docs/e2ee_web_pairing_architecture.md`

### Шаг 5B. Как трактовать cloud backup в web UI

`GET /e2ee/backup` остается полезным, но его смысл ограничен recovery contract.

Если frontend запрашивает backup:

- нельзя угадывать `kdf_params`
- нельзя угадывать `backup_key_format`
- нужно использовать только explicit metadata из ответа backend
- если формат не поддерживается клиентом, такой backup нужно считать `unsupported`
- наличие backup не означает, что новый web browser сразу увидит старую историю

Пример ответа `GET /e2ee/backup`:

```json
{
  "user_id": 42,
  "encrypted_private_key": "BASE64_BACKUP_CIPHERTEXT",
  "salt": "BASE64_SALT",
  "iv": "BASE64_IV",
  "kdf_algorithm": "pbkdf2-sha256",
  "kdf_params": {
    "iterations": 310000,
    "hash": "sha256",
    "key_length": 32
  },
  "version": 1,
  "backup_key_type": "x25519",
  "backup_key_format": "pkcs8-der-base64",
  "backup_key_scope": "device_identity",
  "backup_format_version": 1,
  "cipher_algorithm": "aes-256-gcm",
  "cipher_tag_embedded": true,
  "aad_mode": "none",
  "password_processing": "utf8-raw",
  "created_at": "2026-03-20T10:00:00Z",
  "updated_at": "2026-04-01T09:15:00Z"
}
```

`204 No Content` по-прежнему означает только то, что backup не создан.

---

## Что делать frontend с `revoked_device_ids`

Если backend вернул:

```json
{
  "revoked_device_ids": [
    "old-web-device-1",
    "old-web-device-2"
  ]
}
```

то UI может показать:

- `Мы завершили предыдущие web-сессии для вашей безопасности`

Если пользователь НЕ хочет отключать старые браузеры:

```json
{
  "replace_other_web_sessions": false
}
```

Но для security policy web UI лучше оставлять `true`.

---

## Ошибки, которые frontend должен обрабатывать

### `POST /auth/login`

`401 Unauthorized`

```json
{
  "error": {
    "code": "INVALID_CREDENTIALS",
    "message": "invalid credentials"
  }
}
```

Действие frontend:

- показать ошибку логина
- не переходить к E2EE bootstrap

### `POST /e2ee/device/register`

`400 Bad Request`

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "invalid request body"
  }
}
```

или

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "public_key is required"
  }
}
```

Действие frontend:

- проверить `device_id`
- проверить `public_key`
- повторить bootstrap

### Любой защищенный endpoint после device rotation

`401 Unauthorized`

Это типичный случай для старого браузера, который уже вытеснен новым.

Действие frontend:

1. очистить локальную web session
2. закрыть websocket
3. показать экран `Session expired`
4. перевести пользователя на login

---

## Рекомендуемый порядок хранения на frontend

### Можно хранить

- `device_id` в `localStorage` или `IndexedDB`
- `publicKey` в `IndexedDB`
- `privateKey` в `IndexedDB`
- linked web tokens в memory storage или secure cookie flow

### Нельзя хранить

- master password в `localStorage`
- расшифрованный backup blob в `localStorage`

---

## Минимальный production-ready bootstrap sequence

1. `POST /auth/login`
2. `getOrCreateWebDeviceId()`
3. сгенерировать новый локальный `X25519 key pair`
4. `POST /e2ee/device/register`
5. заменить bootstrap tokens на returned linked-device tokens
6. подключить websocket через `/ws?token=...&web_device_id=...`
7. загрузить `/auth/me`
8. загрузить `/conversations`
9. при открытии чата:
    - `GET /e2ee/keys/<peer_id>`
    - `GET /e2ee/keys/<my_user_id>`
    - `GET /conversations/{conversation_id}/messages`
10. отправлять ciphertext через `POST /conversations/{conversation_id}/messages`
11. если нужна старая история:
    - создать QR pairing session
    - пройти approve на mobile
    - дождаться `history sync / re-wrap`

---

## Короткий вывод для frontend команды

- `/auth/login` это только вход пользователя
- `/e2ee/device/register` это обязательный web E2EE bootstrap
- canonical web session tokens нужно брать именно из `/e2ee/device/register`
- если `replace_other_web_sessions=true`, новый браузер автоматически вытесняет старые
- новый web browser должен иметь свой отдельный `X25519 key pair`
- backup это explicit recovery contract, но не shortcut для old history на новом web device
- если нужна старая E2EE-история, используйте QR linked-device flow и `history sync / re-wrap`
