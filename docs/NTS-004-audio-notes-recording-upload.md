# NTS-004 — Аудиозаметки: Диктофонная запись и загрузка аудиофайлов

Реализация расширяет функционал модуля заметок на backend (Go) в рамках задачи **NTS-004: Аудиозаметки**. Обеспечивается поддержка создания заметок с аудиозаписями со встроенного диктофона в веб-интерфейсе, загрузка готовых аудиофайлов (`mp3`, `m4a`, `wav`, `webm`, `ogg`, `aac`, `flac`), сохранение метаданных (длительность звучания, амплитудная волна `waveform`), стриминг аудио с поддержкой `HTTP 206 Partial Content` (Range requests) для плавной перемотки в плеере, а также интеграция с очередью асинхронной очистки MinIO (`note_object_cleanup`). Изменения выполнены строго в `workspace-backend`.

---

## 1. Схема данных и миграции

Миграция: `db/migrations/20260930000100_notes_audio_recording.sql`

1. **Расширение типа заметки**:
   - `notes_type_check` обновлен для поддержки типа `'audio'`:
   ```sql
   ALTER TABLE notes DROP CONSTRAINT IF EXISTS notes_type_check;
   ALTER TABLE notes ADD CONSTRAINT notes_type_check CHECK (type IN ('text', 'checklist', 'rich', 'audio'));
   ```

2. **Таблица аудиовложений `note_audio_attachments`**:
   ```sql
   CREATE TABLE note_audio_attachments (
       id UUID PRIMARY KEY,
       note_id UUID NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
       user_id BIGINT NOT NULL,
       storage_key TEXT NOT NULL,
       file_name TEXT NOT NULL,
       mime_type TEXT NOT NULL,
       size_bytes BIGINT NOT NULL,
       duration NUMERIC(10, 2),
       waveform JSONB,
       is_recording BOOLEAN NOT NULL DEFAULT false,
       status VARCHAR(32) NOT NULL DEFAULT 'uploaded' CHECK (status IN ('pending', 'uploaded', 'deleted')),
       created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
   );
   ```

3. **Индексы**:
   - `idx_note_audio_attachments_note_id` на `(note_id)`
   - `idx_note_audio_attachments_user_id` на `(user_id)`

4. **Очистка объектов при удалении**:
   - При безвозвратной очистке заметки (`DELETE /api/v1/notes/{id}/hard`) или очистке корзины (`POST /api/v1/notes/trash/empty`) ключи `storage_key` из `note_audio_attachments` автоматически добавляются в транзакционную очередь `note_object_cleanup` для надёжного удаления из MinIO.

---

## 2. API Эндпоинты

### 2.1. Создание аудиозаметки напрямую (Multipart)
`POST /api/v1/notes/audio`

Позволяет за один запрос создать заметку типа `audio` и загрузить к ней записанный диктофоном фрагмент или готовый аудиофайл.

**Формат**: `multipart/form-data`
- `file` (обязательный) — бинарный файл аудио (mp3, m4a, wav, webm, ogg, aac, flac).
- `title` (опциональный) — заголовок заметки. Если не указан, автоматически генерируется («Голосовая запись 30.09.2026 15:30» для диктофона или имя файла).
- `content` (опциональный) — текстовое описание или расшифровка.
- `folder_id` (опциональный) — UUID папки.
- `color` (опциональный) — HEX цвет `#RRGGBB`.
- `tags` (опциональный) — массив тегов (в JSON или через запятую).
- `duration` (опциональный) — длительность в секундах (например, `42.5`).
- `waveform` (опциональный) — JSON-массив нормализованных амплитуд `[0.1, 0.4, 0.8, 0.2]`.
- `is_recording` (опциональный) — булево значение (`true`, если записано с микрофона).
- `is_pinned`, `is_archived`, `is_favorite` (опциональные).

