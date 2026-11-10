# CAL-007: Интеграции — Напоминания, Часовые Пояса и Создание Ссылки QMeet

## 1. Обзор задачи (Scope & Purpose)
Реализация сервисной интеграции календаря со следующими возможностями:
1. **Автоматическая ссылка на звонок QMeet**:
   - Автоматическая генерация уникальной комнаты звонка (`qmeet-<slug>-<event_id>`) и URL при создании онлайн-события или вызове специального эндпоинта.
   - Возможность генерации / обновления ссылки QMeet для любого существующего события календаря (`POST /api/v1/calendar/events/:id/qmeet`).
   - Поддержка провайдера `"qmeet"` в качестве первого класса наряду с LiveKit и Cisco.
2. **Часовые пояса (Timezones)**:
   - Добавление колонки `timezone` (по умолчанию `Asia/Almaty`) в таблицу `events`.
   - Поддержка IANA-часовых поясов с встроенной `time/tzdata` базой (гарантия работы на Windows/Linux/Docker).
   - Эндпоинт `GET /api/v1/calendar/timezones` для получения корпоративных и мировых часовых поясов с динамическим смещением относительно UTC.
   - Эндпоинт `POST /api/v1/calendar/timezone/convert` для пересчета времени встречи между произвольными часовыми поясами.
   - Поддержка параметров `?timezone=...` или `?tz=...` в списках и деталях событий для автоматического расчета полей `start_time_local` и `end_time_local`.
3. **Напоминания и Push-уведомления (Reminders & Rich Push)**:
   - Локализованный обратный отсчет на русском языке (`"через 15 минут"`, `"через 1 час"`, `"прямо сейчас"`).
   - Локализация времени встречи по часовому поясу участника/события.
   - Rich Push метаданные (`action: "join_call"`, `meeting_url`, `deep_link`, `room_id`) для прямого открытия видеоконференции QMeet из мобильного push-уведомления или веб-шторки.
   - Эндпоинты `GET /api/v1/calendar/events/:id/reminder` и `GET /api/v1/calendar/reminders/presets`.

---

## 2. Архитектура и миграции базы данных

### 2.1 Миграция `20260929000500_calendar_integrations_tz_qmeet.sql`
```sql
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS timezone VARCHAR(100) NOT NULL DEFAULT 'Asia/Almaty';

CREATE INDEX IF NOT EXISTS idx_events_timezone ON events(timezone);
```

### 2.2 Модели данных (Domain)
- **`domain.Event`**:
  - `Timezone string`: IANA таймзона (напр. `"Asia/Almaty"`).
  - `StartTimeLocal *string`: RFC3339 локальное время начала в запрошенной таймзоне.
  - `EndTimeLocal *string`: RFC3339 локальное время окончания в запрошенной таймзоне.
- **`domain.CreateQMeetLinkResponse`**:
  ```json
  {
    "event_id": "a9c046a6-fdf6-48eb-b1df-ee9c6143c490",
    "meeting_provider": "qmeet",
    "room_name": "qmeet-planning-sync-a9c046a6",
    "meeting_url": "https://meet.qmeet.kz/web/calls/calendar/qmeet-planning-sync-a9c046a6",
    "status": "created",
    "is_online": true
  }
  ```
- **`domain.CalendarTimezoneItem`**:
  ```json
  {
    "id": "Asia/Almaty",
    "name": "Алматы, Астана (UTC+5)",
    "city": "Алматы",
    "country": "Казахстан",
    "offset": "+05:00",
    "offset_minutes": 300,
    "is_default": true
  }
  ```
- **`domain.TimezoneConvertResponse`**:
  ```json
  {
    "source": {
      "timezone": "Asia/Almaty",
      "display_name": "Алматы, Астана (UTC+5)",
      "offset": "+05:00",
      "offset_minutes": 300,
      "start_time": "2026-09-28T15:00:00+05:00",
      "end_time": "2026-09-28T16:00:00+05:00",
      "start_time_formatted": "2026-09-28 15:00",
      "end_time_formatted": "2026-09-28 16:00",
      "start_time_iso": "2026-09-28T15:00:00+05:00",
      "end_time_iso": "2026-09-28T16:00:00+05:00"
    },
    "targets": [
      {
        "timezone": "Europe/Moscow",
        "display_name": "Москва, Санкт-Петербург (UTC+3)",
        "offset": "+03:00",
        "offset_minutes": 180,
        "start_time": "2026-09-28T13:00:00+03:00",
        "end_time": "2026-09-28T14:00:00+03:00",
        "start_time_formatted": "2026-09-28 13:00",
        "end_time_formatted": "2026-09-28 14:00",
        "start_time_iso": "2026-09-28T13:00:00+03:00",
        "end_time_iso": "2026-09-28T14:00:00+03:00"
      }
    ]
  }
  ```

---

## 3. Спецификация API эндпоинтов

