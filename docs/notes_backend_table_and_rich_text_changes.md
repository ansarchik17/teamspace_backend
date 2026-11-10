# Notes Backend: Table Ops and Rich Text Changes

## Что было сделано

В backend для заметок были добавлены 2 новые атомарные операции для `table` block:

1. Удаление одной колонки из таблицы.
2. Очистка одной ячейки без изменения структуры таблицы.

Также были усилены правила валидации rich text для `paragraph.runs`:

1. Backend теперь явно валидирует unsupported marks и возвращает `INVALID_MARK`.
2. Backend теперь явно валидирует некорректные paragraph runs и возвращает `INVALID_PARAGRAPH_RUNS`.
3. Для paragraph block запрещён пустой массив `runs`.
4. Для одного run запрещены дублирующиеся marks.
5. Порядок `runs` сохраняется при JSON round-trip.
6. Таблицы валидируются как прямоугольные: число колонок в каждой строке должно совпадать.

Во всех mutating-операциях заметок сохраняется текущий контракт:

1. `version` обязателен.
2. При успехе `version` увеличивается на `+1`.
3. `updated_at` обновляется.
4. Возвращается полная актуальная заметка, включая `content_doc`.
5. При конфликте версии backend возвращает `NOTE_VERSION_CONFLICT` с `details.server_note`.

## Новые endpoints

### 1. Delete column

`DELETE /api/v1/notes/{noteId}/tables/{blockId}/columns/{colIndex}`

Что делает:

- удаляет колонку `colIndex` во всех строках таблицы
- сохраняет прямоугольную структуру таблицы
- не даёт удалить последнюю оставшуюся колонку

### 2. Clear cell

`DELETE /api/v1/notes/{noteId}/tables/{blockId}/cells/{rowIndex}/{colIndex}`

Что делает:

- не удаляет ячейку структурно
- очищает только `text` у конкретной ячейки
- размер таблицы не меняется

## Curl: Delete column

### Request

```bash
curl -X DELETE "http://localhost:8080/api/v1/notes/b87851af-b3e6-4024-b31a-294b641abefd/tables/tbl_1/columns/1" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "version": 12
  }'
```

### Success response `200 OK`

```json
{
  "id": "b87851af-b3e6-4024-b31a-294b641abefd",
  "user_id": 101,
  "folder_id": null,
  "title": "Table note",
  "content": "",
  "content_doc": {
    "type": "doc",
    "version": 1,
    "blocks": [
      {
        "id": "tbl_1",
        "type": "table",
        "rows": [
          [
            { "text": "Name" },
            { "text": "Status" }
          ],
          [
            { "text": "Alice" },
            { "text": "Done" }
          ],
          [
            { "text": "Bob" },
            { "text": "In progress" }
          ]
        ]
      }
    ]
  },
  "content_text": "Name Status Alice Done Bob In progress ",
  "type": "rich",
  "checklist_items": [],
  "is_pinned": false,
  "is_archived": false,
  "is_deleted": false,
  "created_at": "2026-03-25T11:55:00Z",
  "updated_at": "2026-03-25T12:00:00Z",
  "version": 13,
  "attachments": []
}
```

### Error: table block not found `404`

```json
{
  "error": {
    "code": "TABLE_BLOCK_NOT_FOUND",
    "message": "Table block not found"
  }
}
```

### Error: column out of range `400`

```json
{
  "error": {
    "code": "TABLE_COLUMN_OUT_OF_RANGE",
    "message": "Table column index is out of range"
  }
}
```

### Error: last column delete forbidden `400`

```json
{
  "error": {
    "code": "TABLE_MIN_SIZE_REACHED",
    "message": "Table must keep at least one column"
  }
}
```

### Error: version conflict `409`

```json
{
  "error": {
    "code": "NOTE_VERSION_CONFLICT",
    "message": "Note version conflict. Please sync state from server.",
    "details": {
      "server_note": {
        "id": "b87851af-b3e6-4024-b31a-294b641abefd",
        "version": 13
      }
    }
  }
}
```

## Curl: Clear cell

### Request

```bash
curl -X DELETE "http://localhost:8080/api/v1/notes/b87851af-b3e6-4024-b31a-294b641abefd/tables/tbl_1/cells/1/2" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "version": 12
  }'
```

### Success response `200 OK`

```json
{
  "id": "b87851af-b3e6-4024-b31a-294b641abefd",
  "user_id": 101,
  "folder_id": null,
  "title": "Table note",
  "content": "",
  "content_doc": {
    "type": "doc",
    "version": 1,
    "blocks": [
      {
        "id": "tbl_1",
        "type": "table",
        "rows": [
          [
            { "text": "Name" },
            { "text": "Role" },
            { "text": "Status" }
          ],
          [
            { "text": "Alice" },
            { "text": "Manager" },
            { "text": "" }
          ]
        ]
      }
    ]
  },
  "content_text": "Name Role Status Alice Manager ",
  "type": "rich",
  "checklist_items": [],
  "is_pinned": false,
  "is_archived": false,
  "is_deleted": false,
  "created_at": "2026-03-25T11:55:00Z",
  "updated_at": "2026-03-25T12:03:00Z",
  "version": 13,
  "attachments": []
}
```

