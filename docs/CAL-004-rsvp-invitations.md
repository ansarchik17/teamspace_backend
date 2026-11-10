# CAL-004 — RSVP приглашения и ответы

Backend поддерживает трёхуровневую систему ответов на приглашения: **принять** (`accepted`), **отклонить** (`declined`) и **возможно** (`tentative`). Участник может изменить свой статус в любой момент, а организатор видит актуальный список всех участников с их текущими статусами. Все указанные ниже маршруты требуют Bearer-токен.

Текущий `workspace-front` пока не вызывает новый маршрут списка участников и не показывает отдельную кнопку «Возможно»; примеры UI ниже — инструкция для подключения, а не описание уже работающего экрана.

## Ответить на приглашение

```http
POST /api/v1/calendar/events/{event_id}/respond
Content-Type: application/json
Authorization: Bearer <token>

{
  "status": "accepted",
  "comment": "Буду там"
}
```

### Параметры

- **status** (обязательный): `accepted` | `declined` | `tentative`
- **comment** (опциональный): текстовый комментарий участника, максимум 1000 символов; если не передан, прежний комментарий сохраняется

### Логика обработки

- При **любом** статусе (`accepted`, `declined`, `tentative`) участник остаётся в таблице `event_attendees` с обновлённым полем `status` и `updated_at`
- При `declined` организатору отправляется уведомление о том, что участник отклонил приглашение
- Напоминания об этом событии не отправляются участнику со статусом `declined`; запись настройки напоминания сохраняется, чтобы снова работать после смены ответа
- При повторном вызове с другим статусом предыдущий статус перезаписывается (участник может передумать)
- Повторный запрос с тем же статусом и комментарием не изменяет `updated_at`, не создаёт повторное событие RSVP и не дублирует уведомление об отказе
- При редактировании организатором времени/описания/списка гостей ответы сохранённых участников не сбрасываются; только новые гости получают `pending`
- Организатор события не может ответить на своё же приглашение (возвращается `200 OK` без изменений)
- Backend пытается пометить уведомления о приглашении прочитанными после ответа; ошибка этой фоновой операции не отменяет уже записанный RSVP

### Коды ответа

- `200` — статус успешно обновлён
- `400` — неверный формат запроса, недопустимый статус или комментарий длиннее 1000 символов
- `403` — пользователь не приглашён на это событие
- `404` — событие не найдено

### Пример ответа

```json
{
  "message": "ok"
}
```

## Получить список участников

```http
GET /api/v1/calendar/events/{event_id}/attendees
Authorization: Bearer <token>
```

Возвращает список всех участников события с их текущими статусами, информацией о пользователях и пересчитанной сводкой. Доступно организатору и всем участникам события. Для получения свежего списка после ответа вызовите этот endpoint повторно.

Пример ниже — ответ **организатору**. Для обычного приглашённого backend скрывает комментарии и личные поля профилей других участников (например, email, ИИН и должность); остаются имя, ник, аватар и статус RSVP. Свой комментарий приглашённый видит.

### Коды ответа

- `200` — список участников
- `400` — неверный формат ID события
- `403` — у пользователя нет доступа к этому событию
- `404` — событие не найдено

### Пример ответа

```json
{
  "attendees": [
    {
      "event_id": "550e8400-e29b-41d4-a716-446655440000",
      "user_id": 123,
      "status": "accepted",
      "comment": "Буду там",
      "created_at": "2026-09-20T10:00:00Z",
      "updated_at": "2026-09-21T14:30:00Z",
      "user": {
        "id": 123,
        "username": "john.doe",
        "email": "john.doe@example.com",
        "first_name": "John",
        "last_name": "Doe",
        "full_name": "John Doe",
        "avatar_url": "https://example.com/avatars/123.jpg",
        "position": "Senior Developer",
        "is_active": true
      }
    },
    {
      "event_id": "550e8400-e29b-41d4-a716-446655440000",
      "user_id": 456,
      "status": "tentative",
      "comment": "Может опоздаю",
      "created_at": "2026-09-20T10:00:00Z",
      "updated_at": "2026-09-22T09:15:00Z",
      "user": {
        "id": 456,
        "username": "jane.smith",
        "email": "jane.smith@example.com",
        "first_name": "Jane",
        "last_name": "Smith",
        "full_name": "Jane Smith",
        "avatar_url": "https://example.com/avatars/456.jpg",
        "position": "Product Manager",
        "is_active": true
      }
    },
    {
      "event_id": "550e8400-e29b-41d4-a716-446655440000",
      "user_id": 789,
      "status": "declined",
      "comment": null,
      "created_at": "2026-09-20T10:00:00Z",
      "updated_at": "2026-09-23T11:00:00Z",
      "user": {
        "id": 789,
        "username": "bob.jones",
        "email": "bob.jones@example.com",
        "first_name": "Bob",
        "last_name": "Jones",
        "full_name": "Bob Jones",
        "avatar_url": null,
        "position": "Designer",
        "is_active": true
      }
    }
  ],
  "summary": {
    "total": 3,
    "confirmed": 1,
    "pending": 0,
    "tentative": 1,
    "declined": 1
  }
}
```

## Статусы участников

| Статус | Значение | UI отображение |
|--------|----------|----------------|
| `pending` | Приглашён, ответа нет | "Ожидает ответа" / "Pending" |
| `accepted` | Принял приглашение | "Принял" / "Accepted" / ✓ |
| `declined` | Отклонил приглашение | "Отклонил" / "Declined" / ✗ |
| `tentative` | Возможно придёт | "Возможно" / "Maybe" / ? |

## Подсчёт подтверждённых участников

