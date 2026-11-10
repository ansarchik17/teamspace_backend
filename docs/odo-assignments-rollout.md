# Задача 8: запуск и проверка поручений ЭДО ↔ Workspace

## Состояние проверки

Локальные тесты используют отдельную PostgreSQL и HTTP/gRPC-стенды. Они проверяют реальные транзакции и протоколы каждого сервиса, но не заменяют сквозную проверку трёх развёрнутых приложений с реальным Keycloak. SSH-доступ, реальные токены и production-сеть пока не проверены. Не считать задачу полностью принятой до выполнения серверной матрицы ниже.

## Повторяемая локальная проверка трёх репозиториев

В репозитории фронтенда:

```powershell
$env:TEST_MESSENGER_DATABASE_URL = '<DSN отдельной одноразовой PostgreSQL>'
$env:ODO_TEST_DATABASE_DISPOSABLE = 'true'
node scripts/test-odo-integration.cjs
```

Нужны Go и Node.js. Для нестандартного расположения задать `ODO_TEST_WORKSPACE_REPO` и `ODO_TEST_SOURCE_REPO` абсолютными путями. Runner проверяет приёмник, авторизацию, company isolation, immutable snapshots, worker/retry/receipt, команды выполнения, protobuf, миграции и правила UI. Любой skipped test или ошибка останавливают проверку; вывод содержит имена тестов, без DSN и токенов. Для подробностей запускать соответствующий `go test` локально. Это набор согласованных интеграционных проверок компонентов, а не браузерный E2E с реальным сервером.

## Подготовка запуска

1. Согласовать одну тестовую компанию A, двух активных сотрудников A1/A2 и сотрудника B1 отдельной компании B; использовать тестовые документы и поручения. У сотрудников должны быть реальные Keycloak sub и доступ к документам. Включить модуль `odo`; при управляемых правах выдать `assignment.read` и исполнителям `assignment.complete`.
2. Зафиксировать SHA/образы всех трёх релизов и текущие образы для отката. Push в `odo-integration` не запускает существующий production CI: оба backend workflow настроены на `main`. Не подставлять `:odo-integration`, пока такой образ действительно не собран. Использовать штатный согласованный релиз с закреплёнными образами; не переключать production на непроверенный тег.
3. Сохранить резервные копии БД и серверных env в защищённом хранилище. Не копировать секреты в Git, отчёты или чат. Не заменять существующий `.env` файлом `.env.example`.
4. Сначала отключить sender и новые команды: в ЭДО `ALEM_WORKSPACE_SYNC_ENABLED=false`, `ODO_WORKSPACE_COMMANDS_ENABLED=false`; в Workspace `ODO_COMMANDS_ENABLED=false`. Убедиться, что изменения env применены пересозданием контейнеров: обычный restart не перечитывает env_file.
5. Применить штатными migration runner миграции Workspace `20261014000300` и `20261014000400`; workflow ЭДО `000018`, `000019`, `000020` в правильной БД. Не отмечать версии вручную. Установить совместимые BFF и workflow-service вместе; проверить журналы миграций и readiness. Фронт публиковать после готовности API.

## Согласование конфигурации

| Workspace `/opt/workspace/.env` | ЭДО `/opt/odo/.env` / Keycloak |
|---|---|
| `KEYCLOAK_ISSUER` | Точное совпадение с `ALEM_WORKSPACE_SYNC_IDENTITY_ISSUER` и `iss` сервисного токена |
| `ODO_ASSIGNMENTS_CLIENT_ID` | `ALEM_WORKSPACE_CLIENT_ID`; выделенный confidential client, service accounts enabled |
| `ODO_ASSIGNMENTS_SERVICE_SUB` | Реальный sub service account, не client ID и не сотрудник |
| `ODO_ASSIGNMENTS_AUDIENCE` | Audience mapper сервисного клиента добавляет это значение в `aud` |
| `ODO_ASSIGNMENTS_COMPANIES=<UUID>:<technical user ID>` | `ALEM_WORKSPACE_SYNC_ALLOWED_ORGANIZATIONS=<UUID>`; разрешённые компании должны быть согласованы |
| `ODO_ASSIGNMENTS_SOURCE_ORIGIN=https://teamspace.kz` | `DOCUMENTOLOG_PUBLIC_URL=https://teamspace.kz/odo`; слева только origin, справа возможен путь |
| Приёмник доступен по API origin | `ALEM_WORKSPACE_API_URL` без `/api/v1`; проверить DNS/маршрут из workflow-service |
| Доверенный issuer/JWKS | `ALEM_WORKSPACE_KEYCLOAK_TOKEN_URL`, `ALEM_WORKSPACE_CLIENT_SECRET` только на ЭДО |
| `ODO_COMMANDS_BASE_URL` — доверенный BFF без `/api/v1` | Для общей Docker-сети возможен `http://odo-bff:8080`; между серверами нужен доступный адрес. Не использовать Docker hostname через SSH на другой машине |
| Действующие `JWT_SECRET/JWT_ISSUER/JWT_AUDIENCE` | Совпадающие `WORKSPACE_JWT_SECRET/WORKSPACE_JWT_ISSUER/WORKSPACE_JWT_AUDIENCE`; `WORKSPACE_API_BASE_URL` доступен auth-service |

Проверить Keycloak user-token trust отдельно от service-account trust: сервисный токен не выполняет поручения вместо сотрудника. Секрет handoff не является секретом доставки поручений.