**Ответ `201 Created`**:
```json
{
  "id": "b3e34b10-67c1-4b71-9d2e-3c224f8ad981",
  "user_id": 42,
  "title": "Голосовая запись 30.09.2026 12:45",
  "content": "Обсуждение архитектуры NTS-004",
  "type": "audio",
  "color": "#1687FF",
  "tags": ["audio", "meeting"],
  "content_doc": {
    "type": "doc",
    "version": 1,
    "blocks": [
      {
        "id": "audio-7f28ab1c",
        "type": "audio",
        "attachmentId": "7f28ab1c-e744-4ad9-918d-ef5889da3b76",
        "duration": 42.5,
        "waveform": [0.1, 0.4, 0.8, 0.2],
        "is_recording": true,
        "caption": "Голосовая запись 30.09.2026 12:45"
      },
      {
        "id": "p-1",
        "type": "paragraph",
        "runs": [{"text": "Обсуждение архитектуры NTS-004"}]
      }
    ]
  },
  "audio_attachments": [
    {
      "id": "7f28ab1c-e744-4ad9-918d-ef5889da3b76",
      "note_id": "b3e34b10-67c1-4b71-9d2e-3c224f8ad981",
      "user_id": 42,
      "file_name": "recording.webm",
      "mime_type": "audio/webm",
      "size_bytes": 354120,
      "duration": 42.5,
      "waveform": [0.1, 0.4, 0.8, 0.2],
      "is_recording": true,
      "status": "uploaded",
      "download_url": "https://storage.local/alem-chat-files/notes/audio/42/...",
      "created_at": "2026-09-30T12:45:00Z",
      "updated_at": "2026-09-30T12:45:00Z"
    }
  ],
  "version": 1,
  "created_at": "2026-09-30T12:45:00Z",
  "updated_at": "2026-09-30T12:45:00Z"
}
```

---

### 2.2. Загрузка аудио в существующую заметку
`POST /api/v1/notes/:id/audio`

**Формат**: `multipart/form-data`
- `file` (обязательный) — бинарный файл аудио.
- `duration` (опциональный) — число секунд.
- `waveform` (опциональный) — массив амплитуд в формате JSON.
- `is_recording` (опциональный) — `true` / `false`.

**Ответ `201 Created`**:
```json
{
  "note": { ... },
  "audio": {
    "id": "7f28ab1c-e744-4ad9-918d-ef5889da3b76",
    "note_id": "b3e34b10-67c1-4b71-9d2e-3c224f8ad981",
    "file_name": "recording.webm",
    "mime_type": "audio/webm",
    "size_bytes": 354120,
    "duration": 42.5,
    "waveform": [0.1, 0.4, 0.8, 0.2],
    "is_recording": true,
    "status": "uploaded",
    "download_url": "https://storage.local/alem-chat-files/notes/audio/42/..."
  }
}
```

---

### 2.3. Presigned Upload Flow (прямая загрузка в MinIO / S3)

Для больших файлов и оптимизации нагрузки на бэкенд предусмотрен 2-шаговый presign flow:

#### Шаг 1: Запрос presigned PUT URL
`POST /api/v1/notes/:id/audio/presign` (также поддерживается авто-роутинг через `POST /api/v1/notes/:id/attachments/presign` при передаче `mime_type: audio/*`).

**Тело запроса**:
```json
{
  "file_name": "voice-recording.webm",
  "mime_type": "audio/webm; codecs=opus",
  "size_bytes": 1048576,
  "duration": 75.0,
  "is_recording": true
}
```

**Ответ `201 Created`**:
```json
{
  "attachment_id": "6a57d022-7fcf-4c6e-826c-d2c676774a3f",
  "upload_url": "https://minio.local/alem-chat-files/notes/audio/42/6a57d022-7fcf-4c6e-826c-d2c676774a3f/voice-recording.webm?X-Amz-...",
  "storage_key": "notes/audio/42/6a57d022-7fcf-4c6e-826c-d2c676774a3f/voice-recording.webm"
}
```

Клиент отправляет бинарные данные напрямую в хранилище:
`PUT {upload_url}` с заголовком `Content-Type: audio/webm; codecs=opus`.

#### Шаг 2: Подтверждение завершения загрузки
`POST /api/v1/notes/:id/audio/:attachmentId/complete`

**Тело запроса**:
```json
{
  "file_name": "voice-recording.webm",
  "duration": 75.0,
  "waveform": [0.05, 0.32, 0.65, 0.41, 0.12]
}
```

**Ответ `200 OK`**:
Возвращает актуальный объект заметки со статусом вложения `uploaded` и готовой ссылкой на скачивание.

---

### 2.4. Стриминг аудио с поддержкой Range запросов (HTTP 206)
`GET /api/v1/notes/:id/audio/:attachmentId/stream`

