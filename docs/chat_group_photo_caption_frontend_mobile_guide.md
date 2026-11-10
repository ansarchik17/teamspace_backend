# Личные чаты и группы: фото и текст одним сообщением

Этот документ описывает, как frontend и mobile должны отправлять фото с подписью/caption в личных чатах и группах: одно сообщение, где есть и изображение, и текст, как в Telegram.

## Что поддерживает backend

Backend хранит сообщения личных чатов и групп в таблице `messages`.

Одна запись `messages` может содержать одновременно:

```json
{
  "content": "Текст подписи к фото",
  "type": "image",
  "attachment_url": "https://example.com/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

Нельзя отправлять caption отдельным текстовым сообщением, если пользователь выбрал фото и ввел подпись. Нужно создать одно сообщение с `content` и `attachment_url`.

## Endpoints

### Личный чат

```http
POST /api/v1/conversations/{conversation_id}/messages
Authorization: Bearer <access_token>
Content-Type: application/json
```

`conversation_id` берется из URL. В body его передавать не нужно.

### Группа

```http
POST /api/v1/groups/{group_id}/messages
Authorization: Bearer <access_token>
Content-Type: application/json
```

`group_id` берется из URL. В body его передавать не нужно.

## Поля request body

| Поле | Тип | Обязательное | Где используется | Описание |
| --- | --- | --- | --- | --- |
| `content` | `string` | Нет | Чат, группа | Текст сообщения или caption к фото. |
| `type` | `string` | Нет | Чат, группа | Тип сообщения. Для фото отправляйте `image`. |
| `attachment_url` | `string` | Нет | Чат, группа | URL загруженного файла/фото. |
| `attachment_type` | `string` | Нет | Чат, группа | MIME type файла: `image/jpeg`, `image/png`, `image/webp`. |
| `attachment_size` | `number` | Нет | Чат, группа | Размер файла в байтах. |
| `content_hash` | `string` | Нет | Чат, группа | Hash plaintext content, если клиент использует поиск/дедупликацию. |
| `keys` | `object` | Нет | Личный чат | Encrypted keys для E2EE multi-device flow. |
| `reply_to_id` | `string` | Нет | Чат, группа | ID сообщения, на которое отвечает пользователь. |
| `metadata` | `string` | Нет | Группа | JSON-метаданные, например mentions. |
| `is_ephemeral` | `boolean` | Нет | Личный чат | Флаг исчезающего сообщения. |
| `ttl_seconds` | `number` | Нет | Личный чат | TTL исчезающего сообщения после прочтения. |

Минимальное правило backend:

```text
content или attachment_url должны быть заполнены.
```

Совсем пустой request не пройдет.

## Главный UX flow

Пользовательский сценарий:

1. Пользователь нажимает attach/photo.
2. Клиент открывает image picker.
3. Пользователь выбирает фото.
4. Клиент показывает preview.
5. Под preview есть поле `Caption`.
6. Пользователь нажимает `Send`.
7. Клиент загружает фото.
8. Клиент создает одно сообщение с `content` и `attachment_url`.

Важно:

```text
Один UI action Send = одно backend message.
```

Неправильно:

```text
POST text message
POST image message
```

Правильно:

```text
POST image + caption message
```

## Шаг 1. Загрузить фото

Используйте общий file upload endpoint:

```http
POST /api/v1/files/upload
Authorization: Bearer <access_token>
Content-Type: multipart/form-data
```

Form-data:

| Поле | Значение |
| --- | --- |
| `file` | Сам файл изображения. |
| `type` | `image` |

Пример curl:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/files/upload" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -F "file=@photo.jpg" \
  -F "type=image"
```

Пример успешного ответа:

```json
{
  "object_name": "uploads/9/2026/04/photo.jpg",
  "url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "filename": "photo.jpg",
  "size": 123456,
  "type": "image"
}
```

Для создания сообщения используйте:

| Из upload response | В message request |
| --- | --- |
| `url` | `attachment_url` |
| `size` | `attachment_size` |
| MIME type файла или `type` | `attachment_type` |

Если клиент знает реальный MIME type, лучше отправлять его:

```json
"attachment_type": "image/jpeg"
```

Если MIME type неизвестен, можно отправить:

```json
"attachment_type": "image"
```

## Шаг 2. Создать сообщение в личном чате

```http
POST /api/v1/conversations/{conversation_id}/messages
Authorization: Bearer <access_token>
Content-Type: application/json
```

Body:

