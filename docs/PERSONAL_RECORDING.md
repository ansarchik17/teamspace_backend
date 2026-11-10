# 🎙️ Personal Call Recording — Mobile Developer Guide

> **Версия:** 1.0  
> **Base URL:** `https://your-api.domain/api/v1`  
> **Auth:** `Authorization: Bearer <access_token>` — обязателен для всех запросов

---

## 📖 Концепция

Система персональных записей позволяет **каждому участнику** звонка независимо записывать аудио-трек.

Ключевые правила:
- Каждый пишет **свою** запись — файлы хранятся раздельно
- **Несколько** участников могут писать **одновременно** в одном звонке
- Запись может **остановить только её владелец** (HTTP 403 если чужая)
- `POST /start` — **идемпотентный**: повторный вызов вернёт существующую активную запись
- При завершении звонка все активные записи **автоматически** останавливаются
- Все участники звонка получают **WebSocket-событие** при каждом старте/стопе записи

---

## 🔄 Жизненный цикл записи

```
[Пользователь нажимает "Записать"]
        │
        ▼
POST /calls/recordings/personal/start
        │
        ▼
   status: "recording"  ──── WebSocket → все участники
        │
        ├── Пользователь нажимает "Стоп"
        │         │
        │         ▼
        │   POST /calls/recordings/personal/{id}/stop
        │         │
        │         ▼
        │    status: "stopped" ──── WebSocket → все участники
        │
        ├── Звонок завершился (room_finished)
        │         │
        │         ▼
        │    status: "stopped" (автоматически)
        │
        └── Egress завершился сам (LiveKit webhook)
                  │
                  ▼
           status: "completed" или "failed"
```

### Статусы записи

| Статус | Когда | Что значит |
|--------|-------|-----------|
| `recording` | После старта | Запись идёт прямо сейчас |
| `stopped` | После явного стопа / конца звонка | Остановлена, файл может обрабатываться |
| `completed` | После webhook от LiveKit | Файл готов, можно скачать |
| `failed` | Ошибка egress | Запись не удалась, см. `failure_reason` |

---

## 📡 API Endpoints

### 1. Запустить запись

**Когда вызывать:** Пользователь нажал кнопку "Начать запись" в UI.

```
POST /api/v1/calls/recordings/personal/start
Authorization: Bearer <token>
Content-Type: application/json
```

**Body:**
```json
{
  "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "room_name": "call_42_77_1711454400"
}
```

> `call_id` — UUID звонка (получаете при `POST /calls/start` или `POST /calls/accept`)  
> `room_name` — имя комнаты LiveKit (поле `room_name` из ответа на старт/джойн звонка)

**cURL:**
```bash
curl -X POST https://your-api.domain/api/v1/calls/recordings/personal/start \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9..." \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
    "room_name": "call_42_77_1711454400"
  }'
```

**✅ Ответ 200 OK — запись запущена:**
```json
{
  "recording_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "egress_id": "EG_HxyzAbcDef123",
  "owner_user_id": 42,
  "status": "recording",
  "started_at": "2026-03-26T11:00:00Z"
}
```

**✅ Ответ 200 OK — идемпотентный (запись уже была активна):**
```json
{
  "recording_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "egress_id": "EG_HxyzAbcDef123",
  "owner_user_id": 42,
  "status": "recording",
  "started_at": "2026-03-26T10:55:00Z"
}
```
> ⚠️ Оба ответа выглядят одинаково — разница только во времени `started_at`. Безопасно вызывать несколько раз.

