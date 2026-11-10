# CAL-002: Создание событий

## Описание

Реализация полного цикла создания календарных событий с поддержкой участников, временных интервалов, описания, места проведения, валидации конфликтов расписания, сохранения слотов и автоматической рассылки инвайтов участникам.

## Компоненты

### 1. База данных

Система использует существующие таблицы, созданные в миграции `010_create_calendar.sql` и расширенные в `20260115234500_alter_table_events.sql`:

#### Таблица `events`
```sql
CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id),           -- Организатор события
    calendar_id UUID NOT NULL REFERENCES user_calendars(id), -- Календарь события
    title VARCHAR(255) NOT NULL,
    description TEXT,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    location VARCHAR(255),                                   -- Место проведения
    is_all_day BOOLEAN NOT NULL DEFAULT FALSE,
    is_online BOOLEAN NOT NULL DEFAULT FALSE,
    url TEXT,                                                -- Ссылка на внешний ресурс

    -- Повторяющиеся события
    recurrence_rule TEXT,                                    -- DAILY|WEEKLY|MONTHLY
    recurrence_interval INT NOT NULL DEFAULT 1,
    recurrence_until TIMESTAMPTZ,

    -- Онлайн-встречи
    meeting_provider VARCHAR(50),                            -- none|livekit|cisco
    meeting_room_id VARCHAR(255),
    meeting_url TEXT,
    livekit_room_name VARCHAR(255),
    cisco_meeting_id VARCHAR(255),
    cisco_join_url TEXT,
    online_meeting_status VARCHAR(50),

    color VARCHAR(20),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_events_user_id ON events(user_id);
CREATE INDEX idx_events_time_range ON events(start_time, end_time);
CREATE INDEX idx_events_calendar_id ON events(calendar_id);
```

#### Таблица `event_attendees`
```sql
CREATE TABLE event_attendees (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending|accepted|declined
    comment TEXT,                                    -- Комментарий участника
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ,
    PRIMARY KEY (event_id, user_id)
);

CREATE INDEX idx_event_attendees_user_status ON event_attendees (user_id, status);
CREATE INDEX idx_event_attendees_event_id ON event_attendees (event_id);
CREATE INDEX idx_event_attendees_user_id ON event_attendees(user_id);
```

#### Таблица `event_reminders`
```sql
CREATE TABLE event_reminders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    minutes_before INT NOT NULL,                     -- За сколько минут напомнить
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_sent_occurrence_start TIMESTAMPTZ,          -- Для повторяющихся событий
    last_sent_at TIMESTAMPTZ,
    UNIQUE (event_id, user_id)
);
```

### 2. API Endpoints

#### 2.1. Создание события
```
POST /api/v1/calendar/events
```

**Query Parameters:**
- `force` (boolean, optional) - Создать событие даже при наличии конфликтов расписания

**Request Body:**
```json
{
  "calendar_id": "uuid",
  "title": "Встреча с командой",
  "description": "Обсуждение планов на квартал",
  "start_time": "2024-09-25T10:00:00Z",
  "end_time": "2024-09-25T11:00:00Z",
  "location": "Офис, переговорная 2",
  "is_all_day": false,
  "is_online": true,
  "url": "https://example.com/meeting",

  "recurrence_rule": "WEEKLY",
  "recurrence_interval": 1,
  "recurrence_until": "2024-12-31T00:00:00Z",

  "color": "#2161F5",
  "attendee_ids": [123, 456, 789],
  "reminder_minutes": 15
}
```

**Обязательные поля:**
- `title` - Название события
- `start_time` - Время начала
- `end_time` - Время окончания

