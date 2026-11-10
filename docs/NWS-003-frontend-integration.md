# NWS-003 — Подключение медиаконтента новостей на фронтенде

Контракт сверён с backend main, коммит `be2b2de`. Область задачи: изображения,
галереи, PDF, видео-вставки и форматирование редактора. NWS-004 не входит.

## Авторизация и общие правила

База API: `/api/v1`. Используйте существующий авторизованный HTTP-клиент проекта
(JWT `Authorization: Bearer <access_token>`). Не создавайте отдельный механизм входа.
ID публикаций и вложений — строки UUID. Даты — ISO 8601, размеры — байты.
Все пути ниже начинаются с `/api/v1/publications`.

Загрузка и управление медиа доступны автору публикации или роли `superadmin`.
Обычная роль `admin` и роль `manager` сами по себе не дают права редактировать
чужие вложения. Решение сервера окончательное; обработайте `403` независимо от UI.

## Последовательность подключения

1. Создать черновик через `POST /api/v1/publications` и получить `id`.
2. Загрузить изображения/PDF или добавить видео к этому `id`.
3. Вставить соответствующий блок в локальный `content_doc` редактора.
4. Сохранить документ через `PUT /api/v1/publications/:id`.
5. После открытия новости брать вложения из `GET /api/v1/publications/:id`:
   поле `attachments`; содержимое редактора — `content_doc`.

Медиа-эндпоинты создают вложения, но **не вставляют блоки в `content_doc`**.
Успешная загрузка не означает, что изображение уже находится в тексте новости.

Минимальное создание черновика:

```json
{
  "source_type": "user",
  "type": "news",
  "title": "Новая публикация",
  "status": "draft",
  "target_audience": "all",
  "importance": "normal",
  "allow_comments": true
}
```

## API медиа

| Метод и путь | Запрос | Ответ |
| --- | --- | --- |
| `POST /:id/gallery` | FormData: повторяющиеся `images`, `captions` | `201 { gallery: Attachment[], count: number }` |
| `POST /:id/attachments` | FormData: повторяющиеся `files`, `captions` | `201 { attachments: Attachment[], count: number }` |
| `POST /:id/videos` | JSON `{ video_url, caption?, title? }` | `201 Attachment` (без обёртки) |
| `PUT /:id/attachments/reorder` | JSON `{ attachment_ids: string[] }` | `200 { attachments: Attachment[], count: number }` |
| `DELETE /:id/attachments/:attachment_id` | Без тела | `204`, без JSON |

Для gallery также поддерживаются поля `files` и `image`; для attachments — `file`.
Для подписей есть альтернативное поле `caption`. Рекомендуются основные поля
из таблицы. Подписи передавайте в том же порядке, что и файлы; для пустой подписи
передавайте пустую строку, чтобы не сместить последующие подписи.

```ts
// api — существующий Axios-клиент с baseURL=/api/v1 и авторизацией.
const form = new FormData();
for (const item of selectedImages) {
  form.append('images', item.file);
  form.append('captions', item.caption ?? '');
}
const { data } = await api.post(`/publications/${publicationId}/gallery`, form);
const uploaded = data.gallery ?? [];
// Не задавайте Content-Type вручную: браузер добавляет multipart boundary.

await api.post(`/publications/${publicationId}/videos`, {
  video_url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
  caption: 'Запись мероприятия',
});
```

## Тип вложения

```ts
type Attachment = {
  id: string;
  publication_id: string;
  file_url: string;
  file_name: string;
  file_type: string; // Загрузка: image или pdf; видео-вставка: video.
  file_size?: number;
  caption?: string;
  order_index: number;
  mime_type?: string;
  thumbnail_url?: string;
  video_provider?: 'youtube' | 'rutube' | 'vimeo' | 'direct';
  video_embed_url?: string;
  created_at: string;
};
```

Отображайте `file_url`/`thumbnail_url` из ответа сервера как есть. Не подменяйте
хост на localhost или адрес MinIO. Подписанные ссылки не являются постоянными:
при истечении загрузите публикацию повторно, а не сохраняйте ссылку навсегда
в localStorage. Для идентификации используйте `id`.