**❌ Ответ 400 — невалидный запрос:**
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "invalid call_id format",
    "details": null
  }
}
```

**❌ Ответ 500 — ошибка LiveKit:**
```json
{
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "failed to start recording",
    "details": "connection to livekit egress server failed"
  }
}
```

---

### 2. Остановить запись

**Когда вызывать:** Пользователь нажал "Остановить запись".

```
POST /api/v1/calls/recordings/personal/{recording_id}/stop
Authorization: Bearer <token>
```

> `recording_id` — UUID из поля `recording_id` ответа на `/start`

**cURL:**
```bash
curl -X POST https://your-api.domain/api/v1/calls/recordings/personal/a1b2c3d4-e5f6-7890-abcd-ef1234567890/stop \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9..."
```

**✅ Ответ 200 OK — успешно остановлена:**
```json
{
  "recording_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "egress_id": "EG_HxyzAbcDef123",
  "status": "stopped",
  "stopped_at": "2026-03-26T11:05:00Z"
}
```

**✅ Ответ 200 OK — уже была остановлена (идемпотентный):**
```json
{
  "recording_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "egress_id": "EG_HxyzAbcDef123",
  "status": "completed",
  "stopped_at": "2026-03-26T11:04:55Z"
}
```

**❌ Ответ 403 — попытка остановить чужую запись:**
```json
{
  "error": {
    "code": "FORBIDDEN",
    "message": "you can only stop your own recording",
    "details": null
  }
}
```

**❌ Ответ 404 — запись не найдена:**
```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "recording not found",
    "details": null
  }
}
```

---

### 3. Активные записи в звонке

**Когда вызывать:**  
- При **входе в звонок** (join/rejoin) — чтобы сразу показать баннер если кто-то пишет  
- При **переподключении** (reconnect) — синхронизировать состояние UI

```
GET /api/v1/calls/recordings/personal/active?call_id={call_uuid}
Authorization: Bearer <token>
```

**cURL:**
```bash
curl "https://your-api.domain/api/v1/calls/recordings/personal/active?call_id=3fa85f64-5717-4562-b3fc-2c963f66afa6" \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9..."
```

**✅ Ответ 200 OK — есть активные записи:**
```json
{
  "active_count": 2,
  "active_recorders": [42, 77],
  "is_any_recording_active": true
}
```

**✅ Ответ 200 OK — записей нет:**
```json
{
  "active_count": 0,
  "active_recorders": [],
  "is_any_recording_active": false
}
```

> `active_recorders` — список `user_id` тех, кто сейчас пишет  
> `is_any_recording_active` — главный флаг для показа/скрытия баннера

---

### 4. Мои записи (история)

**Когда вызывать:** Пользователь открыл экран "Мои записи" или "История звонков с File".

```
GET /api/v1/calls/recordings/personal?limit=20&offset=0
Authorization: Bearer <token>
```

**cURL:**
```bash
curl "https://your-api.domain/api/v1/calls/recordings/personal?limit=10&offset=0" \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9..."
```

**✅ Ответ 200 OK:**
```json
{
  "recordings": [
    {
      "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
      "room_name": "call_42_77_1711454400",
      "owner_user_id": 42,
      "egress_id": "EG_HxyzAbcDef123",
      "status": "completed",
      "started_at": "2026-03-26T11:00:00Z",
      "stopped_at": "2026-03-26T11:05:00Z",
      "file_path": "recordings/personal/3fa85f64.../42-1711454400.ogg",
      "duration_sec": 300,
      "failure_reason": null
    },
    {
      "id": "b2c3d4e5-f6a7-8901-bcde-f12345678901",
      "call_id": "4fb96f75-6828-5673-c4gd-3d74a077bfb7",
      "room_name": "call_42_88_1711540800",
      "owner_user_id": 42,
      "egress_id": "EG_AbcXyzQwe456",
      "status": "failed",
      "started_at": "2026-03-25T09:00:00Z",
      "stopped_at": "2026-03-25T09:00:15Z",
      "file_path": null,
      "duration_sec": null,
      "failure_reason": "egress server connection timeout"
    }
  ]
}
```

> **Пагинация:** `limit` (макс. 100), `offset` — стандартный cursor по позиции  
> **Сортировка:** Новые записи идут первыми (`created_at DESC`)

---

### 5. Скачать файл записи

**Когда вызывать:** Пользователь нажал "Скачать" на конкретной записи.

```
GET /api/v1/calls/recordings/personal/{recording_id}/download
Authorization: Bearer <token>
```

> ⚠️ Доступно только когда `status = "completed"` и `file_path != null`

**cURL:**
```bash
curl -L https://your-api.domain/api/v1/calls/recordings/personal/a1b2c3d4-e5f6-7890-abcd-ef1234567890/download \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9..." \
  -o recording.mp4
