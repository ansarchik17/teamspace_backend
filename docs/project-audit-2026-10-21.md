# Аудит backend Workspace

Дата: 21 октября 2026 года. Репозиторий: `axis_workspace/workspace-backend`, ветка `ansar-branch`, исходный commit `67e0407`.

Backend объединяет корпоративный мессенджер, файлы, документы, задачи, календарь, контакты и AI. Проект собирается и проходит доступные обычные тесты, однако содержит критическую ошибку связывания аккаунтов и несколько независимых обходов авторизации. До их устранения считать его безопасным для открытого корпоративного использования нельзя.

Область аудита соответствует сохранённой договорённости: backend. Фронтенд, `odo`, `senim_desk` и резервные копии не входят в выводы. Проверены код, маршруты, схема и миграции, тесты, настройки, CI/CD и deployment. Сценарии доступа ниже подтверждены трассировкой кода; атаки на работающий сервис не выполнялись. Реальная конфигурация production, Keycloak, MinIO и DocumentServer может влиять на доступность отдельных сценариев.

## Архитектура и объём

Приложение — модульный монолит на Go с Gin. Основной поток: router → middleware → handler → service → repository → PostgreSQL или внешний сервис. `cmd/api/main.go` вручную создаёт зависимости и запускает HTTP, WebSocket hub и фоновые обработчики. Отдельные команды обслуживают миграции и слияние пользователей. AI worker запускается тем же бинарным файлом в отдельном режиме.

| Состав | Состояние исходников |
| --- | --- |
| Язык | `go 1.24.4`, `toolchain go1.24.7` |
| Код | 432 Go-файла приложения и команд, около 132 тысяч строк без сгенерированного `docs/docs.go` |
| Тесты | 177 Go-файлов, около 26 тысяч строк |
| API | В Swagger 381 путь и 461 операция; это счёт документации, а не проверенное равенство router и Swagger |
| Данные | 198 файлов миграций с расширением `.sql`; дополнительно есть дубликат с невидимым символом после расширения |
| Слои | 90 handler-файлов, 107 service-файлов, 90 repository-файлов, 67 domain-файлов; auth и agent вынесены отдельно |

| Модуль | Назначение и особенности |
| --- | --- |
| Auth и организации | Keycloak, локальные JWT, SSO, PIN, устройства, tenant host, роли, организационная структура |
| Messenger | Диалоги, группы, каналы, статусы доставки, поиск, вложения, E2EE и синхронизация через WebSocket |
| Drive и Docs | Папки, ACL, ссылки, корзина, версии, квоты, поиск; ONLYOFFICE, шаблоны, импорт, экспорт и восстановление |
| Kanban | Доски, колонки, задачи и подзадачи, роли, проекты, спринты, история, дедлайны, whiteboard |
| Calendar и звонки | События, участники, RSVP, напоминания, Google и Apple Calendar, LiveKit и записи |
| Contacts | Справочник, личные контакты и privacy, импорт CSV/XLSX, LDAP/AD, передача ресурсов при увольнении |
| AI и контент | Чаты, документы и извлечение текста, фоновые jobs, инструменты агента, заметки, публикации |

PostgreSQL хранит основные данные и часть очередей. MinIO хранит байты. Redis используется для кэша и токенов и допускает деградацию при недоступности. Keycloak обслуживает идентификацию, ONLYOFFICE — редактирование и конвертацию, LiveKit — медиасессии. Дополнительные интеграции: RabbitMQ, SMTP, push, LDAP/AD и внешние AI API.

## Критические проблемы доступа

Приоритет P0 означает риск захвата аккаунта. P1 — серьёзный обход доступа, утечку или удаление данных. P2 — ограниченный риск, потерю состояния или эксплуатационный дефект. Приоритет отражает последствия в коде; доступность конкретного маршрута извне зависит от deployment.

### SEC01 P0 Перепривязка аккаунта через неподтверждённый ИИН

Публичный `POST /api/v1/auth/register` принимает ИИН из запроса и создаёт новый Keycloak account с выбранными email и паролем. Затем `UpsertFromKeycloak` находит существующего локального пользователя по ИИН и заменяет его `keycloak_id`, даже если прежний subject уже заполнен. Проверяется только активность пользователя. Следующий допустимый JWT нового аккаунта получает прежний локальный `users.id` и доступ к принадлежащим ему ресурсам.

