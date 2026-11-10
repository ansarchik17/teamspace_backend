# NWS-007: Аналитика прочтений — Подтверждение ознакомления и статистика прочтения

## 1. Обзор задачи

Реализация серверной части (Go / PostgreSQL / Excelize / CSV) для задачи **NWS-007: Аналитика прочтений**:
* **Кнопка «Ознакомлен» (Acknowledgement)**:
  - Фиксация факта подтверждения ознакомления сотрудника с публикацией (`POST /api/v1/publications/:id/acknowledge`).
  - Автоматическая отметка о прочтении публикации (`publication_views`), если просмотр ещё не был зафиксирован.
  - Автоматическое скрытие сквозного баннера (`publication_banner_dismissals`), если публикация являлась активным баннером с флагом `requires_acknowledgement`.
  - Возможность отзыва/снятия отметки об ознакомлении (`DELETE /api/v1/publications/:id/acknowledge` и `POST /api/v1/publications/:id/unacknowledge`).
  - Поле `acknowledged: true/false` в ответе публикации `GET /api/v1/publications/:id` и в карточках списка для фронтенда.
* **Статистика и аналитика прочтений (Read & Acknowledgement Analytics)**:
  - `GET /api/v1/publications/:id/analytics` (с алиасом `/read-stats`):
    - Расчёт целевой аудитории (`total_target_users` с учётом таргетинга `all`, `department`, `ministry`).
    - Количество прочитавших (`views_count`) и процент от целевой аудитории (`view_percent`).
    - Количество ознакомленных (`acknowledged_count`) и процент от целевой аудитории (`acknowledgement_percent`).
    - Количество непрочитавших (`unread_count`) и неознакомленных (`unacknowledged_count`).
    - Временные метки первого и последнего прочтения / ознакомления (`first_view_at`, `last_view_at`, `first_acknowledged_at`, `last_acknowledged_at`).
    - Разбивка статистики по отделам (`department_stats`) и филиалам (`ministry_stats`).
* **Список сотрудников с детальными статусами прочтения (Readers List)**:
  - `GET /api/v1/publications/:id/readers`:
    - Постраничный список целевых сотрудников со статусами:
      - `is_viewed` + `viewed_at` (прочитано ли и когда).
      - `is_acknowledged` + `acknowledged_at` (ознакомлен ли и когда).
      - Данные пользователя: ФИО, аватар (с подписанным URL), должность, email, филиал, отдел.
    - Фильтрация:
      - `status`: `all` (по умолчанию), `read` / `viewed`, `unread`, `acknowledged`, `unacknowledged`.
      - `department_id`: фильтрация по конкретному подразделению.
      - `ministry_id`: фильтрация по конкретному филиалу / министерству.
      - `search`: поиск по имени, фамилии, email или должности.
      - `page`, `limit`: пагинация.
* **Выгрузка списка прочитавших сотрудников (Export Readers)**:
  - `GET /api/v1/publications/:id/readers/export` и `GET /api/v1/publications/:id/export/readers`:
    - Поддержка формата Excel (`format=xlsx`, по умолчанию):
      - Стилизованная шапка с фирменным синим цветом `#2563EB` и белым текстом.
      - Блок метаданных: заголовок публикации, сводная статистика (целевая аудитория, количество и процент прочитавших, количество и процент ознакомившихся).
      - Автоподбор ширины колонок и выравнивание (по центру для дат/номеров/статусов, по левому краю для ФИО/email/должности).
    - Поддержка формата CSV (`format=csv`):
      - Генерация с префиксом UTF-8 BOM (`\xef\xbb\xbf`), чтобы Microsoft Excel на Windows корректно открывал кириллические символы без искажения кодировки.
    - Фильтрация списка в выгрузке полностью совпадает с экраном списка (`status`, `department_id`, `ministry_id`, `search`).
    - Имя файла формируется автоматически с санитайзингом названия публикации и временной меткой: `readers_<clean_title>_YYYYMMDD_HHMM.xlsx`.