```

**✅ Ответ 200 — стрим файла:**
```
HTTP/1.1 200 OK
Content-Type: audio/mp4
Content-Disposition: attachment; filename="42-1711454400.mp4"
Content-Length: 5242880

<binary audio data>
```

> Формат: `audio/mp4` (AAC кодек внутри MP4 контейнера)  
> Совместимость: iOS `AVPlayer`, Android `MediaPlayer` / `ExoPlayer`

**❌ Ответ 403 — попытка скачать чужую запись:**
```json
{
  "error": {
    "code": "FORBIDDEN",
    "message": "access denied: you are not the owner of this recording",
    "details": null
  }
}
```

**❌ Ответ 404 — файл ещё не готов (статус не completed):**
```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "recording file is not yet available (still recording or processing)",
    "details": null
  }
}
```

---

## 📲 WebSocket событие `call_recording_status_changed`

Это событие рассылается **всем участникам звонка** (в реальном времени) каждый раз когда кто-то **начинает** или **останавливает** запись.

### Структура события

```json
{
  "type": "call_recording_status_changed",
  "from": 42,
  "content": {
    "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
    "action": "started",
    "owner_user_id": 42,
    "active_count": 1,
    "is_any_recording_active": true,
    "ts": 1711454400
  },
  "timestamp": 1711454400
}
```

### Поля

| Поле | Тип | Описание |
|------|-----|---------|
| `type` | string | Всегда `"call_recording_status_changed"` |
| `from` | int64 | `user_id` того, кто изменил запись |
| `content.call_id` | string (UUID) | Идентификатор звонка |
| `content.action` | string | `"started"` или `"stopped"` |
| `content.owner_user_id` | int64 | Чья запись изменилась |
| `content.active_count` | int | Сколько записей **сейчас** активно |
| `content.is_any_recording_active` | bool | **Главный флаг** для UI |
| `content.ts` | int64 | Unix timestamp события |

### Примеры событий

**Кто-то начал запись:**
```json
{
  "type": "call_recording_status_changed",
  "from": 42,
  "content": {
    "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
    "action": "started",
    "owner_user_id": 42,
    "active_count": 1,
    "is_any_recording_active": true,
    "ts": 1711454400
  },
  "timestamp": 1711454400
}
```

**Второй участник тоже начал писать:**
```json
{
  "type": "call_recording_status_changed",
  "from": 77,
  "content": {
    "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
    "action": "started",
    "owner_user_id": 77,
    "active_count": 2,
    "is_any_recording_active": true,
    "ts": 1711454460
  },
  "timestamp": 1711454460
}
```

**Первый участник остановил запись (второй всё ещё пишет):**
```json
{
  "type": "call_recording_status_changed",
  "from": 42,
  "content": {
    "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
    "action": "stopped",
    "owner_user_id": 42,
    "active_count": 1,
    "is_any_recording_active": true,
    "ts": 1711454700
  },
  "timestamp": 1711454700
}
```

**Последняя запись остановлена — баннер скрываем:**
```json
{
  "type": "call_recording_status_changed",
  "from": 77,
  "content": {
    "call_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
    "action": "stopped",
    "owner_user_id": 77,
    "active_count": 0,
    "is_any_recording_active": false,
    "ts": 1711454800
  },
  "timestamp": 1711454800
}
```

---

## 🎨 Логика UI (рекомендации)

### Баннер "Идёт запись"

```
Показывать если:  content.is_any_recording_active == true
Скрывать если:   content.is_any_recording_active == false
```

Баннер нужно показывать **всем участникам** — даже тем, кто сам не пишет.  
Текст баннера: **"🔴 Звонок записывается"**

### Кнопка "Начать запись" / "Остановить"

- Кнопка **"Начать"** — активна если у текущего пользователя нет активной записи  
- Кнопка **"Стоп"** — активна если `my_recording.status == "recording"`  
- После нажатия "Стоп" показать лоадер, дождаться 200, обновить UI

### При входе в звонок (join / rejoin)

```
1. Подключились к звонку
2. GET /calls/{call_id}/recordings/personal/active
3. Если is_any_recording_active == true → показать баннер
4. Далее реагировать на WebSocket события
```

---

## 🔐 Права доступа

| Действие | Кто |
|----------|-----|
| Начать запись | Любой участник звонка |
| Остановить запись | Только `owner_user_id` (создатель записи) |
| Скачать файл | Только `owner_user_id` |
| Посмотреть список | Только свои записи |
| Посмотреть активных | Любой участник звонка |

---

## ⚠️ Edge Cases — важно знать

### 1. Параллельные записи
Несколько участников могут писать одновременно. `active_count` может быть > 1. Баннер показывается при `active_count >= 1`.

### 2. Автоостановка при конце звонка
При завершении звонка (`room_finished`) бэкенд автоматически переводит все активные записи в `stopped`. **WS-событие при этом не рассылается** (звонок уже закончился). Файлы могут обрабатываться некоторое время.

### 3. Файл доступен не сразу
После `POST /stop` статус становится `stopped`. Файл станет доступен (статус `completed`) через несколько секунд после обработки на стороне LiveKit. Проверяйте `status == "completed"` перед показом кнопки "Скачать".

### 4. Ошибка записи
Если LiveKit не смог завершить egress — статус станет `failed`. Поле `failure_reason` содержит описание ошибки. Показывать пользователю: "Запись не удалась".

### 5. Двойное нажатие "Старт"
Второй вызов `/start` вернёт ту же запись (идемпотентность). `recording_id` и `egress_id` совпадут с первым вызовом.

---

## 🧪 Полный сценарий тестирования

```bash
# Переменные
TOKEN="eyJhbGciOiJSUzI1NiJ9..."
BASE="https://your-api.domain/api/v1"
CALL_ID="3fa85f64-5717-4562-b3fc-2c963f66afa6"
ROOM="call_42_77_1711454400"

