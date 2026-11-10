# Интеграция n8n с RabbitMQ

## Назначение

Этот документ описывает, как `n8n` должен забирать события из RabbitMQ в текущей архитектуре Alem Workspace.

Текущая схема:

- backend API: `100.72.193.178`
- RabbitMQ: `92.38.48.9`
- virtual host RabbitMQ: `/alem`
- exchange: `alem.events.exchange`
- тип exchange: `topic`

Backend публикует события в RabbitMQ. `n8n` читает их из очередей и запускает workflow. Backend не должен вызывать `n8n` напрямую.

## Как это работает

Поток событий:

1. Пользователь выполняет действие в системе.
2. Backend выполняет основную бизнес-операцию и сохраняет данные.
3. Backend публикует событие в RabbitMQ.
4. RabbitMQ маршрутизирует событие по `routing key`.
5. `n8n` читает событие из очереди, привязанной к `alem.events.exchange`.
6. `n8n` запускает нужную автоматизацию.

Это схема `fire-and-forget`:

- пользовательский запрос не ждёт выполнения `n8n`
- ошибка в RabbitMQ не должна откатывать основную операцию
- если RabbitMQ недоступен, backend пишет ошибку в лог и продолжает работать

## Параметры подключения к RabbitMQ

Используйте в `n8n`:

- Host: `92.38.48.9`
- Port: `5672`
- Username: `alem_n8n`
- Password: пароль пользователя `alem_n8n`
- Virtual host: `/alem`

Важно:

- В backend AMQP URL используется `%2Falem`, потому что в URI символ `/` в имени vhost должен быть URL-encoded.
- В интерфейсе RabbitMQ и в `n8n` сам virtual host всё равно называется `/alem`.

Пример backend-конфига:

```env
RABBITMQ_ENABLED=true
RABBITMQ_URL=amqp://alem_backend:change-me@92.38.48.9:5672/%2Falem
RABBITMQ_EXCHANGE=alem.events.exchange
RABBITMQ_PRODUCER=alem-messenger-api
```

## Exchange и routing key

Exchange:

- Name: `alem.events.exchange`
- Type: `topic`
- Durable: `true`

Формат routing key:

```text
<service_name>.<entity>.<action>
```

Примеры:

- `calendar.event.created`
- `chat.message.sent`
- `kanban.task.created`

`n8n` должен подписывать свои очереди на этот exchange через topic bindings.

## Формат payload

Каждое сообщение отправляется в JSON такого вида:

```json
{
  "meta": {
    "event_id": "uuid4",
    "event_type": "calendar.event.created",
    "timestamp": "2026-06-08T16:21:56Z",
    "producer": "alem-messenger-api"
  },
  "data": {}
}
```

Значения:

- `meta.event_id`: уникальный идентификатор события
- `meta.event_type`: дублирует routing key
- `meta.timestamp`: время события в UTC
- `meta.producer`: имя backend-сервиса
- `data`: полезная нагрузка с минимально необходимыми данными

Если workflow может запускаться повторно, в `n8n` желательно использовать `meta.event_id` для идемпотентности.

## Какие события публикует backend

### Календарь

Публикуются события:

- `calendar.event.created`
- `calendar.event.updated`
- `calendar.event.deleted`
- `calendar.invite.responded`
- `calendar.reminder.set`
- `calendar.reminder.deleted`

Рекомендуемый binding для всех calendar-событий:

```text
calendar.#
```

### Чаты

Публикуются события:

- `chat.conversation.created`
- `chat.message.sent`
- `chat.message.edited`
- `chat.message.deleted_for_user`
- `chat.message.bulk_deleted_for_user`
- `chat.reaction.added`
- `chat.reaction.removed`
- `chat.group.created`
- `chat.group.message.sent`

Рекомендуемый binding для всех chat-событий:

```text
chat.#
```

### Kanban

Публикуются события:

- `kanban.task.created`

Рекомендуемый binding для всех kanban-событий:

```text
kanban.#
```

## Рекомендуемая схема очередей для n8n

Для простой и понятной production-схемы лучше сделать отдельную очередь на каждый домен:

- `n8n.calendar.events`
- `n8n.chat.events`
- `n8n.kanban.events`

Bindings:

- `n8n.calendar.events` -> `calendar.#`
- `n8n.chat.events` -> `chat.#`
- `n8n.kanban.events` -> `kanban.#`

Плюсы:

- calendar, chat и kanban разделены
- проще отлаживать
- проще масштабировать workflow

Если нужен узкий сценарий, можно подписывать очередь не на маску, а на конкретный routing key:

- `calendar.event.created`
- `chat.message.sent`
- `kanban.task.created`

## Как настраивать n8n

### Вариант 1. Отдельный workflow на каждый домен

Сделать отдельный RabbitMQ trigger workflow:

- workflow для календаря на очереди `n8n.calendar.events`
- workflow для чатов на очереди `n8n.chat.events`
- workflow для kanban на очереди `n8n.kanban.events`

