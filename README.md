# Messenger API

Go backend for a corporate workspace: people, drive, kanban, calendar and notebook features. This branch also includes a full Notes module with folders, pin/unpin, checklist notes, soft delete, sync and file/photo attachments.

## Run

Для локальной разработки нужны Node.js 22.21.1+ (см. `.nvmrc`), Go
(toolchain `go1.24.7` указан в `go.mod`) и запущенный Docker Engine с Compose v2.

В каталоге backend:

```bash
npm run dev
```

При первом запуске команда создаёт `.env` из `.env.development.example`, генерирует
`JWT_SECRET` и `PIN_CRYPTO_KEY`, запускает PostgreSQL, Redis, MinIO и Keycloak, ждёт их
готовности и собирает настоящий Go API. Backend остаётся Go-приложением;
устанавливать npm-зависимости в этом каталоге не нужно. Существующий `.env`
сохраняется, переменные терминала имеют приоритет. Первая сборка требует
доступа к Go-модулям и Docker-образам.

MinIO собирается локально из официальных исходников с закреплённым commit
`0d7408fc9969caf07de6a8c3a84f9fbb10a6739e`, тем же, что используется в CI.
Готовый образ `quay.io/minio/minio` не скачивается: registry может отвечать
`unauthorized`. Первая сборка MinIO может занять несколько минут и требует
доступа к Docker Hub, GitHub и Go-модулям. Последующие запуски используют готовый
локальный образ без пересборки и обращения к registry. Каталог сборки —
`docker/minio-dev`. При необходимости пересобрать образ вручную:

```bash
docker compose -p teamspace-dev -f compose.dev.yml build minio
```

В другом терминале:

```bash
cd ../teamspace_frontend
npm ci
npm run dev
```

Frontend: **http://localhost:5173/web/**. API: **http://localhost:8080**.
Проверка API: `/health`, `/ready`; Swagger: `/swagger/index.html`.

Встроенный мессенджер удалён: API личных чатов, групп, каналов, сообщений,
реакций, закреплений, чатовых папок и синхронизации истории больше не доступны.
AI не предлагает отправку сообщений, вход не создаёт чат поддержки. Общий поиск
продолжает искать сотрудников, задачи, встречи, документы и заметки; поле
`messages` в его ответе возвращается пустым массивом для совместимости клиентов.
Уведомления, звонки, календарь, явный доступ к заметкам и регистрация устройств
сохранены. Онлайн-статусы доступны активным коллегам той же компании.

Исторические миграции и таблицы переписки сохранены. Обновление удаляет код и
API мессенджера, не удаляя данные из существующей базы. Старые связи событий с
групповыми календарями по-прежнему доступны из календаря.

Frontend проксирует `/api` и WebSocket на `127.0.0.1:8080`.

| Сервис | Локальный адрес |
| --- | --- |
| PostgreSQL | `127.0.0.1:15433`, база `workspace` |
| Redis | `127.0.0.1:16379` |
| MinIO API | `127.0.0.1:19000` |
| MinIO console | `http://127.0.0.1:19002` |
| Keycloak | `http://localhost:18082`, realm `Workspace` |

Compose использует отдельный проект `teamspace-dev` и постоянные volumes.
Ctrl+C останавливает Go API. Контейнеры и данные сохраняются; остановить
контейнеры можно через `npm run dev:stop`. Изменения Go-кода требуют перезапуска
команды; frontend поддерживает HMR.

Дополнительные команды:

```bash
npm run dev:infra  # Все локальные сервисы без Go API, включая Keycloak
npm run dev:auth   # Только Keycloak; после его старта перезапустите API
npm run dev:api    # Только Go API с текущим .env, без Docker
npm run dev:logs   # Логи локальных контейнеров; API пишет в свой терминал
npm run dev:stop   # Остановить локальные контейнеры, сохранив данные
npm run build
npm test
npm run vet
```

`dev:api` подходит для уже настроенных зависимостей. API автоматически применяет
миграции к базе из `.env`. Для старта в существующей инфраструктуре можно
использовать полный `.env.example`; его порты и SSO-настройки нужно согласовать
со своими сервисами. Автоматический Compose-запуск принимает только локальные
PostgreSQL, Redis, MinIO и Keycloak с `ENV=development`.

