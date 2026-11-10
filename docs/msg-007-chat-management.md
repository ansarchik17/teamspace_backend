# MSG-007 — управление участниками и уведомлениями чата

Backend-контракт для web/mobile: назначение администраторов, исключение участников и персональный mute. Все endpoints требуют `Authorization: Bearer <access_token>`.

## Роли и правила доступа

### Группы

- `owner` назначает и снимает администраторов, исключает администраторов и участников.
- `admin` добавляет и исключает обычных участников, но не может управлять владельцем или другим администратором.
- `member` не управляет составом группы.
- Владелец не может изменить собственную роль или покинуть группу. Передача владения отдельным endpoint пока не поддерживается.
- Для собственного выхода используется `POST /api/v1/groups/{group_id}/leave`, а не endpoint исключения.

### Каналы

- `owner` назначает и снимает администраторов, исключает администраторов и подписчиков.
- `admin` добавляет и исключает обычных подписчиков, но не может управлять владельцем или другим администратором.
- `subscriber` не управляет составом канала.
- Владелец не может изменить собственную роль или быть исключён.
- Для собственной отписки используется `DELETE /api/v1/channels/{channel_id}/subscribe`.

Mute всегда персональный: он не прекращает доставку сообщений и WebSocket-событий, а отключает обычные push-уведомления для текущего пользователя. Push при явном `@упоминании` продолжает отправляться.

## Группы

### Изменить роль участника

```http
PUT /api/v1/groups/{group_id}/members/{user_id}/role
Content-Type: application/json

{"role":"admin"}
```

Допустимые роли: `admin`, `member`. Выполнять операцию может только владелец.

Ответ `200`:

```json
{"message":"role updated successfully"}
```

### Исключить участника

```http
DELETE /api/v1/groups/{group_id}/members/{user_id}
```

Ответ `200`:

```json
{"message":"member removed successfully"}
```

После исключения у записи участника устанавливается `left_at`; история сообщений сохраняется. Группа отправляет участникам системное сообщение об изменении состава.

### Добавить участников

```http
POST /api/v1/groups/{group_id}/members
Content-Type: application/json

{"user_ids":[42,43]}
```

Операция доступна владельцу и администраторам с учётом `max_members`.

### Покинуть группу

```http
POST /api/v1/groups/{group_id}/leave
```

Владелец не может покинуть группу, пока остаётся владельцем.

### Mute/unmute группы

```http
PUT /api/v1/groups/{group_id}/mute
Content-Type: application/json

{"mute":true}
```

Ответ содержит актуальный `is_muted` и обновлённое представление группы:

```json
{
  "message":"group mute setting updated",
  "group_id":"GROUP_UUID",
  "is_muted":true,
  "group":{}
}
```

## Каналы

### Изменить роль подписчика

```http
PUT /api/v1/channels/{channel_id}/members/{user_id}/role
Content-Type: application/json

{"role":"admin"}
```

Допустимые роли: `admin`, `subscriber`. Выполнять операцию может только владелец.

Ответ `200`:

```json
{"message":"role updated"}
```

### Исключить подписчика

```http
DELETE /api/v1/channels/{channel_id}/members/{user_id}
```

Ответ `200`:

```json
{"message":"member removed"}
```

После исключения у записи подписчика устанавливается `left_at`; история постов сохраняется.

### Добавить подписчиков

```http
POST /api/v1/channels/{channel_id}/members
Content-Type: application/json

{"user_ids":[42,43]}
```

Доступно владельцу и администраторам.

### Получить участников

```http
GET /api/v1/channels/{channel_id}/members?role=admin&limit=100&offset=0
GET /api/v1/channels/{channel_id}/admins
```

Полный список участников доступен владельцу и администраторам. Список администраторов доступен только активным подписчикам канала.

### Отписаться от канала

```http
DELETE /api/v1/channels/{channel_id}/subscribe
```

### Mute/unmute канала

```http
PUT /api/v1/channels/{channel_id}/mute
Content-Type: application/json

{"mute":true}
```

Ответ `200`:

```json
{"muted":true}
```

## Личные диалоги

```http
PUT /api/v1/conversations/{conversation_id}/mute
Content-Type: application/json

{"mute":true}
```

Операция доступна только активному участнику диалога. Ответ содержит `conversation_id`, `is_muted` и обновлённый объект `chat`.

## Ошибки

Формат:

```json
{
  "error": {
    "code":"FORBIDDEN",
    "message":"only owner can change member roles",
    "user_message":"Недостаточно прав для выполнения операции."
  }
}
```

| HTTP | Когда |
|---|---|
| `400` | Некорректный UUID, user ID, JSON или роль |
| `401` | Отсутствует или недействителен access token |
| `403` | Пользователь не состоит в чате либо не имеет требуемой роли |
| `404` | Целевой участник или чат не найден |
| `409` | Конфликт состояния, если он будет возвращён нижележащим хранилищем |

Frontend не должен считать локальное изменение успешным до ответа `2xx`. После изменения роли или состава рекомендуется заново загрузить сведения о группе/канале. Специальные WebSocket-события `member_role_updated`, `member_removed` и `chat_muted` текущим backend-контрактом не гарантируются.
