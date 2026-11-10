# Whiteboard WebSocket API

## 1. Краткое описание

Модуль whiteboard добавляет совместное рисование в реальном времени поверх существующих `kanban` boards.  
Каждая комната соответствует одной доске `board_id` и работает как изолированный WebSocket room с собственным lifecycle.

Как это работает:

- `Manager` хранит активные комнаты в памяти и создает их по требованию.
- `Room.Run()` сериализует все изменения состояния через одну goroutine.
- `Client.ReadPump()` принимает входящие события от пользователя.
- `Client.WritePump()` отправляет исходящие события и держит ping/pong lifecycle.
- состояние доски хранится в памяти как актуальный набор draw actions;
- при подключении новый клиент получает `init` с текущим state;
- state сохраняется в PostgreSQL периодически, при отключении последнего клиента и при graceful shutdown.

Важно: в текущей backend-реализации `roomId` это `UUID` существующей `kanban`-доски.

## 2. Endpoint

```http
GET /api/v1/whiteboards/ws/{roomId}
```

Где `{roomId}` — `UUID` доски, например `6d2d4d58-89b7-4b10-85be-a77f7e6738a0`.

## 3. Авторизация

Подключение проходит через тот же auth middleware, что и обычные protected HTTP endpoints.

Пример с Bearer:

```bash
Authorization: Bearer <ACCESS_TOKEN>
```

Пример с cookie:

```bash
Cookie: access_token=<ACCESS_TOKEN>
```

Поддерживается и query-token flow, уже используемый проектом для WebSocket:

```bash
?token=<ACCESS_TOKEN>
```

## 4. Примеры подключения

Через `wscat`:

```bash
npm install -g wscat
wscat -c "wss://alem-workspace.gov.kz/api/v1/whiteboards/ws/6d2d4d58-89b7-4b10-85be-a77f7e6738a0" \
  -H "Authorization: Bearer <ACCESS_TOKEN>"
```

Для локального окружения:

```bash
wscat -c "ws://localhost:8080/api/v1/whiteboards/ws/6d2d4d58-89b7-4b10-85be-a77f7e6738a0" \
  -H "Authorization: Bearer <ACCESS_TOKEN>"
```

Если нужен cookie flow:

```bash
wscat -c "ws://localhost:8080/api/v1/whiteboards/ws/6d2d4d58-89b7-4b10-85be-a77f7e6738a0" \
  -H "Cookie: access_token=<ACCESS_TOKEN>"
```

## 5. JSON contract

```go
type Point struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}

type DrawAction struct {
    ID          string  `json:"id"`
    Tool        string  `json:"tool"`
    Color       string  `json:"color"`
    StrokeWidth float64 `json:"strokeWidth"`
    Points      []Point `json:"points"`
}

type WSMessage struct {
    Type     string      `json:"type"`
    RoomID   string      `json:"roomId"`
    UserID   uint        `json:"userId,omitempty"`
    Action   *DrawAction `json:"action,omitempty"`
    ActionID string      `json:"actionId,omitempty"`
    Payload  any         `json:"payload,omitempty"`
    SentAt   time.Time   `json:"sentAt,omitempty"`
}
```

Поддерживаемые типы сообщений:

- `init`
- `draw`
- `undo`
- `clear`
- `cursor`
- `ping`
- `pong`
- `error`

## 6. Пример draw message

```json
{
  "type": "draw",
  "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0",
  "action": {
    "id": "action-uuid-1",
    "tool": "brush",
    "color": "#FF0000",
    "strokeWidth": 4,
    "points": [
      {"x": 10, "y": 20},
      {"x": 15, "y": 25}
    ]
  }
}
```

## 7. Пример ответа broadcast

```json
{
  "type": "draw",
  "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0",
  "userId": 35,
  "action": {
    "id": "action-uuid-1",
    "tool": "brush",
    "color": "#FF0000",
    "strokeWidth": 4,
    "points": [
      {"x": 10, "y": 20},
      {"x": 15, "y": 25}
    ]
  },
  "sentAt": "2026-06-03T15:30:00Z"
}
```

## 8. Пример init message

```json
{
  "type": "init",
  "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0",
  "payload": {
    "actions": [
      {
        "type": "draw",
        "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0",
        "userId": 35,
        "action": {
          "id": "action-uuid-1",
          "tool": "brush",
          "color": "#FF0000",
          "strokeWidth": 4,
          "points": [
            {"x": 10, "y": 20},
            {"x": 15, "y": 25}
          ]
        }
      }
    ]
  }
}
```

## 9. Пример undo

```json
{
  "type": "undo",
  "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0",
  "actionId": "action-uuid-1"
}
```

## 10. Пример clear

```json
{
  "type": "clear",
  "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0"
}
```

## 11. Пример cursor

```json
{
  "type": "cursor",
  "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0",
  "payload": {
    "x": 100,
    "y": 200
  }
}
```

## 12. Ошибки

Возможные ошибки handshake:

- `401 Unauthorized`
- `403 Forbidden`
- `400 Bad Request`
- `404 Not Found`

Возможные WebSocket ошибки после подключения:

- `invalid message type`
- `invalid payload`
- `room not found / access denied`
- `message too large`
- `action not found`

`error`-сообщение приходит в формате:

```json
{
  "type": "error",
  "roomId": "6d2d4d58-89b7-4b10-85be-a77f7e6738a0",
  "payload": {
    "code": "BAD_REQUEST",
    "message": "invalid payload"
  }
}
```

## 13. Как работает внутри

- `Manager` хранит `map[boardID]*Room`.
- `Room.Run()` управляет `register`, `unregister`, `broadcast` и persistence ticks.
- `Client.ReadPump()` читает входящие JSON-сообщения.
- `Client.WritePump()` единолично пишет в `websocket.Conn`.
- `init` отправляется сразу после регистрации клиента в комнате.
- `draw`, `undo`, `clear` меняют in-memory state.
- `cursor` не сохраняется в БД.
- state периодически сохраняется в таблицу `whiteboard_states`.
- пустые комнаты автоматически удаляются из памяти после idle TTL.

## 14. State storage

PostgreSQL таблица:

- `whiteboard_states.board_id`
- `whiteboard_states.state`
- `whiteboard_states.created_by`
- `whiteboard_states.updated_by`
- `whiteboard_states.created_at`
- `whiteboard_states.updated_at`

Сохраняется именно актуальное состояние рисунка, а не transient cursor events.

## 15. Проверка

```bash
go test ./...
go test ./... -race
```

Ручная smoke-проверка:

1. Подключить двух клиентов к одной `board_id`.
2. Отправить `draw` из первого клиента.
3. Проверить broadcast у второго клиента.
4. Подключить третий клиент и проверить `init`.
5. Проверить `undo`.
6. Проверить `clear`.
7. Переподключиться и убедиться, что state восстановился из БД.

## 16. Checklist

- WebSocket endpoint работает.
- Авторизация работает.
- Два клиента видят рисование друг друга.
- Новый клиент получает `init state`.
- `undo` работает.
- `clear` работает.
- State сохраняется в БД.
- Повторное подключение восстанавливает state.
- `go test ./... -race` проходит.
