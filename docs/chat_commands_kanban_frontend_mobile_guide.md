# Chat Commands для Kanban: Web/Mobile Integration Guide

Документ описывает, как web и mobile должны работать с командами чата для kanban.

Текущие команды:

```text
/create_task
/create_board
/list_tasks
```

Backend endpoints:

```text
GET  /api/v1/chat-commands
GET  /api/v1/chat-commands/kanban/context
POST /api/v1/chat-commands/execute
```

Все endpoints требуют `Authorization: Bearer <access_token>`.

## Общий UX

1. Пользователь вводит `/` в поле чата.
2. Клиент вызывает `GET /api/v1/chat-commands`.
3. Клиент показывает список команд.
4. Пользователь выбирает `/create_task` или `/create_board`.
5. Клиент может:
   - отправить `raw_text` сразу, если команда простая;
   - открыть форму, собрать `args`, затем вызвать execute.
6. Backend либо выполняет команду, либо возвращает `status: "needs_input"` с формой и недостающими полями.
7. Если команда создала задачу и передан `chat_id`, backend может отправить карточку задачи в чат.

Важно: web/mobile не должны сами выполнять бизнес-логику kanban. Клиент только собирает данные и вызывает backend.

## 1. Получить Список Команд

Request:

```bash
curl -X GET "https://YOUR_API_HOST/api/v1/chat-commands" \
  -H "Authorization: Bearer ACCESS_TOKEN"
```

Response:

```json
{
  "commands": [
    {
      "id": "kanban.create_board",
      "title": "Создать доску",
      "description": "Создает kanban-доску с дефолтными колонками.",
      "aliases": ["/create_board"],
      "scopes": ["conversation", "group"],
      "arguments": [
        {
          "name": "board_name",
          "type": "string",
          "label": "Название доски",
          "required": true
        }
      ],
      "examples": ["/create_board Запуск продукта"]
    },
    {
      "id": "kanban.create_task",
      "title": "Создать задачу",
      "description": "Создает задачу в kanban-доске и может отправить карточку задачи в чат.",
      "aliases": ["/create_task"],
      "scopes": ["conversation", "group"],
      "arguments": [
        {
          "name": "board_id",
          "type": "uuid",
          "label": "Доска",
          "required": true,
          "source": "kanban.boards"
        },
        {
          "name": "title",
          "type": "string",
          "label": "Название",
          "required": true
        },
        {
          "name": "description",
          "type": "string",
          "label": "Описание",
          "required": false
        },
        {
          "name": "assignee_ids",
          "type": "user_ids",
          "label": "Исполнители",
          "required": false,
          "source": "kanban.board.members"
        },
        {
          "name": "sprint_id",
          "type": "uuid",
          "label": "Спринт",
          "required": false,
          "source": "kanban.board.sprints"
        },
        {
          "name": "priority",
          "type": "enum",
          "label": "Приоритет",
          "required": false,
          "values": ["low", "medium", "high", "critical"]
        },
        {
          "name": "due_at",
          "type": "datetime",
          "label": "Дедлайн",
          "required": false
        },
        {
          "name": "attachment_file_ids",
          "type": "uuid_array",
          "label": "Файлы",
          "required": false,
          "source": "kanban.uploaded_files"
        },
        {
          "name": "parent_id",
          "type": "uuid",
          "label": "Родительская задача",
          "required": false
        }
      ],
      "examples": ["/create_task Подготовить отчет"]
    }
  ]
}
```

## 2. Получить Kanban Context Для Формы

Этот endpoint нужен, чтобы заполнить форму создания задачи.

Request без выбранной доски:

```bash
curl -X GET "https://YOUR_API_HOST/api/v1/chat-commands/kanban/context" \
  -H "Authorization: Bearer ACCESS_TOKEN"
```

Response:

```json
{
  "commands": [],
  "boards": [
    {
      "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "owner_id": 123,
      "name": "My Board",
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z"
    }
  ]
}
```

Request с выбранной доской:

```bash
curl -X GET "https://YOUR_API_HOST/api/v1/chat-commands/kanban/context?board_id=7a2e6d3e-1111-4444-8888-4ccfb03a1111" \
  -H "Authorization: Bearer ACCESS_TOKEN"
```

Response:

```json
{
  "commands": [],
  "boards": [
    {
      "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "owner_id": 123,
      "name": "My Board",
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z"
    }
  ],
  "selected_board": {
    "board": {
      "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "owner_id": 123,
      "name": "My Board",
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z"
    },
    "background": {
      "mode": "preset_color",
      "preset_id": "preset_default",
      "preset_hex": "#F4F5F7",
      "image_url": null
    },
    "sprints": [
      {
        "id": "8b4a6a91-2222-4444-8888-f5a2df1d2222",
        "board_id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
        "name": "Sprint 1",
        "status": "planned",
        "start_date": null,
        "end_date": null,
        "created_at": "2026-04-12T10:00:00Z",
        "updated_at": "2026-04-12T10:00:00Z"
      }
    ],
    "columns": [
      {
        "column": {
          "id": "14bd12d1-3333-4444-8888-6f56b5ab3333",
          "board_id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
          "key": "TODO",
          "name": "To Do",
          "position": 10,
          "is_system": true,
          "created_at": "2026-04-12T10:00:00Z",
          "updated_at": "2026-04-12T10:00:00Z"
        },
        "tasks": []
      }
    ],
    "members": [
      {
        "board_id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
        "user_id": 123,
        "full_name": "Test User",
        "role": "owner",
        "joined_at": "2026-04-12T10:00:00Z"
      }
    ]
  }
}
```

Frontend/mobile mapping:

```text
boards[]                    -> select для board_id
selected_board.members[]     -> multi-select для assignee_ids
selected_board.sprints[]     -> select для sprint_id
selected_board.columns[]     -> можно использовать для обновления UI доски
```

## 3. Создать Доску Через Чат

Минимальный вариант через `raw_text`:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/chat-commands/execute" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "raw_text": "/create_board Запуск продукта"
  }'
```

Явный вариант через `command_id` и `args`:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/chat-commands/execute" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "command_id": "kanban.create_board",
    "raw_text": "/create_board Запуск продукта",
    "args": {
      "board_name": "Запуск продукта"
    }
  }'
```

Success response, `201 Created`:

```json
{
  "status": "executed",
  "command_id": "kanban.create_board",
  "result": {
    "type": "kanban_board_created",
    "board": {
      "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "owner_id": 123,
      "name": "Запуск продукта",
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z"
    }
  }
}
```

Если не передали название доски:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/chat-commands/execute" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "raw_text": "/create_board"
  }'
```

Response, `200 OK`:

```json
{
  "status": "needs_input",
  "command_id": "kanban.create_board",
  "missing_fields": ["board_name"],
  "form": {
    "title": "Создать доску",
    "fields": [
      {
        "name": "board_name",
        "type": "text",
        "label": "Название доски",
        "required": true
      }
    ]
  }
}
```

## 4. Создать Задачу Через Чат

### 4.1 Минимальный Вариант

Если у пользователя ровно одна доступная доска, backend сам выберет ее.

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/chat-commands/execute" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "raw_text": "/create_task Подготовить отчет"
  }'
```

Success response, `201 Created`:

```json
{
  "status": "executed",
  "command_id": "kanban.create_task",
  "result": {
    "type": "kanban_task_created",
    "task": {
      "id": "a91df210-4444-4444-8888-a8a93a224444",
      "board_id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "column_id": "14bd12d1-3333-4444-8888-6f56b5ab3333",
      "created_by": 123,
      "assignee_ids": [],
      "title": "Подготовить отчет",
      "description": null,
      "priority": "medium",
      "due_at": null,
      "time_spent_sec": 0,
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z",
      "status": "К ВЫПОЛНЕНИЮ"
    },
    "board": {
      "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "owner_id": 123,
      "name": "My Board",
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z"
    }
  }
}
```

### 4.2 Если Досок Несколько

Если `board_id` не передан и у пользователя несколько досок, backend не угадывает доску.

Request:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/chat-commands/execute" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "raw_text": "/create_task Подготовить отчет"
  }'