### 3.1 Генерация ссылки QMeet
- **URL**: `POST /api/v1/calendar/events/:id/qmeet`
- **Авторизация**: Bearer JWT; изменить видеовстречу может только организатор.
- **Успешный ответ**: `200 OK` (`domain.CreateQMeetLinkResponse`)
- **Поведение**:
  - Генерирует стабильное имя комнаты `qmeet-<полный UUID события>`, не зависящее от заголовка. Повторный запрос сохраняет существующую активную ссылку QMeet.
  - Строит прямую ссылку для подключения.
  - Атомарно сохраняет `meeting_provider = 'qmeet'`, `is_online = true`, `online_meeting_status = 'created'`.
  - Рассылает WebSocket и RabbitMQ событие `calendar.event.meeting_updated`.

### 3.2 Список поддерживаемых часовых поясов
- **URL**: `GET /api/v1/calendar/timezones` (доступен как авторизованным, так и публичным клиентам)
- **Успешный ответ**: `200 OK` (`[]domain.CalendarTimezoneItem`)
- **Поведение**: возвращает актуальные смещения UTC с учетом сезонного времени (DST).

### 3.3 Пересчет времени между часовыми поясами
- **URL**: `POST /api/v1/calendar/timezone/convert` (или `/timezones/convert`)
- **Доступ**: при использовании `event_id` требуется право просмотра события; постороннему пользователю возвращается 403.
- **Валидация**: от 1 до 50 целевых IANA TZ; `end_time > start_time`. Неизвестные TZ и серверная зона `Local` возвращают 400. TZ при создании/редактировании и query `timezone`/`tz` также валидируются.
- **Тело запроса**:
  ```json
  {
    "event_id": "a9c046a6-fdf6-48eb-b1df-ee9c6143c490",
    "target_timezones": ["Europe/Moscow", "UTC", "Asia/Tashkent", "Asia/Bishkek"]
  }
  ```
  *Либо с явным указанием времени:*
  ```json
  {
    "start_time": "2026-09-28T10:00:00Z",
    "end_time": "2026-09-28T11:00:00Z",
    "source_timezone": "Asia/Almaty",
    "target_timezones": ["Europe/Moscow", "UTC"]
  }
  ```
- **Успешный ответ**: `200 OK` (`domain.TimezoneConvertResponse`).

### 3.4 Пресеты напоминаний
- **URL**: `GET /api/v1/calendar/reminders/presets`
- **Успешный ответ**: `200 OK`
  ```json
  [
    {"minutes_before": 0, "label": "В момент начала", "description": "Уведомление отправляется ровно в момент начала встречи"},
    {"minutes_before": 5, "label": "За 5 минут", "description": "Краткое напоминание перед началом"},
    {"minutes_before": 10, "label": "За 10 минут", "description": "Время подготовиться к созвону"},
    {"minutes_before": 15, "label": "За 15 минут (рекомендуется)", "description": "Стандартное корпоративное напоминание"},
    {"minutes_before": 30, "label": "За 30 минут", "description": "Заблаговременное напоминание"},
    {"minutes_before": 60, "label": "За 1 час", "description": "За 1 час до начала встречи"},
    {"minutes_before": 120, "label": "За 2 часа", "description": "За 2 часа до начала встречи"},
    {"minutes_before": 1440, "label": "За 1 день", "description": "За 24 часа до запланированного события"}
  ]
  ```

### 3.5 Просмотр напоминания события
- **URL**: `GET /api/v1/calendar/events/:id/reminder`
- **Успешный ответ**: `200 OK` (`domain.EventReminderInfo`).

---

## 4. Rich Push-уведомления и фоновый воркер
1. Фоновый воркер `checkAndSendCustomReminders` каждые 30 секунд проверяет запланированные напоминания с защитой от дубликатов (`TryMarkReminderSent`).
2. В `sendCustomReminder` формируются расширенные уведомления:
   - **Заголовок**: `Напоминание: «Планирование спринта»`
   - **Текст (Online/QMeet)**: `Встреча «Планирование спринта» начнется через 15 минут (в 15:00). Ссылка QMeet готова к подключению`
   - **Текст (Offline)**: `Встреча «Планирование спринта» начнется через 15 минут (в 15:00). Локация: Переговорная 3A`
   - **Metadata push payload**:
     - `action: "join_call"`
     - `meeting_url: "https://meet.qmeet.kz/web/calls/calendar/qmeet-..."`
     - `deep_link: "https://meet.qmeet.kz/web/calls/calendar/qmeet-..."`
     - `room_id: "qmeet-..."`
     - `countdown_text: "через 15 минут"`
     - `timezone: "Asia/Almaty"`
     - `start_time_local: "2026-09-28T15:00:00+05:00"`

---

## 5. Тестирование
Все юнит-тесты покрывают:
- Определение и нормализацию комнат QMeet (`calendar_qmeet_test.go`).
- Генерацию списка часовых поясов, динамические смещения и конвертацию (`calendar_timezone_test.go`).
- Форматирование обратного отсчета на русском языке (`TestFormatCountdownRU`).
- Роутинг и хендлеры API (`calendar_integrations_handler_test.go`).
