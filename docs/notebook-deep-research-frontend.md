# Deep Research: что поменять на фронтенде

Страница: **Документы → Drive → Deep Research → блокнот** (`/web/drive/notebook?chat=<id>`).
Бэкенд готов, подробности пайплайна: [notebook-deep-research.md](notebook-deep-research.md).
Все изменения API обратно совместимы: старый фронт продолжает работать, но без новых возможностей.

## Коротко: что сделать

1. Карточки Студии брать из `GET /notebook/studio/presets` и отправлять `preset` вместо собранного на фронте текста.
2. В `POST /notebook/chats/:id/ask` передавать `lang` и `persist: true`, убрать вызов `/sync` после ответа.
3. По `status` решать, сохранять ли результат в «Результаты» Студии. Отказы (`no_sources`, `unreadable`) **не сохранять**.
4. Кнопки под ответом брать из `suggestions`.
5. Показывать индикатор глубокого анализа и бейдж модели (KazLLM).
6. Все вопросы чата отправлять только в `/notebook/chats/:id/ask` — или в его фоновый вариант `/notebook/chats/:id/ask/jobs` с прогрессом (раздел 6).

### Что нового после слияния с main (23.10.2026)

Изменений в форме API нет; уточнилось поведение в ошибочных случаях:

- Строковые поля запроса (`question`, `focus`, `operation_id`, `preset`, `lang`, `mode`, `format`) с символом `\u0000` → **400** `VALIDATION_ERROR`. Перед отправкой вычищайте его (бывает при вставке текста из бинарных файлов): `text.replace(/\u0000/g, '')`.
- Фоновая задача больше не «зависает» в `running`: если на сервере произошёл внутренний сбой (хранилище файлов недоступно, результат не удалось сохранить), задача сразу переходит в `failed` с `error.http_status = 500`. Раньше такая задача висела до 2 минут и блокировала новые запуски ошибкой 409.
- Повторить задачу в `failed` можно только с **новым** `operation_id`: тот же `operation_id` вернёт ту же завершённую задачу (200).
- Фрагменты для ответов на больших документах теперь подбираются с учётом смыслового поиска; на фронте ничего менять не нужно.
- KazLLM может быть отключён на сервере (`ALEM_KAZ_MODEL=`). Тогда `model` всегда `openai/gpt-oss-120b`; бейдж KazLLM показывать только по фактическому `model`, а не по языку.

---

## 1. Типы

```ts
type NotebookMode = 'fast' | 'thinking';          // «Кратко» / «Подробно»
type NotebookFormat =
  | 'text' | 'report' | 'compare' | 'table' | 'presentation'
  | 'cards' | 'test' | 'chart' | 'mermaid';
type NotebookStatus = 'answered' | 'partial' | 'not_found' | 'no_sources' | 'unreadable';

interface StudioPreset {
  id: string;               // report, presentation, table, chart, cards, test, mermaid, compare, summary, risks, timeline, checklist, faq
  format: NotebookFormat;   // в каком виде рендерить результат
  title: string;            // уже локализован (ru/kz)
  description: string;
  prompt: string;           // шаблон, только для подсказки/tooltip; отправлять не нужно
  min_sources: number;      // compare = 2
  count?: { default: number; max: number }; // есть у presentation, cards, test, faq
}

interface AskRequest {
  question?: string;        // обязателен, если нет preset
  file_ids: string[];       // выбранные источники (чекбоксы слева / «N источника»)
  mode?: NotebookMode;
  format?: NotebookFormat;  // игнорируется, если передан preset
  preset?: string;          // id карточки Студии
  count?: number;           // 1..30, сервер ограничит максимумом пресета
  focus?: string;           // тема пресета (если пусто — используется question)
  lang?: 'ru' | 'kz';       // текущий язык интерфейса (переключатель RU/KZ)
  persist?: boolean;        // true — сервер сам сохранит сообщения в историю
  operation_id?: string;    // uuid на одно нажатие «Отправить»; при ретрае того же запроса — тот же
}

interface NotebookSourceExcerpt { start_line: number; end_line: number; text: string }
interface NotebookSource {
  id: string;               // S1, S2…
  file_id: string;
  file_name: string;
  revision?: string;
  status: 'ready' | 'unreadable' | 'empty';
  message?: string;         // человекочитаемое пояснение (частичный анализ, пароль PDF и т.п.)
  complete: boolean;
  total_lines: number;
  excerpts: NotebookSourceExcerpt[];
}

interface AskResponse {
  content: string;          // Markdown со ссылками на источники
  format: NotebookFormat;
  model?: string;           // 'astanahub/alemllm' = KazLLM
  fallback: boolean;
  sources: NotebookSource[];
  status: NotebookStatus;
  suggestions?: string[];   // 2–3 следующих действия
  stats?: { mode: 'single' | 'deep'; chunks_total: number; chunks_analyzed: number };
  persisted?: boolean;      // true — сообщения уже в истории
}

type JobStatus = 'queued' | 'running' | 'done' | 'failed' | 'cancelled';
type JobStage = 'queued' | 'reading' | 'analyzing' | 'answering' | 'verifying' | 'done';

interface JobProgress { stage: JobStage; chunks_done: number; chunks_total: number }

interface JobError {
  http_status: number;      // какой статус вернул бы синхронный /ask
  code: string;             // AI_QUOTA_EXCEEDED, AI_ACCESS_DISABLED, NOTEBOOK_JOB_INTERRUPTED…
  message: string;
  body?: unknown;           // полное тело ошибки /ask — можно отдать в общий обработчик AI-ошибок
}

interface AskJob {
  job_id: string;
  chat_id: string;
  operation_id: string;
  status: JobStatus;
  progress: JobProgress;
  result?: AskResponse;     // только при status = 'done'
  error?: JobError;         // только при status = 'failed'
  created_at: string;
  updated_at: string;
  started_at?: string;
  finished_at?: string;
}

// WebSocket: { type: 'notebook.research.progress', content: AskJobEvent, … }
type AskJobEvent = Omit<AskJob, 'result' | 'created_at' | 'updated_at' | 'started_at' | 'finished_at'>;
```

