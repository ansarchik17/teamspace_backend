# NWS-003: Медиаконтент — Изображения, файлы и форматирование текста

## 1. Обзор задачи

Реализация бэкенда для таска **NWS-003**:
- **Загрузка галерей картинок**: загрузка пачек изображений с автоматическим выставлением порядка (`order_index`), подписями (`caption`) и созданием блока `gallery` в визуальном редакторе.
- **Прикрепление PDF и документов**: поддержка загрузки PDF, Word (`.doc`, `.docx`), Excel (`.xls`, `.xlsx`), PowerPoint, архивов и других файлов с корректным распознаванием MIME-типов (`application/pdf` и др.) и размера файла.
- **Видео-вставки (Video Embeds)**: парсинг и вставка видео из YouTube (включая ссылки `watch`, `youtu.be`, `embed`, `shorts`), RuTube, Vimeo, а также прямых ссылок (`.mp4`, `.webm`). Формирование стандартизированных `embed_url` для безопасного iframe встраивания на фронтенде.
- **Форматирование текста в ContentDoc**: поддержка форматирования (жирный `bold`, курсив `italic`, зачёркнутый `strike`, подчёркнутый `underline`, моноширинный `code`, ссылки `link`, цветовые метки `color:...`, подстрочный/надстрочный, маркер `highlight`), заголовков разного уровня `heading` (H1-H6), списков, чек-листов, таблиц.
- **Безопасная подписанная отдача медиа**: генерация пресигнед/подписанных URL для файлов и миниатюр (`thumbnail_url`).

---

## 2. Миграция базы данных

Файл: `db/migrations/20260929001100_publications_media_gallery_pdf_video.sql`

Расширение таблицы `publication_attachments`:
- `caption TEXT` — подпись к медиа или изображению в галерее.
- `order_index INT NOT NULL DEFAULT 0` — порядковый номер медиа-файла для сортировки в галерее / списке вложений.
- `mime_type VARCHAR(100)` — точный MIME-тип (`application/pdf`, `image/jpeg`, `video/mp4` и т.д.).
- `thumbnail_url TEXT` — ссылка на миниатюру изображения или превью видео.
- `video_provider VARCHAR(50)` — провайдер видео (`youtube`, `rutube`, `vimeo`, `direct`).
- `video_embed_url TEXT` — сгенерированный URL для `<iframe>` плеера.
- Индекс: `idx_publication_attachments_order` по `(publication_id, order_index ASC)`.

---

## 3. Блоки визуального редактора (`ContentBlock`)

Поддерживаемые типы блоков в `ContentDoc` для новостей:
1. `paragraph`: абзац текста со списком `runs` (поддерживает `bold`, `italic`, `strike`, `underline`, `code`, `link`, `color:#hex`/`color:rgb(...)`, `highlight`, `subscript`, `superscript`).
2. `heading`: заголовок (`level` 1..6, текстовые `runs`).
3. `quote`: цитата.
4. `checklist`: пункты чек-листа (`items`: `id`, `text`, `checked`).
5. `table`: таблица строк (`rows` ячеек `TableCell`).
6. `image`: одиночное изображение (`url`, `attachmentId`, `caption`, `alt`).
7. `gallery`: галерея изображений (`images`: список `GalleryItem` с `url`, `caption`, `alt`, `thumbnail_url`, `width`, `height`).
8. `video`: видео-блок (`embed_url`, `url`, `provider`, `caption`).
9. `file` / `document` / `pdf`: вложенный файл (`url`, `attachmentId`, `file_name`, `file_size`, `mime_type`, `caption`).

---

## 4. Парсинг видео (`domain.ParseVideoEmbed`)

Пакет `internal/domain/video_embed.go`:
- **YouTube**:
  - `https://www.youtube.com/watch?v=VIDEO_ID` -> `https://www.youtube.com/embed/VIDEO_ID`
  - `https://youtu.be/VIDEO_ID` -> `https://www.youtube.com/embed/VIDEO_ID`
  - `https://www.youtube.com/shorts/VIDEO_ID` -> `https://www.youtube.com/embed/VIDEO_ID`
  - `https://www.youtube.com/embed/VIDEO_ID` -> сохраняется
- **RuTube**:
  - `https://rutube.ru/video/VIDEO_ID/` -> `https://rutube.ru/play/embed/VIDEO_ID`
- **Vimeo**:
  - `https://vimeo.com/VIDEO_ID` -> `https://player.vimeo.com/video/VIDEO_ID`
- **Direct**:
  - Прямые ссылки на видеофайлы сохраняются без изменений с провайдером `direct`.

---

## 5. Эндпоинты API

Все эндпоинты защищены авторизацией:

### 5.1. Загрузка вложений (PDF, документы, файлы)
- **Метод**: `POST /api/v1/publications/:id/attachments`
- **Content-Type**: `multipart/form-data`
- **Параметры**:
  - `files` (или `file`): один или несколько файлов (PDF, Word, Excel, ZIP и т.д., до 100 МБ).
  - `captions` (опционально): подписи к файлам.
