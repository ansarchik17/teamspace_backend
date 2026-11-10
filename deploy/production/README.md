# Production deployment

The VDS keeps secrets in `/opt/workspace/.env`. The repository supplies the
production Compose file and deployment scripts; it never contains real
passwords.

Backend logging uses the colored `console` format for the API, AI document
worker and migration command, including when viewed with
`docker logs -f workspace-api`. Levels and operation labels have distinct
colors; HTTP 2xx is green, 3xx blue, 4xx yellow and 5xx red. Console timestamps
include the date and timezone, and durations use readable units. Repeated
events are retained instead of sampled. The structured fields (request ID,
user ID, status, error details) remain available after each message.

Both production Compose variants explicitly set `LOG_FORMAT: console` so
an older `LOG_FORMAT=json` in `/opt/workspace/.env` does not disable colors.
For a JSON log collector, change that environment entry to `json` for the
API and worker. Outside Compose, `LOG_FORMAT=json` selects clean JSON.
Color changes apply after building and deploying the updated backend image.

For the ODO client at `teamspace.kz/odo`, Workspace remains the owner of
the organization tree and employee directory. The ODO browser token needs a
Workspace API audience (currently `alem-workspace`) in addition to its own
`workspace-odo-api` audience. Configure an access-token Audience mapper on
Keycloak client scope `workspace-odo-access` for that Workspace audience.
In `/opt/workspace/.env`, append `workspace-odo` to the existing
`KEYCLOAK_ALLOWED_AZP` comma-separated list and keep the Workspace audience
in `KEYCLOAK_TRUSTED_AUDIENCES`. Do not replace existing clients or accept the
ODO-only audience as a substitute for the Workspace API audience. After the
environment change, recreate just the Workspace API and check its health:

```bash
docker compose --env-file /opt/workspace/.env \
  --env-file /opt/workspace/releases/images.env \
  -f /opt/workspace/compose.prod.yml up -d --no-deps --force-recreate api
curl -fsS http://127.0.0.1:18081/health
docker exec workspace-api printenv KEYCLOAK_ALLOWED_AZP
```

Have the ODO user sign in again to receive a token with the new audience. If
the directory still responds 401, inspect `docker logs --since 5m workspace-api`
for `Token validation failed` before changing any other allowlist.

One-time setup on the VDS as `root`:

```bash
install -d -m 0750 -o actions -g actions \
  /opt/workspace /opt/workspace/releases /opt/workspace/scripts \
  /opt/workspace/secrets /opt/workspace/certs
touch /opt/workspace/.env
chown root:actions /opt/workspace/.env
chmod 0640 /opt/workspace/.env
install -o actions -g actions -m 0640 /dev/null /opt/workspace/releases/images.env
```

Copy `.env.example` to `/opt/workspace/.env`, replace every placeholder, and
merge the required application variables from the root `.env.example`.
Generate secrets with `openssl rand -hex 32`.

The API and frontend are bound only to localhost (`18081` and `13000`). Put a
host reverse proxy with HTTPS in front of them. PostgreSQL, Redis, RabbitMQ,
MinIO and Keycloak are not published to the public network.

LiveKit is included in `compose.prod.yml`. Set `LIVEKIT_API_KEY`,
`LIVEKIT_API_SECRET`, `LIVEKIT_URL=wss://teamspace.kz/livekit`, and
`LIVEKIT_API_URL=http://workspace-livekit:7880` in `/opt/workspace/.env`.
Install the included Nginx configuration so `/livekit` is proxied to the
container's signal port. Allow `7881/tcp` and `7882/udp` in the VDS firewall;
these are required for WebRTC media. The complete call verification
procedure is in `docs/livekit-setup.md`.

Stalwart's WebUI/API is also kept on localhost (`18083`). To publish it through
the separate mail hostname, create an `A` record for `mail.teamspace.kz`
pointing to the VDS address, then install the included virtual host:

```bash
install -m 0644 /opt/workspace/nginx/stalwart.conf \
  /etc/nginx/sites-available/stalwart
ln -sfn /etc/nginx/sites-available/stalwart \
  /etc/nginx/sites-enabled/stalwart
nginx -t
systemctl reload nginx
certbot --nginx --redirect -d mail.teamspace.kz
```

Keep `127.0.0.1:18083` out of the firewall and Docker's published public
interfaces. The Stalwart container must be recreated from its original Compose
definition with `STALWART_PUBLIC_URL=https://mail.teamspace.kz`; do not
remove its data volume.

For the DNS, HTTPS, Keycloak, and service cutover from the temporary domain,
follow [TEAMSPACE_DOMAIN_MIGRATION.md](TEAMSPACE_DOMAIN_MIGRATION.md).

The deploy scripts use a lock so backend and frontend deployments cannot run
over each other. `set-image.sh` stores the previous image references and
`deploy.sh SERVICE rollback` restores them after a failed release.

The workflows use the repository-scoped `GITHUB_TOKEN` for GHCR. Ensure the
package is linked to the repository and that Actions has `packages: read` and
`packages: write` permission. No long-lived registry secret is required.
