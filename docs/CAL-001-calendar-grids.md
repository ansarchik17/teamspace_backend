# CAL-001: Календарные сетки

## Описание

Реализация календарных сеток для отображения событий в различных видах (День/Неделя/Месяц) с поддержкой личных и групповых календарей, а также управления видимостью календарей.

## Компоненты

### 1. База данных

#### Таблица `user_calendar_settings`
```sql
CREATE TABLE user_calendar_settings (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    calendar_id UUID NOT NULL REFERENCES user_calendars(id) ON DELETE CASCADE,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    display_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, calendar_id)
);
```

### 2. API Endpoints

#### 2.1. Получение календарной сетки
```
POST /api/v1/calendar/grid
```

**Request Body:**
```json
{
  "view": "day|week|month",
  "date": "2024-09-24",
  "timezone": "Asia/Almaty",
  "calendar_ids": ["uuid1", "uuid2"],
  "include_group": true
}
```

**Response:**
```json
{
  "view": "week",
  "date": "2024-09-24",
  "start_date": "2024-09-23T00:00:00Z",
  "end_date": "2024-09-30T00:00:00Z",
  "timezone": "Asia/Almaty",
  "events": [
    {
      "id": "uuid",
      "title": "Встреча",
      "start_time": "2024-09-24T10:00:00Z",
      "end_time": "2024-09-24T11:00:00Z",
      "calendar_id": "uuid",
      ...
    }
  ],
  "calendars": [
    {
      "id": "uuid",
      "name": "Мой календарь",
      "color": "#2161F5",
      "is_default": true
    }
  ],
  "group_calendars": [
    {
      "group_id": "uuid",
      "group_name": "Команда разработки",
      "is_visible": true,
      "display_order": 0
    }
  ],
  "group_events": [
    {
      "id": "uuid",
      "title": "Групповая встреча",
      "group_id": "uuid",
      "group_name": "Команда разработки",
      ...
    }
  ]
}
```

**Типы представлений:**
- `day` - события на один день
- `week` - события на неделю (с понедельника по воскресенье)
- `month` - события на месяц

#### 2.2. Получение календарей с настройками
```
GET /api/v1/calendar/calendars/settings
```

**Response:**
```json
[
  {
    "id": "uuid",
    "user_id": 123,
    "name": "Мой календарь",
    "color": "#2161F5",
    "is_default": true,
    "is_visible": true,
    "display_order": 0,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  },
  {
    "id": "uuid",
    "user_id": 123,
    "name": "Рабочий календарь",
    "color": "#FF5733",
    "is_default": false,
    "is_visible": false,
    "display_order": 1,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
]
```

#### 2.3. Обновление видимости календаря
```
PUT /api/v1/calendar/calendars/{id}/visibility
```

**Request Body:**
```json
{
  "is_visible": true,
  "display_order": 0
}
```

Пустой body не допускается. `display_order` должен находиться в диапазоне `0..10000`.

#### 2.4. Слои командных календарей

```http
GET /api/v1/calendar/group-calendars/settings
PUT /api/v1/calendar/group-calendars/{group_id}/visibility
```

Body `PUT` совпадает с личным календарём. Читать и изменять слой можно только пока
пользователь является активным участником активной группы. Настройки персональны и не
влияют на других участников.

**Response:**
```json
{
  "message": "ok"
}
```

## Примеры использования

### Получение событий за день
```bash
curl -X POST https://api.example.com/api/v1/calendar/grid \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "view": "day",
    "date": "2024-09-24",
    "timezone": "Asia/Almaty",
    "include_group": true
  }'
```

### Получение событий за неделю для конкретных календарей
```bash
curl -X POST https://api.example.com/api/v1/calendar/grid \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "view": "week",
    "date": "2024-09-24",
    "timezone": "Asia/Almaty",
    "calendar_ids": ["calendar-uuid-1", "calendar-uuid-2"],
    "include_group": false
  }'
```

### Получение событий за месяц
```bash
curl -X POST https://api.example.com/api/v1/calendar/grid \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "view": "month",
    "date": "2024-09-01",
    "timezone": "Asia/Almaty"
  }'
```

### Скрытие календаря
```bash
curl -X PUT https://api.example.com/api/v1/calendar/calendars/{calendar_id}/visibility \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "is_visible": false
  }'
```

### Изменение порядка отображения календаря
```bash
curl -X PUT https://api.example.com/api/v1/calendar/calendars/{calendar_id}/visibility \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "display_order": 2
  }'
```

## Особенности реализации

1. **Часовые пояса**: API поддерживает IANA timezone. Если не указан, используется UTC;
   некорректное явно переданное значение возвращает `400`.

2. **Фильтрация календарей**:
   - Если `calendar_ids` не указан, используются все видимые календари пользователя
   - Если указан, события получаются только из указанных календарей
   - Приглашения из календарей других организаторов остаются видимыми независимо от
     выбора собственных слоёв; отклонённые приглашения не возвращаются

3. **Групповые события**:
   - Включаются только если `include_group: true`
	- Отображаются только события активных групп, где пользователь является активным участником
	- Каждый групповой слой можно скрыть отдельно

4. **Видимость календарей**:
   - По умолчанию все календари видимы
   - Настройки видимости сохраняются для каждого пользователя отдельно
   - Порядок отображения можно настроить через `display_order`

5. **Представление недели**:
   - Неделя начинается с понедельника
   - Включает 7 полных дней

6. **Представление месяца**:
   - Включает все дни месяца от первого до последнего

7. **Повторяющиеся события**: `DAILY`, `WEEKLY` и `MONTHLY` разворачиваются в occurrences
   внутри выбранного диапазона, даже если исходное событие создано раньше него.

## Миграция

Перед использованием необходимо применить миграцию:

```bash
goose -dir db/migrations postgres "your_connection_string" up
```

Миграция создаст таблицу `user_calendar_settings` и инициализирует настройки для существующих календарей.

## Тестирование

Для тестирования API можно использовать следующий сценарий:

1. Создать несколько календарей
2. Создать события в разных календарях
3. Получить календарную сетку для разных представлений
4. Скрыть некоторые календари
5. Проверить, что события из скрытых календарей не отображаются
6. Включить групповые события
7. Проверить отображение групповых событий

## Интеграция с фронтендом

Фронтенд может:
1. Отображать переключатель видов (день/неделя/месяц)
2. Запрашивать события при изменении даты или вида
3. Отображать список календарей с чекбоксами для управления видимостью
4. Перетаскивать календари для изменения порядка отображения
5. Группировать события по календарям с использованием цветов
