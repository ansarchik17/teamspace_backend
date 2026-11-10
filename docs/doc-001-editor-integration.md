# DOC-001 — интеграция браузерных редакторов

Провайдер — существующий в проекте ONLYOFFICE Docs. Бэкенд выдаёт подписанную
ссылку, возвращает HTML с DocsAPI.DocEditor, передаёт файл DocumentServer и
принимает сохранённую версию в Drive. Изменений схемы БД нет.

## Запуск из браузера

1. Загрузить документ в Drive обычным API загрузки.
2. С Bearer-токеном вызвать
   `GET /api/v1/drive/onlyoffice/editor-link?file_id=<uuid>&mode=edit`.
3. Ответ: `{"url":"https://.../api/v1/drive/onlyoffice/editor?file_id=...&token=...","mode":"edit"}`.
   Открыть `url` в новой вкладке или iframe того же origin. Заголовок Authorization
   при открытии вкладки не нужен; URL действует 10 минут. Не хранить его в аналитике.
4. Фактический `mode` может быть `view`: viewer получает просмотр даже при запросе
   `edit`. Постороннему пользователю доступ запрещён. При открытии HTML права
   проверяются повторно; изменение `mode` в URL не расширяет права токена.
5. DocumentServer получает исходный файл по отдельному подписанному URL (20 минут).
   Callback использует отдельный токен с назначением сохранения (24 часа), не
   разрешающий скачивание или открытие страницы. Для совместимости принимаются
   и прежние 20-минутные edit-токены. После 24 часов документ нужно открыть заново.
6. После закрытия редакторов DocumentServer отправляет callback status=2; бэкенд
   повторно проверяет editor-права, сохраняет новую версию в Drive и событие аудита.
   Сохранение асинхронное: закрытие вкладки не означает немедленную запись в Drive.

Расширения: doc/docx/odt/txt/rtf → word; xls/xlsx/ods/csv → cell;
ppt/pptx/odp → slide. Неизвестные расширения возвращают HTTP 415.
Для редактирования с сохранением исходного расширения используйте DOCX/XLSX/PPTX:
конвертация и сохранение старых форматов зависят от DocumentServer и не реализованы
как отдельный процесс в DOC-001.

DOC-003 дополняет сохранение: force-save включён, ключ сессии сохраняется до
финального callback, повторная доставка не создаёт лишнюю версию. Настройка
периодических снимков в Drive — в `doc-003-drive-synchronization.md`.

## Настройки

- `APP_PUBLIC_URL`: внешний адрес приложения с маршрутом `/api/v1` к бэкенду.
- `ONLYOFFICE_PUBLIC_URL`: адрес DocumentServer, доступный браузеру, например
  `https://office.example.com`. Пустое значение сохраняет `/onlyoffice` proxy.
  Допустимы HTTP/HTTPS, без credentials/query/fragment. CSP страницы разрешает
  загрузку API и iframe с настроенного origin.
- `ONLYOFFICE_DOWNLOAD_BASE_URL`: адрес бэкенда, доступный DocumentServer.
  По умолчанию `APP_PUBLIC_URL`; в Docker — `http://workspace-api:8080`.
- `ONLYOFFICE_JWT_SECRET`: общий секрет с DocumentServer (`JWT_SECRET`).
  С ним конфигурация подписывается HS256, а callback проверяется по JWT из тела
  или `Authorization: Bearer ...`, включая соответствие status/key/url.
  Подпись применяется также к существующему публичному office-preview.
  Пустое значение предназначено только для прежних серверов с отключённым JWT.

Секрет DocumentServer отличается от JWT_SECRET приложения и не передаётся браузеру.
Подписанные URL не пишутся в диагностический console.log конфигурации.
Бэкенд не пересылает свой callback-токен в Authorization при скачивании результата.

Для локального DocumentServer добавлен `docker-compose.onlyoffice.yml`:

1. Указать в `.env` общий секрет, `ONLYOFFICE_IMAGE` с проверенным фиксированным
   тегом/дайджестом `onlyoffice/documentserver` и URL редактора.
2. Запустить `docker compose -f docker-compose.yml -f docker-compose.onlyoffice.yml up -d workspace-onlyoffice api`.
3. При локальном HTTP использовать `http://localhost:18082`; для HTTPS-приложения
   настроить HTTPS reverse proxy (включая WebSocket) перед DocumentServer.
4. Проверить доступность `/healthcheck` DocumentServer. Браузер должен достигать
   DocumentServer, DocumentServer — download/callback бэкенда, бэкенд — URL
   результата DocumentServer. Private-IP разрешение в overlay нужно для Docker API;
   использовать overlay только в доверенной сети.

## Проверка

`go test ./...` проверяет форматы, конфигурацию трёх редакторов, JWT, read-only,
подмену callback, разделение токенов, HTML escaping, CSP и отказ без editor-токена.
Тест `TestOnlyOfficeConfigIntegration` требует одноразовую PostgreSQL через
`TEST_MESSENGER_DATABASE_URL` и проверяет ACL, downgrade, удаление, форматы и JWT.

Приёмка на стенде с DocumentServer: открыть DOCX/XLSX/PPTX, изменить содержимое,
закрыть все редакторы, дождаться callback и проверить новую версию после открытия;
повторить для viewer и пользователя без прав, а также с двумя редакторами.
Фронтенд-кнопки и развёртывание production не входят в бэкенд-изменения.

Протокол: [Browser JWT](https://api.onlyoffice.com/docs/docs-api/additional-api/signature/browser/),
[Callback handler](https://api.onlyoffice.com/docs/docs-api/usage-api/callback-handler/).
