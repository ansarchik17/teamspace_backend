# Smart Agent Frontend Guide

Документ описывает интеграцию фронта с AI Smart Agent endpoint:

```http
POST /api/v1/agent/chat
```

Smart Agent - это не обычный AI-чат. Это офисный ассистент, который может отвечать текстом и вызывать backend-инструменты: искать сотрудников, смотреть календарь, создавать события, работать с Kanban, отправлять рассылки и создавать онлайн-встречи.

## 1. Где находится backend-реализация

Роутинг:

```text
internal/server/router.go
```

Endpoint регистрируется в protected API group:

```go
agent := protected.Group("/agent")
{
    agent.POST("/chat", smartAgentHandler.Chat)
}
```

Handler:

```text
internal/handler/smart_agent_handler.go
```

Основной сервис:

```text
internal/service/smart_agent_service.go
```

Типы request/response:

```text
internal/service/smart_agent_types.go
```

Список tools и их выполнение:

```text
internal/service/smart_agent_tools.go
```

## 2. Авторизация

Endpoint защищён общей auth middleware.

Фронт должен отправлять access token:

```http
Authorization: Bearer <access_token>
Content-Type: application/json
```

Если токен отсутствует или невалидный, запрос будет отклонён middleware до попадания в `SmartAgentHandler`.

## 3. Endpoint

```http
POST /api/v1/agent/chat
```

Назначение: отправить сообщение Smart Agent и получить текстовый ответ, server-side session id, историю диалога и опциональный widget для UI.

## 4. Request Body

Backend type:

```go
type SmartAgentChatRequest struct {
    Message             string       `json:"message" binding:"required"`
    ConversationHistory []LLMMessage `json:"conversation_history,omitempty"`
    SessionID           string       `json:"session_id,omitempty"`
    ForceNewSession     bool         `json:"force_new_session,omitempty"`
}
```

JSON:

```json
{
  "message": "Найди задачи на сегодня",
  "session_id": "optional-existing-session-id",
  "force_new_session": false
}
```

Поля:

| Поле | Тип | Обязательное | Описание |
|---|---:|---:|---|
| `message` | `string` | да | Сообщение пользователя. Не должно быть пустым. |
| `session_id` | `string` | нет | ID текущей server-side сессии. Если передать, backend попробует продолжить контекст из Redis. |
| `force_new_session` | `boolean` | нет | Если `true`, backend создаст новую сессию и проигнорирует старую историю. |
| `conversation_history` | `array` | нет | Поле есть в контракте, но текущая реализация фактически использует Redis-историю по `session_id`, а не client-side history. |

Рекомендация для фронта: хранить `session_id`, который вернул backend, и передавать его в следующих сообщениях этого же диалога.

## 5. Response Body

Backend type:

```go
type SmartAgentChatResponse struct {
    Reply               string            `json:"reply"`
    ConversationHistory []LLMMessage      `json:"conversation_history"`
    SessionID           string            `json:"session_id"`
    Widget              *SmartAgentWidget `json:"widget,omitempty"`
}
```

Пример обычного ответа:

```json
{
  "reply": "У вас есть 3 активные задачи на сегодня.",
  "conversation_history": [
    {
      "role": "user",
      "content": "Покажи мои задачи"
    },
    {
      "role": "assistant",
      "content": "",
      "tool_calls": [
        {
          "id": "call_abc",
          "type": "function",
          "function": {
            "name": "get_kanban_tasks",
            "arguments": "{\"status\":\"TODO,IN_PROGRESS\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "content": "- Подготовить отчёт (Due: 2026-04-20)",
      "tool_call_id": "call_abc",
      "name": "get_kanban_tasks"
    },
    {
      "role": "assistant",
      "content": "Нашёл активные задачи."
    }
  ],
  "session_id": "b5ad77d0-5c86-47e1-9f68-204b9efc9c64",
  "widget": {
    "type": "task_list",
    "title": "Tasks Found",
    "data": [
      {
        "id": "3b063df7-bff3-4f3a-bdb7-12df71e47b17",
        "title": "Подготовить отчёт",
        "priority": "high",
        "due_at": "2026-04-20T09:00:00Z"
      }
    ]
  }
}
```