---

## 2. Структуры базы данных

Функционал опирается на существующие таблицы БД без необходимости ломающих миграций:
1. `publication_acknowledgements`:
   - `publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE`
   - `user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE`
   - `acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP`
   - `PRIMARY KEY (publication_id, user_id)`
2. `publication_views`:
   - `publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE`
   - `user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE`
   - `viewed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP`
   - `PRIMARY KEY (publication_id, user_id)`
3. `publication_banner_dismissals`:
   - `publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE`
   - `user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE`
   - `dismissed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP`
   - `PRIMARY KEY (publication_id, user_id)`
4. `users`:
   - Хранит идентификаторы подразделений `department_id BIGINT` и филиалов `ministry_id BIGINT`.

---

## 3. Спецификация API

### 3.1. Подтверждение ознакомления
* **Метод**: `POST /api/v1/publications/:id/acknowledge`
* **Доступ**: Авторизованный пользователь, имеющий доступ к публикации.
* **Ответ (200 OK)**:
```json
{
  "status": "acknowledged",
  "acknowledged": true,
  "acknowledged_at": "2026-10-01T13:40:00Z"
}
```
*Побочные эффекты*:
- Если просмотр публикации пользователем ещё не был зафиксирован, автоматически создаётся запись в `publication_views`.
- Если публикация являлась активным баннером с требованием обязательного ознакомления, баннер автоматически скрывается для данного пользователя.

### 3.2. Снятие отметки об ознакомлении
* **Метод**: `DELETE /api/v1/publications/:id/acknowledge` или `POST /api/v1/publications/:id/unacknowledge`
* **Доступ**: Авторизованный пользователь.
* **Ответ (200 OK)**:
```json
{
  "status": "unacknowledged",
  "acknowledged": false,
  "acknowledged_at": "0001-01-01T00:00:00Z"
}
```

### 3.3. Аналитика и статистика прочтений
* **Метод**: `GET /api/v1/publications/:id/analytics` (или `GET /api/v1/publications/:id/read-stats`)
* **Доступ**: Автор публикации, SuperAdmin или модератор.
* **Ответ (200 OK)**:
```json
{
  "publication_id": "90cf9fa9-4828-40fe-8f0a-115f5c1894d0",
  "total_target_users": 150,
  "views_count": 120,
  "view_percent": 80.0,
  "acknowledged_count": 95,
  "acknowledgement_percent": 63.33,
  "unread_count": 30,
  "unacknowledged_count": 55,
  "first_view_at": "2026-10-01T08:00:00Z",
  "last_view_at": "2026-10-01T13:30:00Z",
  "first_acknowledged_at": "2026-10-01T08:15:00Z",
  "last_acknowledged_at": "2026-10-01T13:35:00Z",
  "department_stats": [
    {
      "department_id": 101,
      "department_name": "Отдел #101",
      "total_users": 50,
      "views_count": 45,
      "view_percent": 90.0,
      "acknowledged_count": 40,
      "acknowledgement_percent": 80.0
    }
  ],
  "ministry_stats": [
    {
      "ministry_id": 201,
      "ministry_name": "Филиал #201",
      "total_users": 150,
      "views_count": 120,
      "view_percent": 80.0,
      "acknowledged_count": 95,
      "acknowledgement_percent": 63.33
    }
  ]
}
```

### 3.4. Список читателей с фильтрацией и пагинацией
* **Метод**: `GET /api/v1/publications/:id/readers`
* **Query-параметры**:
  - `status`: `all` (по умолчанию), `read`, `unread`, `acknowledged`, `unacknowledged`.
  - `department_id`: числовой ID подразделения.
  - `ministry_id`: числовой ID филиала.
  - `search`: строка поиска по имени, email, должности.
  - `page`: номер страницы (по умолчанию 1).
  - `limit`: количество на странице (по умолчанию 50, максимум 100).
