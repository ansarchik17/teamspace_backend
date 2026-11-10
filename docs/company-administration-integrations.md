# BAS-017 BE-07: корпоративные интеграции входа

OIDC/SAML настраивает только администратор платформы (`users.role='admin'`).
Директор не управляет корпоративным входом, даже в своей компании.
Связь с СД остаётся платформенной настройкой; личные подключения календарей
сохраняют свои API. Изменение политики не отключает уже настроенный вход.

## API для фронта

| Операция | Администратор платформы |
| --- | --- |
| Список | `GET /api/v1/platform/organizations/:organization_id/identity-providers` |
| Создание | `POST` по тому же пути |
| Изменение | `PATCH .../:provider_id` |
| Удаление | `DELETE .../:provider_id` |

Бывшие current-маршруты `/api/v1/organizations/current/identity-providers`
(GET/POST/PATCH/DELETE) возвращают `403 FORBIDDEN`, в том числе для директора.
Администратор платформы использует platform-маршруты. Сервер проверяет
актуальную роль и активность аккаунта. Email, название должности и параметры
запроса не выдают прав. В current-сводке `manage_identity_providers=false`.
Фронт скрывает вкладку интеграций для директора; запрет применяется на бэке
и при прямом запросе. Сообщение ошибки локализовано на ru/kk.

Создание OIDC:

```json
{
  "provider_type":"oidc",
  "display_name":"Корпоративный вход",
  "enabled":true,
  "settings":{
    "authorizationUrl":"https://id.company.kz/authorize",
    "tokenUrl":"https://id.company.kz/token",
    "clientId":"workspace"
  },
  "client_secret":"передаётся только при настройке"
}
```

OIDC требует `authorizationUrl`, `tokenUrl`, `clientId` и `client_secret`.
SAML требует `singleSignOnServiceUrl` и `entityId`, секрет OIDC не требуется.
В ответе POST — 201, PATCH — 200, DELETE — 204. Список GET — массив.

Разрешённые публичные параметры:

- OIDC: `authorizationUrl`, `tokenUrl`, `userInfoUrl`, `logoutUrl`, `clientId`,
  `issuer`, `jwksUrl`, `defaultScope`, `clientAuthMethod`, `validateSignature`,
  `useJwksUrl`.
- SAML: `singleSignOnServiceUrl`, `entityId`, `singleLogoutServiceUrl`,
  `signingCertificate`, `nameIDPolicyFormat`, `validateSignature`.

Принимаются camelCase и snake_case; сервер хранит канонические имена.
Неизвестные параметры, секреты внутри `settings`, конфликтующие варианты
одного ключа и некорректные URL отклоняются. URL должны иметь схему HTTP/HTTPS,
хост и не содержать встроенные логин/пароль или fragment. Булевы параметры
передаются строками `"true"` / `"false"`. Размер тела — не более 64 KiB,
значения настроек — до 4096 символов, секрет — до 8192 байт.

PATCH принимает необязательные `display_name`, `enabled`, `settings`,
`client_secret`. Отсутствие секрета сохраняет существующий. Пустой секрет
OIDC запрещён. `settings` заменяет публичные настройки целиком: обязательные
параметры надо передать снова. Если `settings` отсутствует, они сохраняются.

Пример безопасного ответа:

```json
{
  "id":"uuid",
  "organization_id":"uuid",
  "provider_type":"oidc",
  "alias":"server-generated-alias",
  "display_name":"Корпоративный вход",
  "enabled":true,
  "settings":{"clientId":"workspace","tokenUrl":"https://id.company.kz/token","authorizationUrl":"https://id.company.kz/authorize"},
  "secret_set":true,
  "status":"active"
}
```

В ответах также есть даты создания/изменения и существующее поле
`last_tested_at`. Оно не означает, что проверен полный вход сотрудника:
новый endpoint проверки внешнего провайдера в задачу не входит.
Секрет находится только в Keycloak: в PostgreSQL, административный аудит
и ответы API он не записывается. В API нет `last_error`; технические ошибки
не выдаются UI. Чувствительные и неизвестные поля старых записей фильтруются.

## Изоляция, аудит и сбои

Каждый provider ID проверяется внутри выбранной сервером компании. Назначение
компании в Keycloak mapper создаётся сервером и не принимается от клиента.
Отозванные права и блокировка аккаунта проверяются повторно в транзакции.
Изменение приостановленной компании отклоняется; платформа может читать
её настройки. Изменения записываются в существующий аудит
`identity_provider.create/update/delete` без секретов и параметров подключения.

Все изменения интеграции компании выполняются последовательно, включая
запросы с разных экземпляров Workspace. При ошибке БД/аудита сервер пытается
восстановить настройки Keycloak, а при неудавшемся создании — удалить созданный
провайдер. При откате удаления восстанавливаются провайдер и его mappers.
Восстановление выполняется до следующего изменения интеграции этой компании.

PostgreSQL и Keycloak не имеют общей транзакции. При недоступности Keycloak,
неопределённом результате commit или аварийном завершении процесса может
потребоваться сверка платформенным администратором. Ошибка восстановления
фиксируется в серверном журнале без передачи диагностики клиенту.

Ошибки используют общий контракт и локализацию ru/kk: `VALIDATION_ERROR` / 400,
`FORBIDDEN` / 403, `NOT_FOUND` / 404, `CONFLICT` / 409 при конфликте настройки,
приостановленной компании или ошибке применения внешней настройки;
`INTERNAL_ERROR` / 500 при внутреннем сбое. Фронт ориентируется на код.

Новая миграция и новая роль не нужны. Тесты проверяют фильтрацию секретов,
валидацию настроек, доступ директора и платформы, чужую компанию,
отзыв директорства и компенсацию сбоев аудита при create/update/delete.
Тесты с PostgreSQL запускаются с `TEST_MESSENGER_DATABASE_URL`:

```sh
go test ./internal/domain ./internal/repository ./internal/service ./internal/handler ./internal/keycloak ./internal/server -count=1
```

## Availability on the public login page

`GET /api/v1/auth/login-options?organization=<slug>` returns only
`{"corporate_login":true|false}` with `Cache-Control: no-store`. It queries no
users and exposes no provider details. Only an enabled, active provider of an
active company is available. Missing configuration and lookup errors return
false. The company-specific Keycloak redirect rechecks the same resolver;
internal browser session SSO remains independent of this presentation flag.
