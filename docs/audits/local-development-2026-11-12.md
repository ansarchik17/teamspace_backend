# Архитектура и локальный запуск TeamSpace

Дата проверки: 12 ноября 2026 года. Рассмотрены два соседних репозитория:
`teamspace_backend` и `teamspace_frontend`. Backend и frontend собираются.
Для обоих подготовлена команда `npm run dev`, но фактический совместный запуск
в среде проверки блокируют недоступный Docker Engine и запрет на локальные
TCP-порты. Отдельные проблемы безопасности и тестовой инфраструктуры
перечислены ниже по приоритету.

## Backend

Go-модуль `github.com/west6ide/workspace-backend` использует Gin, pgx/PostgreSQL,
Redis, MinIO, Goose, Zap и Keycloak. В `go.mod` указаны Go 1.24.4 и toolchain
1.24.7. В `internal` находится 706 Go-файлов без `_test.go`; в `db/migrations`
263 SQL-миграции. Это инвентаризация, а не оценка покрытия тестами.

Основной поток запроса:
`internal/server` → `internal/middleware` → `internal/handler` →
`internal/service` / `internal/auth` → `internal/repository` → PostgreSQL/MinIO.
`internal/domain` содержит сущности и контракты. Обмен событиями вынесен в
`internal/events`, realtime — в `internal/websocket` и `internal/whiteboard`.

`cmd/api/main.go` загружает `.env`, создаёт PostgreSQL pool, выполняет Goose,
подключает Redis, собирает зависимости и запускает HTTP-сервер и фоновые задачи.
При недоступном Redis предусмотрена работа без кеша, но часть распределённых
и AI-функций зависит от Redis. Ошибка MinIO логируется с отключением file service;
зависимые файловые сценарии при этом нельзя считать рабочими.

| Подсистема | Основные участки кода | Связь с frontend |
| --- | --- | --- |
| Вход, SSO, восстановление, приглашения | `internal/auth`, `internal/keycloak`, `internal/handler/auth*` | `HttpAuthRepository`, `ProtectedRoute`, auth pages |
| Компании, оргструктура, права и аудит | `organization*`, `employment*`, `security_audit*` | organization/platform pages, access use cases |
| Чаты, группы, каналы, вложения и E2EE | `chat*`, `message*`, `group*`, `channel*`, `e2ee*`, `websocket` | messenger, `HttpChatRepository`, IndexedDB key store |
| Задачи и Kanban | `board*`, `task*`, `kanban*` | board pages и repositories |
| Календарь и звонки | `calendar*`, `call*`, LiveKit/QMeet clients | calendar/meet pages, calls context |
| Drive и офисные документы | `drive*`, `file*`, `onlyoffice*`, WOPI | AlemDrive, встроенные редакторы |
| Заметки и notebook | `note*`, `notebook*` | Notes и research UI |
| AI, квоты и поиск | `alem`, `aiaccess`, `documentsearch`, `agent`, `service/ai*` | AI pages, лимиты и управление бюджетами |
| Публикации и уведомления | `publication*`, `notification*`, `metrics`, `telemetry` | news, dashboard, notification realtime |
| Внешние приложения | Growy, ODO, IForm, SenimDesk, mail/whiteboard adapters | встроенные страницы и SSO/proxy |

Защищённые маршруты проверяют локальные JWT или Keycloak JWT, активность
пользователя и состояние учётных данных. Далее действуют проверки членства,
компании и прав конкретного модуля. В AI предусмотрены accounting, квоты,
резервирование кредитов и ограничение параллельных запросов. Их наличие не
означает, что публичные маршруты автоматически защищены этими middleware.

## Frontend

React 19, TypeScript 5.9, Vite 8, Tailwind CSS 4 и React Router 7.
В `src` — 679 файлов с расширениями ts/tsx/js/jsx. `main.tsx` загружает i18n,
очищает legacy callback storage и монтирует React в StrictMode. `App.tsx`
регистрирует маршруты под `/web/`.

Разделение на `domain`, `application`, `infrastructure`, `presentation` и `shared`
позволяет вынести бизнес-сценарии и HTTP-адаптеры из UI. Однако большие страницы
ещё объединяют сетевые операции, состояние и представление: `MessengerPageContent`
содержит 13 883 строки, `CalendarPage` — 6 280, `BoardPage` — 3 910.

