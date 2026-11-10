# NWS-004: Таргетинг — Публикация для всей компании или выбранных подразделений

## Правила после проверки безопасности

- `RoleSuperAdmin` в текущем коде сериализуется как `admin`. Только эта роль
  может выбирать чужие подразделения. Остальные пользователи могут выбрать
  только свой `ministry_id`/`department_id`, включая при редактировании.
- Одиночный ID и массив объединяются в одну аудиторию. Повторы удаляются;
  допускается 1–100 положительных ID. Лента, просмотр, баннеры, получатели
  уведомлений и статистика используют эту аудиторию.
- Отдельного справочника числовых подразделений в текущих миграциях нет.
  Проверяется наличие хотя бы одного неудалённого пользователя с указанной
  привязкой. Подразделение без таких пользователей выбрать нельзя. Проверка
  получателей рассылки отдельно исключает неактивных пользователей.
- Эти числовые ID не являются UUID полей `organization_id`/`team_id`.
  Для таргетинга по новой корпоративной структуре требуется отдельное согласование
  модели данных; передавать UUID в числовые поля нельзя.
- При редактировании и неизменном `target_audience` отсутствие полей целевых ID
  сохраняет существующие цели. Недопустимые цели дают ошибку валидации, выбор
  чужого подразделения без глобального доступа — `403`.

## 1. Обзор задачи

Реализация серверной части (Go / PostgreSQL) для задачи **NWS-004**:
* **Таргетированная видимость новостей (Target Audience Scope)**:
  - `all`: Публикация для всей компании (доступна всем активным сотрудникам).
  - `ministry` / `branch`: Таргетированная публикация для конкретного филиала / министерства или группы филиалов.
  - `department`: Таргетированная публикация для конкретного отдела / департамента или группы отделов.
* **Поддержка одиночного и множественного таргетинга**:
  - Одиночный выбор: `target_ministry_id`, `target_department_id`.
  - Множественный выбор (мульти-селект подразделений): `target_ministry_ids []int64`, `target_department_ids []int64`.
* **Правила безопасности и изоляции видимости**:
  - Публикация, таргетированная на отдел A, видна исключительно сотрудникам отдела A (а также автору и `SuperAdmin`).
  - Публикация, таргетированная на филиал B, видна исключительно сотрудникам филиала B (а также автору и `SuperAdmin`).
  - Применимо ко всем сущностям публикации: основной ленте новостей (`List`), детальному просмотру (`GetPublication`), баннерам (`ListBanners`) и рассылке широковещательных уведомлений (`BroadcastPublication`).
* **Фильтрация ленты**:
  - Параметры запроса `GET /api/v1/publications?department_id=X` и `?ministry_id=Y` фильтруют ленту новостей как по авторству подразделения, так и по таргетингу на данное подразделение.

---

## 2. База данных и миграции

Миграция: `db/migrations/20261001000200_publications_target_audience_departments.sql`

### Изменения схемы:
```sql
-- Добавление полей таргетинга по отделам и массивам подразделений
ALTER TABLE publications
    ADD COLUMN IF NOT EXISTS target_department_id BIGINT NULL,
    ADD COLUMN IF NOT EXISTS target_ministry_ids BIGINT[] NULL,
    ADD COLUMN IF NOT EXISTS target_department_ids BIGINT[] NULL;

-- Обеспечение наличия department_id у пользователей
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS department_id BIGINT NULL;

-- Индексы для ускорения таргетированных выборок
CREATE INDEX IF NOT EXISTS idx_publications_target_department
    ON publications (target_department_id)
    WHERE deleted_at IS NULL AND target_audience = 'department';

CREATE INDEX IF NOT EXISTS idx_users_department
    ON users (department_id)
    WHERE department_id IS NOT NULL;
```

---

## 3. Модели предметной области (Domain Models)

Файл: `internal/domain/publication.go`

### Константы аудитории:
```go
const (
    PublicationAudienceAll        = "all"
    PublicationAudienceMinistry   = "ministry"
    PublicationAudienceBranch     = "branch"
    PublicationAudienceDepartment = "department"
)
```

