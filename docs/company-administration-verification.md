# BAS-017 / BE-09: проверка и выпуск

## Регрессионные проверки

| Сценарий | Автоматическая проверка |
| --- | --- |
| Директор, сотрудник, платформа без компании; чужая компания и platform API | `TestAdministrationAuthorizationMatrix` |
| Смена директора, отзыв глобальной роли, деактивация и удаление аккаунта без ожидания истечения JWT | `TestAdministrationAuthorizationMatrix`, `TestAdministrationRevocationAndFailures` |
| Создание и приостановка компаний, назначение директора, блокировка и запрет реактивации синхронизацией | `TestOrganizationAdministrationLifecycle` |
| Модули, устаревшая revision, откат при ошибке аудита | `TestOrganizationAdministrationLifecycle/module_administration` |
| Квоты компании и сотрудника, конкурентное выделение и загрузка, версии и корзина, перемещение между компаниями, rollback миграции | `TestAdministrationStorageBudgetsAndAccounting` |
| Корпоративный вход: изоляция компании и компенсация изменений Keycloak | `TestCompanyIdentityProviderScopeAndCompensation` (тестовый клиент Keycloak) |
| Сводки, секреты, аудит и capabilities при приостановке | `TestAdministrationOverviewIsolationAndCapabilities`, `TestCompanyAdministrationCapabilities` |
| Коды ошибок одинаковы на ru/kk, локализованный текст, исключение технических деталей | `TestAdministrationErrorContractAcrossLanguages` |
| Обновление существующей схемы, сохранение персональных квот и файлов, отсутствие искусственного бюджета, сохранение модулей | Три release-upgrade теста в `cmd/migrate/calendar_upgrade_test.go` |

Проверки доступа используют настоящий PostgreSQL: недостаточно проверить роль только на фронте или сравнить роль из старого JWT. При отказе административная запись и аудит должны оставаться неизменными.

## Запуск в CI

Авторитетная конфигурация — [.github/workflows/ci.yml](../.github/workflows/ci.yml). Она запускается для PR в main и push в main; push в epic-ветку сам по себе не запускает этот pipeline. Go — 1.24.7, PostgreSQL — 16. База должна быть отдельной тестовой базой: интеграции создают и удаляют временные схемы.

Перед тестами создать расширения `btree_gist` и `pg_trgm` в public и передать `TEST_MESSENGER_DATABASE_URL` и `IFORM_TEST_DATABASE_URL`. Для полноценного общего suite дополнительно нужны MinIO и конвертеры документов, как в workflow.

```sh
go vet ./...
go test ./... -count=1 -timeout=10m
go build ./cmd/api
```

Для быстрого повторения административных проверок на той же тестовой базе:

```sh
go test ./internal/domain ./internal/repository ./internal/service ./internal/handler   -run 'Test(Administration|CompanyAdministration|OrganizationAdministration|CompanyIdentityProvider)'   -count=1 -timeout=3m
go test ./cmd/migrate -run 'Test(CalendarReleaseUpgrade|NotesReleaseUpgrade|NewsReleaseUpgrade)' -count=1
```

Если переменная базы отсутствует, PostgreSQL-тесты пропускаются. Такой запуск не подтверждает интеграционную готовность. База для поиска должна поддерживать Unicode case folding, поскольку общий suite проверяет русскоязычный поиск.

## Локальная проверка 2026-10-12

Использована отдельная временная PostgreSQL 16, без обращения к существующим базам. Исправлены тестовая SQL-параметризация UUID в сводке и подготовка legacy Drive-схемы для release-upgrade тестов. Production SQL и политика доступа не изменялись.

На Go 1.24.7 прошли общий набор тестов с указанными ниже исключениями, `go vet ./...` и сборка API. Матрица прав, сводка, квоты, корпоративные провайдеры и upgrade-тесты выполнены с PostgreSQL, без пропуска. Форматирование проверено с нормализацией Windows-переносов строк.

Общий набор проверялся на Windows. Три существующих теста логирования (`TestHTTPLoggingFormats`, `TestLoggerRetainsOperationsAndRepeatedEvents`, `TestConsoleLoggerColorsLevelsAndOperations`) исключены из общего запуска: Windows запрещает удаление открытого файла логов при очистке TempDir. В Linux CI они выполняются без этого исключения. Проверки MinIO и внешних конвертеров зависят от наличия своих сервисов; локальный результат не заменяет соответствующие CI jobs. Тест компенсации корпоративных провайдеров использует тестовый клиент, а не действующий Keycloak.

## Перед выпуском

1. Применить миграции после резервного копирования штатным deployment-процессом; обновление схемы должно успешно завершиться до запуска новых API.
2. Проверить Test and lint и Production document converters в PR. Локальная проверка не подтверждает результат ещё не запущенного GitHub Actions.
3. В тестовом окружении проверить вход администратора платформы и директора, назначение директора и управление корпоративным провайдером на настоящем Keycloak.
4. Передать фронту [company-administration-api.md](company-administration-api.md) и связанные контракты. Проверять права по capabilities для интерфейса; сервер остаётся источником авторизации.

BE-09 не создаёт PR, не выполняет merge и не развёртывает production.

Переход со старого общего бюджета на Drive без зависимости от AxisMail:
`TestCompanyDriveBudgetIgnoresLegacyMailReservation` проверяет миграцию,
сохранность квот и файлов, распределение директором и запрет загрузки сверх личного лимита.