`LocalStorageAuthSessionStore` хранит access/refresh tokens и legacy aliases
в localStorage. `authorizedFetch` добавляет Bearer и web-device ID, объединяет
конкурентные refresh-запросы и повторяет GET/HEAD при временных ошибках.
`ProtectedRoute` восстанавливает cookie-сессию, проверяет onboarding и выполняет
tenant handoff. Токены в localStorage доступны JavaScript того же origin;
поэтому защита от XSS и изоляция встроенных приложений здесь существенны.

Telegram UI в `public/growy-telegram` исполняется отдельно от React и имеет
собственные requests/history/realtime/avatar/reply модули. В `deploy/mail`
находится отдельное почтовое приложение, в `deploy/whiteboard` — Python backend
и собственный frontend. Их сборки и окружения не заменяются запуском главного Vite.

## Приоритетные проблемы

### Публичное удаление файлов

**Высокий приоритет.** В `internal/server/router.go` маршрут
`DELETE /api/v1/files/public` зарегистрирован до защищённой группы.
`FileHandler.DeletePublicFile` принимает `object_name` и непосредственно вызывает
`FileService.DeleteFile`, который удаляет объект из общего bucket. Проверки
пользователя, владельца, публичного префикса или capability-подписи отсутствуют.
Знание имени объекта достаточно для вызова удаления через API. Защищённый
`FileHandler.DeleteFile` также не проверяет владельца перед удалением.

Нужно закрыть публичное удаление либо ввести узкую подписанную capability,
а в обычном удалении применять ACL ресурса до обращения к MinIO. Добавить проверки
анонимного доступа и удаления чужого объекта. Статический вывод относится к API;
наличие дополнительного ограничения в конкретном внешнем ingress не проверялось.

### Создание событий от имени указанного пользователя

**Высокий приоритет.** Публичные маршруты `/api/v1/calendar/public/events`,
`/api/v1/calendar/events/public` и `/api/v1/calendar/events/batch` находятся вне
группы аутентификации. `resolvePublicEventUserID` доверяет `user_id`, email и
заголовкам `X-User-Id` / `X-User-Email`. `CreatePublicEvent` передаёт выбранный ID
в обычное создание события. Batch-обработчик допускает отсутствие пользователя,
а service находит получателей по email и создаёт записи календаря.

Нужно определить контракт интеграции: authenticated caller с проверкой прав
либо серверная подпись с ограничением компании/получателей. Произвольные browser
headers не должны выступать подтверждением личности.

### Учётные данные EPS в браузере

**Высокий приоритет.** `teamspace_frontend/src/shared/config/epsConfig.ts` содержит
фиксированный API token и пароль сервисного аккаунта. `epsAuth.ts` и `epsMail.ts`
используют их в запросах. Такие значения попадают в browser bundle.
`VITE_*`-переменные тоже доступны браузеру и не устраняют проблему хранения секрета.

Нужно перенести сервисный вход на backend, привязать доступ к текущему
пользователю и заменить опубликованные credentials на стороне EPS. Значения
секретов в этом отчёте не воспроизводятся.

### Общий seed администратора

**Высокий приоритет.** Миграция
`db/migrations/20260827000100_seed_admin_user_fixed.sql` вставляет активного
администратора с фиксированным паролем, без проверки `ENV`. API запускает миграции
в любом окружении. Для базы с применённой миграцией требуется проверить состояние
этой учётной записи и сменить/отозвать credentials. Новое создание dev-пользователя
лучше отделить от общей production migration chain.

### Миграции выполняются двумя способами

**Средний приоритет.** Docker entrypoint сначала запускает `cmd/migrate`, затем
`cmd/api/main.go` повторно вызывает `goose.Up`. Migration CLI использует
`goose.NewProvider` и явный список допустимых исторических пропусков, API —
глобальный `goose.Up`. Это два разных правила запуска одной migration chain.
В частности, `RUN_DB_MIGRATIONS=false` читается CLI, но не отключает миграции API.
Строка подключения API также собирается через `fmt.Sprintf`, тогда как CLI
экранирует credentials через `url.UserPassword`.

