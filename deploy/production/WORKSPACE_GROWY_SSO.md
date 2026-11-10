# Workspace password session to Growy

Growy can accept a short-lived Ed25519 assertion from an authenticated
Workspace session. The assertion expires after 45 seconds and its `jti` is
consumed once in Growy's database. Keep the private key only in the
Workspace API environment; Growy receives only the public key. Never put either
key in frontend build variables or commit them to Git.

## Create the key pair on the server

Run once on the production server in a private shell:

```sh
umask 077
openssl genpkey -algorithm ED25519 -out /root/workspace-growy-sso.pem
WORKSPACE_SSO_PRIVATE_KEY_DER_B64="$(openssl pkey -in /root/workspace-growy-sso.pem -outform DER | base64 | tr -d '\n')"
WORKSPACE_SSO_PUBLIC_KEY_DER_B64="$(openssl pkey -in /root/workspace-growy-sso.pem -pubout -outform DER | base64 | tr -d '\n')"
```

Append the values to the existing production env files. The variables created
above stay in the current root shell, so they can be written without printing
the private key to the terminal. Do not put the private value in Growy's file:

```sh
printf '\nGROWY_SSO_PRIVATE_KEY_DER_B64=%s\nGROWY_SSO_ISSUER=https://teamspace.kz\n' \
  "$WORKSPACE_SSO_PRIVATE_KEY_DER_B64" >> /opt/workspace/.env
printf '\nWORKSPACE_SSO_PUBLIC_KEY_DER_B64=%s\nWORKSPACE_SSO_ISSUER=https://teamspace.kz\n' \
  "$WORKSPACE_SSO_PUBLIC_KEY_DER_B64" >> /opt/growy/.env
chmod 0640 /opt/workspace/.env
```

Keep the private key file and Workspace env file readable only by root and the
existing deployment group; preserve Growy's existing env-file ownership and
permissions. Back up the key securely: replacing it invalidates
tickets that have not yet expired. Do not run the append commands a second time;
that would add duplicate environment entries.

## Deploy and check

Deploy the updated Workspace backend, Growy backend, and Growy frontend. Recreate
the Workspace API and Growy API containers after changing their env files. The
browser opens `/growy/`; Growy first asks Workspace for a protected handoff,
then exchanges it at `/growy/api/token/workspace/`. If there is no valid
Workspace session, the existing Keycloak login flow remains the fallback.

Check the Workspace route while logged in; it should return HTTP 200 and a JSON
`ticket`. Check it while logged out; it should return HTTP 401. Then open
`https://teamspace.kz/growy/` in the same browser session. The handoff
endpoint must return HTTP 200 in the browser network panel, and the Growy token
exchange must return HTTP 200. Repeating the exact same ticket must return HTTP
401. Do not paste or log the ticket value.

## Общие CRM/WhatsApp API и проверка отзыва доступа

Дополнительно настройте в Workspace:

```dotenv
GROWY_PUBLIC_URL=https://teamspace.kz/growy
GROWY_ACCESS_CHECK_TOKEN=<общий случайный серверный секрет>
```

И в Growy:

```dotenv
WORKSPACE_ACCESS_CHECK_URL=https://teamspace.kz/api/internal/growy/access-check
WORKSPACE_ACCESS_CHECK_TOKEN=<тот же серверный секрет>
WORKSPACE_SSO_SESSION_SECONDS=300
```

Внутренний endpoint защищён bearer секретом и проверяет текущую запись пользователя, компанию и роль. Growy проверяет его на каждый HTTP запрос, refresh и WebSocket сообщение. При блокировке, смене компании/роли или недоступности проверки старый доступ закрывается. Access и refresh выдаются максимум на пять минут: затем нужен новый handoff через текущую Workspace сессию.

Workspace frontend может обращаться с обычной пользовательской авторизацией к:

- `/api/v1/growy/api/crm/leads/`;
- `/api/v1/growy/api/crm/deals/`;
- `/api/v1/growy/api/whatsapp/conversations/`;
- `/api/v1/growy/api/whatsapp/conversations/{id}/messages/`.

Workspace получает короткий токен Growy от имени текущего пользователя и пересылает запрос в его компании. Администраторские маршруты, чужие URL и client-selected tenant/role не допускаются. HTTP proxy не туннелирует WebSocket. Запросы ограничены 2 MiB; роли manager/tenant_admin имеют CRM доступ, employee/issuer автоматически не повышаются. Сопоставление должностей с Growy ролями остается источником прав.

Если email уже занят в Growy, SSO возвращает 409. После проверки принадлежности обоих аккаунтов администратор Growy связывает стабильный Workspace ID командой `python manage.py link_workspace_identity --workspace-user-id 123 --growy-user-id 456`. Автоматическое объединение по email не выполняется.

После изменений выполнить Growy миграции 0261 и 0262. Подробный контракт и команды проверки — в Growy `docs/workspace_growy_integration.md`. Перед deployment требуется сборка Workspace с настоящими зависимостями и проверка реального обмена с PostgreSQL. В локальном sandbox полная Go сборка не прошла из-за отсутствующих модулей и недоступного Go proxy; изолированные проверки контрактов и Django тесты прошли.
