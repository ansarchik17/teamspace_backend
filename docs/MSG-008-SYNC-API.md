# MSG-008 — восстановление соединения и синхронизация сообщений

Единый контракт для frontend/mobile. Backend подключает HTTP API и WebSocket
`sync_request`. Миграция `20260924000100_add_message_sync_tracking.sql` добавляет
монотонный `messages.sequence_number` и подтверждённый курсор устройства.

## Гарантии и область действия

- Возвращаются только сообщения личных и групповых чатов, доступных текущему пользователю.
- Удалённые для всех и скрытые только для текущего пользователя сообщения не возвращаются.
- Порядок строго возрастающий по `sequence_number`.
- Повтор сообщения допустим при разрыве между получением и подтверждением; клиент обязан
  дедуплицировать по `message.id`. Потеря подтверждённого сообщения не допускается.
- Один batch содержит не более 500 сообщений; рекомендуемый размер — 100.
- Канальные посты и пропущенные события edit/delete/reaction/status в этот API не входят.
  После reconnect клиент обновляет открытый чат/канал обычными history API.

## HTTP sync

`POST /api/v1/messages/sync`

Требуется `Authorization: Bearer <token>`.

```json
{
  "device_id": "web-550e8400-e29b-41d4-a716-446655440000",
  "conversation_id": "optional-chat-or-group-uuid",
  "after_sequence": 1042,
  "limit": 100
}
```

- `device_id` — постоянный ID установки: 1–128 символов `[A-Za-z0-9._:-]`.
- `conversation_id` — необязательный фильтр одного личного/группового чата.
- `after_sequence` — последний успешно применённый клиентом sequence. При первом запросе
  можно не передавать: backend использует подтверждённый курсор устройства.
- `limit` — `1..500`, по умолчанию `100`.

Ответ:

```json
{
  "messages": [{
    "id": "message-uuid",
    "conversation_id": "chat-uuid",
    "sequence_number": 1043,
    "content": "Сообщение"
  }],
  "has_more": false,
  "last_sequence": 1043,
  "synced_at": "2026-09-24T10:00:00Z"
}
```

Пустой batch возвращает переданный/сохранённый курсор в `last_sequence`.

## WebSocket sync

После успешного переподключения отправьте:

```json
{
  "type": "sync_request",
  "device_id": "web-550e8400-e29b-41d4-a716-446655440000",
  "last_sequence": 1042,
  "sync_limit": 100
}
```

Backend отвечает событием `sync_response`; HTTP-ответ выше находится в поле `content`.
При ошибке приходит `sync_error` с `content.error`.

## Подтверждение позиции

Endpoint нужен, если клиент не делает следующий sync-запрос сразу после batch:

`PUT /api/v1/messages/sync/position`

```json
{
  "device_id": "web-550e8400-e29b-41d4-a716-446655440000",
  "conversation_id": "optional-chat-or-group-uuid",
  "sequence": 1043
}
```

Текущий курсор:

`GET /api/v1/messages/sync/status?device_id=web-...&conversation_id=optional-uuid`

## Обязательный алгоритм клиента

1. Сохранить постоянный `device_id` для установки.
2. После reconnect временно складывать live-события в очередь.
3. Отправить sync с последним успешно применённым sequence.
4. Для каждого сообщения сделать upsert по `message.id`, а не append.
5. Сортировать по `sequence_number`; использовать `id` как уникальный ключ.
6. Только после применения всего batch сохранить `last_sequence`.
7. Пока `has_more=true`, повторять запрос с новым `last_sequence`.
8. Применить накопленные live-события тем же idempotent upsert и обновить историю
   открытого чата/канала для пропущенных edit/delete/reaction/status событий.
9. При `sync_error` выполнить тот же цикл через HTTP.

```ts
const byId = new Map(messages.map(message => [message.id, message]));
for (const message of response.messages) {
  byId.set(message.id, { ...byId.get(message.id), ...message });
}
if (response.has_more) requestSync(response.last_sequence);
```

Не используйте ограниченный список последних ID как единственную защиту: источник истины —
нормализованное хранилище `id -> message` и сохранённый sequence.

## Ошибки

- `400` — некорректный JSON, device ID, UUID, sequence или limit.
- `401` — отсутствует/просрочен access token.
- `500` — ошибка синхронизации; сохранить прежний курсор и повторить с backoff.

Reconnect backoff с jitter: `1s, 2s, 4s, 8s, 15s, 30s` (максимум 30 секунд).

## Приёмка

1. Получить A, сохранить ID и sequence.
2. Отключить сеть, отправить с другого аккаунта B/C/D.
3. Восстановить сеть: B/C/D появляются один раз и по порядку.
4. Оборвать соединение во время batch и повторить sync: повтор не создаёт дубль.
5. Проверить backlog больше `limit` до `has_more=false`.
6. Проверить два устройства с разными `device_id`.
7. Проверить, что чужой чат и удалённые сообщения не возвращаются.
