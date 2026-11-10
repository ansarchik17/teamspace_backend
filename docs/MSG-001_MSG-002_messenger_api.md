# MSG-001 / MSG-002 — Корпоративный мессенджер: API-документация

## Задачи

| ID | Описание |
|----|----------|
| MSG-001 | Структура чатов — личные чаты, групповые чаты, каналы |
| MSG-002 | Обмен контентом — текст, файлы, изображения, голосовые сообщения |

---

## Аутентификация

Все запросы требуют:

```
Authorization: Bearer <ACCESS_TOKEN>
x-web-device-id: <WEB_DEVICE_ID>
```

---

## MSG-001 — Структура чатов

### 1. Личные чаты (Conversations)

#### Создать / получить личный чат

```
POST /api/v1/conversations
```

**Body:**
```json
{
  "peer_id": 42
}
```

**Ответ `200`:**
```json
{
  "id": "uuid",
  "peer_id": 42,
  "created_at": "2026-09-22T10:00:00Z"
}
```

> Если чат уже существует — возвращает существующий (idempotent).

---

#### Список личных чатов

```
GET /api/v1/conversations?limit=20&offset=0
```

**Ответ `200`:**
```json
{
  "conversations": [...],
  "count": 5
}
```

---

#### Удалить / скрыть личный чат

```
DELETE /api/v1/conversations/{conversation_id}
```

> Скрывает чат для текущего пользователя, не удаляет для собеседника.

#### Восстановить скрытый чат

```
POST /api/v1/conversations/{conversation_id}/unhide
```

#### Замьютить / размьютить уведомления

```
PUT /api/v1/conversations/{conversation_id}/mute
```

**Body:**
```json
{ "mute": true }
```

---

### 2. Групповые чаты (Groups)

#### Создать группу

```
POST /api/v1/groups
```

**Body:**
```json
{
  "name": "Название группы",
  "member_ids": [1, 2, 3]
}
```

#### Получить список групп

```
GET /api/v1/groups
```

#### Получить группу

```
GET /api/v1/groups/{group_id}
```

#### Обновить группу

```
PUT /api/v1/groups/{group_id}
```

#### Обновить аватар группы

```
PUT /api/v1/groups/{group_id}/avatar
```

#### Удалить группу (только владелец)

```
DELETE /api/v1/groups/{group_id}
```

#### Скрыть / восстановить группу

```
DELETE /api/v1/groups/{group_id}/hide
POST   /api/v1/groups/{group_id}/unhide
```

#### Участники

```
POST   /api/v1/groups/{group_id}/members              — добавить участника
DELETE /api/v1/groups/{group_id}/members/{user_id}    — удалить участника
POST   /api/v1/groups/{group_id}/leave                — покинуть группу
PUT    /api/v1/groups/{group_id}/members/{user_id}/role  — изменить роль
```

#### Замьютить группу

```
PUT /api/v1/groups/{group_id}/mute
```

**Body:** `{ "mute": true }`

---

### 3. Каналы (Channels)

#### Создать канал

```
POST /api/v1/channels
```

**Body:**
```json
{
  "name": "Название канала",
  "username": "channel_username",
  "description": "Описание"
}
```

#### Мои каналы

```
GET /api/v1/channels
```

#### Поиск каналов

```
GET /api/v1/channels/search?q=название
```

#### Получить канал по ID / по username

```
GET /api/v1/channels/{channel_id}
GET /api/v1/channels/username/{username}
```

#### Обновить канал / аватар

```
PUT /api/v1/channels/{channel_id}
PUT /api/v1/channels/{channel_id}/avatar
```

#### Удалить канал

```
DELETE /api/v1/channels/{channel_id}
```

#### Подписка

```
POST   /api/v1/channels/{channel_id}/subscribe
DELETE /api/v1/channels/{channel_id}/subscribe
```

#### Замьютить канал

```
PUT /api/v1/channels/{channel_id}/mute
```

**Body:** `{ "mute": true }`

#### Участники / администраторы

```
GET  /api/v1/channels/{channel_id}/members
GET  /api/v1/channels/{channel_id}/admins
POST /api/v1/channels/{channel_id}/members          — добавить участников
PUT  /api/v1/channels/{channel_id}/members/{user_id}/role
DELETE /api/v1/channels/{channel_id}/members/{user_id}
```

#### Посты канала

```
POST /api/v1/channels/{channel_id}/posts        — создать пост
GET  /api/v1/channels/{channel_id}/posts        — список постов
GET  /api/v1/channels/{channel_id}/posts/pinned — закреплённые посты
```