* **Ответ (200 OK)**:
```json
{
  "publication_id": "90cf9fa9-4828-40fe-8f0a-115f5c1894d0",
  "total_count": 150,
  "page": 1,
  "limit": 50,
  "readers": [
    {
      "user": {
        "id": 42,
        "first_name": "Алихан",
        "last_name": "Нурланов",
        "full_name": "Алихан Нурланов",
        "avatar_url": "/api/v1/storage/download?...",
        "position": "Ведущий инженер"
      },
      "email": "alikhan@example.kz",
      "department_id": 101,
      "department_name": "Отдел #101",
      "ministry_id": 201,
      "ministry_name": "Филиал #201",
      "is_viewed": true,
      "viewed_at": "2026-10-01T08:30:00Z",
      "is_acknowledged": true,
      "acknowledged_at": "2026-10-01T08:35:00Z"
    }
  ]
}
```

### 3.5. Выгрузка списка читателей (Excel / CSV)
* **Метод**: `GET /api/v1/publications/:id/readers/export` (или `/export/readers`)
* **Query-параметры**:
  - `format`: `xlsx` (по умолчанию) или `csv`.
  - `status`, `department_id`, `ministry_id`, `search`.
* **Заголовки ответа**:
  - При `format=xlsx`:
    - `Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`
    - `Content-Disposition: attachment; filename="readers_Заголовок_20261001_1340.xlsx"`
  - При `format=csv`:
    - `Content-Type: text/csv; charset=utf-8`
    - `Content-Disposition: attachment; filename="readers_Заголовок_20261001_1340.csv"`

---

## 4. Контроль доступа

1. Кнопка **«Ознакомлен»** доступна любому сотруднику, входящему в целевую аудиторию опубликованной новости.
2. Просмотр **аналитики прочтений**, **списка читателей** и **экспорт отчётов** разрешены только:
   - Автору публикации (`pub.AuthorID == userID`);
   - Пользователям с ролью `admin` (`domain.RoleSuperAdmin`);
   - Пользователям с ролью `manager` (модераторам).
3. Для неавторизованных или не имеющих прав пользователей возвращается `403 Forbidden` (`apperrors.Forbidden`).

---

## 5. Тестирование и верификация

* `internal/service/publication_nws007_test.go`:
  - `TestNWS007_ModelsJSON`: сериализация / десериализация ответов ознакомления, аналитики и поля `acknowledged` в публикации.
  - `TestNWS007_SanitizeExportFileName`: проверка очистки недопустимых символов из имени файла отчёта и поддержка кириллицы.
  - `TestNWS007_CSVExportGeneration`: генерация CSV с заголовком UTF-8 BOM и корректными русскими значениями статусов.
  - `TestNWS007_XLSXExportGeneration`: создание валидного файла Excel через `excelize` и проверка структуры таблицы.
* `internal/handler/publication_nws007_test.go`:
  - `TestNWS007_HandlerValidation`: проверка валидации UUID и корректности обработки ошибок 400 для всех 8 эндпоинтов NWS-007.

* `internal/service/publication_nws007_regression_test.go`:
  - Интеграционный тест подтверждения, стабильности сохранённой даты, статистики и прав доступа.
  - Проверка реальной CSV-выгрузки и защиты от spreadsheet formula injection.
  - Некорректный таргетинг отклоняется без выдачи всей аудитории.
  - Ошибка записи скрытия баннера откатывает подтверждение и просмотр в одной транзакции.
* `internal/server/router_test.go`: проверка регистрации реального роутера с включённым обработчиком публикаций и сохранения маршрута статистики рассылок.

Повторное подтверждение возвращает исходный `acknowledged_at` из БД. Подтверждение,
просмотр и скрытие обязательного баннера сохраняются атомарно. Текстовые поля CSV,
которые могут интерпретироваться как формулы, получают префикс `'`.
