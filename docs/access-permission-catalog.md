# Каталог прав: сверка Workspace, ЭДО, SenimDesk и iForm

Задача 2, проверено 20.10.2026. Таблицы ниже сохраняют исходную сверку; в задачах 5–6 исправлены зависимости и серверные проверки. Актуальный [результат задачи 6](position-access.md#серверное-применение-прав-задача-6) имеет приоритет над пометками «задача 6» в исходных таблицах. Объектная область, участник этапа и состояние процесса проверяются дополнительно к праву. В реестре 49/68/11 кодов; сейчас к выдаче доступны 47/52/11, неподключённые действия отмечены в API и не выдаются.

Результат изменения Workspace: **ЭДО 49 прав, SenimDesk 68, iForm 11**. Добавлены только шесть существующих `archive.*` ЭДО. Права склада SenimDesk уже были в каталоге. Старые наборы должностей не расширяются автоматически. Коды без самостоятельной операции сохранены для совместимости сохранённых политик и явно отмечены ниже; их удаление либо подключение к операциям требует отдельного исправления потребителя.

## Проверенные исходники

Проверены исходники API и интерфейсов, а не работа production. Ссылки закреплены на версиях, использованных при сверке.

| Сервис | Версия | Основания проверки |
| --- | --- | --- |
| Workspace | Текущие изменения поверх backend `6c9aeaf`, front `559b2f5` | [Каталог и валидация](../internal/domain/access_catalog.go), [сопоставление iForm](../internal/domain/iform.go), [серверное управление](../internal/handler/organization_access.go); front: `OrganizationAccessPage.tsx`, `PositionAccessWorkspace.tsx`, `HttpAccessRepository.ts` |
| ЭДО backend | [`c5f9648`](https://github.com/west6ide/workspace-odo-backend/tree/c5f964818051731d96c1dca33e825bac5a4595c1) | [Реестр миграций auth](https://github.com/west6ide/workspace-odo-backend/tree/c5f964818051731d96c1dca33e825bac5a4595c1/internal/migrations/sql/auth), [BFF middleware](https://github.com/west6ide/workspace-odo-backend/blob/c5f964818051731d96c1dca33e825bac5a4595c1/internal/bff/middleware/middleware.go), [документы](https://github.com/west6ide/workspace-odo-backend/blob/c5f964818051731d96c1dca33e825bac5a4595c1/internal/document/service/service.go), [workflow](https://github.com/west6ide/workspace-odo-backend/blob/c5f964818051731d96c1dca33e825bac5a4595c1/internal/workflow/service/service.go) |
| ЭДО front | [`2404ee2`](https://github.com/west6ide/workspace-odo-front/tree/2404ee2ab87c8c07d8bfe82319c6472a7dad7b8e) | `src/permissions.ts`, `src/components/Sidebar.tsx`, `DocumentDetails.tsx`, `ArchivePage.tsx`, `ReportsPanel.tsx`; действия карточки берутся также из `available_actions` API |
| SenimDesk backend | [`1c5e267`](https://github.com/west6ide/workspace-sd-backend/tree/1c5e2676d894659e4bb69616bf430c7c4924a3d2) | [Реестр](https://github.com/west6ide/workspace-sd-backend/blob/1c5e2676d894659e4bb69616bf430c7c4924a3d2/internal/repository/postgres/rbac_repository.go), [маршруты](https://github.com/west6ide/workspace-sd-backend/blob/1c5e2676d894659e4bb69616bf430c7c4924a3d2/internal/delivery/http/router.go), [область](https://github.com/west6ide/workspace-sd-backend/blob/1c5e2676d894659e4bb69616bf430c7c4924a3d2/internal/delivery/http/access_scope_service.go), [клиентская компания](https://github.com/west6ide/workspace-sd-backend/blob/1c5e2676d894659e4bb69616bf430c7c4924a3d2/internal/domain/rbac_client_permissions.go) |
| SenimDesk front | [`cb48db3`](https://github.com/west6ide/workspace-sd-front/tree/cb48db3cafbdc6e81962b580ebaa2de61b37140e) | `src/shared/lib/permissions.ts`, `useCan`, `useCanInventory`, `RolesPage`, `InventoryPage`, `src/widgets/Sidebar/Sidebar.tsx` |
| iForm | [`63b86af`](https://github.com/west6ide/iform/tree/63b86afc366cc029e0c6fc29938bc71ac0c99cea) | Под `backend/src/main/java/kz/beles/iform/`: [реестр и синхронизация](https://github.com/west6ide/iform/blob/63b86afc366cc029e0c6fc29938bc71ac0c99cea/backend/src/main/java/kz/beles/iform/identity/WorkspaceDirectory.java), `identity/Access.java`, `api/ApiController.java`, `design/DesignService.java`, `runtime/RuntimeService.java`; интерфейс `frontend/src/main.tsx` |

## ЭДО: документы, вложения и справочник

Пути ниже относятся к `/api/v1`. «Чтение документа» означает одно из `document.read`, `document.read_department`, `document.read_all` в своей области либо применимую архивную ветку доступа. Право записи само по себе не отменяет проверку компании, автора, ACL, доступного перехода и блокировок.

| Код | Операция API / элемент интерфейса | Область, зависимости и найденные расхождения |
| --- | --- | --- |
| `document.create` | `POST /documents`; создание документа | Компания автора. Сейчас также разрешает автору изменение и вложения собственного черновика без отдельных `update`/`upload`; UI строже сервера |
| `document.read` | `GET /documents`, `/documents/{id}`; список и карточка | Свои и выданные по ACL документы компании |
| `document.read_department` | Те же GET; документы подразделения | Свои/доступные плюс подразделение, без других компаний |
| `document.read_all` | Те же GET; все документы компании | Расширяет чтение, не назначает согласующим/подписантом |
| `document.update` | `PATCH/PUT /documents/{id}`, план workflow; редактирование | Доступный авторский черновик/доработка; сейчас сервер допускает также `document.create` |
| `document.register` | `POST /documents/{id}/register`, `/claim-registration`; регистрация | Доступный документ, допустимое состояние и резервирование регистрации |
| `document.close` | `POST /documents/{id}/close`; завершение | Доступный документ и допустимый переход; сейчас также используется веткой возврата из архива |
| `document.archive` | `POST /documents/{id}/archive`; архивирование карточки | Альтернатива `archive.write`/`archive.manage` для документа, не управление всеми делами архива |
| `document.delete_draft` | Отдельный DELETE обычного документа не найден | Код зарегистрирован, фактическое удаление черновика не подключено; задача 6/7 |
| `document.view_history` | История и integrity карточки | Не является исключительным ограничителем: API/UI допускают обычное чтение документа; задача 6 |
| `document.send_to_approval` | `POST /documents/{id}/send-to-approval`; отправка | Чтение, корректный маршрут и допустимый переход; требуется document-service при переходе |
| `document.send_to_signing` | `POST /documents/{id}/send-to-signing`, `/signing/start`; отправка | Доступный документ, подписант, состояние. Само по себе не даёт подписать |
| `attachment.upload` | `POST /documents/{id}/attachments`, `PUT /attachments/{id}`; загрузка/замена | Редактируемый доступный документ. Сервер допускает автора собственного черновика с `document.create` |
| `attachment.download` | `GET /attachments/{id}/download`; скачивание | Доступ к документу; архивная выдача дополнительно требует успешной записи аудита |
| `attachment.delete` | Самостоятельный DELETE вложения не найден | Встречается в общей BFF-проверке маршрута вложений, не реализует отдельное удаление; задача 6/7 |
| `department.read` | Справочник подразделений; выбор участников | Только своя компания. BFF допускает справочник также по ряду рабочих прав; точный минимальный набор выбора участников проверить в задачах 5/8 |

## ЭДО: поручения, согласование, подпись, отчёты

| Код | Операция API / элемент интерфейса | Область, зависимости и найденные расхождения |
| --- | --- | --- |
| `assignment.create` | Создание поручения workflow; форма поручения | Документ, исполнитель и сроки. Чтение документа/справочника необходимо для полного UI-сценария |
| `assignment.read` | `GET /assignments`, `/assignments/{id}`; список поручений | Связь с поручением и компанией; Workspace-проекция отдельно допускает владельца/администратора, задача 9 |
| `assignment.update` | `PATCH /assignments/{id}`; изменение | Сейчас автор документа/поручения проходит и без права; наличие права также требует проверки границ чужого поручения, задача 6/7 |
| `assignment.complete` | `POST /assignments/{id}/complete`; исполнение | Сейчас назначенный исполнитель может пройти без отдельного права; задачи 6/7/9 |
| `assignment.close` | `POST /assignments/{id}/close`; приёмка | Сейчас автор документа/поручения проходит без отдельного права; задачи 6/7 |
| `assignment.cancel` | `POST /assignments/{id}/cancel`; отмена | То же расхождение авторства с точным набором; задачи 6/7 |
| `approval.read` | `GET /workflow/routes`; список согласований | Своя компания, доступный маршрут |
| `approval.start` | Создание маршрута / отправка на согласование | BFF и workflow считают альтернативой `document.send_to_approval`, но переход document-service требует именно последний код. Задача 5 требует этот код вместе с запуском; унификация guards остаётся задачей 6 |
| `approval.approve` | `POST /approval-steps/{id}/approve`; согласовать | Назначенный участник текущего этапа, актуальный маршрут |
| `approval.return_for_revision` | `POST /approval-steps/{id}/return`; вернуть | Назначенный участник текущего этапа, обязательный комментарий |
| `approval.reject` | `POST /approval-steps/{id}/reject`; отклонить | Назначенный участник текущего этапа, обязательный комментарий |
| `signature.read` | `GET /documents/{id}/signatures`; список подписей | Доступный документ; есть альтернативы `sign`/`verify` в BFF |
| `signature.sign` | Payload/complete маршрута `/documents/{id}/signing/...`; подписать | Назначенный подписант, документ и криптографическая проверка. Сейчас также открывает отказ от подписи |
| `signature.reject` | `POST /documents/{id}/signing/reject`; отказаться | Назначенный подписант; `signature.sign` также принимается, поэтому отдельный запрет отказа не обеспечен |
| `signature.verify` | `GET /signatures/{id}/verify`, integrity; проверить | Отдельная операция не везде исключительна: есть альтернативы чтения; задача 6 |
| `report.read` | `GET /reports/summary`, `/documents`, `/assignments`, `/execution`; отчёты | Сервер сейчас принимает это право и для `/export`, хотя UI требует `report.export`; задача 6 |
| `report.export` | `/reports/documents/export`, `/assignments/export`, `/execution/export`; экспорт | В пределах доступной выборки; BFF считает альтернативой `report.read` также при чтении |

Права чтения всех документов не заменяют назначение текущим согласующим/подписантом. Широкие OR-условия в поручениях нужно исправлять с проверкой обоих условий: рабочее право **и** допустимая связь с объектом.

## ЭДО: архив

Эти шесть кодов уже зарегистрированы в ЭДО (`000026_archive_permissions.sql`) и проверяются его BFF/document-service. В Workspace добавлена возможность явно выдавать их должности. Группа «Архив» и её новые тексты возвращаются с переводом `ru`/`kk`; формат API и коды сохранены.

| Код | Операция API / элемент интерфейса | Область и зависимости |
| --- | --- | --- |
| `archive.read` | `GET /archive/documents`, `/stats`, `/cases`; раздел архива | Доступные документы/дела подразделения. Архивная ветка ACL не открывает чужие черновики |
| `archive.write` | Создание/изменение дел; `/documents/{id}/archive`, `/unarchive`, `/archive-requisites`; кнопки изменения | Доступный объект, допустимое состояние; для навигации нужно чтение архива/документа |
| `archive.export` | `POST /archive/export`, экспорт дела; выгрузка | Доступные архивные документы; журнал выдачи обязателен. Для UI нужно чтение; обычный маршрут скачивания вложения имеет отдельную BFF-проверку |
| `archive.audit` | `GET /archive/audit-logs`, `/documents/{id}/archive-audit`; журнал | Доступные документы/дела, своя компания; для навигации нужно чтение |
| `archive.view_all` | Расширение выборок архива на всю компанию | Само по себе разрешает чтение, не изменение/экспорт/журнал и не другую компанию |
| `archive.manage` | Альтернатива read/write/export/audit в проверках BFF и UI | Не расширяет область до всей компании. Выгрузка вложения в document-service отдельно проверяет `attachment.download`/`archive.export`; в задаче 5 добавлена явная зависимость manage → export |

`document.read_all` и `archive.view_all` — разные ветки области. Архивные права применяются к архивно читаемым состояниям (включая подписанные/закрытые/завершённые), а не ко всем черновикам компании. Дела ограничиваются подразделением; `archive.manage` не заменяет `archive.view_all`.

## ЭДО: совместные проекты НПА

Все десять кодов зарегистрированы, но [политика проекта](https://github.com/west6ide/workspace-odo-backend/blob/c5f964818051731d96c1dca33e825bac5a4595c1/internal/projectapproval/policy.go) использует только первый как прямое рабочее разрешение. Остальные действия определяются локальными участниками/ролями проекта и их uppercase capabilities. Это не реализует ограничение должности Workspace. Раздел НПА в sidebar дополнительно зависит от чтения обычных документов.

| Код | Целевая операция / UI | Текущее решение API |
| --- | --- | --- |
| `npa.project.create` | `POST /npa/projects`; создать | Проверяется, но также допускается `document.create`; устранить неявную альтернативу в задаче 6 |
| `npa.project.read` | Список/карточка `/npa/projects` | Членство/автор проекта, `PROJECT_VIEW`; код должности не ограничивает действие |
| `npa.project.manage` | Правка проекта и участников | Роли проекта, `PROJECT_EDIT`/`PROJECT_MANAGE_MEMBERS`; код должности не ограничивает действие |
| `npa.document.edit` | Совместный редактор документа | `DOCUMENT_EDIT` локального проекта; код должности не ограничивает действие |
| `npa.comment.create` | Комментарий | `COMMENT_CREATE`; код должности не ограничивает действие |
| `npa.suggestion.create` | Предложение правки | `SUGGESTION_CREATE`; код должности не ограничивает действие |
| `npa.suggestion.resolve` | Решение по предложению | `SUGGESTION_RESOLVE`; код должности не ограничивает действие |
| `npa.version.manage` | Создание/восстановление версии | `VERSION_CREATE`/`VERSION_RESTORE`; код должности не ограничивает действие |
| `npa.project.finalize` | Завершение проекта | `PROJECT_FINALIZE`; код должности не ограничивает действие |
| `npa.audit.read` | Журнал проекта | `AUDIT_VIEW`; код должности не ограничивает действие |

Задача 6: в управляемом режиме пересекать право должности с допустимой локальной функцией участника. Не превращать право должности в членство во всех проектах. Верхнеуровневые технические обходы `permission.manage`/`npa.project.manage_all` через должности не выдавать.

## SenimDesk: 68 кодов

Реестр содержит шесть действий для каждого из 11 ресурсов ниже: `read`, `read_all`, `create`, `update`, `delete`, `export` (66 кодов), плюс два `rbac_audit`. Полный код — `ресурс:действие`, например `warehouse:update`. В Workspace уже присутствовали все эти коды; системный аудит исключён.

| Действие для каждого ресурса | Проверка / зависимость |
| --- | --- |
| `read` | GET списка/карточки ресурса, доступ к странице; собственные tenant/object-фильтры |
| `read_all` | Расширение области в пределах тенанта там, где обработчик использует scope. Workspace уже добавляет соответствующий `read`; не отменяет ограничения клиентской компании |
| `create` | POST создания; соответствующая кнопка при наличии реализации |
| `update` | PUT/PATCH и операции изменения ресурса; отдельные побочные полномочия отмечены ниже |
| `delete` | DELETE ресурса при наличии реализации |
| `export` | Отдельный серверный guard подтверждён для отчётов. Наличие остальных кодов в реестре не подтверждает защищённую операцию экспорта |

| Ресурс (все шесть кодов) | API / UI | Подтверждённые ограничения и расхождения |
| --- | --- | --- |
| `clients` | Клиенты/компании; маршруты клиентов | CRUD с action guards; scope тенанта/связей. Отдельная проверка `clients:export` не найдена |
| `projects` | Проекты; маршруты проектов и участников | CRUD; изменение участников связано с `projects:update`. `projects:export` не найден как отдельный guard |
| `objects` | Объекты; маршруты объектов | CRUD и связи с компанией/проектом. `objects:export` не найден как отдельный guard |
| `inventory` | Инвентарь; `/inventory/...` | `RequireInventoryPermission`, CRUD/import; часть дополнительных операций и tenant-root исключений требует задачи 6. `inventory:export` не найден как отдельный guard |
| `warehouse` | Склад; `/warehouse/...`, складская часть InventoryPage | CRUD и своя область; уже был в Workspace. `warehouse:export` не найден как отдельный guard |
| `tickets` | Заявки; `/tickets/...`, плановые заявки, dashboard | CRUD с проверкой участника/объекта; read_all учитывается через scope. `tickets:export` не найден как отдельный guard |
| `knowledge` | База знаний; статьи/категории | CRUD guards есть. Отдельное расширение области по `knowledge:read_all` и отдельный export guard не найдены |
| `reports` | Отчёты/аналитика; `/reports`, `/analytics` | `reports:read`, scope `read_all`, отдельный `reports:export`; самостоятельные create/update/delete операции не найдены |
| `workers` | Сотрудники; `/workers`, `/users`, `/admin/rbac` | CRUD; `workers:read` также читает локальный RBAC, а `workers:update` управляет его ролями/назначениями. Это лишнее административное полномочие для обычного изменения сотрудника, задача 6. Export guard не найден |
| `settings` | Настройки; клиентские типы `/admin/config/client-types` | `settings:read` используется в доступе UI штатной компании; `settings:update` — при изменении конфигурации. Самостоятельные create/delete/export и расширение `settings:read_all` не найдены |
| `groups` | Группы и их участники/доступ | CRUD, `groups:update` для синхронизации доступа. Отдельное расширение `groups:read_all` и export guard не найдены |
| `rbac_audit:view_audit` | `/admin/rbac/audit`; журнал доступа | Собственный/доступный журнал, зависит также от scope и серверной фильтрации |
| `rbac_audit:view_audit_all_tenant` | Тот же журнал | Вся текущая компания, без межкорпоративного аудита |

Общий `workspaceScopeResource` выбирает resources clients/projects/objects/inventory/warehouse/tickets/reports/workers по пути. Поэтому нельзя считать любое `read_all` универсальной рабочей галочкой без проверки фильтров конкретного handler. В задаче 6 нужны запросы allow/deny к бизнес-операциям и проверка дополнительных guards, а не только сравнение реестров.

Для **клиентской компании** каталог пересекается с реальным `clientPermissionAllowlist` SenimDesk:

| Ресурс | Разрешённые действия |
| --- | --- |
| `projects` | `read`, `read_all` |
| `objects`, `inventory`, `knowledge`, `workers`, `groups` | `create`, `read`, `update`, `delete`, `export` |
| `tickets` | `create`, `read`, `read_all`, `update`, `delete`, `export` |
| `reports` | `read`, `export` |
| `clients` | `create`, `read`, `read_all`, `update`, `delete` |
| `warehouse`, `settings`, оба `rbac_audit` | Не делегируются клиентскому тенанту |

Это совпадение контрактов двух сторон, а не доказательство реализации каждого разрешённого действия. Устаревший ресурс `equipment` не добавляется.

## iForm: 11 кодов и зависимости ролей

Каждый код сопоставлен с разрешённой ролью `WorkspaceDirectory.ROLES` и проверками `Access`, `DesignService`, `RuntimeService`, `ApiController`. Все операции ограничены подтверждённым тенантом; карточка заявки требует авторства, назначения либо операционной/аудиторской роли. Текущий контекст Workspace синхронизируется на запрос, при ошибке источника старый снимок не используется для продолжения доступа.

| Код Workspace → роль iForm | Операция API / UI | Зависимости и границы |
| --- | --- | --- |
| `iform.use` → `EMPLOYEE` | `/forms/{key}`, `/requests/drafts`, submit; формы и заявки сотрудника | Опубликованный процесс с аудиторией EMPLOYEE, свои заявки. Не даёт конструктор/публикацию/согласование |
| `iform.design` → `PROCESS_DESIGNER` | Создание процесса, чтение/правка draft, материалы; конструктор | Процесс своей компании; изменение с revision. Чтение черновика доступно также publisher; публикация требует отдельной роли |
| `iform.publish` → `PROCESS_PUBLISHER` | Publish/archive процесса, версии; публикация | Корректный черновик и revision; не выдаёт автоматически design. Тестовый запуск также использует publish-проверку |
| `iform.approve` → `APPROVER` | `POST /tasks/{id}/actions`; APPROVE/REJECT/RETURN | Текущий назначенный участник APPROVAL, taskVersion/dataRevision, комментарий для отказа/возврата. Не даёт решать чужую задачу |
| `iform.hr` → `HR` | Кадровые процессы/Workspace operations, ограниченные документы, кадровые этапы | Для запуска employee-процесса нужно также `iform.use`. Для APPROVAL нужно `iform.approve`, для подписи `iform.sign`; чтение restricted PDF всё равно требует доступ к заявке |
| `iform.finance` → `FINANCE_APPROVER` | Подбор участника `ROLE:FINANCE_APPROVER` в схеме процесса | Функция маршрута, не самостоятельное решение. Назначенному на APPROVAL также нужно `iform.approve` |
| `iform.accountant` → `ACCOUNTANT` | Подбор `ROLE:ACCOUNTANT` | Аналогично: точное назначение и право действия этапа, без системного администрирования |
| `iform.director` → `DIRECTOR` | Подбор `ROLE:DIRECTOR` | Функция процесса, не `organizations.owner_id`. Согласование/подпись требуют своих прав |
| `iform.sign` → `SIGNER` | Назначение подписанта SIGNATURE; экран подписи | Активный назначенный подписант. `/demo/signing/...` работает только в DEMO; production callback без проверенного контракта отклоняется, рабочую интеграцию подписи отдельно проверить |
| `iform.operate` → `OPERATOR` | Список заявок/задач, `/incidents`, действия инцидента; оператор | Просмотр заявок компании, безопасное повторение/VERIFY операций; не даёт решение чужого APPROVAL и не подписывает за другого |
| `iform.audit` → `AUDITOR` | `/audit-events`, версии процессов, просмотр заявок; аудит | Чтение в текущей компании; не даёт изменение процесса, решение или подпись |

`HR`, `FINANCE_APPROVER`, `ACCOUNTANT`, `DIRECTOR` используются также селекторами участников. Если по селектору найдено несколько сотрудников и подразделение не устраняет неоднозначность, iForm требует точного назначения. Само название должности не заменяет роль/структуру.

`TENANT_ADMIN` — техническое администрирование шаблонов, календаря и подключений; `CLIENT` — другая аудитория. Они не добавлены как права должности сотрудника. Существующее автоматическое добавление владельцу `TENANT_ADMIN` и пяти рабочих ролей в `IFormRoles` остаётся расхождением задачи 6; совпадение явного каталога с iForm не устраняет этот обход.

## Что нельзя делегировать и что осталось исправить

Workspace принимает только коды своего приложения. Не допускаются `*`, `all`, `admin`, произвольные роли и коды другого приложения. Из ЭДО исключены `user.manage`, `department.manage`, `position.manage`, `permission.manage`, `dictionary.manage`, `delegation.manage`, `audit.read`; последний относится к общему аудиту, в отличие от делегируемого архивного журнала. Не добавлен технический обход `npa.project.manage_all`. В SenimDesk исключён `system_audit:view_audit_all_system`. iForm `TENANT_ADMIN` нельзя явно выдать через политику должности.

| Следующая задача | Конкретный результат этой сверки |
| --- | --- |
| 3–4: должности, назначение, применение | Создание пустой должности, корректное назначение директором-владельцем/суперадмином, сохранение и состояния module/managed |
| 5: зависимости — выполнено | Единые правила в API и редакторе, alternatives чтения, start → send_to_approval, manage → export, независимые функции iForm, выбор только видимых строк; [правила и совместимость](position-access.md#зависимости-прав-задача-5) |
| 6: серверные guards и UI — выполнено локально | Точные действия и участники ЭДО, пересечение НПА с ролями проекта и отзыв сессии, отсутствие owner/admin рабочих ролей, защита RBAC/сотрудников/справочника СД; неподключённые галочки недоступны для выдачи. Выпуск всех потребителей ещё требуется |
| 7–8: workflow ЭДО | Для поручений требовать право вместе с отношением к объекту; состояния/сроки/возврат/закрытие; список участников должен допускаться и справочником API, и UI |
| 9–10: Workspace Work и приёмка | Устранить отдельные разрешения владельца/администратора в проекции поручений, проверить отзыв прав и одинаковые запреты через все интерфейсы |

В частности, frontend ЭДО разрешает открывать выбор сотрудников по более широкому набору прав, чем auth API `/users` (в BFF — `user.manage`, `document.create`, `document.send_to_signing`). Скрывать/показывать кнопки без исправления этой зависимости недостаточно.

## Проверки и обновление

Регрессионные тесты Workspace проверяют явную выдачу шести archive-кодов без автоматического расширения старого набора, отказ для системных/чужих/неизвестных кодов, SD read_all → read, клиентский каталог, mapping iForm и новые зависимости задачи 5. Неполные новые наборы отклоняются без записи; manage принимается вместе с export. Тест обработчика проверяет переводы ru/kk, условные пояснения iForm, неизменность codes/количества и исходного каталога. Проверка i18n проверяет полноту ключей.

Для сверки новых версий есть [тесты реальных реестров](../internal/domain/access_catalog_contract_test.go). Они читают локальные checkout без сети/БД и падают при неизвестном или пропавшем коде. Без переменной соответствующий контрактный тест явно пропускается. Это проверка реестра и клиентского allowlist, не замена endpoint-тестов авторизации.

```powershell
$env:ACCESS_CONTRACT_ODO_REPO = 'C:\path\workspace-odo-backend'
$env:ACCESS_CONTRACT_SD_REPO = 'C:\path\workspace-sd-backend'
$env:ACCESS_CONTRACT_IFORM_REPO = 'C:\path\iform'
go test ./internal/domain -run 'TestAccessCatalog.*ServiceContract' -count=1 -v
go test ./internal/domain ./internal/i18n
go test ./internal/handler -run TestAccessCatalog -count=1
```

При этой сверке прошли полные тесты `internal/domain`, `internal/i18n`, `internal/handler`, включая три контрактных теста с переданными checkout. Также прошли существующие тесты ЭДО на отдельные права архива, запрет раскрытия чужой компании/черновика и обязательность аудита скачивания. Интеграционные тесты Workspace с БД пропущены: `TEST_MESSENGER_DATABASE_URL` и `TEST_NOTES_DATABASE_URL` не настроены.

Для этого изменения нужен новый Workspace backend. Новая миграция Workspace, переменная окружения и изменение формата frontend API не нужны: UI строит список из каталога. ЭДО должен содержать существующую миграцию `000026_archive_permissions.sql` и проверки соответствующей версии. Для новых прав по-прежнему нужны сохранённая политика должности и включённое управление приложением. Production, визуальная приёмка и общий E2E трёх сервисов этой сверкой не подтверждены. Изменения не развёрнуты.