# 1. Запустить запись
curl -s -X POST $BASE/calls/recordings/personal/start \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"call_id\":\"$CALL_ID\",\"room_name\":\"$ROOM\"}" | jq .

# Сохранить recording_id из ответа
RECORDING_ID="a1b2c3d4-e5f6-7890-abcd-ef1234567890"

# 2. Проверить активные записи (должен быть active_count=1)
curl -s $BASE/calls/$CALL_ID/recordings/personal/active \
  -H "Authorization: Bearer $TOKEN" | jq .

# 3. Идемпотентный старт (должен вернуть ту же запись)
curl -s -X POST $BASE/calls/recordings/personal/start \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"call_id\":\"$CALL_ID\",\"room_name\":\"$ROOM\"}" | jq .

# 4. Остановить запись
curl -s -X POST $BASE/calls/recordings/personal/$RECORDING_ID/stop \
  -H "Authorization: Bearer $TOKEN" | jq .

# 5. Проверить статус после остановки (active_count=0)
curl -s $BASE/calls/$CALL_ID/recordings/personal/active \
  -H "Authorization: Bearer $TOKEN" | jq .

# 6. Посмотреть список своих записей
curl -s "$BASE/calls/recordings/personal?limit=5&offset=0" \
  -H "Authorization: Bearer $TOKEN" | jq .

# 7. Скачать файл (когда status=completed)
curl -L $BASE/calls/recordings/personal/$RECORDING_ID/download \
  -H "Authorization: Bearer $TOKEN" \
  -o my_recording.mp4

# 8. Тест 403 — попытка остановить чужую запись
OTHER_RECORDING_ID="bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
curl -s -X POST $BASE/calls/recordings/personal/$OTHER_RECORDING_ID/stop \
  -H "Authorization: Bearer $TOKEN" | jq .
# Ожидаем: {"error": {"code": "FORBIDDEN", ...}}
```

---

## 📋 Таблица всех endpoints

| Метод | URL | Описание |
|-------|-----|---------|
| `POST` | `/calls/recordings/personal/start` | Начать запись (идемпотентно) |
| `POST` | `/calls/recordings/personal/{recording_id}/stop` | Остановить запись (owner only) |
| `GET`  | `/calls/recordings/personal/active?call_id=uuid` | Активные записи в звонке |
| `GET`  | `/calls/recordings/personal` | Мои записи (paginated) |
| `GET`  | `/calls/recordings/personal/{recording_id}/download` | Скачать файл (owner only) |

---

## 🗂️ Swagger UI

Все endpoints доступны на:  
```
GET /swagger/index.html
```
Тег: **`calls`**
