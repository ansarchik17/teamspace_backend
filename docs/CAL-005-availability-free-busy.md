# CAL-005 — Доступность, график Free/Busy и алерты о пересечениях

Backend предоставляет полный функционал проверки доступности участников, построения интерактивного графика занятости (**Free/Busy**) и заблаговременного предупреждения о конфликтах расписания с разграничением уровней критичности (**error** для подтверждённых встреч, **warning** для статуса `tentative`). Все запросы требуют Bearer-токен.

---

## 1. Получение графика Free/Busy участников

Эндпоинт позволяет запросить интервалы занятости для нескольких участников за указанный временной диапазон, возвращает матрицу таймлайна с шагом слота (`slot_step_minutes`), общие свободные интервалы (`mutual_free_slots`) и алерты о пересечениях.

```http
POST /api/v1/calendar/events/free-busy
Content-Type: application/json
Authorization: Bearer <token>

{
  "start_time": "2026-09-28T09:00:00+05:00",
  "end_time": "2026-09-28T18:00:00+05:00",
  "attendee_ids": [10, 20, 35],
  "timezone": "Asia/Almaty",
  "slot_step_minutes": 30,
  "include_organizer": true,
  "exclude_event_id": null
}
```

> **Примечание**: Также поддерживаются алиасы `POST /api/v1/calendar/free-busy`, а также `GET /api/v1/calendar/events/free-busy` и `GET /api/v1/calendar/free-busy` с query-параметрами:
> `?from=2026-09-28T09:00:00Z&to=2026-09-28T18:00:00Z&user_ids=10,20,35&timezone=Asia/Almaty&slot_step_minutes=30`

### Параметры запроса

- **start_time** (обязательный): начало диапазона просмотра в формате RFC3339 (или `from` в GET).
- **end_time** (обязательный): конец диапазона просмотра в формате RFC3339 (или `to` в GET).
- **attendee_ids** / **user_ids** (опциональный): список ID проверяемых участников.
- **include_organizer** (опциональный, default: `true`): автоматически включать текущего пользователя в проверку.
- **timezone** (опциональный, default: `"Asia/Almaty"`): часовой пояс для привязки сетки таймлайна.
- **slot_step_minutes** (опциональный, default: `30`): шаг дискретизации сетки (от 5 до 240 мин).
- **exclude_event_id** (опциональный, UUID): ID встречи, которую нужно исключить из подсчёта занятости (удобно при редактировании существующего события).

Диапазон ограничен 90 днями, число участников — 100 (включая организатора), сетка — 5000 слотами и 100000 ячейками «слот × участник». При превышении лимитов возвращается `400`; сократите диапазон/список участников либо увеличьте шаг сетки. Неверные query-параметры, неизвестный часовой пояс и шаг вне 5–240 минут также возвращают `400`, без молчаливой подмены значений.

Названия чужих событий маскируются во всех `alerts` и `busy_intervals`: `Занято` или `Предварительно`; чужие `event_id` не возвращаются. `username` и `email` в `users` доступны только для текущего пользователя. `total_busy_minutes` считает объединение интервалов, включая tentative: пересечения не учитываются дважды.

### Пример успешного ответа (`200 OK`)

```json
{
  "start_time": "2026-09-28T09:00:00+05:00",
  "end_time": "2026-09-28T18:00:00+05:00",
  "timezone": "Asia/Almaty",
  "slot_step_minutes": 30,
  "users": [
    {
      "user_id": 10,
      "user_name": "Иван Иванов",
      "username": "ivan.ivanov",
      "email": "ivan@example.com",
      "avatar_url": "https://example.com/avatars/10.jpg",
      "total_busy_minutes": 60,
      "busy_intervals_count": 1,
      "tentative_intervals_count": 0,
      "busy_intervals": [
        {
          "start_time": "2026-09-28T11:00:00+05:00",
          "end_time": "2026-09-28T12:00:00+05:00",
          "status": "busy",
          "event_id": "73bc51a2-8fe0-4293-9c86-1df2aa9bb49b",
          "event_title": "Обсуждение архитектуры",
          "is_recurring": false
        }
      ]
    },
    {
      "user_id": 20,
      "user_name": "Петр Петров",
      "avatar_url": null,
      "total_busy_minutes": 30,
      "busy_intervals_count": 0,
      "tentative_intervals_count": 1,
      "busy_intervals": [
        {
          "start_time": "2026-09-28T14:30:00+05:00",
          "end_time": "2026-09-28T15:00:00+05:00",
          "status": "tentative",
          "event_id": null,
          "event_title": "Предварительно",
          "is_recurring": true
        }
      ]
    }
  ],
  "mutual_free_slots": [
    {
      "start_time": "2026-09-28T09:00:00+05:00",
      "end_time": "2026-09-28T11:00:00+05:00",
      "duration_minutes": 120
    },
    {
      "start_time": "2026-09-28T12:00:00+05:00",
      "end_time": "2026-09-28T14:30:00+05:00",
      "duration_minutes": 150
    },
    {
      "start_time": "2026-09-28T15:00:00+05:00",
      "end_time": "2026-09-28T18:00:00+05:00",
      "duration_minutes": 180
    }
  ],
  "timeline_grid": [
    {
      "start_time": "2026-09-28T11:00:00+05:00",
      "end_time": "2026-09-28T11:30:00+05:00",
      "status": "busy",
      "total_users": 2,
      "free_count": 1,
      "busy_count": 1,
      "tentative_count": 0,
      "occupancy_rate": 0.5,
      "available_user_ids": [20],
      "busy_user_ids": [10],
      "tentative_user_ids": [],
      "available_users": [{"user_id": 20, "full_name": "Петр Петров"}],
      "busy_users": [{"user_id": 10, "full_name": "Иван Иванов"}],
      "tentative_users": []
    }
  ],
  "alerts": [
    {
      "type": "overlap",
      "severity": "error",
      "user_id": 10,
      "user_name": "Иван Иванов",
      "event_id": "73bc51a2-8fe0-4293-9c86-1df2aa9bb49b",
      "event_title": "Обсуждение архитектуры",
      "start_time": "2026-09-28T11:00:00+05:00",
      "end_time": "2026-09-28T12:00:00+05:00",
      "overlap_minutes": 60,
      "message": "У вас уже запланировано событие: 'Обсуждение архитектуры' (11:00 - 12:00, наложение 60 мин)"
    },
    {
      "type": "tentative",
      "severity": "warning",
      "user_id": 20,
      "user_name": "Петр Петров",
      "event_id": null,
      "event_title": "Предварительно",
      "start_time": "2026-09-28T14:30:00+05:00",
      "end_time": "2026-09-28T15:00:00+05:00",
      "overlap_minutes": 30,
      "message": "Участник Петр Петров имеет предварительную запись (tentative, наложение 30 мин)"
    }
  ],
  "summary": {
    "total_users": 2,
    "all_free": false,
    "conflicted_users_count": 2,
    "total_slots_count": 18,
    "free_slots_count": 15,
    "busy_slots_count": 3
  }
}
```

