# Фото и текст одним сообщением: каналы, личные чаты, группы

Этот документ описывает, как frontend и mobile должны отправлять фото и текст в стиле Telegram: одна карточка сообщения, где есть фото и подпись/caption, а не два отдельных сообщения.

## Каналы

Backend уже поддерживает хранение текста и вложения в одной записи `channel_posts`.

Endpoint создания поста:

```http
POST /api/v1/channels/{channel_id}/posts
Authorization: Bearer <access_token>
Content-Type: application/json
```

Тело запроса может содержать одновременно:

```json
{
  "content": "Текст подписи к фото",
  "attachment_url": "https://example.com/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

Поля:

| Поле | Тип | Обязательное | Описание |
| --- | --- | --- | --- |
| `content` | `string` | Нет | Текст поста или caption к фото. |
| `attachment_url` | `string` | Нет | URL загруженного файла/фото. |
| `attachment_type` | `string` | Нет | MIME type или тип вложения, например `image/jpeg`, `image/png`, `video/mp4`, `file`. |
| `attachment_size` | `number` | Нет | Размер файла в байтах. |

`channel_id` не нужно передавать в body. Он берется из URL.

## Личные чаты и группы

Backend также поддерживает хранение текста и вложения в одной записи `messages`.

Endpoint личного чата:

```http
POST /api/v1/conversations/{conversation_id}/messages
Authorization: Bearer <access_token>
Content-Type: application/json
```

Endpoint группы:

```http
POST /api/v1/groups/{group_id}/messages
Authorization: Bearer <access_token>
Content-Type: application/json
```

Тело запроса может содержать одновременно:

```json
{
  "content": "Текст подписи к фото",
  "type": "image",
  "attachment_url": "https://example.com/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

Поля для личных чатов и групп:

| Поле | Тип | Обязательное | Описание |
| --- | --- | --- | --- |
| `content` | `string` | Нет | Текст сообщения или caption к фото. |
| `type` | `string` | Нет | Тип сообщения: `text`, `image`, `video`, `file`, `audio`, `voice`, `video_message`. Для фото отправляйте `image`. |
| `attachment_url` | `string` | Нет | URL загруженного файла/фото. |
| `attachment_type` | `string` | Нет | MIME type или тип вложения, например `image/jpeg`, `image/png`, `video/mp4`, `file`. |
| `attachment_size` | `number` | Нет | Размер файла в байтах. |
| `content_hash` | `string` | Нет | Hash plaintext content, если клиент использует поиск/дедупликацию. |
| `keys` | `object` | Нет | Encrypted keys для E2EE в личном чате. |
| `reply_to_id` | `string` | Нет | ID сообщения, на которое отвечает пользователь. |

Минимальное правило: должен быть хотя бы `content` или `attachment_url`. Совсем пустое сообщение backend не примет.

## Главный UX-сценарий как в Telegram

Пользователь выбирает фото, видит preview, вводит подпись в поле caption и нажимает `Send`.

Клиент должен сделать два backend-запроса:

1. Загрузить файл.
2. Создать channel post с `content` и `attachment_url` в одном JSON.

Нельзя отправлять caption отдельным текстовым постом. Caption должен быть в поле `content` того же post request, где указан `attachment_url`.

## Шаг 1. Загрузка фото

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

Для создания поста используйте:

| Из upload response | В channel post |
| --- | --- |
| `url` | `attachment_url` |
| `type` или MIME type файла | `attachment_type` |
| `size` | `attachment_size` |

Если клиент знает реальный MIME type файла, лучше отправлять его:

```json
"attachment_type": "image/jpeg"
```

Если MIME type неизвестен, допустимо отправить:

```json
"attachment_type": "image"
```

## Шаг 2. Создание поста с фото и caption

После успешной загрузки файла создайте пост или сообщение:

```http
POST /api/v1/channels/{channel_id}/posts
Authorization: Bearer <access_token>
Content-Type: application/json
```

Body:

```json
{
  "content": "Текст к фото, который пользователь ввел в caption",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456
}
```

Для личного чата:

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

Для группы:

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

Пример curl:

```bash
curl -X POST "https://YOUR_API_HOST/api/v1/channels/CHANNEL_UUID/posts" \
  -H "Authorization: Bearer ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  --data-raw '{
    "content": "Тест текст для того что бы посмотреть",
    "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
    "attachment_type": "image/jpeg",
    "attachment_size": 123456
  }'
```

Пример ответа:

```json
{
  "id": "POST_UUID",
  "channel_id": "CHANNEL_UUID",
  "author_id": 9,
  "content": "Тест текст для того что бы посмотреть",
  "attachment_url": "https://YOUR_API_HOST/api/v1/files/download?object_name=uploads/9/2026/04/photo.jpg",
  "attachment_type": "image/jpeg",
  "attachment_size": 123456,
  "is_pinned": false,
  "is_deleted": false,
  "is_edited": false,
  "views_count": 0,
  "comments_count": 0,
  "author_name": "User Name",
  "author_avatar": null,
  "channel_name": "Channel Name",
  "channel_avatar": null,
  "reactions": [],
  "is_viewed": false,
  "created_at": "2026-04-19T10:00:00Z",
  "updated_at": "2026-04-19T10:00:00Z"
}
```

## Варианты постов

Backend позволяет все три варианта для каналов, личных чатов и групп.

### Только текст

```json
{
  "content": "Обычный текстовый пост"
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

## Требования к UI frontend/mobile

### Composer

Поведение должно быть таким:

1. Пользователь нажимает attach/photo.
2. Клиент открывает picker.
3. После выбора фото показывается preview.
4. Под preview есть поле `Caption`.
5. Кнопка `Send` отправляет один channel post.

Важно: если пользователь ввел caption, не отправляйте его отдельным текстовым постом.

### Loading state

На время отправки надо показывать один общий loading state:

```text
Uploading photo...
Publishing post...
```

Можно разделять внутренне на два шага, но для пользователя это одна операция отправки.

### Ошибки

Если upload файла упал, post создавать нельзя.

Если upload прошел, но создание post упало, покажите ошибку создания поста. Файл уже мог остаться в storage, это нормально для текущего backend flow.

Рекомендуемые user-facing ошибки:

| Ситуация | Текст |
| --- | --- |
| Upload failed | `Не удалось загрузить фото. Попробуйте еще раз.` |
| Create post failed | `Фото загружено, но пост не опубликован. Попробуйте отправить снова.` |
| No permission | `У вас нет прав публиковать посты в этом канале.` |

## Права доступа

Создавать посты в канале может только:

```text
owner
admin
```

Если обычный `subscriber` попробует создать пост, backend вернет ошибку доступа.

## Что делать frontend

Псевдокод:

```ts
async function sendChannelPhotoPost(channelId: string, file: File, caption: string) {
  const upload = await uploadFile({
    file,
    type: 'image',
  });

  return createChannelPost(channelId, {
    content: caption.trim() || undefined,
    attachment_url: upload.url,
    attachment_type: file.type || 'image',
    attachment_size: file.size,
  });
}
```

Upload:

```ts
async function uploadFile({ file, type }: { file: File; type: string }) {
  const form = new FormData();
  form.append('file', file);
  form.append('type', type);

  const res = await fetch('/api/v1/files/upload', {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
    },
    body: form,
  });

  if (!res.ok) {
    throw new Error('file upload failed');
  }

  return res.json();
}
```

Create post:

```ts
async function createChannelPost(channelId: string, body: {
  content?: string;
  attachment_url?: string;
  attachment_type?: string;
  attachment_size?: number;
}) {
  const res = await fetch(`/api/v1/channels/${channelId}/posts`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  });

  if (!res.ok) {
    throw new Error('create channel post failed');
  }

  return res.json();
}
```

Для личного чата меняется только endpoint:

```ts
async function createConversationMessage(conversationId: string, body: {
  content?: string;
  type?: string;
  attachment_url?: string;
  attachment_type?: string;
  attachment_size?: number;
}) {
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

Для личного чата и группы в body обязательно добавляйте:

```ts
type: 'image'
```

Для группы:

```ts
async function createGroupMessage(groupId: string, body: {
  content?: string;
  type?: string;
  attachment_url?: string;
  attachment_type?: string;
  attachment_size?: number;
}) {
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

## Что делать mobile

Псевдокод:

```dart
Future<ChannelPost> sendChannelPhotoPost({
  required String channelId,
  required File image,
  required String caption,
}) async {
  final upload = await uploadFile(
    file: image,
    type: 'image',
  );

  return createChannelPost(
    channelId: channelId,
    content: caption.trim().isEmpty ? null : caption.trim(),
    attachmentUrl: upload.url,
    attachmentType: upload.mimeType ?? 'image',
    attachmentSize: upload.size,
  );
}
```

Create post body:

```dart
final body = <String, dynamic>{
  if (content != null && content.isNotEmpty) 'content': content,
  'attachment_url': attachmentUrl,
  'attachment_type': attachmentType,
  'attachment_size': attachmentSize,
};
```

Для личного чата и группы добавьте в body:

```dart
'type': 'image',
```

Не отправляйте два запроса на создание поста:

```text
POST text post
POST image post
```

Нужно отправлять один post/message:

```text
POST image + caption post
```

## Отображение поста

Если в post есть `attachment_url` и `attachment_type` начинается с `image`, UI должен показывать:

1. Изображение.
2. Под ним текст из `content`, если он есть.
3. Реакции, просмотры, дата, меню и остальные элементы поста.

Пример логики:

```ts
const isImage = post.attachment_url && post.attachment_type?.startsWith('image');

if (isImage) {
  renderImage(post.attachment_url);
}

if (post.content) {
  renderCaption(post.content);
}
```

## Важные правила

1. Caption хранится в `content`.
2. Фото хранится в `attachment_url`.
3. Один UI action `Send` должен создавать один channel post.
4. Для фото используйте `attachment_type: image/jpeg`, `image/png`, `image/webp` или хотя бы `image`.
5. `attachment_size` передавайте в байтах.
6. Не кладите file URL в `content`, если используете новый flow.
7. Для старых постов, где URL мог быть в `content`, frontend/mobile может оставить legacy rendering как fallback.

## Совместимость

Этот flow не ломает старые клиенты:

- старый текстовый пост продолжает работать через `content`;
- старый пост с одним вложением продолжает работать через `attachment_url`;
- новый flow просто использует оба поля одновременно.

- личный чат и группа продолжают работать через `content`;
- новый flow для личных чатов и групп использует `type`, `attachment_url`, `attachment_type`, `attachment_size`;
- старые клиенты не ломаются, потому что новые поля опциональные.