## 2. Студия: карточки как готовые промты

**Загрузка каталога** — при открытии блокнота и при смене языка:

```ts
const presets = await api.get<StudioPreset[]>(`/notebook/studio/presets?lang=${lang}`);
```

Первые 8 карточек соответствуют текущим кнопкам: Отчёт, Презентация, Таблица, График, Карточки, Тест, Диаграмма, Сравнение. Ещё 5 новых: Краткое резюме, Риски, Хронология, Чек-лист, FAQ. Их можно показать под кнопкой «Ещё» или вторым рядом.

**Клик по карточке:**
- если у пресета есть `count`, показать маленький поповер с числом (по умолчанию `count.default`, максимум `count.max`) и необязательным полем «Тема»;
- если выбрано источников меньше `min_sources`, сделать карточку disabled с подсказкой «Выберите минимум 2 источника»;
- если источников 0 — карточка disabled, подсказка «Добавьте источники»;
- отправить:

```ts
await api.post<AskResponse>(`/notebook/chats/${chatId}/ask`, {
  preset: preset.id,
  count,                       // только если у пресета есть count
  focus: topic || undefined,   // или текст из поля ввода чата, если пользователь его заполнил
  file_ids: selectedIds,
  mode,
  lang,
  persist: true,
  operation_id: crypto.randomUUID(),
});
```

Текст запроса на фронте собирать **не нужно**: сервер подставляет промт пресета и формат.

**Рендер результата** по `response.format` (как сейчас): `cards`, `test`, `table`, `chart` (JSON-блок), `mermaid`, `presentation` (слайды через `---`), остальное — Markdown.

## 3. Статусы: что сохранять в «Результаты»

| `status` | Показать в чате | Добавить в «Результаты» Студии | Примечание в UI |
|---|---|---|---|
| `answered` | ответ | да | — |
| `partial` | ответ | да | жёлтая плашка «Часть документа не проанализирована» + `sources[].message` |
| `not_found` | ответ | нет (или серой карточкой) | «В документах нет сведений по вопросу» |
| `no_sources` | подсказку | **нет** | кнопка «Выбрать из Drive» |
| `unreadable` | список файлов | **нет** | подсветить нечитаемые источники слева (`sources[].status !== 'ready'`) |

Это убирает баг со скриншота, где в «Результаты · 1» попала карточка «К сожалению, в текущем запросе отсутствуют…».

## 4. История: `persist: true` вместо `/sync`