- **Ответ (201 Created)**:
```json
{
  "attachments": [
    {
      "id": "c62241d7-27b9-4796-bf75-84f938f267a1",
      "publication_id": "0d65b796-ff6b-4e12-b258-aa8241477439",
      "file_url": "https://.../api/v1/files/download?object_name=...",
      "file_name": "Report2026.pdf",
      "file_type": "pdf",
      "file_size": 2450120,
      "caption": "Годовой отчет",
      "order_index": 0,
      "mime_type": "application/pdf",
      "created_at": "2026-09-29T10:00:00Z"
    }
  ],
  "count": 1
}
```

### 5.2. Загрузка галереи изображений
- **Метод**: `POST /api/v1/publications/:id/gallery`
- **Content-Type**: `multipart/form-data`
- **Параметры**:
  - `images` (или `files`): до 50 изображений (`jpg`, `jpeg`, `png`, `webp`, `gif`, до 25 МБ каждое).
  - `captions` (опционально): подписи к каждому изображению.
- **Ответ (201 Created)**:
```json
{
  "gallery": [
    {
      "id": "...",
      "file_url": "https://...",
      "file_name": "photo1.jpg",
      "file_type": "image",
      "order_index": 0,
      "caption": "Открытие выставки"
    }
  ],
  "count": 1
}
```

### 5.3. Добавление видео-вставки
- **Метод**: `POST /api/v1/publications/:id/videos`
- **Content-Type**: `application/json`
- **Тело запроса**:
```json
{
  "video_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
  "caption": "Интервью с министром"
}
```
- **Ответ (201 Created)**:
```json
{
  "id": "e9ffc575-30db-44a6-ba9e-566b5952db58",
  "file_url": "https://www.youtube.com/embed/dQw4w9WgXcQ",
  "file_name": "Youtube video",
  "file_type": "video",
  "caption": "Интервью с министром",
  "order_index": 2,
  "mime_type": "video/embed",
  "video_provider": "youtube",
  "video_embed_url": "https://www.youtube.com/embed/dQw4w9WgXcQ"
}
```

### 5.4. Удаление вложения
- **Метод**: `DELETE /api/v1/publications/:id/attachments/:attachment_id`
- **Ответ**: `204 No Content`

### 5.5. Изменение порядка вложений (Reorder)
- **Метод**: `PUT /api/v1/publications/:id/attachments/reorder`
- **Content-Type**: `application/json`
- **Тело запроса**:
```json
{
  "attachment_ids": [
    "uuid-3",
    "uuid-1",
    "uuid-2"
  ]
}
```
- **Ответ (200 OK)**: список обновлённых вложений в новом порядке.

---

## 6. Защита и валидация

1. **Разграничение прав**: Загрузка, добавление видео, удаление и сортировка вложений доступны только автору публикации либо пользователю с ролью `superadmin`.
2. **Лимиты размеров**:
   - Изображения: максимум 25 МБ.
   - PDF: максимум 50 МБ; изображения вне галереи: максимум 50 МБ.
   - Общий размер multipart-запроса: максимум 100 МБ, до 50 файлов.
   - Количество картинок в галерее за раз: до 50 шт.
   - Максимум блоков в документе: 2000 шт.
   - Размер `content_doc`: до 4 МБ.
3. **Безопасность URL**: Подписываются только объекты автора публикации; поддерживаются raw object keys и прежние download URL. Внешние ссылки допускаются только HTTP(S). Видео-провайдеры проверяются по точному имени хоста, embed URL нормализуются.
4. **Форматы**: загрузка JPEG, PNG, GIF, WebP и PDF; проверяются заголовки изображений, размеры и сигнатура/концевой маркер PDF. Это не антивирусная проверка и не полная проверка PDF. Видео добавляется через URL, не multipart.
5. **Атомарность**: вложения одной загрузки сохраняются одной транзакцией. При редактировании отсутствие `attachments` сохраняет вложения; пустой массив удаляет их. Существующие ID сохраняются по переданному ID или совпадающему object key.
6. **Очистка**: фоновый обработчик удаляет неиспользуемые объекты с повторными попытками; перед удалением проверяет ссылки из публикаций.

## 7. Запуск и сквозная проверка

Миграции `20260929001100` и `20260929001200` входят в Docker-образ и применяются автоматически существующим `/app/migrate` до запуска API. Повторный старт не применяет их заново. При локальном запуске `cmd/api` также выполняет `goose.Up`; ручной SQL не нужен.

`TestNWS003MediaIntegration` проверяет реальные PostgreSQL и MinIO: применение и повторный запуск миграций, загрузку/чтение изображения и PDF с подписью, видео, права, сохранение ID, сортировку, откат при ошибке БД, очередь очистки и повторные попытки. Используются временная схема БД и отдельный случайный bucket; тест удаляет только собственные ресурсы. В CI PostgreSQL и отдельный MinIO поднимаются автоматически.

Для ручного запуска установите переменные `TEST_MESSENGER_DATABASE_URL` (тестовая PostgreSQL), `TEST_NWS_MINIO_ENDPOINT` (host:port), `TEST_NWS_MINIO_ACCESS_KEY`, `TEST_NWS_MINIO_SECRET_KEY`, затем выполните:

```sh
go test ./internal/service -run '^TestNWS003MediaIntegration$' -count=1 -v -timeout 120s
```

Эта проверка проходит через реальные сервисы и хранилища, но не заменяет браузерный тест интерфейса и production smoke-test после деплоя.