Сортируйте вложения по `order_index`. Reorder принимает полный список ID текущих
вложений без повторов, включая PDF и видео, а не только выбранные картинки.
После удаления вложения удалите и связанный блок/элемент галереи из редактора,
затем сохраните документ. Удаление вложения и сохранение документа — разные запросы.

## Формат редактора и сохранение

`content_doc` имеет форму `{ type: "doc", version: 1, blocks: [...] }`.
Имена JSON-полей чувствительны к регистру: именно `attachmentId`, но
`file_name`, `file_size`, `mime_type`, `embed_url`, `thumbnail_url`.

Пример документа (подставьте реальные ID и URL, полученные от API):

```json
{
  "type": "doc",
  "version": 1,
  "blocks": [
    {"id":"text-1","type":"paragraph","runs":[{"text":"Важный текст","marks":["bold"]}]},
    {"id":"image-1","type":"image","attachmentId":"<attachment UUID>","url":"<file_url>","alt":"Фото","caption":"Подпись"},
    {"id":"gallery-1","type":"gallery","images":[{"url":"<file_url>","caption":"Фото 1","alt":"Описание"}]},
    {"id":"pdf-1","type":"pdf","attachmentId":"<attachment UUID>","url":"<file_url>","file_name":"report.pdf","file_size":12345,"mime_type":"application/pdf"},
    {"id":"video-1","type":"video","url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ","embed_url":"https://www.youtube.com/embed/dQw4w9WgXcQ","provider":"youtube"}
  ]
}
```

Поддержаны `paragraph`, `heading` (level 1–6), `quote`, `checklist`, `table`,
`image`, `gallery`, `video`, `file`, `document`, `pdf`. Текстовые `runs`:
`{ text, marks?: string[] }`. Основные marks: `bold`, `italic`, `strike`,
`underline`, `code`, `highlight`, `subscript`, `superscript`,
`link:https://example.com`, `color:#FF0000`. Чек-лист: `items` с
`{ id, text, checked }`; таблица: `rows`, прямоугольный массив ячеек `{ text }`.

`PUT /:id` требует обязательные поля публикации, поэтому нельзя отправить только
`content_doc`. Передавайте `source_type`, `type`, `title`, `target_audience`,
`importance` из текущей новости вместе с новым документом и актуальным состоянием
редактора (`status`, `allow_comments`, остальные редактируемые поля).

При сохранении текста **не передавайте `attachments: []`**, если хотите сохранить
вложения: пустой массив удаляет их. Отсутствие поля `attachments` сохраняет текущие
вложения. Если редактируете весь список, передавайте весь список с существующими
`id`, а не только новое вложение. Не отправляйте целиком PublicationResponse:
создайте отдельный DTO запроса с разрешёнными полями.

## Ограничения и отображение

- Gallery: JPEG/JPG, PNG, GIF, WebP; 1–50 файлов, до 25 MiB на файл.
- Attachments: те же изображения или PDF; 1–50 файлов, до 50 MiB на файл.
- Общий multipart-запрос: до 100 MiB, включая служебные данные формы.
- Изображение: максимум 40 миллионов пикселей; проверяется соответствие формату.
- Word, Excel, ZIP, SVG и multipart-видео этим API не принимаются.
- Видео: URL YouTube, RuTube, Vimeo или прямой URL `.mp4`/`.webm`.
  `direct` отображайте через `<video controls>`, остальные — через iframe
  с проверенным `video_embed_url`. Не вставляйте произвольный HTML пользователя.
- Документ: до 2000 блоков, до 4 MiB `content_doc`; весь JSON-запрос публикации
  также ограничен 4 MiB. Галерея в одном блоке — до 50 элементов.

Обрабатывайте `400/422` (невалидные данные), `401` (вход), `403` (нет прав),
`404` (объект недоступен), `413` (слишком большой запрос), `5xx` (ошибка сервера).
Не считайте загрузку успешной до HTTP-ответа. На `204` не вызывайте `.json()`.
Не показывайте сырой ответ с техническими деталями как HTML.

## Проверка фронтенда

Проверить загрузку двух картинок с разными подписями, PDF, видео всех нужных
провайдеров, сортировку и удаление. Сохранить и повторно открыть новость:
блоки и вложения должны остаться. Проверить отклонение SVG/ZIP, превышение лимита,
ошибку сети и отсутствие прав у другого пользователя. После истечения URL
повторное получение новости должно обновлять медиа-ссылки.