- Отправлять `persist: true` во всех запросах `/ask`.
- Если `response.persisted === true`, **не вызывать** `POST /notebook/chats/:id/sync`, иначе сообщения задвоятся.
- Если `persisted` нет или `false` (например, сбой БД), можно вызвать `/sync` как раньше.
- Для пресета в историю пишется название карточки и тема, например «Карточки: бюджет».
- Сервер не сохраняет `no_sources` и `unreadable`. Показывайте их в чате локально.

Память диалога работает сама: сервер берёт последние 6 сообщений из истории. Поэтому история должна сохраняться (через `persist` или `/sync`), иначе уточнения вроде «а подробнее по второму пункту?» не поймут контекст.

## 5. Кнопки под ответом

Сейчас «Составь чек-лист по ответу» и «Сравни приоритеты» захардкожены. Вместо этого:

```tsx
{response.suggestions?.map(s => (
  <Chip key={s} onClick={() => ask({ question: s })}>{s}</Chip>
))}
```

Клик отправляет текст как обычный `question` (формат `text`, без пресета).

## 6. Долгие ответы: фоновые задачи с прогрессом

Для больших документов (> 150 000 символов) сервер анализирует их по частям, ответ идёт **до ~3 минут**. Синхронный `/ask` при этом держит соединение, и прокси может его оборвать. Поэтому рекомендуемый путь — фоновая задача. Синхронный `/ask` продолжает работать как раньше (таймаут клиента ≥ 240 с).

**Запуск** — то же тело, что у `/ask`:

```ts
const operationId = crypto.randomUUID();               // одно нажатие «Отправить»
const res = await api.post<AskJob>(`/notebook/chats/${chatId}/ask/jobs`, { ...askBody, operation_id: operationId });
// 202 — задача создана; 200 — задача с этим operation_id уже была (ретрай после сетевой ошибки)
let job = res.data;
```

Ошибки проверки (нет вопроса, нет доступа, лимит доступа к ИИ, чужой блокнот) приходят **сразу** в ответе POST, тем же форматом, что у `/ask`.

`409 CONFLICT` — у пользователя уже идёт задача (одна на пользователя). В `details.job_id`/`details.chat_id` — активная задача: предложить «Дождаться» (подписаться на неё) или «Отменить».

**Прогресс** — WebSocket плюс опрос:

```ts
ws.on('notebook.research.progress', (msg) => {
  const e = msg.content as AskJobEvent;
  if (e.job_id !== job.job_id) return;
  render(e.status, e.progress);
  if (e.status === 'done' || e.status === 'failed') refresh();   // результат в событии не приходит
});

// Опрос обязателен: событие может не дойти (другой сервер, переподключение).
// Каждые 3 с, пока статус queued/running; при живом WebSocket можно реже (10 с).
async function refresh() {
  job = (await api.get<AskJob>(`/notebook/chats/${chatId}/ask/jobs/${job.job_id}`)).data;
  render(job.status, job.progress);
  if (job.status === 'done') showAnswer(job.result!);     // дальше как с ответом /ask: status, suggestions, persisted…
  if (job.status === 'failed') showAiError(job.error!);   // http_status + code, как ошибки /ask
}
```

**Индикатор** по `progress.stage`:

| `stage` | Текст |
|---|---|
| `queued` | «В очереди…» |
| `reading` | «Читаю документы…» |
| `analyzing` | «Анализирую по частям: {chunks_done} из {chunks_total}» + полоса прогресса |
| `answering` | «Формирую ответ…» |
| `verifying` | «Проверяю ответ…» (режим «Подробно») |

После `done`, если `result.stats.mode === 'deep'`, показывать подпись «Глубокий анализ: проанализировано {chunks_analyzed} из {chunks_total} частей» по `result.stats` (там считаются все части документа, поэтому число может отличаться от `progress.chunks_total`).

**Стоп**: `DELETE /notebook/chats/:id/ask/jobs/:job_id` → задача со `status: 'cancelled'`. Уже выполненные шаги остаются списанными. Повторный DELETE безопасен; если задача успела завершиться, вернётся её итог.

**Перезагрузка страницы**: сохранить `job_id` в `sessionStorage` по `chat_id`; после загрузки сделать GET и продолжить показ прогресса. Результат хранится 7 дней.

