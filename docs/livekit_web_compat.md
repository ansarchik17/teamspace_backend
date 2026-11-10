# LiveKit Web Compatibility

Этот backend-патч сделан как additive-слой поверх уже работающего mobile flow.

Что осталось без изменений:
- `POST /api/v1/calls/start`
- `POST /api/v1/calls/group/start`
- `POST /api/v1/calls/accept`
- `POST /api/v1/calls/rejoin`
- `POST /api/v1/calls/decline`
- `POST /api/v1/calls/invite`
- все текущие push payload для mobile

Что добавлено для web:
- `GET /api/v1/calls/active`
- WS `incoming_call` при обычном direct `start`
- WS `call_invite` при group `start`
- WS `incoming_call` или `call_invite` при `invite` в уже существующий звонок
- WS `call_accepted` при `accept`

## WS events

### `incoming_call`
Используется для личного звонка или invite в существующий direct-call room.

Пример payload:

```json
{
  "type": "incoming_call",
  "content": {
    "call_id": "uuid",
    "room_id": "call_1_2_1711111111",
    "room_name": "call_1_2_1711111111",
    "chat_id": "conversation-id",
    "from_user_id": 123,
    "from_name": "Alice",
    "is_video": true,
    "sent_at": "2026-04-06T12:00:00Z",
    "sent_at_unix": 1775476800
  }
}
```

### `call_invite`
Используется для group-call invite.

Пример payload:

```json
{
  "type": "call_invite",
  "content": {
    "call_id": "uuid",
    "room_name": "call_group_group-uuid_1711111111",
    "room_id": "call_group_group-uuid_1711111111",
    "chat_id": "group-uuid",
    "group_id": "group-uuid",
    "group_name": "Finance Team",
    "from_user_id": 123,
    "from_name": "Alice",
    "from_full_name": "Alice",
    "is_video": true,
    "sent_at": "2026-04-06T12:00:00Z",
    "sent_at_unix": 1775476800
  }
}
```

### `call_accepted`
Уходит тому, кто звонил, когда второй участник принял звонок.

Пример payload:

```json
{
  "type": "call_accepted",
  "content": {
    "call_id": "uuid",
    "room_name": "call_1_2_1711111111",
    "accepted_by_user_id": 456,
    "accepted_by_name": "Bob"
  }
}
```

## Recovery endpoint

`GET /api/v1/calls/active`

Назначение:
- восстановить входящий/ringing state после reload
- восстановить ongoing call banner
- не ломать старый `GET /api/v1/calls`

Backend возвращает только те звонки, которые ещё можно считать активными или recoverable:
- room реально жива в LiveKit
- или звонок ещё в коротком ringing window
- или LiveKit временно недоступен, но call в `accepted/ongoing`

## Рекомендованный web flow

1. Держать основной app websocket подключённым.
2. На `incoming_call` или `call_invite` показывать incoming modal.
3. На `Accept` вызывать `POST /api/v1/calls/accept`.
4. После `accept` подключаться к LiveKit room.
5. На `Decline` вызывать `POST /api/v1/calls/decline`.
6. После reload делать `GET /api/v1/calls/active`.
7. Для reconnect в уже текущий звонок использовать `POST /api/v1/calls/rejoin`.

## Важно для web identity

На `start`, `accept`, `rejoin`, `join` нужно передавать:
- `X-Call-Session-ID`
- `X-Web-Device-ID` или `X-Device-ID`

Это нужно, чтобы identity в LiveKit была стабильной и не конфликтовала между вкладками.