**Опциональные поля:**
- `calendar_id` - ID календаря (если не указан, используется дефолтный календарь пользователя)
- `description` - Описание события
- `location` - Место проведения
- `is_all_day` - Событие на весь день (default: false)
- `is_online` - Онлайн-встреча (default: false)
- `url` - Ссылка на внешний ресурс
- `recurrence_rule` - Правило повторения: `DAILY`, `WEEKLY`, `MONTHLY`
- `recurrence_interval` - Интервал повторения (default: 1)
- `recurrence_until` - До какой даты повторять
- `color` - Цвет события (если не указан, используется цвет календаря)
- `attendee_ids` - Список ID участников
- `reminder_minutes` - За сколько минут напомнить (5, 10, 15, 30, 60, 1440)

**Response (201 Created):**
```json
{
  "id": "event-uuid",
  "user_id": 100,
  "calendar_id": "calendar-uuid",
  "title": "Встреча с командой",
  "description": "Обсуждение планов на квартал",
  "start_time": "2024-09-25T10:00:00Z",
  "end_time": "2024-09-25T11:00:00Z",
  "location": "Офис, переговорная 2",
  "is_all_day": false,
  "is_online": true,
  "url": "https://example.com/meeting",
  "recurrence_rule": "WEEKLY",
  "recurrence_interval": 1,
  "recurrence_until": "2024-12-31T00:00:00Z",
  "color": "#2161F5",
  "meeting_provider": "livekit",
  "meeting_url": "https://meet.example.com/room-name",
  "livekit_room_name": "cal-event-uuid",
  "online_meeting_status": "active",
  "created_at": "2024-09-25T09:00:00Z",
  "updated_at": "2024-09-25T09:00:00Z",
  "is_organizer": true,
  "reminder_minutes": 15,
  "attendees": [
    {
      "event_id": "event-uuid",
      "user_id": 100,
      "status": "accepted",
      "created_at": "2024-09-25T09:00:00Z",
      "user": {
        "id": 100,
        "username": "organizer",
        "first_name": "Иван",
        "last_name": "Иванов"
      }
    },
    {
      "event_id": "event-uuid",
      "user_id": 123,
      "status": "pending",
      "created_at": "2024-09-25T09:00:00Z",
      "user": {
        "id": 123,
        "username": "attendee1",
        "first_name": "Петр",
        "last_name": "Петров"
      }
    }
  ]
}
```

**Error Response (409 Conflict):**
```json
{
  "error": "calendar_conflict",
  "message": "В указанное время обнаружены конфликты расписания",
  "details": {
    "conflicts": [
      {
        "user_id": 123,
        "user_name": "Петр Петров",
        "event_id": "other-event-uuid",
        "event_title": "Другая встреча",
        "start_time": "2024-09-25T09:30:00Z",
        "end_time": "2024-09-25T10:30:00Z",
        "status": "accepted",
        "overlap_minutes": 30
      }
    ],
    "suggested_slots": [
      {
        "start_time": "2024-09-25T11:00:00Z",
        "end_time": "2024-09-25T12:00:00Z",
        "score": 100,
        "label": "Все свободны"
      }
    ]
  }
}
```

#### 2.2. Проверка доступности участников
```
POST /api/v1/calendar/events/check-availability
```

**Request Body:**
```json
{
  "start_time": "2024-09-25T10:00:00Z",
  "end_time": "2024-09-25T11:00:00Z",
  "attendee_ids": [123, 456, 789],
  "exclude_event_id": "event-uuid",
  "timezone": "Asia/Almaty"
}
```

**Response:**
```json
{
  "is_available": false,
  "checked_users_count": 3,
  "conflicted_users_count": 1,
  "checked_user_ids": [123, 456, 789],
  "organizer_included": true,
  "conflicts": [
    {
      "user_id": 123,
      "user_name": "Петр Петров",
      "username": "attendee1",
      "event_id": "other-event-uuid",
      "event_title": "Другая встреча",
      "start_time": "2024-09-25T09:30:00Z",
      "end_time": "2024-09-25T10:30:00Z",
      "status": "accepted",
      "overlap_minutes": 30
    }
  ],
  "conflicts_by_user": [
    {
      "user_id": 123,
      "user_name": "Петр Петров",
      "conflicts": [...]
    }
  ],
  "suggested_slots": [
    {
      "start_time": "2024-09-25T11:00:00Z",
      "end_time": "2024-09-25T12:00:00Z",
      "score": 100,
      "label": "Все свободны",
      "available_user_ids": [123, 456, 789]
    }
  ]
}
```