### Error: row out of range `400`

```json
{
  "error": {
    "code": "TABLE_ROW_OUT_OF_RANGE",
    "message": "Table row index is out of range"
  }
}
```

### Error: column out of range `400`

```json
{
  "error": {
    "code": "TABLE_COLUMN_OUT_OF_RANGE",
    "message": "Table column index is out of range"
  }
}
```

### Error: table block not found `404`

```json
{
  "error": {
    "code": "TABLE_BLOCK_NOT_FOUND",
    "message": "Table block not found"
  }
}
```

### Error: version conflict `409`

```json
{
  "error": {
    "code": "NOTE_VERSION_CONFLICT",
    "message": "Note version conflict. Please sync state from server.",
    "details": {
      "server_note": {
        "id": "b87851af-b3e6-4024-b31a-294b641abefd",
        "version": 13
      }
    }
  }
}
```

## Curl: Update rich note with mixed paragraph runs

Этот пример показывает, как теперь корректно передавать mixed formatting внутри одного paragraph.

### Request

```bash
curl -X PATCH "http://localhost:8080/api/v1/notes/b87851af-b3e6-4024-b31a-294b641abefd" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "version": 13,
    "type": "rich",
    "content_doc": {
      "type": "doc",
      "version": 1,
      "blocks": [
        {
          "id": "p1",
          "type": "paragraph",
          "runs": [
            { "text": "Normal " },
            { "text": "Bold", "marks": ["bold"] },
            { "text": " Italic", "marks": ["italic"] },
            { "text": " Both", "marks": ["bold", "italic"] }
          ]
        }
      ]
    }
  }'
```

### Success response `200 OK`

```json
{
  "id": "b87851af-b3e6-4024-b31a-294b641abefd",
  "user_id": 101,
  "folder_id": null,
  "title": "Rich note",
  "content": "",
  "content_doc": {
    "type": "doc",
    "version": 1,
    "blocks": [
      {
        "id": "p1",
        "type": "paragraph",
        "runs": [
          { "text": "Normal " },
          { "text": "Bold", "marks": ["bold"] },
          { "text": " Italic", "marks": ["italic"] },
          { "text": " Both", "marks": ["bold", "italic"] }
        ]
      }
    ]
  },
  "content_text": "Normal Bold  Italic  Both ",
  "type": "rich",
  "checklist_items": [],
  "is_pinned": false,
  "is_archived": false,
  "is_deleted": false,
  "created_at": "2026-03-25T11:00:00Z",
  "updated_at": "2026-03-25T12:05:00Z",
  "version": 14,
  "attachments": []
}
```

## Rich text validation errors

### Error: unsupported mark `400`

```json
{
  "error": {
    "code": "INVALID_MARK",
    "message": "block[0].runs[0]: unknown mark \"underline\""
  }
}
```

### Error: empty paragraph runs `400`

```json
{
  "error": {
    "code": "INVALID_PARAGRAPH_RUNS",
    "message": "block[0]: paragraph must contain at least one run"
  }
}
```

### Error: duplicate marks in one run `400`

```json
{
  "error": {
    "code": "INVALID_PARAGRAPH_RUNS",
    "message": "block[0].runs[0]: duplicate mark \"bold\""
  }
}
```

## Технически что изменилось в коде

Изменения были внесены в следующие части backend:

1. `internal/service/note_service.go`
   Реализована бизнес-логика `DeleteTableColumn` и `ClearTableCell`.

2. `internal/handler/note_handler.go`
   Добавлены HTTP handlers под новые routes.

3. `internal/server/router.go`
   Зарегистрированы новые endpoints.

4. `internal/domain/note.go`
   Усилена валидация `content_doc`, `paragraph.runs` и table shape.

5. `internal/apperrors/errors.go`
   Добавлены новые application error codes.

6. `docs/swagger.yaml`, `docs/swagger.json`, `docs/docs.go`
   Swagger был regenerated и теперь содержит новые endpoints.

## Что важно для mobile/frontend

1. После каждой mutate-операции использовать ответ backend как source of truth.
2. Не вычислять новый `version` локально.
3. При `NOTE_VERSION_CONFLICT` брать `details.server_note.version` и синкаться с сервером.
4. Для rich text передавать paragraph как массив `runs`, а не как один склеенный кусок текста.
5. Для таблиц считать backend-правилом, что таблица обязана быть прямоугольной.