### Структуры запросов и ответов:
```go
type CreatePublicationRequest struct {
    ...
    TargetAudience      string  `json:"target_audience" binding:"required,oneof=all ministry branch department"`
    TargetMinistryID    *int64  `json:"target_ministry_id,omitempty"`
    TargetDepartmentID  *int64  `json:"target_department_id,omitempty"`
    TargetMinistryIDs   []int64 `json:"target_ministry_ids,omitempty"`
    TargetDepartmentIDs []int64 `json:"target_department_ids,omitempty"`
    ...
}

type UpdatePublicationRequest struct {
    ...
    TargetAudience      string  `json:"target_audience" binding:"required,oneof=all ministry branch department"`
    TargetMinistryID    *int64  `json:"target_ministry_id,omitempty"`
    TargetDepartmentID  *int64  `json:"target_department_id,omitempty"`
    TargetMinistryIDs   []int64 `json:"target_ministry_ids,omitempty"`
    TargetDepartmentIDs []int64 `json:"target_department_ids,omitempty"`
    ...
}

type PublicationResponse struct {
    ...
    TargetAudience      string  `json:"target_audience"`
    TargetMinistryID    *int64  `json:"target_ministry_id,omitempty"`
    TargetDepartmentID  *int64  `json:"target_department_id,omitempty"`
    TargetMinistryIDs   []int64 `json:"target_ministry_ids,omitempty"`
    TargetDepartmentIDs []int64 `json:"target_department_ids,omitempty"`
    ...
}
```

---

## 4. Логика видимости и безопасность (Security & Visibility)

Файл: `internal/service/publication_security.go`

Функция `publicationVisible(pub *domain.Publication, user *domain.User, now time.Time) bool`:
1. **Автор и SuperAdmin**: Всегда имеют доступ к публикации независимо от подразделения (`pub.AuthorID == user.ID || user.Role == domain.RoleSuperAdmin`).
2. **Таргетинг на филиал (`ministry` / `branch`)**:
   - Требует наличия `user.MinistryID`.
   - Проверяет равенство с `pub.TargetMinistryID` либо вхождение `*user.MinistryID` в массив `pub.TargetMinistryIDs`.
3. **Таргетинг на отдел (`department`)**:
   - Требует наличия `user.DepartmentID`.
   - Проверяет равенство с `pub.TargetDepartmentID` либо вхождение `*user.DepartmentID` в массив `pub.TargetDepartmentIDs`.
4. **Таргетинг на всю компанию (`all`)**:
   - Доступно всем авторизованным сотрудникам.

---

## 5. Выборка и фильтрация в репозитории

Файл: `internal/repository/publication_repository.go`

### 1. Лента публикаций (`List`):
* Автоматически учитывает подразделения текущего пользователя (`currentUserMinistryID`, `currentUserDepartmentID`).
* Для обычных пользователей формируется `WHERE`-условие:
  ```sql
  (
    target_audience = 'all'
    OR author_id = $currentUserID
    OR (target_audience IN ('ministry', 'branch') AND (target_ministry_id = $mID OR $mID = ANY(target_ministry_ids)))
    OR (target_audience = 'department' AND (target_department_id = $dID OR $dID = ANY(target_department_ids)))
  )
  ```
* Поддерживает фильтрацию по URL-параметрам `?department_id=X` и `?ministry_id=Y`: новость попадает в выборку, если она создана от имени подразделения или таргетирована на него.

### 2. Баннеры (`ListBanners`):
* Закрепленные плашки-баннеры отображаются только пользователям, подходящим под условия аудитории (включая проверку `department_id` и `target_department_ids`).

### 3. Оповещения и рассылка (`GetTargetAudienceRecipients`):
* При создании рассылки для важной/срочной новости пуши и email отправляются строго сотрудникам целевых филиалов или отделов.
* Подсчет охвата пользователей в статистике (`GetBroadcastStats`) рассчитывает точный объем аудитории выбранных подразделений.

---

## 6. Примеры API

### Создание новости для конкретного отдела:
```http
POST /api/v1/publications
Content-Type: application/json
Authorization: Bearer <token>

{
  "source_type": "department",
  "type": "news",
  "title": "План спринта отдела разработки",
  "content": "Детали задач на ближайшие 2 недели...",
  "target_audience": "department",
  "target_department_id": 42,
  "importance": "normal"
}
```

### Создание новости для нескольких филиалов:
```http
POST /api/v1/publications
Content-Type: application/json
Authorization: Bearer <token>

{
  "source_type": "ministry",
  "type": "announcement",
  "title": "Семинар для региональных филиалов",
  "content": "Приглашаем сотрудников филиалов Астана и Алматы...",
  "target_audience": "branch",
  "target_ministry_ids": [101, 102],
  "importance": "important"
}
```

### Получение ленты с фильтрацией по отделу:
```http
GET /api/v1/publications?department_id=42&page=1&limit=20
Authorization: Bearer <token>
```
