# ALem Super App Backend

## Техническая документация backend

Версия документа: 1.0  
Статус: актуально по состоянию на 2 июня 2026 года  
Репозиторий: `alem-super-app-backend`

## 1. Назначение документа

Настоящий документ описывает архитектуру, состав модулей, модель исполнения запросов, инфраструктурные зависимости, структуру хранения данных и эксплуатационные особенности backend-сервиса `alem-super-app-backend`.

Документ предназначен для:

- backend-разработчиков;
- frontend-разработчиков;
- DevOps-инженеров;
- технических лидов;
- специалистов по сопровождению и аудиту.

## 2. Общая характеристика системы

`alem-super-app-backend` представляет собой монолитный backend-сервис на Go, реализующий API и системную логику для следующих предметных областей:

- аутентификация и управление сессиями;
- личные и групповые чаты;
- каналы;
- звонки и записи звонков;
- календарь и встречи;
- kanban и task management;
- drive и файловое хранилище;
- notes и notebook;
- иерархия, министерства и HR-профили;
- уведомления и push-доставка;
- AI-модули, агент и LLM-интеграции;
- публичные приложения;
- служебные административные и интеграционные потоки.

Архитектурно система построена по слоистой схеме:

`router -> handler -> service -> repository -> DB / storage / external APIs`

При этом часть правил доступа и бизнес-условий реализована не только в `service`, но и непосредственно в SQL-запросах слоя `repository`.

## 3. Точка входа и сборка зависимостей

Основная точка входа расположена в файле [`cmd/api/main.go`].

Функции `main.go`:

- загрузка конфигурации;
- инициализация логирования;
- подключение к Postgres;
- применение миграций `goose.Up(...)`;
- подключение Redis;
- инициализация Keycloak-клиентов и verifier;
- инициализация MinIO;
- инициализация LiveKit;
- создание telemetry и websocket hub;
- создание всех repositories;
- создание всех services;
- создание всех handlers;
- сборка HTTP router;
- запуск HTTP server;
- запуск фоновых процессов.

`main.go` выступает в роли единственного composition root. Контейнер зависимостей в проекте не используется.

Дополнительная утилита расположена в [`cmd/user-merge/main.go`] и предназначена для слияния дублей пользователей.

## 4. Структура репозитория

Ключевые директории репозитория:

- `cmd` — исполняемые entrypoints;
- `internal/config` — конфигурация приложения;
- `internal/server` — HTTP server и router;
- `internal/handler` — HTTP handlers и embedded pages;
- `internal/service` — бизнес-логика и orchestration;
- `internal/repository` — доступ к данным и SQL;
- `internal/domain` — доменные модели, DTO, enums, validation;
- `internal/auth` — локальный auth-контур, JWT, PIN, session storage;
- `internal/keycloak` — интеграция с Keycloak;
- `internal/idp` — интеграция с eGov IDP;
- `internal/alem` — клиент LLM API;
- `internal/websocket` — realtime hub;
- `internal/metrics` — экспорт системных метрик;
- `internal/telemetry` — runtime telemetry звонков;
- `internal/infrastructure/database` — bootstrap подключения к Postgres;
- `db/migrations` — миграции схемы БД;
- `db/diagnostics` — служебные SQL-диагностики;
- `docs` — эксплуатационная и инфраструктурная документация.

## 5. Конфигурация

Конфигурация загружается из [`internal/config/config.go`].

Конфигурационный слой охватывает:

- HTTP server;
- Postgres;
- Redis;
- JWT;
- Keycloak;
- LiveKit;
- MinIO;
- SMTP;
- APNS/Push;
- IDP;
- AI/Agent;
- meeting providers;
- public URL и прикладные флаги.

Тесты подтверждают наличие environment overrides как минимум для:

- LiveKit;
- Agent моделей;
- SMTP.

## 6. HTTP-слой

### 6.1 Server

HTTP server реализован в [`internal/server/http.go`].

Характеристики:

- используется стандартный `http.Server`;
- таймауты берутся из конфигурации;
- `MaxHeaderBytes = 1MB`;
- поддерживается graceful shutdown.

### 6.2 Router

Основной router расположен в [`internal/server/router.go`].  
Drive-роуты частично вынесены в [`internal/server/router_drive.go`].

Маршруты разделены на 4 зоны:

- `public`;
- `internal`;
- `auth` без обязательной авторизации;
- `protected`.

### 6.3 Middleware

Основные middleware:

- [`internal/middleware/auth.go`] — локальная auth-проверка, bearer/query/cookie extraction;
- [`internal/middleware/keycloak_auth.go`] — валидация Keycloak JWT;
- [`internal/middleware/internal_token.go`] — защита internal endpoints;
- [`internal/middleware/ratelimit.go`] — rate limit с Redis и in-memory fallback;
- [`internal/middleware/trace_id.go`] — генерация и прокидывание `X-Trace-ID`;
- [`internal/middleware/common.go`] — CORS, запись app errors, единый error response.

Особенности:

- идентификатор rate limit: `user_id`, при отсутствии — IP;
- при отказе Redis rate limit работает в режиме fail-open;
- для websocket допустим query token;
- web auth flow использует также `HttpOnly` cookies.

## 7. Модель исполнения запроса

Типовой путь запроса:

1. запрос приходит в `router`;
2. проходит через middleware;
3. попадает в `handler`;
4. `handler` парсит входные данные и вызывает `service`;
5. `service` выполняет бизнес-логику, permission checks, orchestration;
6. `repository` выполняет SQL-запросы к Postgres;
7. при необходимости `service` обращается к Redis, MinIO, LiveKit, Keycloak, SMTP, push-провайдерам и другим внешним системам;
8. результат возвращается через `handler`.

Дополнительно возможны side effects:

- websocket fanout;
- push notifications;
- email-отправка;
- запись telemetry;
- обновление кешей;
- запуск cleanup/retry логики.

## 8. Основные модули

### 8.1 Аутентификация и сессии

Ключевые файлы:

- [`internal/handler/auth_handler.go`]
- [`internal/auth/service_keycloak.go`]
- [`internal/auth/token.go`]
- [`internal/auth/token_storage.go`]
- [`internal/auth/quick_pin_service.go`]

Функции модуля:

- регистрация;
- логин по паролю;
- Keycloak login flow;
- silent SSO;
- refresh/logout/logout-all;
- local JWT issuance;
- linked web sessions;
- PIN login;
- QR policy для web login;
- onboarding;
- синхронизация пользователей с Keycloak;
- управление профилем и паролем.

Цепочка:

`AuthHandler -> auth.Service / QuickPinService -> UserRepository + UserHRProfileRepository + QuickPinRepository + LoginLogRepository + TokenStorage + Keycloak`

Особенности:

- локальный JWT-контур существует отдельно от Keycloak;
- refresh-token flow умеет перевыпускать новую пару токенов;
- linked web devices и browser sessions управляются через Redis token storage;
- web login flow использует cookies и не публикует токены в redirect URL;
- `azp` в Keycloak JWT валидируется по allowlist.

### 8.2 Пользователи, HR и профиль

Ключевые файлы:

- [`internal/repository/user_repository.go`]
- [`internal/repository/user_hr_profile_repository.go`]
- [`internal/service/profile_section_service.go`]
- [`internal/service/user_qr_service.go`]

Функции модуля:

- user CRUD и административное редактирование;
- HR directory;
- scoping по министерствам и орг-иерархии;
- QR tokens;
- education;
- licenses/certifications;
- online/status;
- детали пользователя;
- canonical user resolution.

### 8.3 Чаты, сообщения и группы

Ключевые файлы:

- [`internal/handler/chat_handler.go`]
- [`internal/handler/group_handler.go`]
- [`internal/service/chat_service.go`]
- [`internal/service/group_service.go`]
- [`internal/repository/chat_repository.go`]
- [`internal/repository/message_repository.go`]
- [`internal/repository/group_repository.go`]
Функции модуля:

- direct conversations;
- group chats;
- сообщения и история;
- mentions;
- shared media;
- reactions;
- hide/unhide;
- unread counters;
- forwarding;
- broadcast;
- pinned items;
- note/task/event sharing.

Цепочка:

`ChatHandler/GroupHandler -> ChatService/GroupService -> ChatRepository + MessageRepository + GroupRepository + ReactionRepository + PinnedRepository + UserRepository + wsHub + NotificationService`

Особенности:

- realtime fanout идёт через websocket hub;
- часть share-потоков вызывает `NoteService`, `KanbanService`, `CalendarService`;
- существуют скрытые чаты и скрытые группы;
- есть поддержка ephemeral messages и cleanup.

### 8.4 Unified chats, folders, pinned и themes

Ключевые файлы:

- [`internal/service/unified_service.go`]
- [`internal/service/folder_service.go`]
- [`internal/service/pinned_service.go`]
- [`internal/service/chat_theme_service.go`]