#### 2.3. Поиск свободных слотов
```
POST /api/v1/calendar/events/suggest-times
```

**Request Body:**
```json
{
  "date": "2024-09-25",
  "timezone": "Asia/Almaty",
  "attendee_ids": [123, 456],
  "duration_minutes": 60,
  "workday_start": "09:00",
  "workday_end": "18:00",
  "slot_step_minutes": 15,
  "include_organizer": true,
  "limit": 10,
  "allow_partial": false,
  "preferred_start": "2024-09-25T10:00:00Z",
  "preferred_end": "2024-09-25T14:00:00Z"
}
```

**Response:**
```json
{
  "date": "2024-09-25",
  "timezone": "Asia/Almaty",
  "duration_minutes": 60,
  "checked_users_count": 3,
  "organizer_included": true,
  "workday_start": "09:00",
  "workday_end": "18:00",
  "suggested_slots": [
    {
      "type": "perfect",
      "start_time": "2024-09-25T11:00:00Z",
      "end_time": "2024-09-25T12:00:00Z",
      "timezone": "Asia/Almaty",
      "score": 100,
      "label": "Все свободны",
      "available_user_ids": [100, 123, 456],
      "available_users": [
        {"user_id": 100, "full_name": "Иван Иванов"},
        {"user_id": 123, "full_name": "Петр Петров"}
      ]
    }
  ],
  "partial_suggested_slots": [
    {
      "type": "partial",
      "start_time": "2024-09-25T14:00:00Z",
      "end_time": "2024-09-25T15:00:00Z",
      "score": 66,
      "label": "2/3 свободны",
      "available_user_ids": [100, 123],
      "conflicted_user_ids": [456]
    }
  ]
}
```

#### 2.4. Обновление события
```
PUT /api/v1/calendar/events/{event_id}
```

**Request Body:**
```json
{
  "title": "Обновленное название",
  "description": "Новое описание",
  "start_time": "2024-09-25T11:00:00Z",
  "end_time": "2024-09-25T12:00:00Z",
  "location": "Новое место",
  "attendee_ids": [123, 456, 789, 1000]
}
```

Все поля опциональны. Обновляются только переданные поля.

**Response (200 OK):**
```json
{
  "id": "event-uuid",
  ...
}
```

#### 2.5. Удаление события
```
DELETE /api/v1/calendar/events/{event_id}
```

**Response (200 OK):**
```json
{
  "message": "ok"
}
```

#### 2.6. Ответ на приглашение
```
PUT /api/v1/calendar/events/{event_id}/respond
```

**Request Body:**
```json
{
  "status": "accepted",
  "comment": "Буду обязательно!"
}
```

**Допустимые статусы:**
- `accepted` - Принято
- `declined` - Отклонено

**Response (200 OK):**
```json
{
  "message": "ok"
}
```

#### 2.7. Установка напоминания
```
PUT /api/v1/calendar/events/{event_id}/reminder
```

**Request Body:**
```json
{
  "minutes_before": 15
}
```

**Допустимые значения:**
- 5, 10, 15, 30, 60, 1440 (1 день)

**Response (200 OK):**
```json
{
  "message": "ok"
}
```

#### 2.8. Удаление напоминания
```
DELETE /api/v1/calendar/events/{event_id}/reminder
```

**Response (200 OK):**
```json
{
  "message": "ok"
}
```

## Примеры использования

### Создание простого события
```bash
curl -X POST https://api.example.com/api/v1/calendar/events \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Встреча с клиентом",
    "description": "Обсуждение проекта",
    "start_time": "2024-09-25T14:00:00Z",
    "end_time": "2024-09-25T15:00:00Z",
    "location": "Офис клиента",
    "attendee_ids": [123, 456]
  }'
```