#### Действия с постом

```
GET    /api/v1/channels/posts/{post_id}
PUT    /api/v1/channels/posts/{post_id}
DELETE /api/v1/channels/posts/{post_id}
POST   /api/v1/channels/posts/{post_id}/view       — отметить просмотренным
GET    /api/v1/channels/posts/{post_id}/viewers    — кто просмотрел
POST   /api/v1/channels/posts/{post_id}/pin        — закрепить
POST   /api/v1/channels/posts/{post_id}/reactions/{reaction}
DELETE /api/v1/channels/posts/{post_id}/reactions/{reaction}
```

---

## MSG-002 — Обмен контентом

### Отправка сообщений

#### В личный чат

```
POST /api/v1/conversations/{conversation_id}/messages
```

#### В групповой чат

```
POST /api/v1/groups/{group_id}/messages
```

**Body (одинаковый для обоих):**
```json
{
  "content": "Текст сообщения",
  "type": "text",
  "attachment_url": "/uploads/file.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 204800,
  "reply_to_id": "uuid-сообщения-для-ответа",
  "is_ephemeral": false,
  "ttl_seconds": 60
}
```

**Поле `type`** — одно из:
| Значение | Описание |
|----------|----------|
| `text` | Текстовое сообщение |
| `image` | Изображение |
| `video` | Видео |
| `file` | Файл любого формата |
| `audio` | Аудиофайл |
| `voice` | Голосовое сообщение |
| `video_message` | Видеосообщение (кружок) |

> `content` или `attachment_url` — хотя бы одно должно быть заполнено.

**Ответ `201`:** объект `MessageView`

---

### Получение истории сообщений

#### Личный чат

```
GET /api/v1/conversations/{conversation_id}/messages?limit=30&offset=0
```

#### Групповой чат

```
GET /api/v1/groups/{group_id}/messages?limit=30&offset=0
```

---

### Действия с сообщениями

#### Редактировать сообщение

```
PUT /api/v1/messages/{message_id}
```

**Body:** `{ "content": "Новый текст" }`

#### Удалить одно сообщение

```
DELETE /api/v1/messages/{message_id}
```

#### Удалить несколько сообщений

```
DELETE /api/v1/messages
```

**Body:**
```json
{
  "message_ids": ["uuid1", "uuid2"]
}
```

#### Переслать сообщения

```
POST /api/v1/messages/forward
```

**Body:**
```json
{
  "messages": [
    {
      "original_message_id": "uuid",
      "target_chat_id": "uuid",
      "content": "Forwarded text"
    }
  ]
}
```

#### Отметить прочитанным

```
POST /api/v1/messages/{message_id}/read
```

#### Кто просмотрел сообщение

```
GET /api/v1/messages/{message_id}/viewers
```

#### Реакции

```
POST   /api/v1/messages/{message_id}/reactions      — поставить реакцию
DELETE /api/v1/messages/{message_id}/reactions/{reaction}  — убрать реакцию
```

**Body для добавления:** `{ "reaction": "👍" }`

---

### Общие медиафайлы (фото и видео из истории)

```
GET /api/v1/conversations/{conversation_id}/shared-media?limit=30&offset=0
GET /api/v1/groups/{group_id}/shared-media?limit=30&offset=0
```

Возвращает только сообщения типа `image` и `video` с `attachment_url`.

**Ответ:**
```json
{
  "items": [
    {
      "message_id": "uuid",
      "sender_id": 42,
      "sender_name": "Имя Фамилия",
      "sent_at": "2026-09-22T10:00:00Z",
      "type": "image",
      "attachment_url": "/uploads/media/photo.jpg",
      "attachment_type": "image/jpeg",
      "attachment_size": 204800
    }
  ],
  "total": 145,
  "has_more": true
}
```

---

### Поделиться задачей / событием / заметкой в чат

```
POST /api/v1/chats/share
```

**Body:**
```json
{
  "target_chat_id": "uuid-чата-или-группы",
  "item_type": "task",
  "item_id": "item-uuid",
  "comment": "Смотри эту задачу"
}
```

**`item_type`:** `task` | `event` | `note`

---

## Права доступа

| Действие | Кто может |
|----------|-----------|
| Удалить группу | Только владелец |
| Удалить канал | Только владелец |
| Кикнуть из канала | Администратор / владелец |
| Редактировать чужое сообщение | Нельзя |
| Скрыть чат | Любой участник (только для себя) |