Эндпоинт предназначен для аудиоплееров в браузере и мобильных клиентах:
- Автоматически поддерживает заголовок `Range: bytes=start-end`.
- Возвращает `HTTP 206 Partial Content`, `Accept-Ranges: bytes`, `Content-Range: bytes 0-1024/5000000`.
- Позволяет пользователю свободно перематывать запись в любую позицию без ожидания полной загрузки файла.

---

### 2.5. Получение списка и отдельных аудиовложений
- `GET /api/v1/notes/:id/audio` — список всех аудиозаписей заметки с актуальными presigned URL для воспроизведения.
- `GET /api/v1/notes/:id/audio/:attachmentId` — метаданные одного аудиовложения.
- `DELETE /api/v1/notes/:id/audio/:attachmentId` — удаление аудиозаписи из заметки и очистка из MinIO.

---

## 3. Руководство по интеграции браузерного диктофона (Web Voice Recorder)

### Запись звука через `MediaRecorder` API
```typescript
let mediaRecorder: MediaRecorder;
let audioChunks: Blob[] = [];

async function startRecording() {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  // Chrome / Firefox поддерживают 'audio/webm;codecs=opus'
  // Safari на iOS/macOS поддерживает 'audio/mp4'
  const mimeType = MediaRecorder.isTypeSupported('audio/webm;codecs=opus')
    ? 'audio/webm;codecs=opus'
    : 'audio/mp4';

  mediaRecorder = new MediaRecorder(stream, { mimeType });
  audioChunks = [];

  mediaRecorder.ondataavailable = (event) => {
    if (event.data.size > 0) {
      audioChunks.push(event.data);
    }
  };

  mediaRecorder.start(250); // сбор чанков каждые 250мс для вычисления волны
}

async function stopAndUpload(noteId?: string): Promise<Note> {
  return new Promise((resolve, reject) => {
    mediaRecorder.onstop = async () => {
      const audioBlob = new Blob(audioChunks, { type: mediaRecorder.mimeType });
      const durationSeconds = calculateRecordedDuration();
      const waveformArray = getNormalizedWaveformData(); // [0.1, 0.4, 0.8, ...]

      const formData = new FormData();
      formData.append('file', audioBlob, 'recording.webm');
      formData.append('duration', durationSeconds.toString());
      formData.append('waveform', JSON.stringify(waveformArray));
      formData.append('is_recording', 'true');

      if (!noteId) {
        // Создание новой аудиозаметки
        formData.append('title', 'Голосовая заметка');
        const resp = await fetch('/api/v1/notes/audio', {
          method: 'POST',
          headers: { Authorization: `Bearer ${token}` },
          body: formData,
        });
        resolve(await resp.json());
      } else {
        // Добавление аудио в существующую заметку
        const resp = await fetch(`/api/v1/notes/${noteId}/audio`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${token}` },
          body: formData,
        });
        const data = await resp.json();
        resolve(data.note);
      }
    };

    mediaRecorder.stop();
  });
}
```

---

## 4. Поддерживаемые форматы и ограничения

| Расширение | MIME-тип | Назначение |
|---|---|---|
| `.webm` | `audio/webm`, `video/webm` | Web Voice Recorder (Chrome, Firefox, Edge, Opera) |
| `.m4a` / `.mp4` | `audio/mp4`, `audio/m4a`, `audio/x-m4a` | Safari (iOS/macOS), диктофоны iPhone |
| `.mp3` | `audio/mpeg`, `audio/mp3` | Универсальный формат аудиофайлов |
| `.wav` | `audio/wav`, `audio/wave`, `audio/x-wav` | Высококачественная запись без сжатия |
| `.ogg` | `audio/ogg`, `application/ogg` | Свободный аудиоконтейнер Vorbis/Opus |
| `.aac` | `audio/aac`, `audio/x-aac` | Сжатое аудио |
| `.flac` | `audio/flac`, `audio/x-flac` | Lossless аудио |

- **Максимальный размер аудиофайла**: `100 МБ` (`NoteMaxAudioSizeBytes = 100 * 1024 * 1024`).
- **Авто-нормализация имени файла**: если клиент отправляет generic имя `"blob"` (характерно для веб-диктофона), бэкенд нормализует имя в `recording.webm` (для диктофона) или `audio.mp3` (для загруженных файлов).