### Создание онлайн-встречи с напоминанием
```bash
curl -X POST https://api.example.com/api/v1/calendar/events \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Zoom-встреча",
    "start_time": "2024-09-25T10:00:00Z",
    "end_time": "2024-09-25T11:00:00Z",
    "is_online": true,
    "attendee_ids": [123, 456, 789],
    "reminder_minutes": 15
  }'
```

### Создание повторяющегося события
```bash
curl -X POST https://api.example.com/api/v1/calendar/events \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Еженедельный стендап",
    "start_time": "2024-09-25T09:00:00Z",
    "end_time": "2024-09-25T09:30:00Z",
    "recurrence_rule": "WEEKLY",
    "recurrence_interval": 1,
    "recurrence_until": "2024-12-31T00:00:00Z",
    "attendee_ids": [123, 456, 789]
  }'
```

### Проверка доступности перед созданием
```bash
# Шаг 1: Проверяем доступность
curl -X POST https://api.example.com/api/v1/calendar/events/check-availability \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "start_time": "2024-09-25T14:00:00Z",
    "end_time": "2024-09-25T15:00:00Z",
    "attendee_ids": [123, 456]
  }'

# Шаг 2: Если is_available = true, создаем событие
curl -X POST https://api.example.com/api/v1/calendar/events \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Встреча",
    "start_time": "2024-09-25T14:00:00Z",
    "end_time": "2024-09-25T15:00:00Z",
    "attendee_ids": [123, 456]
  }'
```

### Поиск свободных слотов и создание
```bash
# Шаг 1: Ищем свободные слоты
curl -X POST https://api.example.com/api/v1/calendar/events/suggest-times \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "date": "2024-09-25",
    "timezone": "Asia/Almaty",
    "attendee_ids": [123, 456],
    "duration_minutes": 60,
    "workday_start": "09:00",
    "workday_end": "18:00",
    "limit": 5
  }'

# Шаг 2: Выбираем подходящий слот из suggested_slots и создаем событие
curl -X POST https://api.example.com/api/v1/calendar/events \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Встреча",
    "start_time": "2024-09-25T11:00:00Z",
    "end_time": "2024-09-25T12:00:00Z",
    "attendee_ids": [123, 456]
  }'
```

### Принятие приглашения
```bash
curl -X PUT https://api.example.com/api/v1/calendar/events/{event_id}/respond \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "accepted",
    "comment": "Буду обязательно"
  }'
```

### Отклонение приглашения
```bash
curl -X PUT https://api.example.com/api/v1/calendar/events/{event_id}/respond \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "declined",
    "comment": "К сожалению, занят в это время"
  }'
```

## Особенности реализации

### 1. Валидация временных интервалов

**Базовые проверки:**
- `end_time` должен быть строго позже `start_time`; нулевой интервал отклоняется
- Для повторяющихся событий `recurrence_until` не может быть раньше `start_time`
- Участники должны существовать и быть доступны организатору по правилам области пользователей

**Проверка конфликтов расписания:**
Система автоматически проверяет занятость всех участников (включая организатора):
- Загружаются все события участников в указанном временном интервале
- Проверяются пересечения с учетом статусов (`accepted` события учитываются, `declined` - нет)
- Если обнаружены конфликты, возвращается `409 Conflict`. Для чужих встреч
  раскрывается только факт занятости, без названия, ID и точного интервала события
- Можно принудительно создать событие с параметром `?force=true`

**Расчет предложенных слотов:**
При конфликте система автоматически предлагает альтернативные временные слоты:
- Учитывается рабочее время (по умолчанию 09:00-18:00)
- Поиск ведется в пределах того же дня
- Слоты оцениваются по доступности участников
- Возвращается до 5 лучших вариантов

### 2. Сохранение и управление участниками