В ЭДО: `ALEM_WORKSPACE_USER_SYNC_ENABLED=false`, `ALEM_WORKSPACE_ASSIGNMENTS_TIMEZONE=Asia/Almaty` либо согласованные company overrides; `ALEM_WORKSPACE_SYNC_TIMEOUT=10s`, `ALEM_WORKSPACE_SYNC_POLL_INTERVAL=5s`, `ALEM_WORKSPACE_SYNC_MAX_ATTEMPTS=8`. Политика timezone должна соответствовать компании. В Workspace: `ODO_COMMANDS_TIMEOUT=10s`. Проверить часы серверов и TLS, не отключать проверку сертификатов.

Сначала включить `ODO_ASSIGNMENTS_ENABLED=true` на Workspace, затем `ALEM_WORKSPACE_SYNC_ENABLED=true` в workflow-service ЭДО. Убедиться в успешной доставке нового тестового поручения. Историю переносить отдельно по [плану backfill](odo-assignments-backfill.md); `ODO_ASSIGNMENTS_BACKFILL_ENABLED` оставить false вне согласованного переноса. После этого включить `ODO_WORKSPACE_COMMANDS_ENABLED=true` в ЭДО и `ODO_COMMANDS_ENABLED=true` в Workspace и опубликовать фронт задачи 7. Оба compose используют серверный env_file; отдельный контейнер sender не нужен — воркер работает в workflow-service.

## Серверная матрица приёмки

Проводить в согласованной тестовой компании. Для испытания сбоев использовать отдельный стенд/прокси, не останавливать общий production API.

| Сценарий | Проверяемый результат |
|---|---|
| Создать поручение документа A на A1 в ЭДО | После доставки GET Workspace A1 содержит одну карточку с тем же assignment/document/company, revision, сроком и приоритетом; A2 и B1 её не видят |
| Повторить тот же event ID и тело после потери ACK | Один UUID карточки, без повторного изменения; `duplicate` и durable receipt, очередь становится sent |
| Доставить старую revision после новой | `stale`, новая карточка и исполнитель не откатываются |
| Поменять исполнителя A1 → A2 в ЭДО | Revision растёт, у A1 карточка исчезает, у A2 появляется; A1 не может выполнить старую карточку |
| Выполнить из Workspace A2 с результатом | ЭДО сохраняет результат/время/receipt, ответ confirmed; UI ждёт проекцию; после outbox status completed и новая revision |
| Повтор команды после timeout или reload | Те же command ID, revision и результат; возвращается тот же receipt, второй результат и event не создаются |
| Отмена отдельного активного поручения в ЭДО | В Workspace source_status cancelled; выполнить и подсветить просроченным нельзя |
| Компания B и чужие идентификаторы | Нет чтения/выполнения/сервисной доставки вне allowlist. Admin A не может повторять события B и не получает её счётчики |
| Отключить assignment.complete / модуль odo | Сервер отказывает; UI не заменяет проверку сервера |
| Ошибка 503, затем восстановление | Pending с ограниченными попытками и задержкой, неизменные event ID/тело; после восстановления доставка завершается |
| Исчерпание попыток / 409 | Failed доступен диагностике, не помечен sent. После устранения причины возможен административный retry того же события |

Для каждого сценария записать дату, SHA трёх релизов, компанию, assignment ID, event ID, revision, command ID (если есть), ожидаемый/фактический результат. Не записывать Bearer tokens, result_comment или персональные данные. Обычный период polling — 5 секунд; при нормальной очереди проверять появление за 30 секунд, при backlog ориентироваться на next_retry_at и состояние очереди.

## Диагностика и повтор

В ЭДО существуют защищённые маршруты с правами `admin.users` или `admin.roles`:

- `GET /api/v1/integrations/alem-workspace/sync-status` — счётчики очереди текущей компании.
- `GET /api/v1/integrations/alem-workspace/metrics` — Prometheus counters по состояниям текущей компании.
- `POST /api/v1/integrations/alem-workspace/assignments/{source assignment ID}/retry` — только failed-события текущей компании; `retried=false` означает отсутствие подходящего события, а не успешную доставку. Воркера нужно оставить включённым; проверить последующее sent.

Считать вызов retry успешно принятым только после HTTP 200, а доставку — после durable receipt и sent. Диагностика требует проверенной компании из авторизации; отсутствие компании не раскрывает глобальную очередь. Legacy-события без компании не доступны company-admin: разбирать их операторским backfill, не подставлять компанию SQL-запросом.

Read-only SQL для workflow-БД, с параметрами доверенной компании и поручения:

```sql
SELECT id, event_id, assignment_id, revision, status, attempts,
       next_retry_at, updated_at, last_error
FROM documentolog_assignment_outbox
WHERE organization_id = $1::uuid AND assignment_id = $2
ORDER BY created_at, id;
```

Не выгружать payload целиком в общие логи. По event ID найти безопасную ошибку в журналах workflow-service и соответствующий запрос Workspace. 401 — issuer/aud/azp/sub/exp или доверие пользовательскому JWT; 403 — allowlist/модуль/права/активность/компания; 409 — конфликт данных, не лечится слепым повтором; 422 — снимок не соответствует контракту; сеть/503 — доступность и таймауты. После исправления причины повторять сохранённое событие без изменения event ID, payload и revision. `held_backfill`/`superseded` не переводить вручную в pending.

## Остановка и откат

При проблеме сначала выключить команды на Workspace, затем на ЭДО; при сбое репликации выключить sender. Сохранить очередь и receipt, не удалять failed и не обнулять revision. Вернуть согласованные предыдущие образы штатным deploy rollback. Аддитивные миграции автоматически не откатывать: новые durable записи нужны для расследования и безопасного повтора. После восстановления повторить матрицу на тестовых данных и только затем расширять allowlist.