### Защита приватности (Privacy Redaction)

- Если запрашивающий пользователь просматривает своё собственное расписание или встречу, организатором которой он является, он видит оригинальные `event_id` и `event_title`.
- Для других участников детали скрываются: `event_id = null`, название заменяется на `"Занято"` (для `accepted`/`pending`) либо `"Предварительно"` (для `tentative`).
- Временные интервалы начала и конца слота (`start_time`, `end_time`) сохраняются, чтобы фронтенд мог корректно отрисовать графические блоки в сетке без нарушения конфиденциальности.

---

## 2. Проверка доступности слота с алертами

```http
POST /api/v1/calendar/events/check-availability
Content-Type: application/json
Authorization: Bearer <token>

{
  "start_time": "2026-09-28T11:00:00Z",
  "end_time": "2026-09-28T12:00:00Z",
  "attendee_ids": [20, 35],
  "exclude_event_id": null,
  "timezone": "Asia/Almaty"
}
```

### Дополнения в ответе

В структуру ответа добавлены поля:
- `alerts` — массив объектов `CalendarConflictAlert` с подробными текстовыми сообщениями на русском языке.
- `has_hard_conflicts` (`bool`) — `true`, если есть хотя бы одно пересечение с подтверждённым событием (`accepted`/`pending`).
- `has_tentative_conflicts` (`bool`) — `true`, если есть пересечения со статусом `tentative`.

---

## 3. Предупреждение о конфликтах при редактировании событий

При изменении времени события (`start_time`, `end_time`) или списка участников (`attendee_ids`):

### Редактирование события целиком

```http
PUT /api/v1/calendar/events/{event_id}
PUT /api/v1/calendar/events/{event_id}?force=true
Content-Type: application/json
Authorization: Bearer <token>

{
  "start_time": "2026-09-28T11:00:00Z",
  "end_time": "2026-09-28T12:00:00Z"
}
```

- По умолчанию (`force=false` или параметр не указан): если новое время пересекается с событиями участников, запрос отклоняется с кодом `409 Conflict`:

```json
{
  "error": "calendar_conflict",
  "message": "В указанное время обнаружены конфликты расписания",
  "details": {
    "conflicts": [ ... ],
    "alerts": [ ... ],
    "suggested_slots": [ ... ]
  }
}
```

- Если передан `?force=true` (или `"force": true` в теле запроса), событие сохраняется, несмотря на наложение слотов.

### Редактирование одного повторения серии

```http
PUT /api/v1/calendar/events/{event_id}/occurrences/{occurrence_date}?force=true
```

Поддерживает аналогичную проверку конфликтов с параметром `?force=true` при переносе конкретного экземпляра повторяющегося события.

При переносе исключается только редактируемый экземпляр (включая его предыдущий перенос). Пересечение с другим экземпляром той же серии возвращает `409`, если не задан `force=true`.

---

## 4. Поддержка регулярных событий (RRULE) и исключений

1. **Разворачивание серий**: `CalendarAvailabilityService` автоматически разворачивает все повторяющиеся события (`DAILY`, `WEEKLY`, `MONTHLY`) участников с помощью парсера RRULE.
2. **Исключения (Exceptions)**:
   - Удалённые экземпляры серии (`deleted`) **не создают занятости** (слот считается свободным).
   - Перенесённые экземпляры серии (`modified`) освобождают исходное время и блокируют новое время.
3. **Статусы RSVP**:
   - `accepted` / `pending` создают статус `"busy"` и алерт уровня `"error"`.
   - `tentative` создаёт статус `"tentative"` и алерт уровня `"warning"`.
   - `declined` не создаёт занятости для данного участника.