Поля:

| Поле | Тип | Описание |
|---|---:|---|
| `reply` | `string` | Финальный текстовый ответ ассистента. Его нужно показывать как сообщение ассистента. |
| `session_id` | `string` | ID сессии. Фронт должен сохранить его и отправлять в следующих запросах. |
| `conversation_history` | `array` | Полная история, которую backend хранит для LLM. Можно использовать для debug, но не обязательно рендерить всю как чат. |
| `widget` | `object/null` | Структурированные данные для UI. Возвращается только если tool сгенерировал widget. |

## 6. Session Flow

### Первый запрос в новом чате

Фронт отправляет только `message`:

```json
{
  "message": "Покажи мои задачи на сегодня"
}
```

Backend:

1. Создаёт новый UUID `session_id`.
2. Формирует system prompt.
3. Отправляет запрос в LLM.
4. Выполняет tools при необходимости.
5. Сохраняет историю в Redis на 24 часа.
6. Возвращает `session_id`.

Фронт сохраняет `session_id` в состоянии текущего AI-диалога.

### Следующий запрос в том же чате

```json
{
  "message": "Закрой первую задачу",
  "session_id": "b5ad77d0-5c86-47e1-9f68-204b9efc9c64"
}
```

Backend загрузит историю из Redis:

```text
smart_agent:session:<session_id>
```

TTL Redis-сессии:

```text
24 часа
```

### Начать новый диалог

```json
{
  "message": "Начнём новый сценарий",
  "session_id": "old-session-id",
  "force_new_session": true
}
```

Backend создаст новый `session_id`.

## 7. Что происходит внутри backend

Полный runtime flow:

1. `SmartAgentHandler.Chat` достаёт `user_id` из auth context.
2. Handler парсит JSON body в `SmartAgentChatRequest`.
3. `SmartAgentService.ProcessChat` создаёт или принимает `session_id`.
4. Если Redis доступен и это не новая сессия, backend загружает историю по ключу `smart_agent:session:<session_id>`.
5. Backend получает текущего пользователя через `userRepo.GetByID`.
6. Формируется system prompt:
   - роль: `Smart Office Assistant`;
   - текущее время в UTC+5;
   - данные текущего пользователя: имя, ID, email;
   - список capabilities;
   - правило быть кратким;
   - правило использовать tools когда нужно.
7. В LLM отправляются:
   - system message;
   - Redis history;
   - новое user message.
8. Backend вызывает OpenAI Chat Completions.
9. Если LLM возвращает `finish_reason = "tool_calls"`, backend выполняет requested tools.
10. Результаты tools добавляются в messages как `role = "tool"`.
11. Цикл повторяется максимум 5 раз, чтобы LLM могла обработать tool result и дать финальный ответ.
12. История сохраняется в Redis, максимум последние 20 сообщений.
13. Backend возвращает response.

## 8. LLM Provider

Smart Agent использует OpenAI Chat Completions напрямую.

Текущая модель в коде:

```text
gpt-4o
```

API key берётся из env:

```text
OPENAI_API_KEY
```

Backend detail для dev/prod: в текущем коде есть fallback hardcoded key, если env отсутствует. Это backend security issue, фронту ничего делать не нужно, но для production ключ должен приходить только из secret/env.

## 9. Tools

Tools - это backend-функции, которые LLM может попросить выполнить. Фронт напрямую tools не вызывает. Фронт отправляет только пользовательский текст в `/agent/chat`.

### 9.1 `search_users`

Назначение: искать сотрудников по имени.

Пример user message:

```text
Найди пользователя Иванов
```

Backend вызывает:

```go
userRepo.SearchVisibleTo(ctx, executorUserID, query, 10, 0)
```

Может вернуть widget:

```json
{
  "type": "user_list",
  "title": "Found 2 users for 'Иванов'",
  "data": [
    {
      "id": 42,
      "name": "Иван Иванов",
      "email": "ivan@example.com",
      "position": "Manager",
      "avatar": "https://example.com/avatar.png"
    }
  ]
}
```

Рендер на фронте: список пользователей с именем, должностью, email и avatar.