**Ошибки задачи**: `error.code === 'NOTEBOOK_JOB_INTERRUPTED'` — сервер перезапускался; кнопка «Повторить» отправляет запрос с **новым** `operation_id`. То же для любой задачи в `failed` (например, `http_status: 500` — внутренний сбой): повтор только с новым `operation_id`. Остальные коды — как в разделе 8. Повтор после сетевой ошибки **самого POST** (ответа не было) — с тем же `operation_id`: сервер вернёт уже созданную задачу (200) вместо второй платной.

`job_id` можно хранить в `localStorage` (а не только в `sessionStorage`), чтобы прогресс восстанавливался и в новой вкладке. Если GET вернул 404 (задачу удалили через 7 дней или удалили блокнот) — очистить сохранённый `job_id`.

Синхронный `/ask` (если остаётся где-то): прогресса нет, показывать шаги по таймеру; «Стоп» — `AbortController`; таймаут клиента ≥ 240 с.

## 7. Язык и KazLLM

- Передавать `lang` по переключателю RU/KZ в шапке.
- Казахский вопрос (или `lang: 'kz'`, или казахские документы) в режиме «Кратко» обрабатывает **KazLLM** (`astanahub/alemllm`). Ответ приходит на языке вопроса.
- Если `response.model === 'astanahub/alemllm'`, можно показать бейдж «KazLLM» рядом со временем ответа. Если `fallback: true`, бейдж не показывать: ответ дала резервная модель.
- Режим «Подробно» использует модели рассуждения, KazLLM там не применяется.

## 8. Ошибки

Как раньше. Кредиты и лимиты возвращаются унифицированным форматом AI-ошибок:

| HTTP | `code` | UI |
|---|---|---|
| 429 | `AI_CREDIT_LIMIT` | «Лимит ИИ исчерпан» + время восстановления, если есть |
| 403 | `AI_CHAT_ACCESS` | «Нет доступа к ИИ» |
| 400 | `VALIDATION_ERROR` | валидация: нет вопроса и пресета, неизвестный пресет, сравнение с < 2 источниками, символ `\u0000` в тексте |
| 409 | `CONFLICT` | только `/ask/jobs`: уже идёт задача, см. `details.job_id` |
| 500 | `INTERNAL_ERROR` | «Не удалось выполнить анализ», кнопка «Повторить» (для задачи — с новым `operation_id`) |
| 504/сеть | — | «Не удалось получить ответ», кнопка «Повторить» с тем же `operation_id` |

## 9. Ссылки на источники

Без изменений. Ссылки в `content` ведут на `/web/drive/notebook?chat=…&source=…&start_line=…&end_line=…&revision=…`. Фрагмент открывать через `GET /notebook/chats/:id/sources/:doc_id?start_line=&end_line=&revision=`. Текст «(ссылка на фрагмент не подтверждена)» — это нормальная серверная пометка, ссылкой её делать не нужно.

## Чек-лист приёмки

- [ ] Карточки Студии рендерятся из `/studio/presets`, при RU/KZ меняются названия.
- [ ] Карточки с `count` спрашивают количество, «Сравнение» неактивно при одном источнике.
- [ ] В пустом блокноте любая карточка или вопрос не добавляет ничего в «Результаты».
- [ ] После ответа нет запроса `/sync`, если `persisted: true`; история не задваивается после перезагрузки.
- [ ] Чипы под ответом берутся из `suggestions` и отправляют вопрос.
- [ ] Вопрос по документу > 150 тыс. символов идёт через `/ask/jobs`: полоса «Анализирую по частям: N из M» растёт, ответ приходит без таймаута, есть подпись «Глубокий анализ…».
- [ ] При отключённом WebSocket прогресс и результат всё равно приходят опросом GET.
- [ ] «Стоп» во время анализа переводит задачу в `cancelled`, ответ не появляется.
- [ ] Перезагрузка страницы во время анализа продолжает показывать прогресс той же задачи.
- [ ] Второй запуск во время активной задачи показывает 409 с предложением дождаться или отменить.
- [ ] Уточнение «а подробнее по второму пункту?» отвечает по контексту предыдущего ответа.
- [ ] Вопрос на казахском даёт ответ на казахском, `model = astanahub/alemllm`.
- [ ] `partial` показывает плашку; нечитаемый источник подсвечен слева.
- [ ] Вопрос с вставленным `\u0000` не даёт 400: символ вычищается до отправки.
- [ ] Задача в `failed` (в т.ч. 500) показывает ошибку и «Повторить» с новым `operation_id`; новый запуск после неё не получает 409.
