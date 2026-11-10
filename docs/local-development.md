# Совместный локальный запуск Workspace

В локальной структуре `belesai/axis_workspace` фронтенд находится в
`workspace-front`, бэкенд — в `workspace-backend`. Общий запуск:

```sh
cd belesai/axis_workspace
npm run dev
```

Версионируемая реализация — `workspace-backend/scripts/local-dev.mjs`.
Локальная обёртка `belesai/scripts/local-dev.mjs` содержит:

```js
import '../axis_workspace/workspace-backend/scripts/local-dev.mjs'
```

Также можно вызвать скрипт напрямую из `axis_workspace`:

```sh
node workspace-backend/scripts/local-dev.mjs axis
```

Требуются работающий Docker Desktop, локальный `workspace-backend/.env`,
`belesai/docker-compose.local.yml` и существующие контейнеры инфраструктуры,
описанные в `belesai/ЗАПУСК.md`. Их данные при запуске не пересоздаются.

После свежего клонирования фронтенда скрипт сам выполняет `npm ci`, если нет
`node_modules/.bin/vite`. Не нужно отдельно устанавливать зависимости перед
первым общим запуском. Ошибка установки останавливает запуск с ненулевым кодом.
Исходники и lock-файл фронтенда при этом не изменяются.

Далее запускается инфраструктура, бэкенд `workspace-api`, проверяется
`http://localhost:18081/health`, и запускается Vite на
`http://localhost:5174/web/` с прокси API на локальный бэкенд.

- `npm run dev:build` — пересборка Docker-образа после изменений Go-кода.
- `npm run logs` — логи бэкенда.
- `Ctrl+C` — остановка Vite; бэкенд и инфраструктура остаются в Docker.
- `npm run stop` — остановка бэкенда.

Для прямого вызова доступны флаги `--build`, `--logs`, `--stop` после `axis`.
Существующий запуск SenimDesk через ту же локальную обёртку также сохранён.