```json
{
  "content": "Текст к фото, который пользователь ввел в caption",
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

С `reply_to_id`:

```json
{
  "content": "Ответ с фото",
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456,
  "reply_to_id": "MESSAGE_UUID"
}
```

Если личный чат использует E2EE, клиент должен продолжать отправлять `keys` так же, как для обычного сообщения:

```json
{
  "content": "Текст к фото",
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456,
  "keys": {
    "device-id-1": "encrypted-message-key-for-device-1",
    "device-id-2": "encrypted-message-key-for-device-2"
  }
}
```

Backend не меняет E2EE contract для `keys`. Новые attachment поля просто сохраняются в том же message.

## Шаг 3. Создать сообщение в группе

```http
POST /api/v1/groups/{group_id}/messages
Authorization: Bearer <access_token>
Content-Type: application/json
```

Body:

```json
{
  "content": "Текст к фото, который пользователь ввел в caption",
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

С mentions в `metadata`:

```json
{
  "content": "@user текст к фото",
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456,
  "metadata": "{\"mentions\":[\"user\"]}"
}
```

`metadata` сейчас строка, а не object. Если frontend/mobile используют mentions, нужно отправлять JSON как string.

## Варианты сообщений

### Только текст

Личный чат:

```json
{
  "content": "Обычное текстовое сообщение"
}
```

Группа:

```json
{
  "content": "Обычное текстовое сообщение"
}
```

### Только фото без caption

```json
{
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

### Фото с caption

```json
{
  "content": "Caption к фото",
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

## Frontend implementation

### Общий upload

```ts
async function uploadImage(file: File) {
  const form = new FormData();
  form.append('file', file);
  form.append('type', 'image');

  const res = await fetch('/api/v1/files/upload', {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
    },
    body: form,
  });

  if (!res.ok) {
    throw new Error('image upload failed');
  }

  return res.json() as Promise<{
    url: string;
    size: number;
    type?: string;
    filename?: string;
    object_name?: string;
  }>;
}
```

Не ставьте `Content-Type` вручную для `FormData`. Browser сам выставит boundary.

### Личный чат

```ts
type SendConversationPhotoMessageParams = {
  conversationId: string;
  file: File;
  caption: string;
  replyToId?: string;
  keys?: Record<string, string>;
};

async function sendConversationPhotoMessage({
  conversationId,
  file,
  caption,
  replyToId,
  keys,
}: SendConversationPhotoMessageParams) {
  const upload = await uploadImage(file);

  const body = {
    content: caption.trim() || undefined,
    type: 'image',
    attachment_url: upload.url,
    attachment_type: file.type || upload.type || 'image',
    attachment_size: file.size || upload.size,
    reply_to_id: replyToId,
    keys,
  };

  const res = await fetch(`/api/v1/conversations/${conversationId}/messages`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  });

  if (!res.ok) {
    throw new Error('create conversation message failed');
  }

  return res.json();
}
```

### Группа

```ts
type SendGroupPhotoMessageParams = {
  groupId: string;
  file: File;
  caption: string;
  replyToId?: string;
  mentions?: string[];
};

async function sendGroupPhotoMessage({
  groupId,
  file,
  caption,
  replyToId,
  mentions,
}: SendGroupPhotoMessageParams) {
  const upload = await uploadImage(file);

  const metadata = mentions?.length
    ? JSON.stringify({ mentions })
    : undefined;

  const body = {
    content: caption.trim() || undefined,
    type: 'image',
    attachment_url: upload.url,
    attachment_type: file.type || upload.type || 'image',
    attachment_size: file.size || upload.size,
    reply_to_id: replyToId,
    metadata,
  };

  const res = await fetch(`/api/v1/groups/${groupId}/messages`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  });

  if (!res.ok) {
    throw new Error('create group message failed');
  }

  return res.json();
}
```

## Mobile implementation

### Общий upload

```dart
Future<UploadResponse> uploadImage(File image) async {
  final formData = FormData.fromMap({
    'file': await MultipartFile.fromFile(
      image.path,
      filename: image.path.split('/').last,
    ),
    'type': 'image',
  });

  final response = await dio.post(
    '/api/v1/files/upload',
    data: formData,
    options: Options(
      headers: {
        'Authorization': 'Bearer $accessToken',
      },
    ),
  );

  return UploadResponse.fromJson(response.data);
}
```

### Личный чат

```dart
Future<Message> sendConversationPhotoMessage({
  required String conversationId,
  required File image,
  required String caption,
  String? replyToId,
  Map<String, String>? keys,
}) async {
  final upload = await uploadImage(image);

  final body = <String, dynamic>{
    if (caption.trim().isNotEmpty) 'content': caption.trim(),
    'type': 'image',
    'attachment_url': upload.url,
    'attachment_type': upload.mimeType ?? 'image',
    'attachment_size': upload.size,
    if (replyToId != null) 'reply_to_id': replyToId,
    if (keys != null && keys.isNotEmpty) 'keys': keys,
  };

  final response = await dio.post(
    '/api/v1/conversations/$conversationId/messages',
    data: body,
    options: Options(
      headers: {
        'Authorization': 'Bearer $accessToken',
      },
    ),
  );

  return Message.fromJson(response.data);
}
```

### Группа

```dart
Future<Message> sendGroupPhotoMessage({
  required String groupId,
  required File image,
  required String caption,
  String? replyToId,
  List<String>? mentions,
}) async {
  final upload = await uploadImage(image);

  final metadata = mentions != null && mentions.isNotEmpty
      ? jsonEncode({'mentions': mentions})
      : null;

  final body = <String, dynamic>{
    if (caption.trim().isNotEmpty) 'content': caption.trim(),
    'type': 'image',
    'attachment_url': upload.url,
    'attachment_type': upload.mimeType ?? 'image',
    'attachment_size': upload.size,
    if (replyToId != null) 'reply_to_id': replyToId,
    if (metadata != null) 'metadata': metadata,
  };

  final response = await dio.post(
    '/api/v1/groups/$groupId/messages',
    data: body,
    options: Options(
      headers: {
        'Authorization': 'Bearer $accessToken',
      },
    ),
  );

  return Message.fromJson(response.data);
}
```

## UI требования

### Composer

Поведение composer:

1. Пользователь выбирает фото.
2. UI показывает preview изображения.
3. Под preview есть input `Caption`.
4. Кнопка `Send` отправляет одну операцию.
5. После успешной отправки preview и caption очищаются.

Не нужно создавать отдельный текстовый bubble для caption.

### Loading state

Внутри операция состоит из двух запросов:

```text
Uploading image
Creating message
```

Для пользователя это один loading state отправки. Не надо добавлять сообщение в список два раза.

Рекомендуемая optimistic UI логика:

1. Создать временное local message с preview local file.
2. Показать статус `sending`.
3. После upload и create message заменить local message на response backend.
4. Если upload упал, показать ошибку и оставить retry.
5. Если upload прошел, но create message упал, retry должен повторить create message с уже полученным `attachment_url`, если клиент сохранил upload response.

## Отображение сообщения

Если у message есть `attachment_url` и `attachment_type` начинается с `image`, UI должен показывать:

1. Изображение.
2. Под ним `content`, если он есть.
3. Reply preview, реакции, дата, статус доставки и меню как обычно.

Пример:

```ts
const isImage =
  Boolean(message.attachment_url) &&
  (message.attachment_type?.startsWith('image') || message.type === 'image');

if (isImage) {
  renderImage(message.attachment_url);
}

if (message.content) {
  renderCaption(message.content);
}
```

Для старых сообщений, где file URL мог лежать внутри `content`, можно оставить legacy fallback. Но для нового flow URL не должен попадать в `content`.

## Ошибки

| Ситуация | Что делать UI |
| --- | --- |
| Upload failed | Показать `Не удалось загрузить фото. Попробуйте еще раз.` |
| Create message failed | Показать `Фото загружено, но сообщение не отправлено. Попробуйте еще раз.` |
| No permission / not participant | Показать `У вас нет доступа к этому чату.` |
| Empty message | Не отправлять request, если нет ни caption, ни файла. |

## Совместимость со старым кодом

Изменение backward-compatible:

- старый frontend/mobile может продолжать отправлять только `content`;
- старый flow с `type: "text"` работает;
- новые поля `attachment_url`, `attachment_type`, `attachment_size` опциональные;
- структура response уже содержит attachment поля;
- миграции БД не нужны, поля уже есть в `messages`.

Старое сообщение:

```json
{
  "content": "Привет"
}
```

Новое сообщение:

```json
{
  "content": "Фото с подписью",
  "type": "image",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

Оба варианта валидны.

## Важные правила

1. Caption хранится в `content`.
2. Фото хранится в `attachment_url`.
3. Для фото отправляйте `type: "image"`.
4. `attachment_type` лучше отправлять как MIME type: `image/jpeg`, `image/png`, `image/webp`.
5. `attachment_size` отправляйте в байтах.
6. Один клик `Send` должен создавать одно сообщение.
7. Не кладите URL файла в `content`.
8. Для группы `metadata` остается строкой JSON.
9. Для личного E2EE-чата `keys` продолжайте формировать как раньше.
