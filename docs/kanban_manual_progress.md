# Kanban Manual Progress

`boards.manual_completion_percent` хранит бизнес-прогресс доски отдельно от технического процента закрытых задач.

Правило расчета для `GET /api/v1/kanban/boards`:

- если `manual_completion_percent` заполнен, в `progress.completion_percent` возвращается это значение;
- если `manual_completion_percent` равен `NULL`, процент считается автоматически по `completed_tasks / total_tasks`;
- `total_tasks` и `completed_tasks` всегда остаются фактическими значениями по задачам.

Это нужно для досок, где бизнес-прогресс проекта не совпадает с количеством закрытых kanban-задач, например для seed-досок крупных проектов из Excel.
