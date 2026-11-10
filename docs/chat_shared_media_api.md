# Chat Shared Media API

## Что изменилось

Добавлены новые API-ручки для получения общего списка фото и видео из истории чата с пагинацией:

- `GET /api/v1/conversations/{conversation_id}/shared-media`
- `GET /api/v1/groups/{group_id}/shared-media`

Ручки возвращают только сообщения:

- с `type IN ('image', 'video')`
- у которых заполнен `attachment_url`
- которые не удалены глобально (`messages.is_deleted = false`)
- которые не удалены персонально для текущего пользователя (`deleted_messages`)
- которые не скрыты логикой `last_cleared_at`

Это позволяет мобильному клиенту открывать экран "Фото и видео" без загрузки всей истории сообщений.

## Query-параметры

- `limit` — количество элементов на страницу, по умолчанию `30`
- `offset` — смещение для пагинации, по умолчанию `0`

## Формат ответа

```json
{
  "items": [
    {
      "message_id": "msg-uuid-111",
      "sender_id": 135,
      "sender_name": "Тамерлан Картаев",
      "sent_at": "2026-05-30T12:00:00Z",
      "type": "image",
      "attachment_url": "/uploads/media/encrypted_image_abc123.jpg",
      "attachment_type": "image/jpeg",
      "attachment_size": 2048576,
      "encrypted_keys": {
        "user_135": "aes-encrypted-key-for-tamerlan",
        "user_142": "aes-encrypted-key-for-askar"
      }
    },
    {
      "message_id": "msg-uuid-112",
      "sender_id": 142,
      "sender_name": "Аскар Абылхайыров",
      "sent_at": "2026-05-30T12:05:00Z",
      "type": "video",
      "attachment_url": "/uploads/media/encrypted_video_xyz789.mp4",
      "attachment_type": "video/mp4",
      "attachment_size": 15485760,
      "encrypted_keys": {
        "user_135": "aes-encrypted-key-for-tamerlan",
        "user_142": "aes-encrypted-key-for-askar"
      }
    }
  ],
  "total": 145,
  "has_more": true
}
```

## Пример для personal chat

```bash
curl 'https://alem-workspace.gov.kz/api/v1/conversations/8fd6f0df-fc80-49ae-9ebd-2e0a3238f1f5/shared-media?limit=30&offset=0' \
  -H 'accept: application/json' \
  -H 'authorization: Bearer <ACCESS_TOKEN>' \
  -H 'x-web-device-id: <WEB_DEVICE_ID>' \
  -b 'access_token=<ACCESS_TOKEN>; refresh_token=<REFRESH_TOKEN>'
```

## Пример для group chat

```bash
curl 'https://alem-workspace.gov.kz/api/v1/groups/c5f847b7-9e8d-4b0d-8be6-3c0c9e2ce501/shared-media?limit=30&offset=0' \
  -H 'accept: application/json' \
  -H 'authorization: Bearer <ACCESS_TOKEN>' \
  -H 'x-web-device-id: <WEB_DEVICE_ID>' \
  -b 'access_token=<ACCESS_TOKEN>; refresh_token=<REFRESH_TOKEN>'
```

## Пример успешного ответа

```json
{
  "items": [
    {
      "message_id": "4fd7f4d0-4b1d-4a2d-9a22-8dfb1d659001",
      "sender_id": 35,
      "sender_name": "Аскар Абылхайыров",
      "sent_at": "2026-05-30T10:15:43Z",
      "type": "image",
      "attachment_url": "/uploads/media/7f2d9f0e-photo.jpg",
      "attachment_type": "image/jpeg",
      "attachment_size": 1182456,
      "encrypted_keys": {}
    }
  ],
  "total": 1,
  "has_more": false
}
```

## Замечания по безопасности

- Не сохраняйте реальные `access_token`, `refresh_token` и cookie в документации, логах или примерах в репозитории.
- Для production-запросов используйте актуальный `Bearer` токен и `x-web-device-id`.

## Что реализовано в backend

- personal chat shared media: `GET /conversations/{conversation_id}/shared-media`
- group chat shared media: `GET /groups/{group_id}/shared-media`
- единый SQL-фильтр по `image` и `video`
- поддержка `total` и `has_more`
- учёт персонально удалённых сообщений
- учёт скрытия истории через `last_cleared_at`