**Автоматическое добавление организатора:**
- Организатор автоматически добавляется в список участников
- Его статус сразу устанавливается как `accepted`
- Дублирование участников предотвращается на уровне базы данных (PRIMARY KEY)

**Статусы участников:**
- `pending` - Приглашение отправлено, ожидается ответ
- `accepted` - Участник принял приглашение
- `declined` - Участник отклонил приглашение

**Batch Insert:**
Участники добавляются пакетной операцией через `pgx.CopyFrom` для производительности:
```go
// Псевдокод
rows := [][]any{
  {eventID, organizerID, "accepted", time.Now()},
  {eventID, attendee1ID, "pending", time.Now()},
  {eventID, attendee2ID, "pending", time.Now()},
}
tx.CopyFrom(ctx, "event_attendees", columns, rows)
```

### 3. Рассылка инвайтов

**Уведомления в системе:**
При создании события backend синхронно пытается сохранить in-app уведомление
`calendar_invite` для каждого уникального участника (кроме организатора) до ответа
`201`. Сервис уведомлений также пытается отправить push. Ошибки доставки логируются;
они не отменяют уже созданное событие. Для гарантированной доставки при сбое БД
уведомлений потребуется отдельный transactional outbox.
- Метаданные включают: `event_id`, `start_time`, `location`

**Email:** Отправка обычного email зависит от настройки `MAIL_NOTIFICATION_TYPES`
и доступности почтового сервиса. Email ставится в обработку через сервис уведомлений;
отдельный `.ics`-инвайт при обычном создании события не гарантируется. Ошибка
push/email не отменяет уже сохранённое событие и in-app приглашение.

### 4. Напоминания

**Создание напоминаний:**
- Напоминания создаются через параметр `reminder_minutes` при создании события
- Применяются ко всем участникам (включая организатора)
- Используется `UPSERT` для предотвращения дублирования

**Допустимые интервалы:**
- 5 минут
- 10 минут
- 15 минут (популярный вариант)
- 30 минут
- 60 минут (1 час)
- 1440 минут (1 день)

**Обработка повторяющихся событий:**
Для повторяющихся событий система отслеживает:
- `last_sent_occurrence_start` - время начала последнего occurrence, для которого было отправлено напоминание
- `last_sent_at` - время отправки напоминания
- Это предотвращает повторную отправку одного и того же напоминания

### 5. Онлайн-встречи

**Автоматическое создание room:**
Если `is_online: true`, система автоматически:
1. Определяет подходящий провайдер (LiveKit, Cisco, или none)
2. Создает комнату для встречи
3. Генерирует URL для подключения
4. Сохраняет данные в полях:
   - `meeting_provider`
   - `meeting_url`
   - `livekit_room_name` или `cisco_meeting_id`

**Провайдеры:**
- **LiveKit**: Для внутренних встреч, до 50 участников
- **Cisco Webex**: Для крупных встреч или внешних участников
- **none**: Если интеграции недоступны

**Deferred Provisioning:**
Для Cisco провайдера создание комнаты происходит асинхронно в фоне, чтобы не задерживать ответ API.

### 6. Календари и цвета

**Выбор календаря:**
- Если `calendar_id` не указан, используется дефолтный календарь пользователя
- Система автоматически создает дефолтный календарь при первом использовании
- Можно создать несколько календарей (Личный, Рабочий, и т.д.)