Следующая доработка: единый migration runner и один владелец старта миграций,
совместимые DSN и явные правила upgrade для существующих баз.

### Readiness не проверяет зависимости

**Средний приоритет.** `internal/server/router.go:readyCheck` всегда возвращает
200. После старта процесс может потерять PostgreSQL, но `/ready` продолжит
объявлять готовность. Нужна bounded-проверка необходимых зависимостей с 503
при их недоступности; `/health` можно оставить проверкой живого процесса.

### Размер frontend и поддерживаемость

**Средний приоритет.** В исходной сборке основной index chunk имеет около
3,22 МБ, Mermaid vendor — 2,89 МБ, один large-vendor — 1,35 МБ до gzip.
Большие страницы импортируются в `App.tsx` напрямую. Это увеличивает загрузку
и сложность изменения общих состояний. Следует выносить маршруты и тяжёлые
редакторы/экспортёры в lazy imports, затем разделять крупные страницы по сценариям.
Замеры реального LCP, памяти и времени интерактивности в браузере не выполнялись.

### Тесты Telegram не соответствуют подключённым зависимостям

**Средний приоритет.** Общий запуск показывает пять ошибок: topics, inline image,
reconnect, realtime и переключение phone/QR. В VM fixture accounts отсутствует
`window.GrowyTelegramBindReply`, хотя HTML приложения загружает `reply-gesture.js`
перед `inbox.js`. QR fixture не моделирует `style`, используемый в renderAccounts.
Часть последующих assertions падает после этих ранних ошибок.

Нужно обновить fixtures и подключать те же зависимости, что использует страница,
после чего отдельно проверить оставшиеся assertions. Эти падения сами по себе
не доказывают неисправность всех пяти сценариев в реальном браузере.

## Изменения локального запуска

Backend получил npm scripts, `.nvmrc`, `.env.development.example`,
`compose.dev.yml` и `scripts/dev.mjs`. Launcher создаёт локальный `.env` только
при его отсутствии и генерирует секреты. Он поднимает постоянные PostgreSQL,
Redis, MinIO и Keycloak в Compose-проекте `teamspace-dev`, ждёт health checks, собирает
и запускает Go API. Существующие Docker Compose и deployment scripts сохранены.

При повторном запуске в пользовательском терминале скачивание закреплённого
образа MinIO из Quay завершилось `unauthorized`. Локальный Compose исправлен:
MinIO теперь собирается в `docker/minio-dev/Dockerfile` из официального commit
`0d7408fc9969caf07de6a8c3a84f9fbb10a6739e`, уже используемого CI. `pull_policy: build`
исключает скачивание готового MinIO-образа; постоянный volume сохранён.
Compose-конфигурация проверена. Фактическая Docker-сборка из sandbox не выполнена:
доступ к сокету работающего Docker Engine запрещён правами окружения.

Для регистрации добавлен Keycloak 26.8.2 с импортом realm `Workspace`, ролью `user`
и клиентом `workspace-client`. После конфликта с занятым портом 8081 локальный
порт изменён на `18082` и вынесен в `KEYCLOAK_HTTP_PORT`. Launcher проверяет
согласованность внутренних/публичных адресов, issuer и JWKS до запуска сервисов.
Данные Keycloak сохраняются в отдельном volume.

Команды `seed:admin` и `seed:director` создают локальные тестовые аккаунты только
в `ENV=development` и PostgreSQL проекта `teamspace-dev`. Они проверяют совпадения
существующих аккаунтов и не меняют чужие credentials. Пароли хранятся в исключённых
из Git файлах `.cache/dev-admin.json` и `.cache/dev-director.json` с правами 0600.

Frontend по умолчанию отправляет Workspace API и WebSocket на
`http://127.0.0.1:8080`. Env читается для выбранного Vite mode с приоритетом
переменных терминала. Для IForm, AI и EPS адреса можно задать через env.
Порт frontend закреплён на 5173; cookies удалённого target переписываются
на текущий host. Добавлены `.env.example`, общий `npm test` и инструкции README.