```

Response, `200 OK`:

```json
{
  "status": "needs_input",
  "command_id": "kanban.create_task",
  "missing_fields": ["board_id"],
  "result": {
    "type": "kanban_task_form_context",
    "context": {
      "commands": [],
      "boards": [
        {
          "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
          "owner_id": 123,
          "name": "My Board",
          "created_at": "2026-04-12T10:00:00Z",
          "updated_at": "2026-04-12T10:00:00Z"
        },
        {
          "id": "a0c38d5e-5555-4444-8888-c2bc68565555",
          "owner_id": 123,
          "name": "Запуск продукта",
          "created_at": "2026-04-12T10:00:00Z",
          "updated_at": "2026-04-12T10:00:00Z"
        }
      ]
    }
  },
  "form": {
    "title": "Создать задачу",
    "description": "Заполните недостающие поля и повторите execute.",
    "fields": [
      {
        "name": "board_id",
        "type": "select",
        "label": "Доска",
        "required": true,
        "source": "kanban.boards",
        "options": []
      },
      {
        "name": "title",
        "type": "text",
        "label": "Название задачи",
        "required": true
      }
    ]
  }
}
```

Клиент должен показать выбор доски, затем повторить execute с `args.board_id`.

### 4.3 Полное Создание Задачи

Request:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/chat-commands/execute" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Client-Instance-ID: web-tab-123" \
  -d '{
    "command_id": "kanban.create_task",
    "raw_text": "/create_task Подготовить отчет",
    "client_request_id": "3fe3c24a-6666-4444-8888-cf39909e6666",
    "chat_id": "c7c9b7e5-7777-4444-8888-a31f9d417777",
    "chat_type": "group",
    "send_result_to_chat": true,
    "args": {
      "board_id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "sprint_id": "8b4a6a91-2222-4444-8888-f5a2df1d2222",
      "title": "Подготовить отчет",
      "description": "Собрать финальные цифры и отправить руководителю.",
      "assignee_ids": [123, 456],
      "priority": "medium",
      "due_at": "2026-04-13T18:00:00+05:00",
      "attachment_file_ids": [],
      "parent_id": null
    }
  }'
```

Success response, `201 Created`:

```json
{
  "status": "executed",
  "command_id": "kanban.create_task",
  "result": {
    "type": "kanban_task_created",
    "task": {
      "id": "a91df210-4444-4444-8888-a8a93a224444",
      "board_id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "sprint_id": "8b4a6a91-2222-4444-8888-f5a2df1d2222",
      "column_id": "14bd12d1-3333-4444-8888-6f56b5ab3333",
      "created_by": 123,
      "assignee_ids": [123, 456],
      "title": "Подготовить отчет",
      "description": "Собрать финальные цифры и отправить руководителю.",
      "priority": "medium",
      "due_at": "2026-04-13T18:00:00+05:00",
      "time_spent_sec": 0,
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z",
      "status": "К ВЫПОЛНЕНИЮ"
    },
    "board": {
      "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "owner_id": 123,
      "name": "My Board",
      "created_at": "2026-04-12T10:00:00Z",
      "updated_at": "2026-04-12T10:00:00Z"
    }
  },
  "chat_message": {
    "id": "d3e19cc2-8888-4444-8888-a8b723c18888",
    "conversation_id": "c7c9b7e5-7777-4444-8888-a31f9d417777",
    "sender_id": 123,
    "content": "Создана задача: Подготовить отчет",
    "type": "share_task",
    "metadata": "{\"board_id\":\"7a2e6d3e-1111-4444-8888-4ccfb03a1111\",\"created_from_command\":true,\"kind\":\"task\",\"task_id\":\"a91df210-4444-4444-8888-a8a93a224444\",\"title\":\"Подготовить отчет\"}",
    "created_at": "2026-04-12T10:00:00Z",
    "updated_at": "2026-04-12T10:00:00Z",
    "sender_username": "test.user",
    "sender_full_name": "Test User",
    "read_by_count": 0,
    "delivered_count": 0,
    "is_read": false,
    "reactions": []
  }
}
```

`chat_message` будет `null` или отсутствовать, если:

```json
{
  "send_result_to_chat": false
}
```

или если не передан `chat_id`.

### 4.4 Создать Задачу И Доску Одним Запросом

Если передать `board_name` без `board_id`, backend:

1. Ищет доступную доску с таким именем.
2. Если нашел, создает задачу в ней.
3. Если не нашел, создает новую доску и задачу в ней.

Request:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/chat-commands/execute" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "command_id": "kanban.create_task",
    "raw_text": "/create_task Подготовить отчет",
    "args": {
      "board_name": "Запуск продукта",
      "title": "Подготовить отчет",
      "priority": "high"
    }
  }'
```

Success response:

```json
{
  "status": "executed",
  "command_id": "kanban.create_task",
  "result": {
    "type": "kanban_task_created",
    "task": {
      "id": "a91df210-4444-4444-8888-a8a93a224444",
      "board_id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "title": "Подготовить отчет",
      "priority": "high",
      "status": "К ВЫПОЛНЕНИЮ"
    },
    "board": {
      "id": "7a2e6d3e-1111-4444-8888-4ccfb03a1111",
      "name": "Запуск продукта"
    }
  }
}
```

## 5. Файлы Для Задачи

Если нужно прикрепить файлы при создании задачи:

1. Сначала загрузить файл:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/kanban/tasks/upload" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -F "file=@./report.pdf"
```