**Цвета событий:**
- Если цвет не указан при создании события, используется цвет календаря
- Цвет календаря можно изменить в настройках
- Формат: hex-код (#RRGGBB)

### 7. Повторяющиеся события

**Типы повторений:**
- `DAILY` - Каждый день
- `WEEKLY` - Каждую неделю (в тот же день недели)
- `MONTHLY` - Каждый месяц (в то же число)

**Интервал повторения:**
- `recurrence_interval: 1` - Каждый период (каждый день/неделю/месяц)
- `recurrence_interval: 2` - Каждый второй период
- И так далее

**Окончание повторений:**
- `recurrence_until` - Дата окончания повторений
- Если не указана, событие повторяется бесконечно (не рекомендуется)

**Отображение в календарной сетке:**
Повторяющиеся события автоматически разворачиваются в отдельные occurrence при запросе календарной сетки в заданном диапазоне дат.

### 8. Транзакционность

Создание записи события, участников и напоминаний выполняется в одной транзакции:
1. Создание/обновление записи события
2. Добавление участников
3. Создание напоминаний

При ошибке этих DB-операций транзакция откатывается. Уведомления создаются после
коммита события и не входят в эту транзакцию.

### 9. Гамификация

Система интегрирована с модулем гамификации и отслеживает:
- `ActionMeetingCreated` - За создание встречи
- `ActionEventReminder` - За установку напоминания

События публикуются в RabbitMQ для асинхронной обработки.

### 10. Синхронизация с внешними календарями

После создания события система автоматически синхронизирует его с подключенными внешними календарями:
- Google Calendar
- Apple Calendar (iCloud)

Синхронизация происходит асинхронно и не блокирует основную операцию.

## Интеграция с фронтендом

### Типичный флоу создания события

1. **Пользователь открывает форму создания события**
   - Фронтенд отображает форму с полями: название, время, участники, место
   - Загружает список календарей пользователя (`GET /api/v1/calendar/calendars/settings`)

2. **Пользователь выбирает время и участников**
   - При изменении времени или участников фронтенд вызывает проверку доступности
   - `POST /api/v1/calendar/events/check-availability`
   - Отображает конфликты или галочку "Все свободны"

3. **Если есть конфликты**
   - Фронтенд предлагает альтернативные слоты из `suggested_slots`
   - Пользователь может выбрать предложенный слот или нажать "Найти еще"
   - При нажатии "Найти еще" вызывается `POST /api/v1/calendar/events/suggest-times`

4. **Создание события**
   - Пользователь нажимает "Создать"
   - `POST /api/v1/calendar/events`
   - При успехе фронтенд показывает уведомление и обновляет календарную сетку

5. **Если 409 Conflict**
   - Фронтенд показывает модальное окно с конфликтами
   - Предлагает использовать `?force=true` или выбрать другое время

### Обработка приглашений

1. **Получение приглашения**
   - Пользователь получает push/in-app уведомление
   - Клик на уведомление открывает детали события

2. **Просмотр деталей**
   - `GET /api/v1/calendar/events/{event_id}`
   - Отображается информация о событии, участниках, времени

3. **Ответ на приглашение**
   - Кнопки "Принять" / "Отклонить"
   - `PUT /api/v1/calendar/events/{event_id}/respond`
   - Опциональное поле для комментария

4. **Установка напоминания**
   - Dropdown с вариантами: 5, 10, 15, 30 мин, 1 час, 1 день
   - `PUT /api/v1/calendar/events/{event_id}/reminder`

### Отображение в календарной сетке

События отображаются через существующий эндпоинт:
```
POST /api/v1/calendar/grid
```

События автоматически группируются по календарям и окрашиваются в соответствующие цвета.

## Тестирование

### Unit-тесты

Покрыть тестами следующие сценарии:

1. **Валидация:**
   - `end_time` раньше `start_time` → ошибка
   - Несуществующие участники → ошибка
   - Некорректный `recurrence_rule` → ошибка

2. **Создание события:**
   - Успешное создание базового события
   - Создание с участниками
   - Создание с напоминанием
   - Создание повторяющегося события
   - Создание онлайн-встречи

3. **Проверка конфликтов:**
   - Конфликт с существующим событием → `409 Conflict`
   - Конфликт с `force=true` → успех
   - Нет конфликтов → успех

4. **Участники:**
   - Организатор автоматически добавлен как `accepted`
   - Остальные участники добавлены как `pending`
   - Дублирование участников предотвращается

### Интеграционные тесты

1. **E2E создание события:**
   - Создать событие → проверить в БД
   - Проверить, что участники добавлены
   - Проверить, что уведомления отправлены

2. **Обновление события:**
   - Создать → обновить → проверить изменения
   - Добавить участников → проверить уведомления

3. **Ответ на приглашение:**
   - Создать событие с участниками
   - От имени участника принять приглашение
   - Проверить статус в БД

4. **Напоминания:**
   - Создать событие с напоминанием
   - Проверить запись в `event_reminders`
   - Обновить напоминание → проверить UPSERT

### Тестирование API

```bash
# 1. Создание простого события
POST /api/v1/calendar/events
{"title":"Test","start_time":"2024-09-26T10:00:00Z","end_time":"2024-09-26T11:00:00Z"}
Ожидание: 201 Created

# 2. Создание с конфликтом
POST /api/v1/calendar/events
{"title":"Conflict","start_time":"2024-09-26T10:30:00Z","end_time":"2024-09-26T11:30:00Z","attendee_ids":[123]}
Ожидание: 409 Conflict (если у участника есть событие в это время)

# 3. Создание с force
POST /api/v1/calendar/events?force=true
{"title":"Forced","start_time":"2024-09-26T10:30:00Z","end_time":"2024-09-26T11:30:00Z","attendee_ids":[123]}
Ожидание: 201 Created

# 4. Проверка доступности
POST /api/v1/calendar/events/check-availability
{"start_time":"2024-09-26T14:00:00Z","end_time":"2024-09-26T15:00:00Z","attendee_ids":[123]}
Ожидание: 200 OK, is_available: true/false

# 5. Ответ на приглашение
PUT /api/v1/calendar/events/{event_id}/respond
{"status":"accepted"}
Ожидание: 200 OK

# 6. Установка напоминания
PUT /api/v1/calendar/events/{event_id}/reminder
{"minutes_before":15}
Ожидание: 200 OK
```

## Безопасность

1. **Авторизация:**
   - Все эндпоинты требуют Bearer токена
   - Пользователь может создавать события только от своего имени

2. **Валидация участников:**
   - Проверка существования всех участников
   - Защита от SQL-инъекций через параметризованные запросы

3. **Права доступа:**
   - Обновить/удалить событие может только организатор
   - Ответить на приглашение может только участник
   - Просмотреть событие могут только участники

4. **Rate Limiting:**
   - Рекомендуется ограничить создание событий (например, 100 событий в час на пользователя)
   - Защита от спама приглашениями

## Метрики и мониторинг

Рекомендуется отслеживать:

1. **Производительность:**
   - Время создания события (P50, P95, P99)
   - Количество событий с конфликтами
   - Процент использования `force=true`

2. **Бизнес-метрики:**
   - Количество созданных событий в день/неделю/месяц
   - Среднее количество участников на событие
   - Процент принятых/отклоненных приглашений
   - Популярность интервалов напоминаний

3. **Ошибки:**
   - 409 Conflict rate
   - Failed email deliveries
   - Failed notification deliveries

## Дальнейшее развитие

Возможные улучшения в будущих итерациях:

1. **Делегирование:**
   - Возможность назначить другого пользователя организатором
   - Co-hosts для событий

2. **Расширенная периодичность:**
   - Повторение по дням недели (каждые ПН, СР, ПТ)
   - Повторение по последнему дню месяца
   - Исключения из повторений

3. **Интеграция с календарными приложениями:**
   - Экспорт в .ics формате
   - Subscription URL для внешних клиентов

4. **Расширенные уведомления:**
   - Напоминание за N дней/часов
   - Напоминание после события (для фиксации итогов)
   - Настройка каналов уведомлений (email/push/SMS)

5. **Аналитика:**
   - Статистика по встречам
   - Heat-map занятости
   - Рекомендации по оптимальному времени встреч

---

**Статус:** Базовое создание реализовано; гарантированная доставка email/push требует отдельного outbox
**Версия:** 1.0
**Дата:** 2024-09-25
