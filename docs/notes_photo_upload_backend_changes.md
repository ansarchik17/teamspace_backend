# Notes Photo Upload Backend Changes

## Что исправлено

Проблема была в том, что backend генерировал `upload_url` через внутренний MinIO endpoint, например `messenger-minio:9000`, который доступен только внутри docker network и не открывается с iPad/iPhone/Android.

В backend внесены следующие изменения:

1. Presigned `upload_url` теперь переписывается на внешний base URL из `MINIO_PUBLIC_BASE_URL`.
2. Для bucket на старте выставляется CORS-конфигурация под `GET`, `HEAD`, `PUT`.
3. `complete` теперь проверяет, что объект реально существует в storage, и только после этого переводит attachment в `uploaded`.
4. `POST /notes/{id}/attachments/{attachmentId}/complete` теперь возвращает полную актуальную заметку, а не только attachment.
5. Photo attachments теперь попадают в `note.attachments` при `GET /notes/{id}`.
6. Для photo attachments `download_url` формируется через внешний API URL, поэтому он доступен мобильному клиенту.

## Используемые env

Пример:

```env
APP_PUBLIC_URL=http://92.38.48.9:18080
MINIO_ENDPOINT=messenger-minio:9000
MINIO_BUCKET=messenger
MINIO_PUBLIC_BASE_URL=http://92.38.48.9:19000/messenger
MINIO_USE_SSL=false
```

Логика такая:

- `MINIO_ENDPOINT` может оставаться внутренним для backend
- `MINIO_PUBLIC_BASE_URL` должен быть внешним и доступным с mobile/web
- `APP_PUBLIC_URL` используется для `download_url` через API proxy

## Новый mobile flow

1. `POST /api/v1/notes/{noteId}/attachments/presign`
2. `PUT {upload_url}`
3. `POST /api/v1/notes/{noteId}/attachments/{attachmentId}/complete`
4. `GET /api/v1/notes/{noteId}`

## Curl: presign

```bash
curl -X POST "http://localhost:8080/api/v1/notes/b87851af-b3e6-4024-b31a-294b641abefd/attachments/presign" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "mime_type": "image/jpeg",
    "size_bytes": 524288
  }'
```

### Пример ответа `201`

```json
{
  "attachment_id": "11111111-1111-1111-1111-111111111111",
  "upload_url": "http://92.38.48.9:19000/messenger/note-photos/b87851af-b3e6-4024-b31a-294b641abefd/11111111-1111-1111-1111-111111111111?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=...",
  "storage_key": "note-photos/b87851af-b3e6-4024-b31a-294b641abefd/11111111-1111-1111-1111-111111111111",
  "expires_at": "2026-03-25T18:30:00Z"
}
```

Что важно:

- `upload_url` больше не должен содержать `messenger-minio`
- host должен совпадать с внешним storage host из `MINIO_PUBLIC_BASE_URL`

## Curl: upload file to presigned URL

```bash
curl -X PUT "http://92.38.48.9:19000/messenger/note-photos/b87851af-b3e6-4024-b31a-294b641abefd/11111111-1111-1111-1111-111111111111?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=..." \
  -H "Content-Type: image/jpeg" \
  --data-binary "@photo.jpg"
```

Ожидаемое поведение:

- storage отвечает успешным `200 OK` или `204 No Content`
- upload проходит с внешнего клиента без `Failed host lookup`

## Curl: complete

```bash
curl -X POST "http://localhost:8080/api/v1/notes/b87851af-b3e6-4024-b31a-294b641abefd/attachments/11111111-1111-1111-1111-111111111111/complete" \
  -H "Authorization: Bearer <TOKEN>"
```

### Пример ответа `200`

```json
{
  "id": "b87851af-b3e6-4024-b31a-294b641abefd",
  "user_id": 101,
  "folder_id": null,
  "title": "Photo note",
  "content": "",
  "content_doc": {
    "type": "doc",
    "version": 1,
    "blocks": [
      {
        "id": "img_1",
        "type": "image",
        "attachmentId": "11111111-1111-1111-1111-111111111111",
        "alt": "Uploaded photo"
      }
    ]
  },
  "content_text": "",
  "type": "rich",
  "checklist_items": [],
  "is_pinned": false,
  "is_archived": false,
  "is_deleted": false,
  "created_at": "2026-03-25T17:55:00Z",
  "updated_at": "2026-03-25T18:05:00Z",
  "version": 4,
  "attachments": [
    {
      "file_id": "11111111-1111-1111-1111-111111111111",
      "real_name": "",
      "mime_type": "image/jpeg",
      "size": 524288,
      "download_url": "http://92.38.48.9:18080/api/v1/files/download?object_name=note-photos/b87851af-b3e6-4024-b31a-294b641abefd/11111111-1111-1111-1111-111111111111",
      "created_at": "2026-03-25T18:00:00Z"
    }
  ]
}
```

## Curl: read note after complete

```bash
curl -X GET "http://localhost:8080/api/v1/notes/b87851af-b3e6-4024-b31a-294b641abefd" \
  -H "Authorization: Bearer <TOKEN>"
```

### Что теперь должно быть в ответе

- photo attachment присутствует в `attachments`
- `attachments[].download_url` указывает на внешний API URL
- mobile может открыть его и отрисовать фото

## Ошибки

### Presign failed

```json
{
  "error": {
    "code": "ATTACHMENT_PRESIGN_FAILED",
    "message": "Failed to create upload URL"
  }
}
```

### Attachment not found

```json
{
  "error": {
    "code": "ATTACHMENT_NOT_FOUND",
    "message": "Attachment not found"
  }
}
```

### Attachment not uploaded

```json
{
  "error": {
    "code": "ATTACHMENT_NOT_UPLOADED",
    "message": "Attachment was not uploaded to storage"
  }
}
```

### Upload failed

```json
{
  "error": {
    "code": "UPLOAD_FAILED",
    "message": "Upload failed"
  }
}
```

### Invalid file type

```json
{
  "error": {
    "code": "INVALID_FILE_TYPE",
    "message": "Invalid file type. Allowed: image/jpeg, image/png, image/heic, image/webp"
  }
}
```

### File too big

```json
{
  "error": {
    "code": "FILE_TOO_BIG",
    "message": "File size exceeds maximum of 15728640 bytes"
  }
}
```

## Что поменялось в коде

1. `internal/service/file_service.go`
   Переписывание presigned URL на внешний host, проверка существования объекта, bucket CORS.

2. `internal/service/note_service.go`
   `complete` теперь возвращает полную заметку, а photo attachments подмешиваются в `note.attachments`.

3. `internal/repository/note_repository.go`
   Добавлен список uploaded photo attachments.

4. `internal/handler/note_handler.go`
   Swagger/handler contract для `complete` обновлён под возврат полной заметки.

5. `internal/apperrors/errors.go`
   Добавлены коды:
   - `ATTACHMENT_PRESIGN_FAILED`
   - `UPLOAD_FAILED`
   - `ATTACHMENT_NOT_FOUND`
   - `ATTACHMENT_NOT_UPLOADED`