Keycloak получает realm `Workspace`, роль `user` и public client `workspace-client`
из `docker/keycloak-dev/Workspace-realm.json`. Регистрация выполняется по приглашению
через backend; открытая регистрация в самом Keycloak выключена. Данные Keycloak
хранятся в отдельном постоянном volume. Его административные credentials берутся
из `KEYCLOAK_ADMIN_USER` / `KEYCLOAK_ADMIN_PASS` в `.env`; это отдельная учётная
запись от администратора платформы TeamSpace. Порт задаётся `KEYCLOAK_HTTP_PORT`
(по умолчанию `18082`). При смене порта согласуйте его с `KEYCLOAK_URL`,
`KEYCLOAK_PUBLIC_URL`, `KEYCLOAK_ISSUER` и `KEYCLOAK_JWKS_URL`;
launcher проверяет их согласованность перед запуском.

Локальный Compose не поднимает LiveKit/QMeet, EPS/mail, Growy, ODO, IForm,
OnlyOffice/Collabora или AI gateway. Для них нужны отдельные сервисы
и параметры из `.env.example`. Существующие локальные пользователи без `keycloak_id`
могут входить по локальному паролю, но регистрация новых пользователей по приглашению
требует Keycloak. Приглашения и проверочные коды требуют настроенного SMTP.
Текущие миграции содержат seed
администратора; связанные риски описаны в аудите.

Анализ архитектуры, проблем и результатов проверок:
[аудит backend и frontend](docs/audits/local-development-2026-11-12.md).

### Тестовый директор компании

После старта backend и применения миграций выполните в другом терминале:

```bash
npm run seed:director
```

Команда создаёт компанию **TeamSpace Demo** и пользователя **director.demo**
(`director@teamspace.test`). Случайный пароль выводится после успешной проверки
аккаунта и сохраняется в игнорируемом Git файле `.cache/dev-director.json`
с правами `0600`. Вход: `http://localhost:5173/web/login`.

Пользователь имеет глобальную роль `user` и назначается владельцем своей компании
через `organizations.owner_id`. Создание выполняется одной транзакцией только
в PostgreSQL Compose-проекта `teamspace-dev` при `ENV=development`.
Повторный запуск проверяет того же директора и пароль. Если тестовые имя/email
или slug уже заняты другим либо изменённым аккаунтом, команда завершается
без изменения существующих пользователей. Keycloak для этого локального
парольного аккаунта не требуется.

### Локальный администратор платформы

При запущенном backend команда `npm run seed:admin` создаёт отдельного
администратора с email `admin@admin.com`, логином `admin.platform` и ролью `admin`,
без привязки к компании. Пароль читается из `.cache/dev-admin.json`
(`email`, `username`, `password`); этот файл исключён из Git и должен иметь права
`0600`. Команда предназначена только для локального `ENV=development` и
PostgreSQL проекта `teamspace-dev`. Совпадающий аккаунт проверяется повторно;
существующие пользователи с другими правами или credentials не изменяются.
Создание компаний: `http://localhost:5173/web/platform/administration`.

SQL-миграции больше не создают общего администратора с фиксированным паролем.
Миграция `20261115000100_retire_insecure_seed_admin.sql` отключает прежний
локальный `admin@admin.kz` / `admin`, только если пароль остался исходным и нет
привязки к Keycloak. Она заменяет пароль случайным неизвестным значением,
отзывает ранее выданные токены и записывает событие в аудит. Другие аккаунты,
включая `admin@admin.com`, а также аккаунт с уже изменённым паролем сохраняются.
Откат миграции не восстанавливает небезопасные credentials. Для новой локальной
базы администратор создаётся явно командой `npm run seed:admin`.

Создание событий календаря требует входа, включая совместимые адреса
`POST /api/v1/calendar/public/events` и `POST /api/v1/calendar/events/public`.
Они используют обычный контракт `POST /api/v1/calendar/events`: организатор
берётся из проверенной сессии; `user_id`, email и заголовки `X-User-*` не
выбирают другого организатора. Для приглашения коллег передавайте `attendee_ids`.
`POST /api/v1/calendar/events/batch` также требует входа и создаёт события только
в календаре текущего пользователя. Если email хотя бы одного элемента ему не
принадлежит, весь пакет получает `403` до создания событий.

`PUT /api/v1/auth/password` поддерживает локальные аккаунты с bcrypt-паролем
без обращения к Keycloak. Текущий пароль обязателен; новый должен содержать
не менее 8 символов и укладываться в 72 байта UTF-8. Сохранение хеша, отзыв
старых сессий и аннулирование прежних подтверждений выполняются одной транзакцией.
Успешный ответ для локального аккаунта содержит `reauthentication_required: true`:
клиент очищает прежние токены и предлагает войти с новым паролем.

Useful commands:
```bash
make test
make fmt
make swagger
```