Функции модуля:

- unified list чатов из нескольких источников;
- пользовательские папки;
- pinned chats;
- pinned messages;
- chat themes.

### 8.5 Каналы

Ключевые файлы:

- [`internal/handler/channel_handler.go`]
- [`internal/service/channel_service.go`]
- [`internal/repository/channel_repository.go`]

Функции модуля:

- создание и редактирование каналов;
- подписки и mute;
- роли участников;
- channel posts;
- pinned posts;
- post viewers;
- reactions;
- realtime broadcasts.

Цепочка:

`ChannelHandler -> ChannelService -> ChannelRepository + UserRepository + PinnedRepository + wsHub`

Таблицы:

- `channels`;
- `channel_members`;
- `channel_posts`;
- `channel_post_views`;
- `channel_post_reactions`.

### 8.6 Drive и file storage

Ключевые файлы:

- [`internal/handler/drive_handler.go`]
- [`internal/handler/file_handler.go`]
- [`internal/service/drive_service.go`]
- [`internal/service/file_service.go`]
- [`internal/repository/drive_repository.go`]

Функции модуля:

- folder CRUD;
- file upload/download/delete/update;
- public links;
- public file access;
- recent items;
- search and filtering;
- sharing and inherited permissions;
- OnlyOffice callback.

Цепочка:

`DriveHandler/FileHandler -> DriveService/FileService -> DriveRepository + RecentRepository + MinIO`

Особенности:

- effective permission может наследоваться по folder tree;
- есть собственные file scopes;
- public access реализован отдельными token-based маршрутами.

### 8.7 Calendar и meeting management

Ключевые файлы:

- [`internal/handler/calendar_handler.go`]
- [`internal/service/calendar_service.go`]
- [`internal/service/calendar_availability_service.go`]
- [`internal/service/calendar_meeting_provider_service.go`]
- [`internal/repository/calendar_repository.go`]
- [`internal/repository/calendar_repository_reminders.go`]

Функции модуля:

- events CRUD;
- attendees;
- recurring events;
- availability check;
- suggest times;
- reminders;
- invite actions;
- public event creation;
- user/event search;
- today summary;
- upcoming/weekly views;
- online meetings;
- Google/Apple sync.

Цепочка:

`CalendarHandler -> CalendarService -> CalendarRepository + UserRepository + NotificationService + HierarchyService + GoogleCalendarService + AppleCalendarService + CalendarAvailabilityService + CalendarMeetingProviderService`

Таблицы:

- `events`;
- `event_attendees`;
- `event_reminders`;
- Google/Apple mapping tables.

Особенности:

- overlapping проверяется по attendee participation;
- reminders учитывают recurrence;
- small meetings могут уходить в LiveKit;
- large meetings могут уходить в Cisco.

### 8.8 Calls и LiveKit

Ключевые файлы:

- [`internal/handler/livekit_handler.go`]
- [`internal/service/livekit_service.go`]
- [`internal/service/livekit_identity.go`]
- [`internal/repository/call_repository.go`]
- [`internal/repository/recording_repository.go`]
- [`internal/repository/personal_recording_repository.go`]

Функции модуля:

- direct calls;
- group calls;
- assistant calls;
- accept/decline/end/join/rejoin;
- participants;
- incoming call push;
- call lifecycle;
- room cleanup;
- server webhook processing;
- call recordings;
- personal recordings;
- diagnostics and telemetry.

Цепочка:

`LiveKitHandler/PersonalRecordingHandler -> LiveKitService + NotificationService + CallHistoryRepository + CallRecordingRepository + PersonalCallRecordingRepository + UserRepository + FileService + telemetry`

Особенности:

- handler содержит существенную orchestration-логику;
- используется session-aware participant identity;
- есть stale device semantics;
- есть cleanup/retry after grace period;
- telemetry ведётся отдельно от Prometheus-метрик.

### 8.9 Kanban

Ключевые файлы:

- [`internal/handler/kanban_handler.go`]
- [`internal/service/kanban_service.go`]
- [`internal/service/kanban_service_access.go`]
- [`internal/repository/BoardRepository.go`]
- [`internal/repository/TaskRepository.go`]
- [`internal/repository/kanban_access_sql.go`]

Функции модуля:

- boards CRUD;
- members и roles;
- assistant board flows;
- tasks CRUD;
- task move/status/history;
- attachments;
- columns;
- comments;
- worklogs;
- labels;
- goals;
- projects;
- sprints;
- search;
- my todo;
- board stats;
- SLA/cycle time/reopen/time-in-status/throughput/capacity/burn-down;
- org access;
- org stats access.

