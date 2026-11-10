# Company Workspace hosts

Workspace company hosts use `<organization-slug>.teamspace.kz`. For example,
Sultan Corp uses `https://sultan-corp.teamspace.kz`. The company slug is the
same slug used for its mail domain. `teamspace.kz` remains the platform entry.

## Before enabling the frontend switch

1. Confirm the wildcard A record reaches the Workspace server. The wildcard
   MX and SPF records used by Mail can coexist with the Workspace HTTPS host.
2. Install an automatically renewable TLS certificate covering
   `*.teamspace.kz` using DNS-01. The existing root-domain certificate does not
   cover company names unless its SAN list explicitly includes the wildcard.
3. The live `server_name *.teamspace.kz` belongs to Growy and currently sends
   `/` to `/growy/`. The candidate
   `deploy/production/nginx/tenant-workspace-wildcard.conf` keeps the Growy and
   ODO includes, Office, LiveKit and Keycloak routes, while sending `/` and
   `/web/` to the Workspace frontend. The exact `mail.teamspace.kz` and
   `inbox.teamspace.kz` virtual hosts remain separate.
4. Send `/api/` through the Workspace frontend, just as the root host does.
   Its internal Nginx routes Workspace API, AI and other integrations, and
   preserves the original `Host` for the API's tenant check. A direct host
   proxy for all of `/api/` would bypass those integration routes.
5. Ensure Redis is available to the API. Root-to-tenant login uses a 30-second
   single-use code stored in Redis. It fails closed when Redis is unavailable.
6. The browser login page is always `https://teamspace.kz/web/login`. A visitor
   opening a company host without a session is sent there, then returned to
   their company's host by the single-use handoff after authentication.
   Automatic Keycloak SSO on company hosts remains disabled; keep its callback
   on the platform host rather than adding a broad redirect URI wildcard.

## Server activation

The backend deployment places the candidate file in `/opt/workspace/nginx/`
but does not change the live Nginx virtual host. After the frontend central
login change is deployed, run as root on the Workspace server:

```sh
/opt/workspace/scripts/activate-tenant-workspace-nginx.sh
```

The script backs up `/etc/nginx/conf.d/growy-tenant-wildcard.conf`, checks the
wildcard certificate and included files, runs `nginx -t`, and reloads Nginx.
If validation or reload fails, it restores the prior file. Confirm that
`https://sultan-corp.teamspace.kz/web/` serves Workspace and that
`https://sultan-corp.teamspace.kz/growy/` still serves Growy before enabling
the frontend switch. Confirm `https://teamspace.kz/web/login` remains available.

The production frontend workflow pins `VITE_TENANT_WORKSPACE_ENABLED=false`
until the HTTPS host and API route are ready. After completing the steps above,
change the build argument in `.github/workflows/frontend.yml` to use the
repository variable `VITE_TENANT_WORKSPACE_ENABLED`, set that variable to
`true`, and rebuild/deploy the frontend. This
causes authenticated company users on the root host to move to their own host
using the one-time code. Platform administrators remain on the root host.

The API validates the issued session against the tenant host at login and
refresh, and validates every protected API request against the authenticated
user's organization. It does not trust the URL alone as the tenant identity.

Do not remove the root hostname during rollout: it is the platform entry and
the origin that starts the one-time handoff. To stop automatic handoff, set
`VITE_TENANT_WORKSPACE_ENABLED=false` and redeploy the frontend.