`make swagger` automatically installs the pinned `swag` generator into `bin/`
on its first run, then updates `docs/docs.go`, `docs/swagger.json` and
`docs/swagger.yaml`. Go and network access are required for the first install;
no global `swag` installation or PATH changes are needed.

By default, the API applies SQL migrations from [`db/migrations`](./db/migrations)
at startup. Set `RUN_DB_MIGRATIONS=false` to use an already migrated database
without opening a migration connection or changing the migration ledger.
The API and `cmd/migrate` use the same enable/disable switch. If a deployment
runs `/app/migrate` separately, enable migrations for that command and disable
them for the API process afterward. `MIGRATIONS_DIR` overrides the SQL directory;
its default is `db/migrations` for the API and `/app/db/migrations` for the runner.

## Email Notifications

The backend supports SMTP email notifications with HTML + plain text fallback.

Required env:
```bash
MAIL_ENABLED=true
MAIL_PROVIDER=smtp
SMTP_HOST=smtp.yourdomain.com
SMTP_PORT=587
SMTP_USERNAME=info@yourdomain.com
SMTP_PASSWORD=change-me
SMTP_FROM_EMAIL=info@yourdomain.com
SMTP_FROM_NAME="Workspace"
SMTP_USE_TLS=true
SMTP_SKIP_TLS_VERIFY=false
```

Optional recipient override for Mailcow users:
```bash
MAIL_DOMAIN=yourdomain.com
MAIL_NOTIFICATION_TYPES=incoming_call,system,security,calendar_invite,calendar_event,web_login
```

Recipient resolution rules:
- all users receive email to the address stored in the database
- invalid or empty recipient emails are skipped with warning logs and do not break notification delivery

Test endpoints:
- `POST /api/v1/admin/email/test` for authenticated super admins
- `POST /api/internal/email/test` for internal callers with `X-Internal-Token`
- `GET /api/internal/mail/oauth/profile` for mail-stack callers with `X-Internal-Token` + `Authorization: Bearer <keycloak_access_token>`

## IDP OAuth

IDP OAuth endpoints live under `/api/v1/idp`.

Implemented:
- `GET /api/v1/auth/idp/start`
- `GET /api/v1/auth/idp/callback`
- optional debug endpoints behind `IDP_ENABLE_DEBUG_API=true`:
  - `GET /api/v1/idp/auth-url`
  - `POST /api/v1/idp/token`
  - `POST /api/v1/idp/token/refresh`
  - `GET /api/v1/idp/user/basic`
  - `GET /api/v1/idp/user/phone`
  - `GET /api/v1/idp/user/email`

Required env:
```bash
IDP_BASE_URL=https://idp.example.com
IDP_CLIENT_ID=...
IDP_CLIENT_SECRET=...
IDP_REDIRECT_URI=https://workspace.yourdomain.com/api/v1/auth/idp/callback
IDP_SCOPES=user:basic:read user:phone:read user:email:read
IDP_STATE_TTL=10m
IDP_ENV=prod
```

Optional env:
```bash
IDP_DEFAULT_LANG=ru
IDP_REQUEST_TIMEOUT=15s
IDP_ENABLE_DEBUG_API=false
FRONTEND_SUCCESS_URL=https://workspace.yourdomain.com/web/auth/callback
FRONTEND_ERROR_URL=https://workspace.yourdomain.com/web/auth/callback
```

Notes:
- production login flow is backend-managed: frontend does not receive raw IDP tokens
- `redirect_uri` is always taken from backend config
- IDP access/refresh tokens are used only inside callback to fetch profile data and are not persisted

## Notes API

Notes routes live under `/api/v1/notes`.

Implemented:
- note CRUD
- create note with optional files/photos in the main `POST /api/v1/notes` multipart request
- notes folders under `/api/v1/notes/folders`
- pin/unpin ordering: pinned first, then `updated_at DESC`
- checklist mode with embedded items
- search by `title` + `content` with PostgreSQL FTS and `ILIKE` fallback
- trash via soft delete and restore
- sync endpoints:
  - `GET /api/v1/notes/sync`
  - `POST /api/v1/notes/sync/batch`
- file and photo attachments:
  - `POST /api/v1/notes/{id}/attachments`
  - `GET /api/v1/notes/{id}/attachments`
  - `DELETE /api/v1/notes/{id}/attachments/{fileId}`

## Sync Logic

The server keeps `updated_at` and `version` on every note.