`summary.confirmed` считает только участников со статусом `accepted` (включая организатора). `tentative` не считается подтверждением. При необходимости фронт может пересчитать те же значения из `attendees`:

```javascript
// Подсчёт принятых приглашений
const acceptedCount = attendees.filter(a => a.status === 'accepted').length;

// Подсчёт всех, кто не отклонил (pending + accepted + tentative)
const nonDeclinedCount = attendees.filter(a => a.status !== 'declined').length;

// Подсчёт по статусам для детальной статистики
const stats = {
  accepted: attendees.filter(a => a.status === 'accepted').length,
  tentative: attendees.filter(a => a.status === 'tentative').length,
  declined: attendees.filter(a => a.status === 'declined').length,
  pending: attendees.filter(a => a.status === 'pending').length
};
```

## События для повторяющихся встреч

При ответе на приглашение повторяющегося события статус применяется ко **всей серии**, а не к одному экземпляру. Изменённые или удалённые экземпляры серии (см. [CAL-003](./CAL-003-recurrent-events.md)) используют тот же список участников, что и основная серия.

Для создания отдельного списка участников для одного экземпляра необходимо создать отдельное событие.

## Ограничения и безопасность

- Только приглашённые участники могут отвечать на приглашение (проверка по таблице `event_attendees`)
- Организатор события всегда считается "accepted" и не может изменить свой статус
- Список участников видят только организатор и сами участники события
- Все запросы параметризованы для предотвращения SQL-инъекций
- Constraint в БД гарантирует, что статус может быть только: `pending`, `accepted`, `declined`, `tentative`

## Миграция БД

Добавлен constraint для проверки допустимых статусов:

```sql
ALTER TABLE event_attendees
    ADD CONSTRAINT event_attendees_status_check
    CHECK (status IN ('pending', 'accepted', 'declined', 'tentative')) NOT VALID;

UPDATE event_attendees SET status = 'pending'
WHERE status NOT IN ('pending', 'accepted', 'declined', 'tentative');

ALTER TABLE event_attendees VALIDATE CONSTRAINT event_attendees_status_check;
```

Существующие поля:
- `event_id` — UUID события
- `user_id` — ID участника
- `status` — статус ответа (по умолчанию `pending`)
- `comment` — опциональный комментарий
- `created_at` — дата приглашения
- `updated_at` — дата последнего изменения статуса

## Пример интеграции на фронтенде

### Ответ на приглашение

```typescript
async function respondToInvite(eventId: string, status: 'accepted' | 'declined' | 'tentative', comment?: string) {
  const response = await fetch(`/api/v1/calendar/events/${eventId}/respond`, {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${token}`,
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({ status, comment })
  });

  if (!response.ok) {
    throw new Error('Failed to respond to invite');
  }

  return await response.json();
}
```

### Получение списка участников

```typescript
interface Attendee {
  event_id: string;
  user_id: number;
  status: 'pending' | 'accepted' | 'declined' | 'tentative';
  comment?: string;
  created_at: string;
  updated_at?: string;
  user: {
    id: number;
    username: string;
    email: string;
    first_name: string;
    last_name: string;
    full_name: string;
    avatar_url?: string;
    position?: string;
    is_active: boolean;
  };
}

async function getEventAttendees(eventId: string): Promise<Attendee[]> {
  const response = await fetch(`/api/v1/calendar/events/${eventId}/attendees`, {
    headers: {
      'Authorization': `Bearer ${token}`
    }
  });

  if (!response.ok) {
    throw new Error('Failed to fetch attendees');
  }

  const data = await response.json();
  return data.attendees;
}
```

### UI компонент с кнопками ответа

```tsx
function EventInviteActions({ eventId }: { eventId: string }) {
  const [responding, setResponding] = useState(false);

  const handleRespond = async (status: 'accepted' | 'declined' | 'tentative') => {
    setResponding(true);
    try {
      await respondToInvite(eventId, status);
      // Обновить UI или перезагрузить список событий
    } catch (error) {
      console.error('Failed to respond:', error);
    } finally {
      setResponding(false);
    }
  };

  return (
    <div className="invite-actions">
      <button onClick={() => handleRespond('accepted')} disabled={responding}>
        ✓ Принять
      </button>
      <button onClick={() => handleRespond('tentative')} disabled={responding}>
        ? Возможно
      </button>
      <button onClick={() => handleRespond('declined')} disabled={responding}>
        ✗ Отклонить
      </button>
    </div>
  );
}
```

### Отображение списка участников с иконками статусов

```tsx
function AttendeesList({ eventId }: { eventId: string }) {
  const [attendees, setAttendees] = useState<Attendee[]>([]);

  useEffect(() => {
    getEventAttendees(eventId).then(setAttendees);
  }, [eventId]);

  const getStatusIcon = (status: string) => {
    switch (status) {
      case 'accepted': return '✓';
      case 'declined': return '✗';
      case 'tentative': return '?';
      default: return '⏳';
    }
  };

  return (
    <div className="attendees-list">
      <h3>Участники ({attendees.length})</h3>
      {attendees.map(attendee => (
        <div key={attendee.user_id} className={`attendee status-${attendee.status}`}>
          <img src={attendee.user.avatar_url} alt={attendee.user.full_name} />
          <div>
            <div className="name">{attendee.user.full_name}</div>
            {attendee.user.position && <div className="position">{attendee.user.position}</div>}
            {attendee.comment && <div className="comment">{attendee.comment}</div>}
          </div>
          <span className="status-badge">
            {getStatusIcon(attendee.status)} {attendee.status}
          </span>
        </div>
      ))}
    </div>
  );
}
```