Этот вариант лучше, если:

- разные команды отвечают за разные автоматизации
- логика calendar/chat/kanban не связана между собой

### Вариант 2. Одна очередь на всё

Можно сделать одну очередь, например `n8n.all.events`, и привязать её так:

```text
#
```

После этого уже внутри `n8n` ветвить логику по `meta.event_type`.

Этот вариант допустим только если:

- трафик небольшой
- одна команда владеет всеми workflow

Для текущей архитектуры лучше использовать отдельные очереди по доменам.

## Примеры payload

### Создание события календаря

```json
{
  "meta": {
    "event_id": "3a1b44c0-58c8-4c4b-b3c6-2b1220a4f001",
    "event_type": "calendar.event.created",
    "timestamp": "2026-06-08T16:21:56Z",
    "producer": "alem-messenger-api"
  },
  "data": {
    "event_id": "2357e2a8-3bd3-4bf6-82dd-c2e228ae82ee",
    "organizer_id": 9,
    "title": "TEST",
    "attendee_ids": [
      60
    ]
  }
}
```

### Отправка сообщения в чат

```json
{
  "meta": {
    "event_id": "6d3fa8ff-6b89-45bf-912f-9b5c21455001",
    "event_type": "chat.message.sent",
    "timestamp": "2026-06-08T16:40:00Z",
    "producer": "alem-messenger-api"
  },
  "data": {
    "message_id": "uuid",
    "chat_id": "uuid",
    "sender_id": 9,
    "message_type": "text"
  }
}
```

### Создание задачи kanban

```json
{
  "meta": {
    "event_id": "41f09bb0-2c49-4414-88d2-348f947dd001",
    "event_type": "kanban.task.created",
    "timestamp": "2026-06-08T16:44:35Z",
    "producer": "alem-messenger-api"
  },
  "data": {
    "task_id": "uuid",
    "board_id": "uuid",
    "created_by": 9,
    "title": "Test task"
  }
}
```

Точный набор полей в `data` может немного отличаться в зависимости от события. В `n8n` нужно опираться в первую очередь на:

- `meta.event_type`
- идентификаторы сущностей в `data`

Если нужны полные данные, `n8n` должен сходить в backend по REST API.

## Что должен делать n8n

Нормальные use cases:

- отправка уведомлений
- вызов внешних систем
- обогащение события через REST API
- создание follow-up задач
- запись в аудит
- отправка email или других сообщений

Плохие use cases:

- вынос критичной синхронной бизнес-логики из backend в `n8n`
- ожидание, что `n8n` отработает до ответа API пользователю

`n8n` здесь должен использоваться как асинхронный orchestration layer.

## Как проверять

### Календарь

1. Создать очередь с binding `calendar.#`
2. Создать новое событие календаря
3. Убедиться, что сообщение пришло в очередь
4. Убедиться, что workflow в `n8n` стартовал

### Чаты

1. Создать очередь с binding `chat.#`
2. Отправить новое сообщение в чат
3. Убедиться, что сообщение пришло в очередь
4. Убедиться, что workflow в `n8n` стартовал

### Kanban

1. Создать очередь с binding `kanban.#`
2. Создать новую задачу
3. Убедиться, что сообщение пришло в очередь
4. Убедиться, что workflow в `n8n` стартовал

## Troubleshooting

### Backend пишет, что RabbitMQ инициализирован, но в очереди нет сообщений

Проверьте:

- exchange существует в `/alem`
- очередь существует в `/alem`
- очередь привязана к `alem.events.exchange`
- binding key совпадает с routing key

### RabbitMQ ошибка: no access to this vhost

Проверьте:

- у пользователя есть права на `/alem`
- backend использует `%2Falem` в AMQP URI

Правильный пример:

```text
amqp://alem_backend:password@92.38.48.9:5672/%2Falem
```

### Exchange существует, но bindings пустые

Если exchange был удалён и создан заново, старые bindings уже не вернутся. Их нужно создать повторно вручную.

### Сообщение есть в очереди, но n8n ничего не делает

Проверьте:

- правильные credentials в `n8n`
- выбран `vhost /alem`
- указано правильное имя очереди
- consumer node активен
- workflow активирован

## Production-рекомендации

- Использовать один общий exchange `alem.events.exchange`
- Дать consumer’ам самим управлять своими очередями
- Не плодить exchange на каждый сервис
- Использовать topic bindings
- Держать payload лёгким
- За полными данными ходить из `n8n` в backend по REST API

## Текущий статус

Текущая интеграция backend с RabbitMQ уже вручную проверена в RabbitMQ для:

- calendar
- chat
- kanban

Тестовые очереди успешно получили события по bindings:

- `calendar.#`
- `chat.#`
- `kanban.#`

Это подтверждает, что backend корректно публикует события в `alem.events.exchange` внутри `/alem`.
