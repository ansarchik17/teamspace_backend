# LiveKit setup for Workspace calls

The call stack has three separate parts:

1. Workspace API creates the call and signs a LiveKit JWT.
2. LiveKit transports audio, video, and data between participants.
3. Nginx exposes the LiveKit WebSocket endpoint, while the LiveKit container
   exposes the WebRTC media ports.

The frontend must never receive `LIVEKIT_API_SECRET`. It only receives a
short-lived room token from `/api/v1/calls/start`, `/accept`, or `/rejoin`.

## Local development

The root `docker-compose.yml` starts `workspace-livekit` with the local-only
`devkey`/`devsecret-change-me` pair from `.env`:

```powershell
cd D:\workspace\workspace\workspace-backend
docker compose up -d workspace-postgres workspace-redis workspace-rabbitmq workspace-minio workspace-keycloak workspace-livekit api
docker compose ps
docker compose logs --tail=100 workspace-livekit api
```

The browser connects to `ws://localhost:7880`. The API container talks to the
same server through `http://workspace-livekit:7880` for RoomService calls.

For local browser calls, LiveKit uses:

- `7880/tcp` — WebSocket/API;
- `7881/tcp` — WebRTC TCP fallback;
- `7882/udp` — local WebRTC UDP mux.

## Production deployment

Use a real DNS name and a trusted certificate. The production compose publishes
the signal endpoint only on `127.0.0.1:17880`; the host Nginx configuration
publishes it at `https://teamspace.kz/livekit`.

Put these values in `/opt/workspace/.env`:

```dotenv
LIVEKIT_IMAGE=livekit/livekit-server:v1.13.7
LIVEKIT_API_KEY=<random-key>
LIVEKIT_API_SECRET=<random-secret>
LIVEKIT_URL=wss://teamspace.kz/livekit
LIVEKIT_API_URL=http://workspace-livekit:7880
LIVEKIT_PUBLIC_URL=
LIVEKIT_PUBLIC_USERS=
```

The same API key and secret must be used by Workspace API and the LiveKit
container. Generate them once and keep them only in the server-side `.env`:

```bash
openssl rand -hex 16
openssl rand -hex 32
```

Install the Nginx virtual host from `deploy/production/nginx/workspace.conf`,
then obtain a trusted certificate before using `wss://`:

```bash
nginx -t
systemctl reload nginx
certbot --nginx --redirect -d teamspace.kz -d www.teamspace.kz
```

Start LiveKit and the API:

```bash
cd /opt/workspace
docker compose --env-file /opt/workspace/.env -f compose.prod.yml up -d workspace-livekit api
docker compose --env-file /opt/workspace/.env -f compose.prod.yml ps
docker logs --tail=100 workspace-livekit
docker logs --tail=100 workspace-api
```

Open these firewall ports on the LiveKit host:

- `80/tcp` and `443/tcp` for HTTPS/WSS;
- `7881/tcp` for WebRTC TCP fallback;
- `7882/udp` for WebRTC media through LiveKit's UDP mux.

The `/api/v1/calls/webhook` endpoint must be reachable from LiveKit and the
LiveKit webhook must be signed with the same API key/secret. The webhook is
needed for reliable participant and call lifecycle updates; it is not required
to establish the media connection itself.

## Verification checklist

1. `docker compose ps` shows `workspace-livekit` and `workspace-api` running.
2. API logs contain `livekit_room_api_healthcheck` with `result=ok`.
3. `POST /api/v1/calls/start` returns non-empty `token`, `url`, `room_name`, and
   `call_id`.
4. The returned `url` is `ws://localhost:7880` locally or
   `wss://teamspace.kz/livekit` in production.
5. Browser developer tools show a successful WebSocket connection and ICE
   candidates.
6. Both participants use the same `room_name` and can hear each other.
7. Ending the call produces a lifecycle event and clears the active call banner.

If signaling succeeds but audio/video does not, check the firewall and the
`7881/tcp` plus `7882/udp` ports first. See the official LiveKit
deployment guide: <https://docs.livekit.io/transport/self-hosting/deployment/>.
