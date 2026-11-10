# BAS-017: подключение административного кабинета

## Авторизация и выбор кабинета

Все запросы требуют `Authorization: Bearer <token>`. Администратор платформы имеет серверную роль `admin`; email и название должности не определяют права. Директор определяется `organizations.owner_id` и управляет своей компанией. Сервер проверяет актуальные права и активность учётной записи при каждом запросе.

Директор загружает `GET /api/v1/organizations/current/administration/overview`. Администратор платформы загружает `GET /api/v1/platform/administration/overview`, затем выбирает компанию и вызывает `GET /api/v1/platform/organizations/{organization_id}/administration/overview`. Компания директора определяется сервером, передавать её ID в current-запросе не нужно. Директор не имеет доступа к platform-маршрутам даже для своей компании.

## Сводка компании

Ответ содержит:

| Поле | Содержимое |
| --- | --- |
| `organization` | `id`, `name`, `slug`, `status`, `owner_id` (может быть null) |
| `capabilities` | Булевы флаги доступных действий, перечислены ниже |
| `users` | `total`, `active`, `inactive`, `blocked` |
| `unit_count` | Количество подразделений без удалённых |
| `modules` | Модули с доступностью, включением и revision; формат BE-05 |
| `storage` | Бюджет, выделенный и использованный объём, свободный остаток и revision; формат BE-06 |
| `integrations` | Счётчики `total`, `enabled`, `pending`, `error`, `disabled` |
| `recent_audit` | До пяти последних событий: `items`, опциональный `next_before_id` |

Счётчики сотрудников исключают удалённые аккаунты. `active` отражает состояние аккаунта, независимо от приостановки компании. `blocked` — отдельный счётчик административно заблокированных пользователей, он пересекается с `inactive`. Глобальные счётчики учитывают только сотрудников компаний, без аккаунтов без компании.

Интеграции — только корпоративные OIDC/SAML. `enabled` отражает настройку включения, остальные счётчики — сохранённый статус. Это не проверка доступности внешнего провайдера; счётчики могут пересекаться. Настройки, секреты и технические ошибки в сводку не включаются.

Сводка читается в одной транзакции с согласованным снимком данных. Ответ имеет `Cache-Control: no-store`. После изменения нужно загрузить сводку заново.

## Capabilities

Фронт использует флаги для отображения действий. Они описывают права выбранного API, а не заменяют серверную авторизацию и проверки конкретного сотрудника.

| Флаг | Директор/current | Platform/company |
| --- | --- | --- |
| `manage_users`, `manage_structure`, `manage_permissions`, `manage_modules` | Активная компания | Активная компания |
| `set_user_quotas` | Активная компания с настроенным бюджетом | Активная компания |
| `set_module_availability`, `set_company_quota`, `manage_sd_link`, `manage_identity_providers` | false | Активная компания |
| `manage_company_lifecycle`, `assign_owner` | false | true, включая приостановленную компанию |
| `read_audit` | true | true |

Администратор платформы, использующий current-маршрут, получает current-возможности. Для платформенных настроек необходимо использовать platform-маршрут. Приостановленная компания недоступна директору (404), но доступна платформе для чтения, восстановления и назначения директора. Изменения прочих настроек приостановленной компании отклоняются.

`storage.quota_bytes: null` означает ненастроенный бюджет компании; действует существующая политика персональных квот. Значение 0 является настроенным нулевым бюджетом.

## Сводка платформы

Ответ глобального overview содержит `capabilities` (`create_companies`, `manage_company_lifecycle`, `assign_owners`, `read_global_audit`), `companies` (`total`, `active`, `suspended`, `without_owner`), `users`, `unit_count`, `companies_with_storage_budget`, `integrations`, `recent_audit`. `without_owner` считает компании с `owner_id: null`. Глобальная сводка не возвращает список компаний — он загружается отдельным API.

## API разделов

Для company-разделов префикс директора: `/api/v1/organizations/current`; платформы: `/api/v1/platform/organizations/{organization_id}`. Точные запросы и ответы описаны в следующих документах:

| Раздел | Документ |
| --- | --- |
| Права директора и существующая вкладка прав | [company-administration-access.md](company-administration-access.md) |
| Список, создание, статус компаний, назначение директора | [company-administration-lifecycle.md](company-administration-lifecycle.md) |
| Аудит компании и платформы, пагинация | [company-administration-audit.md](company-administration-audit.md) |
| Сотрудники, поиск, блокировка | [company-administration-users.md](company-administration-users.md) |
| Доступность и включение модулей | [company-administration-modules.md](company-administration-modules.md) |
| Бюджет компании и персональные квоты | [company-administration-storage.md](company-administration-storage.md) |
| Корпоративные OIDC/SAML, безопасные настройки | [company-administration-integrations.md](company-administration-integrations.md) |

Полный аудит компании: `GET {prefix}/administration/audit`; глобальный аудит: `GET /api/v1/platform/administration/audit`. Для продолжения передавать `before_id=next_before_id`, limit от 1 до 100. Остальные списки имеют собственные параметры пагинации по соответствующим документам.

Модули `available` и `enabled` определяют доступность приложения. Старый флаг режима управления правами доступа не является выключателем модуля. Для изменения модулей и квот передавать полученный `revision`. При 409 перечитать состояние и предложить пользователю повторить изменение.

## Язык и ошибки

Передавать `Accept-Language: ru` или `kk`; затем сервер использует язык профиля, затем русский. Контракт языка: [localization-api-contract.md](localization-api-contract.md). Ошибки: [api-error-localization.md](api-error-localization.md).

Структура ошибки: `{"error":{"code":"FORBIDDEN","message":"…","user_message":"…"}}`. Фронт принимает решения по HTTP-статусу и стабильному `error.code`, а не по тексту. Для прямого показа использовать `user_message`. Типовые статусы: 400 — некорректный запрос, 401 — требуется вход, 403 — недостаточно прав, 404 — объект недоступен или не найден, 409 — конфликт состояния/revision. Изменение языка не изменяет код ошибки.

Перед запуском API применить миграции соответствующих BE-задач. Для overview отдельная миграция не нужна.

Проверки и требования к выпуску: [company-administration-verification.md](company-administration-verification.md).

Журнал безопасности, IP-фильтры и экспорт BAS-018: [security-audit-api.md](security-audit-api.md).

Проверки изоляции поиска и ИИ BAS-022: [tenant-isolation-security.md](tenant-isolation-security.md).

Для назначения бюджета по физическому остатку MinIO суперадмин использует
`GET /api/v1/platform/organizations/{organization_id}/administration/storage/capacity`.
Настройка метрик, резерв и расчёт `max_quota_bytes` описаны в
[company-administration-storage.md](company-administration-storage.md).

Бюджет компании относится только к Drive в MinIO. Директор распределяет его
между сотрудниками; подключение AxisMail для этого не требуется. Контракт и миграция:
[company-administration-storage.md](company-administration-storage.md#бюджет-только-для-drive).