Цепочка:

`KanbanHandler -> KanbanService -> BoardRepository + TaskRepository + UserRepository + HierarchyRepository + DriveRepository + FileService + NotificationService + GamificationService`

Особенности:

- SQL-видимость досок и задач зашита в repository;
- поддерживаются ownership, direct membership, explicit org access и leadership access;
- `task_history` является центральным источником аналитики;
- статистика не строится из кеша, а рассчитывается на основе task data.

### 8.10 Notes и Notebook

Ключевые файлы:

- [`internal/handler/note_handler.go`]
- [`internal/service/note_service.go`]
- [`internal/repository/note_repository.go`]
- [`internal/handler/notebook_handler.go`]
- [`internal/service/notebook_service.go`]
- [`internal/repository/notebook_repository.go`]

Функции `notes`:

- note CRUD;
- sync и sync batch;
- folders;
- pin/unpin;
- move;
- soft delete;
- restore;
- hard delete;
- rich-content document model;
- table cell/column operations;
- attachments;
- photo presign/upload/complete;
- note sharing.

Таблицы `notes`:

- `notes`;
- `note_folders`;
- `note_attachments`;
- `note_photo_attachments`;
- `note_audit_events`;
- `note_shares`.

Особенности `notes`:

- optimistic locking через `version`;
- доступ возможен по owner, explicit share и shared note в chat/group;
- есть отдельный rich-content validation layer.

Функции `notebook`:

- notebook chats;
- chat history;
- sync messages;
- chat documents.

Таблицы `notebook`:

- `notebook_chats`;
- `notebook_messages`;
- `notebook_chat_documents`.

### 8.11 Уведомления, devices и email

Ключевые файлы:

- [`internal/handler/notification_handler.go`]
- [`internal/service/notification_service.go`]
- [`internal/service/internal_notification_service.go`]
- [`internal/service/email_log_service.go`]
- [`internal/service/email_service.go`]
- [`internal/service/apns_voip.go`]

Функции модуля:

- push notifications;
- incoming call push;
- call cancel push;
- internal notifications;
- email logging and mapping;
- SMTP sending;
- ICS calendar invites;
- APNS VoIP;
- device registration.

Цепочка:

`NotificationHandler/InternalNotificationHandler/EmailLogHandler/DeviceHandler -> NotificationService/InternalNotificationService/EmailLogService -> DeviceRepository + NotificationRepository + CallHistoryRepository + UserRepository + EmailLogRepository + EmailMappingRepository + EmailService + wsHub`

### 8.12 Иерархия, министерства и HR mapping

Ключевые файлы:

- [`internal/handler/hierarchy_handler.go`]
- [`internal/service/hierarchy_service.go`]
- [`internal/repository/hierarchy_repository.go`]
- [`internal/service/ministry_service.go`]
- [`internal/service/hr_org_mapping_service.go`]

Функции модуля:

- full hierarchy tree;
- public hierarchy tree and search;
- state body / department / division management;
- leader assignment;
- user assignment to hierarchy;
- HR org mappings;
- embedded operational views.

Особенности:

- права завязаны на scope и орг-роль;
- синхронизация ролей частично связана с Keycloak admin API.

### 8.13 Search

Ключевые файлы:

- [`internal/handler/search_handler.go`]
- [`internal/service/search_service.go`]

Функции:

- search users;
- search messages;
- find identical messages;
- search all;
- global search.

Особенности:

- реализован как orchestration над несколькими providers;
- поддерживает timeouts;
- merge и sorting выполняются на service-уровне.

### 8.14 Public apps, dashboard, gamification, reviews

Ключевые файлы:

- [`internal/service/public_app_service.go`]
- [`internal/service/dashboard_service.go`]
- [`internal/service/gamification_service.go`]
- [`internal/service/review_service.go`]

Функции:

- public apps catalog;
- user favorites;
- ratings;
- access control;
- dashboard summary;
- gamification stats;
- user reviews.

Особенность reviews:

- создание review разрешено только при наличии подтверждённого взаимодействия через chat/task/calendar.

### 8.15 AI-модули и agent

Ключевые файлы:

- [`internal/handler/alem_handler.go`]
- [`internal/service/alem_service.go`]
- [`internal/handler/unified_ai_handler.go`]
- [`internal/service/unified_ai_service.go`]
- [`internal/handler/ai_assistant_handler.go`]
- [`internal/service/ai_assistant_service.go`]
- [`internal/handler/smart_agent_handler.go`]
- [`internal/agent/service.go`]