Go API не имеет автоматического reload в новом npm launcher: после изменения
Go-файлов нужен перезапуск команды. Frontend сохраняет Vite HMR.

Базовые адреса: frontend `http://localhost:5173/web/`, API `http://localhost:8080`,
PostgreSQL `127.0.0.1:15433`, Redis `127.0.0.1:16379`, MinIO `127.0.0.1:19000`,
MinIO console `http://127.0.0.1:19002`, Keycloak `http://localhost:18082`.

Локальная пара не поднимает внешние AI/mail/CRM/ODO/IForm/video/office services;
SSO для Workspace обслуживает локальный Keycloak. SMTP отправителя настраивается
отдельно в `.env`.
EPS пока сохраняет существующий удалённый адрес по умолчанию. Legacy
`scripts/local-dev.mjs` с путями axis_workspace/senim_desk и `make up` со старыми
именами messenger-сервисов не относятся к новому npm launcher.

## Результаты проверок

| Проверка | Результат |
| --- | --- |
| Frontend `npm ci --offline` | 606 пакетов установлены из кеша; онлайн vulnerability audit не выполнялся |
| Frontend `npm run build` | Проходит TypeScript и Vite build, включая повторную сборку после изменения proxy |
| Frontend `npm run lint` | 0 ошибок, 476 предупреждений |
| Frontend `npm run i18n:check` | Проходит; 5 649 ru-ключей, 3 640 строк с кириллицей вне каталогов при baseline 3 647 |
| Frontend Node tests | 154 теста, 149 pass, 5 fail в Telegram fixtures |
| Backend `npm run build` | Go API собран toolchain 1.24.7 |
| Backend `go vet ./...` | Проходит |
| Backend `go test ./...` | 14 пакетов проходят; 10 пакетов обрываются на `httptest` из-за запрета listen TCP |
| Проверка синтаксиса launcher и Compose config | `node --check scripts/dev.mjs` и `docker compose -f compose.dev.yml config --quiet` проходят |
| Vite proxy config | Проходят проверки локального target, WebSocket, фиксированного порта, mode env, приоритета shell и адресов интеграций |
| Keycloak config | Проходят проверки Compose, синтаксиса healthcheck, realm/client/audience, произвольного порта и отклонения несовпадающих URL/issuer |
| Seed commands | Синтаксис проходит; запуск при ENV=production отклоняется до обращения к БД |
| Frontend `npm run dev` | Остановка на `listen EPERM 127.0.0.1:5173` в sandbox |
| Backend `npm run dev` | Создаёт локальный `.env`; остановка из-за недоступного Docker Engine |
| Backend `npm run dev:api` | Собирает и исполняет API; остановка при подключении к PostgreSQL из-за запрета TCP в sandbox |
| Production browser check | Блокируется `listen EPERM` до запуска браузерного сценария |

DB integration tests без test DSN пропускаются; прохождение соответствующих
Go-пакетов не подтверждает применение всех 263 SQL-миграций на новой базе.
Попытка запустить отдельный PostgreSQL в `/private/tmp` также заблокирована
ограничением shared memory. При первой попытке запуска Docker Desktop macOS
сообщала `kLSNoExecutableErr`. Позже Docker заработал в пользовательском терминале,
но доступ из sandbox остался запрещён. Пользователь подтвердил отправку приглашения,
регистрацию нового аккаунта и назначение директора после настройки SMTP и Keycloak.
Это ручная проверка основного пути; автоматические DB/browser integration tests
по-прежнему требуют окружения с доступом к Docker и локальным портам.

## Следующие действия

1. Выполнить автоматическую проверку свежего bootstrap в окружении с доступом к Docker.
2. Проверить `/health`, `/ready`, профиль, чат/WebSocket, загрузку файла
   и приглашение сотрудника от имени директора.
3. Закрыть публичное удаление файлов и недоверенное создание календарных событий;
   перенести EPS credentials на сервер и обработать фиксированный admin seed.
4. Исправить Telegram fixtures и повторить полный Node suite.
5. Выполнить Go integration suite с отдельной тестовой БД и MinIO, затем проверить
   fresh migration и upgrade через единый migration runner.