Rules:
- every successful mutation increments `version`
- client updates must send the current note `version`
- if the server version is newer, the API returns `409` with code `NOTE_VERSION_CONFLICT`
- `GET /api/v1/notes/sync?since=<ISO_DATE>` returns all changed notes after the given timestamp, including deleted ones
- `POST /api/v1/notes/sync/batch` accepts offline-created or offline-updated notes plus delete/restore/hard-delete actions

Recommended client flow:
1. Pull changes with `GET /notes/sync`.
2. Apply local offline changes with `POST /notes/sync/batch`.
3. On `409`, replace the local note with the server copy and reapply user edits if needed.

## Notes Storage

Database objects added for notes:
- `note_folders`
- `notes`
- `note_attachments`
- `note_audit_events`

Attachments reuse the existing `files` table and MinIO storage, so uploaded note files can be handled with the same storage backend already used by the project.

## Personal Team AI

The existing `/api/v1/ai/chats/:id/messages` chat runs a model-driven read
loop in `internal/service/ai_context_chat.go`. The model chooses datasets, filters,
pages and details and combines the returned facts. There are no keyword-to-answer
rules, arbitrary SQL, arbitrary HTTP URLs or privileged data-reading accounts.
The first completion uses `tool_choice=auto`: general conversation, explanations,
writing, translation, math and coding can return an explicit `answer_mode=general`
envelope immediately. Capabilities come from the assistant instructions and source
catalogue, not the user's profile. General answers preserve Markdown, code and JSON
and have no verification cards or sources. The initial profile read still verifies
current authorization; it is not presented as evidence for a general answer.
Personal facts and attachment contents require source reads before answering.
If the gateway omits `tool_calls` without a valid general-answer envelope, or
explicitly rejects tool parameters, its unclassified answer is discarded and the
same model is asked for strict JSON. Both protocols use the same authorized
readers, pagination, sanitization and budgets. The JSON protocol accepts only
complete `read`/`answer` objects; a first-step answer must explicitly use
`answer_mode=general` and have no cards. Unread grounded answers and malformed
plans fail explicitly. The model chooses the mode from the request and history;
this is not an independent factual correctness guarantee. Authentication, rate-limit and server
errors do not trigger this compatibility path. No question-to-answer rules are
introduced, and JSON control envelopes are never saved as assistant replies.

Contextual chat retains up to 40 recent messages within a 64 KB history budget,
dropping whole old turns rather than cutting the latest request. General replies
allow up to 6000 completion tokens so explanations and code have room to finish.

Sources are declared in `internal/service/ai_context/catalog.json`. Workspace
reads go through the same authenticated router, tenant checks and object ACLs as
the UI. The actor comes only from server middleware. The model cannot set user,
organization, tenant, tokens or cookies. `tasks.mine=true` resolves the assignee
server-side; training reads default to the current employee even for managers.
Growy reuses the user's existing Workspace SSO session: CRM/WhatsApp reads
target `/api/platform/v1/`, enrollment/progress reads target `/api/my/`, and
unfinished courses target `/api/training-programs/my-unfinished/`. The LMS
proxy routes are GET-only and retain Growy's own permission checks.

Available domains include profile/organization, tasks/boards/goals/history,
calendar, Drive, notes, conversations/groups/channels, notifications/recordings,
AI chat history, Growy CRM/WhatsApp/training, ODO documents/workflows/assignments,
SenimDesk tickets/works/objects/projects/clients/knowledge, and corporate mail.
Drive, both upload paths of the current AI chat, and mail/ODO file attachments
can supply parsed text. PDF/DOCX/TXT/XLSX/PPTX use the existing parser; unsupported
formats are reported rather than guessed. Large text can be read through
`text_page` (HTTP sources) or `page` (chat/Drive text readers). The model must
respect pagination and state what remains unexamined.

Connector setup on the Workspace server:

- `ALEM_API_KEY` and `ALEM_DEFAULT_MODEL`: the provider and chosen model must
  support native function calling or follow the strict JSON read-plan protocol.
  The configured default is used for contextual chats, including old chats.
  Compatibility negotiation adds one model request when native tools are absent.
- `AI_CONTEXT_ODO_URL`: the reachable ODO BFF base URL. Production Compose defaults
  to `http://odo-bff:8080`. ODO validates the current
  Workspace access JWT and maps the actual user/company. Do not use a service JWT.
- `SD_ACCESS_API_URL` and `SD_ACCESS_INTERNAL_TOKEN`: reuse the existing SSO
  configuration. Deploy the SD `/api/internal/workspace/ai-read-token` endpoint
  with its matching `WORKSPACE_ACCESS_INTERNAL_TOKEN` and existing tenant/position
  policy settings. It resolves an already linked account, issues a two-minute
  GET-only token with the actual role/company and returns the verified tenant host.
  It does not provision a user, company, membership or refresh token.