Состав AI-слоя:

- `AlemService` — legacy/general AI chat API;
- `UnifiedAIService` — singleton AI chat per user;
- `AIAssistantService` — compact voice-ready assistant endpoints;
- `internal/agent` — новый агентный контур.

Возможности `internal/agent`:

- session lifecycle;
- intent resolution;
- entity extraction;
- confirmation flow;
- capability registry;
- tool execution;
- audit;
- adaptive web search.

## 9. WebSocket и realtime

Ключевые файлы:

- [`internal/websocket/hub.go`]
- [`internal/websocket/client.go`]
- [`internal/websocket/session_key.go`]
- [`internal/handler/websocket_handler.go`]

Функции:

- websocket connection handling;
- online/offline presence;
- realtime sync между несколькими устройствами;
- sender echo suppression по session key;
- адресная отправка событий пользователю.

## 10. Метрики и telemetry

Ключевые файлы:

- [`internal/metrics/collector.go`]
- [`internal/telemetry/call_telemetry.go`]

Поддерживаются:

- HTTP metrics;
- Go runtime metrics;
- websocket counters;
- call telemetry snapshot;
- recent call events.

## 11. Хранение данных

Основная СУБД — Postgres.

Хранилища и кэши:

- Redis — sessions, token whitelist, rate limit, AI cache;
- MinIO — файлы и вложения;
- Postgres — бизнес-данные, audit, mappings, notifications, metadata.

## 12. Миграции базы данных

Миграции расположены в `db/migrations` и покрывают все домены системы.

Ключевые блоки миграций:

- users и auth;
- conversations/messages/groups;
- kanban/tasks/history/comments/labels/goals/projects/sprints;
- calendar/reminders/integrations;
- drive/public links/file scopes;
- AI/unified AI/agent;
- notebook/notes/note shares;
- calls/participants/recordings/personal recordings;
- E2EE/web pairing/backups;
- public apps/ratings/access;
- HR/profile sections/QR;
- channels;
- notifications extensions и device metadata.

## 13. Внешние интеграции

### 13.1 Keycloak

Ключевые файлы:

- [`internal/keycloak/admin.go`]
- [`internal/keycloak/keycloak_props.go`]
- [`internal/keycloak/jwks_cache.go`]
- [`internal/keycloak/verifier.go`]

Функции:

- OIDC login flow;
- direct password flow;
- JWKS verification;
- admin user management;
- role lookup;
- realm-aware validation.

### 13.2 eGov IDP

Ключевые файлы:

- [`internal/idp/client.go`]
- [`internal/service/idp_oauth_service.go`]

Функции:

- authorize URL;
- code exchange;
- refresh;
- user basic/phone/email.

### 13.3 Alem LLM API

Ключевые файлы:

- [`internal/alem/client.go`]
- [`internal/alem/methods.go`]

Функции:

- list models;
- chat completion;
- streaming;
- embeddings;
- rerank.

### 13.5 Push и почта

Используются:

- Firebase/APNS для push;
- APNS VoIP для звонков;
- SMTP для email;
- ICS invites для calendar.

## 14. Фоновые процессы

В приложении присутствуют фоновые процессы, запускаемые из `main.go` или связанных сервисов:

- reminder worker для calendar reminders;
- cleanup ephemeral messages;
- private call timeout worker;
- cleanup private call rooms;
- websocket presence lifecycle;
- LiveKit recording/personal recording finalization.

## 15. Docker и CI/CD

Ключевые файлы:

- [`Dockerfile`]
- [`docker-compose.yml`]
- [`.github/workflows/ci.yml`]
- [`.gitlab-ci.yml`]

Назначение:

- локальный и серверный контейнерный запуск;
- CI pipeline в GitHub Actions;
- CI/CD pipeline в GitLab.

## 16. Тестовое покрытие

Тесты распределены по ключевым подсистемам:

- auth;
- calendar;
- E2EE;
- LiveKit;
- agent;
- notes;
- search;
- middleware;
- keycloak verifier;
- websocket hub;
- metrics;
- public apps;
- reviews.

Наиболее зрелые контрактные зоны:

- `auth`;
- `calendar`;
- `E2EE`;
- `LiveKit`;
- `agent`;
- `notes`.


Документ может использоваться как базовая техническая спецификация backend-репозитория и как опорный материал для дальнейшей декомпозиции, рефакторинга, аудита и онбординга.