### 9.2 `get_user_events`

Назначение: получить события календаря пользователя за период.

Пример user message:

```text
Проверь мою занятость завтра с 10 до 12
```

Backend вызывает:

```go
calendarService.ListEvents(ctx, viewerID, targetUserID, from, to)
```

Может вернуть widget:

```json
{
  "type": "calendar_events",
  "title": "Calendar events",
  "data": [
    {
      "id": "2c5eb74e-97cb-4fb9-8942-a46df7c832332",
      "title": "Планёрка",
      "start": "2026-04-20T10:00:00Z",
      "end": "2026-04-20T10:30:00Z",
      "color": "#3B82F6"
    }
  ]
}
```

Рендер на фронте: компактный список событий или calendar preview.

### 9.3 `create_calendar_event`

Назначение: создать событие или встречу в календаре.

Пример user message:

```text
Создай встречу завтра в 15:00 на час с Айжан по проекту
```

Backend вызывает:

```go
calendarService.CreateEvent(ctx, userID, &req)
```

Текущая реализация возвращает текст tool result, widget не создаёт.

Важно: system prompt говорит, что destructive/create действия нужно подтверждать с пользователем. Но отдельного server-side confirmation protocol сейчас нет. Если LLM решила вызвать `create_calendar_event`, backend выполнит создание сразу.

Рекомендация для фронта: для действий создания/изменения/рассылки лучше делать UX-подтверждение на клиенте до отправки финальной команды, например:

1. Пользователь: "Создай встречу завтра в 15:00".
2. Ассистент уточняет детали.
3. Фронт показывает confirmation UI.
4. Только после подтверждения пользователь отправляет: "Подтверждаю, создай встречу".

Это не гарантирует server-side safety, но снижает риск случайного действия.

### 9.4 `set_event_reminder`

Назначение: поставить push reminder для события.

Пример user message:

```text
Поставь напоминание за 15 минут до встречи с министром
```

Backend:

1. Если передан `event_id`, использует его.
2. Если `event_id` нет, ищет событие по `event_query` среди событий на ближайшие 30 дней.
3. Вызывает `calendarService.SetReminder`.

Может вернуть widget:

```json
{
  "type": "reminder_set",
  "data": {
    "event_title": "Встреча с министром",
    "minutes_before": 15,
    "status": "active"
  }
}
```

Рендер на фронте: success card/toast с названием события и временем напоминания.

### 9.5 `get_kanban_tasks`

Назначение: найти задачи.

Пример user message:

```text
Покажи мои задачи в работе
```

Backend вызывает:

```go
kanbanService.SearchTasks(ctx, executorID, searchReq)
```

Если пользователь не указал фильтры, backend по умолчанию ищет задачи:

```text
assignee_id = текущий пользователь
status = TODO или IN_PROGRESS
```

Может вернуть widget:

```json
{
  "type": "task_list",
  "title": "Tasks Found",
  "data": [
    {
      "id": "f13ca5ad-156a-4246-af85-7ee2c68cdb1b",
      "title": "Подготовить отчёт",
      "priority": "high",
      "due_at": "2026-04-20T09:00:00Z"
    }
  ]
}
```

Рендер на фронте: список задач. Желательно сделать элементы кликабельными, если frontend route поддерживает открытие задачи по ID.

### 9.6 `get_kanban_boards`

Назначение: получить список доступных Kanban-досок пользователя.

Пример user message:

```text
Какие у меня есть доски?
```

Backend вызывает:

```go
kanbanService.ListUserBoards(ctx, userID)
```

Может вернуть widget:

```json
{
  "type": "board_list",
  "title": "Your Kanban Boards",
  "data": [
    {
      "id": "0cb31d55-d27d-4647-8424-aed3fa33b6a5",
      "name": "Main Board"
    }
  ]
}
```

Рендер на фронте: список досок.

### 9.7 `create_kanban_task`

Назначение: создать задачу на Kanban-доске.

Пример user message:

```text
Создай задачу подготовить отчёт, приоритет высокий, дедлайн завтра
```

Backend:

1. Если `board_id` передан, создаёт задачу в этой доске.
2. Если `board_id` не передан, ищет default board пользователя.
3. Если default board нет, пытается создать его.
4. Вызывает `kanbanService.CreateTask`.

Может вернуть widget:

```json
{
  "type": "task_created",
  "data": {
    "id": "3cf9e9c2-0d4f-4a7e-a2f2-6d17ea3f9070",
    "title": "Подготовить отчёт",
    "view_url": "/kanban?task=3cf9e9c2-0d4f-4a7e-a2f2-6d17ea3f9070"
  }
}
```

Рендер на фронте: success state с кнопкой открыть задачу.

### 9.8 `close_kanban_task`

Назначение: перевести задачу в колонку `DONE`.

Пример user message:

```text
Закрой задачу по отчёту
```

Backend:

1. Если есть `task_id`, использует его.
2. Если `task_id` нет, ищет задачу по тексту.
3. Получает board задачи.
4. Ищет колонку с `key = DONE` или `name = DONE`.
5. Вызывает `kanbanService.MoveTask`.

Текущая реализация возвращает текст, widget не создаёт.

Важно: это изменение данных. Отдельный server-side confirmation protocol сейчас отсутствует.

### 9.9 `send_broadcast`

Назначение: отправить сообщение нескольким пользователям.

Пример user message:

```text
Отправь всем из списка сообщение: совещание переносится на 16:00
```

Backend вызывает:

```go
chatService.BroadcastMessage(ctx, senderID, &req)
```

Текущая реализация возвращает текст:

```text
Broadcast sent. Success: 3, Fail: 0
```

Widget не создаётся.

Важно: это потенциально чувствительное действие. Frontend должен явно показывать подтверждение перед отправкой финальной команды.

### 9.10 `create_online_meeting`

Назначение: создать онлайн-встречу через внешний scheduler.

Пример user message:

```text
Создай онлайн-встречу завтра с 10 до 11 с Айжан и Ерланом
```

Backend:

1. Получает данные организатора.
2. Получает email/name участников по `attendee_ids`.
3. Отправляет POST во внешний scheduler:

```text
https://100.72.193.178/scheduler/api/create_meeting
```

4. Возвращает ссылку, если scheduler её вернул.

Может вернуть widget:

```json
{
  "type": "meeting_created",
  "data": {
    "title": "Проектная встреча",
    "from": "2026-04-21T10:00:00",
    "to": "2026-04-21T11:00:00",
    "organizer": "Иван Иванов",
    "url": "https://meeting.example.com/abc",
    "participants": 2
  }
}
```

Рендер на фронте: success state с названием, временем, организатором, количеством участников и кнопкой открыть ссылку.

## 10. Widget Contract

Backend type:

```go
type SmartAgentWidget struct {
    Type  string      `json:"type"`
    Title string      `json:"title,omitempty"`
    Data  interface{} `json:"data"`
}
```

Фронт должен обрабатывать `widget.type`.

Поддерживаемые типы на текущий момент:

| `widget.type` | Что означает | Рекомендуемый UI |
|---|---|---|
| `user_list` | Результаты поиска сотрудников | User list с avatar/name/email/position |
| `calendar_events` | События календаря | Event list или mini calendar |
| `reminder_set` | Напоминание установлено | Success toast/card |
| `task_list` | Найденные задачи | Task list |
| `board_list` | Список Kanban-досок | Board list |
| `task_created` | Задача создана | Success card + open task CTA |
| `meeting_created` | Онлайн-встреча создана | Meeting card + open URL CTA |

Фронт должен быть tolerant к неизвестным типам:

```ts
switch (widget.type) {
  case "user_list":
    return <UserList data={widget.data} />;
  case "task_list":
    return <TaskList data={widget.data} />;
  default:
    return <GenericAgentWidget title={widget.title} data={widget.data} />;
}
```

## 11. Conversation History

Backend возвращает `conversation_history`, но это не готовая UI-история сообщений в привычном смысле.

Внутри могут быть роли:

| Role | Описание |
|---|---|
| `user` | Сообщение пользователя |
| `assistant` | Сообщение LLM. Иногда содержит `tool_calls` и пустой `content`. |
| `tool` | Результат backend tool. Обычно это JSON/string для LLM, не обязательно для показа пользователю. |
| `system` | System prompt. В response обычно не возвращается, потому что backend сохраняет историю без первого system message. |

Рекомендация для UI:

1. Рендерить собственный chat state на фронте: user message + `reply` ассистента.
2. Не пытаться напрямую отображать все элементы `conversation_history`.
3. `conversation_history` использовать только для debug/devtools или если нужен advanced transcript.
4. Для продолжения диалога использовать `session_id`, а не пересылать всю историю.

## 12. Error Response

Handler использует общий error format:

```json
{
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "Failed to process agent request",
    "details": null
  }
}
```

Возможные ситуации:

| Status | Причина |
|---:|---|
| `400` | Невалидный body или отсутствует required `message`. |
| `401/403` | Auth middleware отклонила запрос. |
| `500` | Ошибка OpenAI, Redis, user lookup, tool execution или другой backend failure. |

Важно: если tool execution внутри завершился ошибкой, backend часто не отдаёт HTTP error. Tool result становится текстом для LLM, а LLM уже отвечает пользователю. Поэтому часть ошибок придёт как обычный `200 OK` с `reply`, например: "Error searching tasks: ...".

## 13. Frontend Integration Flow

Рекомендуемый UI flow:

1. Пользователь открывает Smart Agent экран.
2. Фронт создаёт локальный chat state без `session_id`.
3. Пользователь отправляет первое сообщение.
4. Фронт показывает user bubble и loading state.
5. Фронт вызывает `POST /api/v1/agent/chat`.
6. Backend возвращает `reply`, `session_id`, возможно `widget`.
7. Фронт сохраняет `session_id`.
8. Фронт показывает assistant bubble из `reply`.
9. Если есть `widget`, фронт рендерит его под ответом ассистента.
10. Следующие сообщения отправляются с тем же `session_id`.

## 14. TypeScript Types

```ts
export type AgentRole = "system" | "user" | "assistant" | "tool";

export interface LLMToolFunctionCall {
  name: string;
  arguments: string;
}

export interface LLMToolCall {
  id: string;
  type: "function";
  function: LLMToolFunctionCall;
}

export interface LLMMessage {
  role: AgentRole;
  content: string;
  tool_call_id?: string;
  tool_calls?: LLMToolCall[];
  name?: string;
}

export interface SmartAgentChatRequest {
  message: string;
  session_id?: string;
  force_new_session?: boolean;
  conversation_history?: LLMMessage[];
}

export interface SmartAgentWidget<TData = unknown> {
  type: string;
  title?: string;
  data: TData;
}

export interface SmartAgentChatResponse {
  reply: string;
  conversation_history: LLMMessage[];
  session_id: string;
  widget?: SmartAgentWidget;
}
```

Widget-specific types:

```ts
export interface UserListWidgetData {
  id: number;
  name: string;
  email: string;
  position?: string;
  avatar?: string;
}

export interface CalendarEventWidgetData {
  id: string;
  title: string;
  start: string;
  end: string;
  color?: string;
}

export interface TaskListWidgetData {
  id: string;
  title: string;
  priority: "low" | "medium" | "high" | "critical" | string;
  due_at?: string | null;
}

export interface BoardListWidgetData {
  id: string;
  name: string;
}

export interface TaskCreatedWidgetData {
  id: string;
  title: string;
  view_url: string;
}

export interface ReminderSetWidgetData {
  event_title: string;
  minutes_before: number;
  status: string;
}

export interface MeetingCreatedWidgetData {
  title: string;
  from: string;
  to: string;
  organizer: string;
  url?: string;
  participants: number;
}
```

## 15. Fetch Example