Цепочка: [публичный маршрут](../internal/server/router.go#L256), [создание и upsert](../internal/auth/service_keycloak.go#L348), [поиск по ИИН](../internal/repository/user_repository.go#L737), [замена subject](../internal/repository/user_repository.go#L639), [связывание](../internal/repository/user_repository.go#L673), [ранняя ветка upsert](../internal/repository/user_repository.go#L1472), [middleware](../internal/middleware/keycloak_auth.go#L118).

Условия: активная жертва, известный ИИН, успешные административные операции Keycloak и допустимый JWT регистрационного клиента. Внешняя кастомная проверка уникальности ИИН могла бы помешать созданию аккаунта; в backend её нет. Локальная привязка изменяется до auto-login. `KEYCLOAK_AUTO_CREATE_USERS=false` не защищает найденные совпадения. Захват прежней platform admin-роли и расшифровка старого E2EE из этого автоматически не следуют.

Исправление: отделить регистрацию от связывания идентичностей; не считать введённый ИИН доказательством личности; запретить замену уже связанного subject без отдельного подтверждённого процесса. Проверить также вход и canonical-user resolution, использующие тот же механизм сопоставления.

### SEC02 P1 Самостоятельное вступление в чужую организацию

Та же публичная регистрация принимает `organization_id` и `team_id` и сохраняет их без приглашения или проверки права вступления. Знание UUID компании позволяет создать в ней активного сотрудника при `KEYCLOAK_AUTO_CREATE_USERS=true` — это default. При `false` новая несовпавшая запись не создаётся, но SEC01 остаётся возможен. Проверки согласованности отдела с компанией не заменяют авторизацию вступления. См. [request](../internal/auth/service_keycloak.go#L236), [сохранение полей](../internal/auth/service_keycloak.go#L391), [repository](../internal/repository/user_repository.go#L132).

Назначать компанию и подразделение через проверенное приглашение, администратора или доверенный provisioning.

### SEC03 P1 Анонимное удаление объектов хранилища

`DELETE /api/v1/files/public?object_name=...` передаёт произвольный ключ в `MinIO.RemoveObject`. Нет аутентификации, проверки владельца, public-префикса или ACL. При известном ключе можно физически удалить чужой объект, обходя Drive, корзину и согласование. Перечисление bucket этим маршрутом не предоставляется.

Доказательства: [router](../internal/server/router.go#L188), [handler](../internal/handler/file_handler.go#L284), [удаление](../internal/service/file_service.go#L303). Удаление должно разрешаться через соответствующий ресурс и его права; публичный доступ к объекту не даёт права его удалять.

### SEC04 P1 Выдача ссылки на произвольный файл без ACL

Защищённый `GET /api/v1/files/url?object_name=...` подписывает любой переданный ключ, не используя ID запросившего пользователя. Публичный download доверяет корректной подписи и отдаёт байты. Это обходит ACL Drive, отзыв доступа и его аудит скачиваний; требуется знать ключ объекта.

Доказательства: [handler](../internal/handler/file_handler.go#L102), [подпись](../internal/service/file_service.go#L212), [download](../internal/handler/file_handler.go#L139). Перед выдачей ссылки нужна авторизация на конкретный ресурс, включая версии и состояние удаления.

### SEC05 P1 Публичная привилегированная синхронизация Keycloak

Анонимный `POST /auth/sync-keycloak` запускает синхронизацию пользователей через KC admin. Для отсутствующих аккаунтов и части conflict-recovery пути задаётся единый зашитый постоянный пароль. Корректно разрешённые существующие связи пропускаются: это не универсальный сброс любого аккаунта.

Доказательства: [маршрут](../internal/server/router.go#L282), [handler](../internal/handler/auth_handler.go#L2878), [сервис](../internal/auth/service_keycloak.go#L2736). Закрыть публичный запуск, исключить общий пароль, выделить контролируемую административную процедуру. Значение пароля в отчёт не включено.

### SEC06 P1 Открытые почтовые журналы и маршрутизация

Без входа доступны `/users/notifications` с содержимым email logs и `/email/mappings` со списком, созданием и удалением перенаправлений. Изменение mapping позволяет перенаправлять последующие сообщения. `/users/notification` принимает неподтверждённые входящие сообщения и может запускать email и push.

Доказательства: [маршруты](../internal/server/router.go#L233), [handlers](../internal/handler/email_log_handler.go#L63), [перенаправление](../internal/service/email_log_service.go#L54). В штатном `main` handler создаётся. Приём входящих сообщений требует отдельной серверной аутентификации; управление и просмотр — роли и tenant scope.

### SEC07 P1 Межорганизационная утечка профилей

`GET /users/:id/details` проверяет существование requester, но не ограничивает target его компанией. Возвращаются ИИН, email, Keycloak ID, организация, подразделение, руководитель и другие данные профиля. Глобальные online/never-logged-in списки также требуют проверки области доступа. Новый scoped Contacts API не закрывает старые маршруты.

Доказательства: [handler](../internal/handler/auth_handler.go#L2781), [service](../internal/auth/service_keycloak.go#L2181), [SQL](../internal/repository/user_repository.go#L275). Для всех старых API пользователей нужен единый policy и минимальный response DTO.

### SEC08 P1 Подключение к WebSocket с чужого сайта

Оба WebSocket upgrader принимают любой `Origin`. Auth middleware принимает cookie, а HTTPS auth cookie получает `SameSite=None`. При отправке браузером cross-site cookies сторонняя страница может открыть соединение от имени жертвы и получать её сообщения или данные доски. CORS и проверка tenant по Host этого не закрывают.

Доказательства: [messenger upgrader](../internal/handler/websocket_handler.go#L16), [whiteboard upgrader](../internal/handler/whiteboard_handler.go#L18), [cookie auth](../internal/middleware/keycloak_auth.go#L331), [SameSite](../internal/handler/auth_cookies.go#L113). Проверять Origin по разрешённым адресам перед upgrade. Блокировка сторонних cookies в браузере может препятствовать этому сценарию.

### SEC09 P1 Доступ к исходным сообщениям через legacy пересылку

`POST /messages/forward` с `message_ids` и `target_chat_ids` использует старый путь `ForwardMessages`: исходные сообщения выбираются по UUID, а членство проверяется только в целевых чатах. Зная UUID чужого сообщения, пользователь может копировать его в свой чат. Раскрываются plaintext, метаданные и ссылки вложений; E2EE ciphertext сам по себе не становится расшифрованным.

Доказательства: [выбор пути](../internal/handler/chat_handler.go#L659), [сервис](../internal/service/chat_service.go#L278), [SQL](../internal/repository/message_repository.go#L232). Новый путь `ForwardPreparedMessages` уже имеет исходную проверку; её нужно распространить на legacy и выполнять для каждого оригинала.

### SEC10 P1 Календарь раскрывает ссылки на закрытые встречи

`GET /calendar/users/:user_id/events` позволяет читать занятость любого существующего пользователя, в том числе другой компании. Маскирование скрывает title, description, location и attendees, но оставляет `meeting_url`, `livekit_room_name`, `cisco_join_url`, `url`.

Доказательства: [list](../internal/service/calendar_service.go#L1223), [маскирование](../internal/service/calendar_service.go#L1262), [SQL](../internal/repository/calendar_repository.go#L336), [DTO](../internal/domain/calendar.go#L23). Для чужой занятости нужен отдельный ответ с минимальными полями и проверкой разрешённой области пользователей.

### SEC11 P1 Обход доступа к календарной медиакомнате

`POST /calls/join` игнорирует отсутствие `room_name` в `call_history` и выдаёт LiveKit token с publish/subscribe. Календарные комнаты живут отдельно в events, поэтому их штатный calendar ACL этим путём обходится. SEC10 предоставляет один из источников имени комнаты. Аналогичный fallback в rejoin тоже требует исправления.

Доказательства: [join](../internal/handler/livekit_handler.go#L2300), [grant](../internal/service/livekit_service.go#L162), [создание calendar room](../internal/service/calendar_meeting_provider_service.go#L226). Неизвестная комната должна отклоняться либо разрешаться через авторизацию соответствующего типа ресурса.

### SEC12 P1 Перебор PIN без ограничения попыток

Публичный PIN login находится вне группы rate limiter. Неверный четырёхзначный PIN сразу возвращает ошибку без счётчика попыток, cooldown или блокировки credential. Нужны identifier, device ID и активная PIN-сессия. Bcrypt и mutex замедляют перебор, но не ограничивают число попыток.

Доказательства: [публичный маршрут](../internal/server/router.go#L274), [limiter только protected](../internal/server/router.go#L314), [проверка PIN](../internal/auth/quick_pin_service.go#L218). Ограничивать попытки по credential, устройству, пользователю и IP; проверить также остальные публичные auth API.

### SEC13 P1 Условный SSRF через ONLYOFFICE callback

При пустом `ONLYOFFICE_JWT_SECRET` подпись тела callback от DocumentServer не требуется; backend capability в query остаётся обязательной. Пользователь с собственным редактируемым документом получает capability и document key в browser config и может передать внутренний HTTP URL. Backend скачивает его обычным клиентом с редиректами, а ответ может сохраниться как доступная пользователю версия файла.

Доказательства: [проверка callback](../internal/handler/onlyoffice_callback_auth.go#L15), [выдача capability](../internal/service/onlyoffice_helpers.go#L206), [callbackUrl в config](../internal/service/onlyoffice_helpers.go#L430), [HTML config](../internal/handler/onlyoffice_handler.go#L282), [HTTP download](../internal/service/drive_service.go#L2020). Секрет DocumentServer должен быть обязательным для режима записи; источник результата — ограничен доверенным сервером, с контролем redirect и адресов назначения. При включённой корректной DS JWT-подписи обычный пользователь не может подписать такой callback.

## Другие проблемы безопасности

| Приоритет | Проблема | Код и необходимое изменение |
| --- | --- | --- |
| P1 | Анонимная подмена аватара любого пользователя с возвратом полного профиля, включая ИИН | [публичный handler](../internal/handler/auth_handler.go#L1686). Требовать вход и проверять владельца; возвращать ограниченный DTO |
| P2 | `/admin/email/test` разрешает отправку, если `role` отсутствует; локальный JWT эту роль в Gin context не устанавливает | [role check](../internal/handler/email_log_handler.go#L151), [local claims](../internal/middleware/keycloak_auth.go#L217). Отсутствие роли должно означать отказ |
| P2 | Whiteboard socket после отзыва членства продолжает получать broadcast | [room broadcast](../internal/whiteboard/room.go#L226), [write](../internal/whiteboard/client.go#L94). Закрывать сессии при отзыве доступа и контролировать outbound |
| P2 | HTTP logger пишет весь query, включая OAuth codes и подписанные capabilities | [logger](../internal/middleware/common.go#L65). Редактировать секретные параметры перед логированием |
| P2 | Анонимная загрузка файлов расходует storage без пользовательской квоты; multipart разбирается до проверки размера | [upload](../internal/handler/file_handler.go#L218), [router memory threshold](../internal/server/router.go#L95). Ограничивать HTTP body до parsing, частоту и бюджет публичных загрузок |

## Целостность данных и конкурентные операции

| Приоритет | Дефект и условия | Доказательство и исправление |
| --- | --- | --- |
| P1 | Удалённый участник встречи сохраняет reminders и получает актуальное название в push; при включённом SMTP email также раскрывает описание и локацию | [удаление участников](../internal/repository/calendar_repository.go#L139), [выбор reminders](../internal/repository/calendar_repository_reminders.go#L105), [отправка](../internal/service/calendar_service.go#L1817). Удалять reminders и повторно проверять membership перед отправкой |
| P2 | Любое обновление `attendee_ids` удаляет прежние RSVP, comments и время ответа, даже при неизменном списке | [DELETE и INSERT](../internal/repository/calendar_repository.go#L139). Сохранять существующих, удалять выбывших и добавлять только новых |
| P2 | Два одновременных calendar patch разных полей могут затереть изменения друг друга | [read и patch](../internal/service/calendar_service.go#L639), [полный UPDATE](../internal/repository/calendar_repository.go#L114). Использовать version predicate или атомарные обновления полей |
| P2 | Обычный upload может завершиться после удаления папки или отзыва её ACL | [проверка до I/O](../internal/service/drive_service.go#L1019), [upload](../internal/service/drive_service.go#L1149), [INSERT](../internal/repository/drive_repository.go#L490), [soft delete](../internal/repository/drive_repository.go#L1497). Перепроверять папку и ACL при записи под общим lock; удалять объект при конфликте |
| P2 | Два изменения подзадач A→B и B→A могут создать цикл | [ancestry validation](../internal/service/kanban_service.go#L1900), [запись parent](../internal/repository/TaskRepository.go#L813). Проверку и запись сериализовать; существующие циклы обнаруживать bounded traversal |
| P2 | Удаление Drive по заявке и изменение статуса заявки неатомарны | [delete и update status](../internal/service/drive_service.go#L1456), [status SQL](../internal/repository/drive_delete_request_repository.go#L83). Локальное изменение ресурса и CAS статуса выполнять в одной транзакции |
| P2 | Откат одной исторической миграции содержит синтаксическую ошибку `DROP TABLE IF NOT EXISTS` | [Down](../db/migrations/20260205124521_add_notebook_chat_documents.sql#L9). Для удаления корректно `DROP TABLE IF EXISTS`; проверить нужный диапазон отката на disposable PostgreSQL |

Гонка upload воспроизводится порядком операций: проверка папки → медленный streaming → commit удаления дерева → INSERT метаданных. FK допускает существующую soft-deleted папку. Получается активный файл под удалённой папкой, расходующий квоту и отсутствующий в обычной навигации и корзине. Это не утверждение о потере байтов: нарушается состояние метаданных.

Гонка Kanban допускает цикл, если оба запроса A→B и B→A проходят ancestry validation до первого commit. Последующие repository updates могут успешно выполниться последовательно: блокировка только изменяемой задачи не защищает проверку всей цепочки.

Offboarding выполняет передачу атомарно, но берёт глобальный folder advisory lock и сильные блокировки восьми общих таблиц ([repository](../internal/repository/contact_offboarding_repository.go#L59)). Передача ресурсов сотрудника с большим объёмом данных может задерживать записи Drive/Kanban других компаний. Нужны измерение времени и contention на представительном объёме, а затем более узкая сериализация.

## Сборка и эксплуатация

| Приоритет | Проблема | Доказательство и действие |
| --- | --- | --- |
| P1 | GitLab test job компилирует, но не исполняет тесты из-за `-run '^$'` | [.gitlab-ci.yml](../.gitlab-ci.yml#L98). Запускать реальные тесты и требовать их успеха |
| P1 | GitLab собирает `messenger:latest`, а основной Compose использует `workspace:latest` | [build](../.gitlab-ci.yml#L143), [deploy](../.gitlab-ci.yml#L236), [Compose](../docker-compose.yml#L3). Разворачивать проверенный immutable image |
| P1 | GitHub CI сейчас блокируется на `go vet` | [workflow](../.github/workflows/ci.yml#L62), [копирование sync.Map](../internal/service/office_recovery_integration_test.go#L96). Создать новый service в тесте без копирования lock-содержащей структуры |
| P1 | `RUN_DB_MIGRATIONS=false` не отключает миграции в API | [entrypoint](../docker/entrypoint.sh#L30), [безусловный goose.Up](../cmd/api/main.go#L104). Оставить один контролируемый механизм migrations |
| P2 | Healthcheck AI worker требует HTTP, который worker-only процесс не запускает; deploy проверяет только API | [Dockerfile](../Dockerfile#L57), [worker mode](../cmd/api/main.go#L506), [deploy check](../deploy/production/scripts/deploy.sh#L69). Нужен отдельный worker probe и проверка обоих процессов |
| P2 | `/ready` всегда 200, поэтому не отражает отказ PostgreSQL и функциональную готовность | [handlers](../internal/server/router.go#L1106). Разделить liveness и readiness с bounded dependency checks |
| P2 | PostgreSQL DSN в pool и API migrator собирается без экранирования credentials | [pool](../internal/infrastructure/database/postgres.go#L15), [API](../cmd/api/main.go#L105). Использовать безопасный URL/config builder как в отдельном migrator |
| P2 | Worker не получает часть CA и файловых секретов API | [API volumes](../deploy/production/compose.prod.yml#L31), [worker](../deploy/production/compose.prod.yml#L46). Согласовать необходимые mounts для выбранного способа интеграции |
| P2 | Выбор image и deployment используют разные locks, rollback заменяет общий images.env | [set-image](../deploy/production/scripts/set-image.sh#L7), [deploy](../deploy/production/scripts/deploy.sh#L32). Использовать общую сериализацию и откатывать только свой сервис |
| P2 | `make up` и `make stop` ссылаются на отсутствующие `messenger-*` сервисы | [Makefile](../Makefile#L61). Согласовать названия с Compose |
| P2 | Dev Keycloak не имеет постоянного хранилища realm/users | [Compose](../docker-compose.yml#L144). Добавить долговечную БД или явный воспроизводимый import для локальной среды |

GitHub workflow запускает реальные PostgreSQL-тесты, vet и сборку и публикует image по SHA, но настроен на push/PR только для `main`. Вывод о GitLab не относится автоматически ко всей CI. Обязательность этих проверок в remote repository из локальных файлов не установлена.

В deployment есть persistent volumes, localhost binding внутренних интерфейсов, ротация большинства логов и непривилегированный процесс приложения. При этом код автоматических backup/retention и проверенного восстановления PostgreSQL, Keycloak и MinIO, а также RPO/RTO в репозитории не обнаружен. Это требует проверки инфраструктуры, а не доказывает отсутствие внешних backup. Rollback меняет image без отката схемы; нужна проверенная совместимость версий.

Production Compose не содержит готового ONLYOFFICE, поэтому Docs требуют отдельно подготовленный DocumentServer. Grafana provisioning есть, полного scrape/alerts стека в репозитории нет. Большинство базовых образов закреплены изменяемыми тегами, CPU/RAM limits в production Compose не заданы. При масштабировании следует также учитывать in-process WebSocket hub, whiteboard rooms и fallback-состояние без Redis.

## Зависимости

Проверка `govulncheck` из `golang.org/x/vuln@v1.9.0` выполнена для Go 1.24.7, Linux/amd64 — конфигурации сборки Docker. База обновлена 21 октября 2026 года. Найдены 136 различных advisories на уровне зависимостей, 107 затрагивают импортированные пакеты, 77 имеют потенциально достижимые символы; эти множества пересекаются, их нельзя складывать. Из 77 advisories 51 относится к стандартной библиотеке, часть также затрагивает `x/net`.

Сводка с ID, версиями исправлений и примерами путей вызова сохранена в [JSON](project-audit-2026-10-21-vulnerabilities.json). Это анализ исходников для целевой конфигурации, а не проверка образа, запущенного в production. Статический call graph показывает потенциальную достижимость уязвимых символов, но сам по себе не доказывает наличие управляемого атакующим ввода во всех сценариях. [Документация govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) также описывает зависимость результата от build configuration.

Для Excelize есть путь от обработки XLSX к `GetRows` в [content extraction](../internal/service/content_extraction_service.go#L311). Это приоритетная область для обновления и ограничения ресурсов обработки файлов. Для pgx анализатор отмечает GO-2026-5004, но advisory требует non-default simple protocol и особой формы dollar-quoted SQL: считать весь PostgreSQL API SQL-injectable на этом основании нельзя. [Описание GO-2026-5004](https://pkg.go.dev/vuln/GO-2026-5004).

Go 1.24 уже вне поддерживаемых веток на дату аудита: политика Go поддерживает релиз до появления двух следующих major-версий. [Политика и история выпусков Go](https://go.dev/doc/devel/release#policy). Обновление runtime, библиотек и Docker/CI должно быть согласованным и проходить регрессию; автоматическое массовое изменение версий в рамках анализа не выполнялось.

## Проверки и границы результата

| Проверка | Результат |
| --- | --- |
| `go test -json ./...` | Exit 0; 602 top-level теста прошли, 41 пропущен; с subtests — 843 pass и 43 skip, fail нет |
| `go vet ./...` | Exit 1: `office_recovery_integration_test.go:96`, копирование `DriveService` с `sync.Map` |
| `gofmt -l cmd internal` | Нет замечаний |
| `go build ./cmd/api` с выводом в temp | Успешно |
| `govulncheck -json ./...`, Go 1.24.7, Linux/amd64 | 77 различных advisories на потенциально достижимых символах; подробности в JSON. Exit 0 у JSON-режима не означает отсутствие уязвимостей |
| 6 CI/Compose YAML, bash/sh scripts | Синтаксис прошёл проверку |
| SQL migration numeric IDs | Повторяющихся версий среди `.sql` не найдено |
| PostgreSQL/MinIO integration | В текущем запуске пропущены без disposable test env; Docker daemon недоступен |
| Fresh install и upgrade всех migrations | Динамически не проверены; избранные fixture migrations не заменяют полный migration smoke test |
| Настоящие Keycloak, ONLYOFFICE, LDAP/AD, MinIO, браузеры | Приёмка в рамках этого аудита не выполнена |
| Production load, p95, resource profile и restore drill | Не измерены; оценки ёмкости и SLA из кода не следуют |

Подробные stdout тестов и сырые данные анализатора сохранены во временной папке локальной среды. Репозиторий получает отчёт и краткую сводку advisories без секретов. Исходный код приложения и deployment в рамках аудита не изменялись.

## Сильные стороны и технический долг

Новые Drive/Docs/Contacts содержат содержательные проверки: версии с `expected_version`, транзакционную историю, callback receipts и ротацию session keys, квоты с DB guards, сериализацию дерева папок и оргструктуры, атомарный импорт, directory revisions, offboarding barrier и durable identity outbox. Keycloak verifier проверяет алгоритм, issuer, audience и AZP; internal-token middleware закрывается при пустой конфигурации. В export converter уже есть проверка origin и запрет redirect.

Основная архитектурная проблема — не единый механизм отказа, а неодинаковые правила в параллельных API. Старые файловые и пользовательские endpoints обходят защиты новых Drive и Contacts. Расширение безопасного API не устраняет небезопасный legacy-путь.

Большие файлы усложняют контроль этих различий: LiveKit handler — около 4,5 тысячи строк, Drive service — 3,1 тысячи, Keycloak service — 3 тысячи. В первую очередь стоит выделять policy и сценарии использования; дробление файлов без общих проверок проблему доступа не решит. В миграциях и исходниках RLS policies не обнаружены; настройки действующей БД не проверялись. Реализованная tenant isolation зависит от service и SQL, поэтому нужна проверка каждого пути, включая фоновые задачи и старые aliases.

RabbitMQ publisher не имеет reconnect/confirm/outbox, а при ошибке инициализации приложение продолжает работу с noop publisher ([main](../cmd/api/main.go#L76), [publisher](../internal/events/rabbitmq_publisher.go#L63)). Для интеграций, которым нужна гарантированная доставка, это не даёт той же надёжности, что outbox offboarding; требуемую гарантию следует зафиксировать отдельно.

## Порядок исправлений

1. Закрыть SEC01 и повторное неподтверждённое связывание идентичностей. Одновременно убрать самостоятельное назначение tenant и публичный KC sync. Добавить regression tests на чужой ИИН, уже занятый subject, запрет auto-create и существующие роли/ресурсы.
2. Устранить публичное удаление, произвольную подпись файлов, открытые email API и avatar API. Проверить все обходные пути к одним и тем же данным, aliases и прежние клиентские контракты.
3. Закрыть legacy forwarding, утечки calendar/profile, fallback LiveKit join, Origin и перебор PIN. Сформировать матрицу actor/resource/action для собственника, участника, постороннего, другой компании, inactive и anonymous.
4. Исправить текущий vet failure, включить реальные GitLab-тесты и деплой точного image. Разделить миграции и старт, обеспечить readiness и worker health.
5. Обновить Go и затронутые зависимости, проверить XLSX на ограниченность CPU/памяти и timeout. Повторить vulnerability scan после обновлений на целевой Linux/amd64 конфигурации production.
6. Исправить Calendar state и гонки Drive/Kanban, добавить PostgreSQL regression scenarios с управляемым чередованием операций.
7. Выполнить полный migration smoke test, upgrade существующей схемы, restore drill и приёмку с настоящими Keycloak, MinIO, ONLYOFFICE и браузерами. Затем измерить нагрузку и определить пределы одного экземпляра и план масштабирования realtime.

Практический следующий шаг — отдельный набор исправлений SEC01–SEC06 с тестами доступа. План исправлений не означает, что перечисленные дефекты уже устранены.