- `AI_CONTEXT_MAIL_URL`: the reachable corporate-mail API base URL. Production
  defaults to `http://axis-mail-app-app-1:4100`; other deployments can use an
  explicit origin such as `https://inbox.teamspace.kz`. Deploy its
  `/api/internal/workspace/ai/*` routes.
  Mail revalidates the Workspace session and resolves the immutable user/company
  binding, active mailbox and accepted consent. Both local mail and Stalwart use
  the same user mailbox checks as ordinary mail reads. No mailbox is provisioned.
- Growy uses the existing Workspace proxy and verified SSO identity. Only the
  specific training GET routes were added; mutation routes were not opened.
  CRM/platform APIs use `GROWY_PUBLIC_URL` and optional `GROWY_API_INTERNAL_URL`.
  Training reads use the separate `GROWY_LMS_PUBLIC_URL` (default:
  `APP_PUBLIC_URL/growy`) and optional `GROWY_LMS_INTERNAL_URL`. The LMS connection
  provisions and exchanges credentials with that same LMS backend; CRM credentials
  are never reused for LMS. Both private origins default to empty in Compose.
  To use the colocated LMS over Docker, explicitly set
  `GROWY_LMS_INTERNAL_URL=http://growy-api-1:8000`. Private requests preserve the
  public Host/protocol. LMS requests carry a tenant Origin built from the current
  server-validated organization slug and `TENANT_BASE_DOMAIN` (default:
  `teamspace.kz`), not incoming browser headers. Browser/SSO URLs remain public.
- Iform reads profile, directory, accessible processes/requests/forms, assigned
  tasks, request timeline/document metadata and own notifications through the
  existing Workspace Iform bridge. The bridge resolves access and signs each
  GET for the current user/company. Production uses `iform-backend:8081`.
  Tool pages start at 1 and translate to Iform's zero-based API pages; a full
  list page carries `has_next` so the model can continue reading.

For the shared-server deployment, `connect-ai-services.sh` attaches installed,
running application backends to `workspace_workspace` during backend release.
The ODO BFF, Growy API and mail app Compose files preserve this network on
recreation. Databases retain their original networks. Service names/origins are
operator configuration; they are never model inputs. ODO assignment lists resolve
the ODO user ID via authenticated `/api/v1/auth/me` and enforce that executor
server-side, including for administrators. Network connectivity does not grant
user permission; SSO keys, active membership and service ACLs are still required.

Empty/unreachable connectors are explicit unavailable sources, never empty
results. Native model execution has a 90-second deadline, at most 16 reads and
nine model turns, bounded responses and a total tool-result context budget.
External reads have timeouts and cannot follow redirects. Results strip known
credential/encryption fields and storage/signed URLs before entering the model.
Files and messages are untrusted evidence, not instructions. The model prompt
distinguishes facts, deductions, failures and partial reads. Server-generated
source IDs/statuses are returned separately in `data_sources`; only the public
answer and selected verified cards are preserved in ordinary chat history.

End-to-end encrypted messages remain opaque: metadata is available, plaintext is
not. Images/audio/video require a separate supported extraction/vision source.
Prompt rules and permission controls do not guarantee model correctness; evaluate
real multi-source questions with the configured provider before production rollout.
No live provider/production-tenant validation is implied by unit tests.

Focused checks:

```bash
go test ./internal/service -run 'TestAIReader|TestPersonalAI' -count=1
go test ./internal/handler ./internal/repository ./internal/alem ./cmd/api
```

Opt-in quality evaluation against the configured real provider (uses API tokens):

```bash
# Supply ALEM_API_KEY through the environment; optional ALEM_DEFAULT_MODEL.
TEAM_AI_LIVE_EVAL=1 go test ./internal/service -run '^TestPersonalAILiveQuality$' -v -count=1
```

The live suite uses synthetic user data and covers capabilities, greetings,
explanations, arithmetic, code/JSON, Russian/English/Kazakh, writing, follow-ups,
personal tasks, unavailable mail and mixed requests. It checks routing and some
concrete results and prints answers for human quality review. It does not validate
production connectors or guarantee correctness on arbitrary questions.

The SD tests cover stable identity, company isolation, revoked access and GET-only
credentials. Mail's `tests/ai-context.test.ts` tests mailbox isolation, consent,
revocation, long text, attachments and rejected impersonation without a live DB.
The deployment copy lives in `workspace-front/deploy/mail/source`; keep it in sync
with the canonical mail implementation when releasing these changes.