```ts
async function sendSmartAgentMessage(params: {
  baseUrl: string;
  token: string;
  message: string;
  sessionId?: string;
  forceNewSession?: boolean;
}): Promise<SmartAgentChatResponse> {
  const response = await fetch(`${params.baseUrl}/api/v1/agent/chat`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${params.token}`,
    },
    body: JSON.stringify({
      message: params.message,
      session_id: params.sessionId,
      force_new_session: params.forceNewSession,
    }),
  });

  const data = await response.json();

  if (!response.ok) {
    const message =
      data?.error?.message ||
      data?.error ||
      `Smart Agent request failed with status ${response.status}`;
    throw new Error(message);
  }

  return data as SmartAgentChatResponse;
}
```

## 16. cURL Examples

### Start new session

```bash
curl -X POST "$BASE_URL/api/v1/agent/chat" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "message": "Покажи мои задачи на сегодня"
  }'
```

### Continue session

```bash
curl -X POST "$BASE_URL/api/v1/agent/chat" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "message": "Открой подробности первой задачи",
    "session_id": "b5ad77d0-5c86-47e1-9f68-204b9efc9c64"
  }'
```

### Force new session

```bash
curl -X POST "$BASE_URL/api/v1/agent/chat" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "message": "Начнём заново. Найди сотрудника Айжан",
    "session_id": "b5ad77d0-5c86-47e1-9f68-204b9efc9c64",
    "force_new_session": true
  }'
```

## 17. UX Recommendations

### Loading states

Некоторые tools могут работать дольше обычного:

| Tool | Почему может быть долго |
|---|---|
| `create_online_meeting` | Вызов внешнего scheduler с timeout до 120 секунд. |
| `get_kanban_tasks` | Поиск по задачам и доступам. |
| `create_calendar_event` | Создание события и участников. |

Рекомендация: показывать normal loading state и иметь frontend timeout/retry UX.

### Confirmation UX

Действия, которые меняют данные:

| Tool | Риск |
|---|---|
| `create_calendar_event` | Создаёт событие |
| `set_event_reminder` | Меняет напоминания |
| `create_kanban_task` | Создаёт задачу |
| `close_kanban_task` | Меняет статус задачи |
| `send_broadcast` | Отправляет сообщения другим пользователям |
| `create_online_meeting` | Создаёт встречу во внешнем сервисе |

В текущей backend-реализации нет отдельного API-level confirmation token. Поэтому фронт должен считать, что фраза пользователя может привести к действию сразу после одного запроса.

Практичный вариант:

1. Для потенциально опасных команд фронт сначала отправляет уточняющий prompt в обычном чате: "Сформируй черновик действия, ничего не выполняй".
2. Показывает пользователю preview.
3. После подтверждения отправляет явное сообщение: "Подтверждаю, выполни действие".

Лучший backend-future вариант: добавить server-side confirmation flow, где tool сначала возвращает `action_confirmation` widget, а выполнение происходит только по отдельному confirm endpoint.

### Rendering strategy

Рекомендуемый layout одного assistant response:

```text
Assistant message bubble
Widget block, if exists
Optional debug expand: conversation_history
```

Не нужно рендерить tool messages как обычные сообщения пользователя.

## 18. Known Backend Limitations

1. `conversation_history` в request сейчас почти не используется. Контекст берётся из Redis по `session_id`.
2. История живёт 24 часа. После истечения TTL продолжение по старому `session_id` начнётся без старого контекста.
3. Нет server-side confirmation protocol для create/update/broadcast действий.
4. Tool loop ограничен 5 итерациями.
5. В историю сохраняются максимум последние 20 LLM messages.
6. Ошибки внутри tools могут вернуться как обычный `200 OK` с текстовым ответом ассистента.
7. `create_online_meeting` зависит от внешнего scheduler `https://100.72.193.178/scheduler/api/create_meeting`.
8. Smart Agent использует OpenAI напрямую, а не Alem client.
9. В backend-коде есть hardcoded fallback OpenAI key. Для production это нужно убрать.

## 19. Minimal Frontend Checklist

Для базовой интеграции достаточно:

1. Сделать authenticated `POST /api/v1/agent/chat`.
2. Передавать `message`.
3. Сохранять `session_id` из первого ответа.
4. Передавать `session_id` в следующих запросах.
5. Показывать `reply` как ответ ассистента.
6. Рендерить `widget`, если он есть.
7. Обрабатывать `400`, `401/403`, `500`.
8. Сделать fallback UI для неизвестного `widget.type`.
9. Для create/broadcast/close действий добавить confirmation UX.