Response:

```json
{
  "id": "f2b53040-9999-4444-8888-0a06c59f9999",
  "owner_id": 123,
  "real_name": "report.pdf",
  "download_url": "",
  "size": 125000,
  "mime_type": "application/pdf",
  "version": 1,
  "created_at": "2026-04-12T10:00:00Z",
  "updated_at": "2026-04-12T10:00:00Z"
}
```

2. Потом передать `id` в `attachment_file_ids`:

```json
{
  "args": {
    "attachment_file_ids": ["f2b53040-9999-4444-8888-0a06c59f9999"]
  }
}
```

## 6. Как Клиент Должен Обрабатывать Поля

`raw_text`:

Исходная строка из input чата. Например:

```text
/create_task Подготовить отчет
```

Если `args.title` не передан, backend возьмет title из остатка после `/create_task`.

`command_id`:

Можно передавать для точности:

```text
kanban.create_task
kanban.create_board
```

Если `command_id` не передан, backend определит команду по `raw_text`.

`chat_id`:

ID personal conversation или group chat, куда нужно отправить карточку результата.

`chat_type`:

```text
conversation
group
```

Если `chat_type = group`, backend отправляет карточку через group message.
Иначе отправляет как personal conversation message.

`send_result_to_chat`:

```text
true  -> после создания задачи отправить share_task карточку в чат
false -> только создать задачу и вернуть result
```

Если поле не передано, но `chat_id` передан, backend отправит карточку в чат.

`client_request_id`:

Поле зарезервировано для idempotency на клиенте. Сейчас backend принимает его в контракте, но не хранит отдельный idempotency state. Клиент все равно должен отправлять стабильный UUID на повтор запроса, чтобы позже можно было включить защиту от дублей без изменения API.

## 7. Рекомендованный Full Flow Для /create_task

1. User вводит:

```text
/create_task Подготовить отчет
```

2. Клиент вызывает:

```http
POST /api/v1/chat-commands/execute
```

с `raw_text`.

3. Если backend вернул:

```json
{
  "status": "needs_input",
  "missing_fields": ["board_id"]
}
```

клиент показывает форму выбора доски.

4. После выбора доски клиент вызывает:

```http
GET /api/v1/chat-commands/kanban/context?board_id=...
```

и получает `members`/`sprints`.

5. Клиент показывает расширенную форму:

```text
title
description
board_id
sprint_id
assignee_ids
priority
estimate
due_at
attachments
```

6. Если есть attachments, клиент загружает их через:

```http
POST /api/v1/kanban/tasks/upload
```

7. Клиент вызывает:

```http
POST /api/v1/chat-commands/execute
```

с полным `args`.

8. Если response `status = executed`, клиент:

```text
- закрывает форму;
- добавляет/обновляет карточку сообщения, если пришел chat_message;
- обновляет kanban cache для board_id;
- показывает toast "Задача создана".
```

## 8. Ошибки

Backend возвращает стандартный error shape:

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "Неизвестная команда",
    "details": null
  }
}
```

Возможные частые случаи:

```text
400 BAD_REQUEST       -> неизвестная команда, неправильный UUID, пустой title
401 UNAUTHORIZED      -> нет/невалидный token
403 FORBIDDEN         -> нет прав на kanban-доску или чат
404 NOT_FOUND         -> доска/чат/сущность не найдены
500 INTERNAL_ERROR    -> внутренняя ошибка backend
```

## 9. Что Важно Для Web/Mobile

Клиент должен считать backend источником правды.

Не надо на клиенте решать, может ли пользователь создать задачу в доске. Клиент может скрывать недоступные действия для UX, но финальная проверка всегда на backend.

Не надо отправлять обычное chat message, если пользователь вводит slash command. Для команды нужно вызывать:

```http
POST /api/v1/chat-commands/execute
```

Обычный endpoint сообщений использовать только для обычных сообщений:

```http
POST /api/v1/conversations/{conversation_id}/messages
POST /api/v1/groups/{group_id}/messages
```

Для команды `/create_task` клиент должен показывать один понятный сценарий:

```text
ввод команды -> execute -> needs_input или executed -> форма при необходимости -> execute -> готово
```
