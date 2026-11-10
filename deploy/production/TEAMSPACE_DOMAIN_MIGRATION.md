# Move production services to `teamspace.kz`

This runbook changes the public domain for Workspace, Alem Drive, ODO, Growy,
Whiteboard, LiveKit, Collabora, Keycloak, and Stalwart. It assumes these apps
continue to share the current VDS and URL paths. It does not rename the
TeamSpace product branding or existing user email accounts in the database.

## 1. Prepare DNS in PS.kz

The domain is registered, but the screenshot shows **Create zone**. Create the
DNS zone in PS.kz first. Use the VDS public IPv4 address as `<VDS_IPV4>`; do not
guess it from an old signed file URL.

| Type | Name | Value | Purpose |
| --- | --- | --- | --- |
| A | `@` | `<VDS_IPV4>` | Main Workspace, Keycloak, API, and path-based apps |
| A | `www` | `<VDS_IPV4>` | Web alias |
| A | `*` | `<VDS_IPV4>` | Tenant and Growy subdomains |
| A | `mail` | `<VDS_IPV4>` | Stalwart web/API hostname |
| A | `meet` | `<JITSI_PUBLIC_IPV4>` | Jitsi meeting service, if still enabled |

Set a low TTL (for example, 300 seconds) during cutover. For Jitsi, use the public address of the actual Jitsi host for `meet`; do not
assume it is the Workspace VDS. If the legacy Jitsi endpoint is no longer used,
remove its API route/configuration instead of creating this record. If mail is
in use, copy its existing MX, SPF, DKIM, and DMARC policy and signing records, changing
only the domain/hostnames as required by the Stalwart setup. Do not guess mail
records; invalid MX or SPF/DKIM records can interrupt delivery. Keep the old
zone available during the migration window.

Before requesting certificates, verify that the root, `www`, `mail`, and tenant
addresses resolve to the VDS. If Jitsi is active, verify that `meet` resolves to
the Jitsi host:

```bash
dig +short teamspace.kz A
dig +short www.teamspace.kz A
dig +short mail.teamspace.kz A
dig +short demo.teamspace.kz A
dig +short meet.teamspace.kz A
```

## 2. Back up the live settings

On the VDS, back up the environment files, Nginx virtual hosts, and the
standalone Collabora Compose file before editing them:

```bash
stamp=$(date +%Y%m%d%H%M%S)
cp -a /opt/workspace/.env "/opt/workspace/.env.bak.$stamp"
cp -a /opt/odo/.env "/opt/odo/.env.bak.$stamp"
cp -a /opt/growy/.env "/opt/growy/.env.bak.$stamp"
cp -a /opt/workspace/sd/.env "/opt/workspace/sd/.env.bak.$stamp"
cp -a /opt/collabora/compose.yml "/opt/collabora/compose.yml.bak.$stamp"
cp -a /etc/nginx/sites-enabled/workspace "/etc/nginx/sites-enabled/workspace.bak.$stamp"
```

Back up any separate `mail` virtual host too. Never overwrite the current
Workspace Nginx file wholesale: it may contain live ODO, Growy, or other
locations that are not in this repository's sample.

## 3. Push and deploy the repository changes

The source changes are in six independent repositories. Commit and push each
repository's changes to its deployment branch so that its own workflow can
build and deploy it:

- `workspace/workspace-backend`, `workspace/workspace-front`
- `odo/workspace-odo-backend`, `odo/workspace-odo-front`
- `growy/workspace-growy-backend`, `growy/workspace-growy-front`

The deployment templates now use `teamspace.kz`. The Workspace Nginx sample
also includes the `/office/` reverse proxy to Collabora on `127.0.0.1:9980`.
Do not remove ODO's OnlyOffice service; its public path remains `/odo`.

## 4. Update the VDS environment files

For each existing environment file, first check the current values, then
replace the old domain in the backed-up copy. The files contain secrets; do
not paste them into chat or commit them:

```bash
sed -i 's/axisworkspace\.kz/teamspace.kz/g' /opt/workspace/.env
sed -i 's/axisworkspace\.kz/teamspace.kz/g' /opt/odo/.env
sed -i 's/axisworkspace\.kz/teamspace.kz/g' /opt/growy/.env
sed -i 's/93-170-72-233\.sslip\.io/teamspace.kz/g' /opt/workspace/sd/.env
sed -i 's/92\.38\.49\.200\.sslip\.io/meet.teamspace.kz/g' /opt/workspace/.env
```

Set Collabora to the path-based endpoint already used by the live Nginx setup:

```bash
sed -i 's#^COLLABORA_URL=.*#COLLABORA_URL=https://teamspace.kz/office#' /opt/workspace/.env
```

Confirm the public settings (do not print secret values):

```bash
grep -E '^(APP_PUBLIC_URL|COLLABORA_URL|TENANT_BASE_DOMAIN|KEYCLOAK_PUBLIC_URL|KEYCLOAK_ISSUER|KEYCLOAK_HOSTNAME|STALWART_URL|STALWART_PUBLIC_URL|STALWART_DEFAULT_DOMAIN|LIVEKIT_URL)=' /opt/workspace/.env
grep -E '^(KEYCLOAK_ISSUER_URL|CORS_ALLOWED_ORIGINS|WORKSPACE_API_BASE_URL|ONLYOFFICE_PUBLIC_URL|PUBLIC_VERIFY_BASE_URL|DOCUMENTOLOG_PUBLIC_URL)=' /opt/odo/.env
grep -E '^(KEYCLOAK_SERVER_URL|KEYCLOAK_ISSUER|KEYCLOAK_REALM_URL|TENANT_BASE_DOMAIN|PUBLIC_APP_HOST)=' /opt/growy/.env
```

Set `JITSI_DOMAIN=meet.teamspace.kz` only if the legacy Jitsi service is still
used; that hostname must resolve to the Jitsi host. Service Desk uses
`SD_PUBLIC_BASE_URL=https://teamspace.kz/sd` and
`SD_TENANT_BASE_DOMAIN=teamspace.kz`.

Expected public origins include `https://teamspace.kz`,
`https://teamspace.kz/auth`, `https://teamspace.kz/odo`,
`https://teamspace.kz/office`, and `https://mail.teamspace.kz`.

## 5. Update Nginx and Collabora

In the active Nginx HTTPS server block, change the root virtual host to
`teamspace.kz www.teamspace.kz`, retain all existing app locations, and add the
`/office/` proxy from `nginx/workspace.conf`. Update the separate Stalwart
virtual host to `mail.teamspace.kz`. Keep ODO's locations in the same active
server block.

Update `/opt/collabora/compose.yml` so its allowed WOPI domain is
`teamspace\\.kz` (in Compose YAML, escape the backslash as required by its
quoted scalar), and keep its `/office` service root and TLS-termination
parameters. Recreate Collabora after the Nginx/environment changes:

```bash
cd /opt/collabora
docker compose config
docker compose up -d --force-recreate collabora
```

Do not add an `office.teamspace.kz` DNS record for this deployment: Collabora
is served at `https://teamspace.kz/office`.

## 6. Issue certificates and activate the new virtual hosts

Once DNS points to the VDS and the Nginx `server_name` values are updated,
issue certificates for the root, `www`, and mail names:

```bash
certbot --nginx --redirect -d teamspace.kz -d www.teamspace.kz
certbot --nginx --redirect -d mail.teamspace.kz
nginx -t
systemctl reload nginx
```

Tenant URLs such as `company.teamspace.kz` need a certificate covering
`*.teamspace.kz`. A wildcard certificate requires DNS-01 validation. Use a
PS.kz DNS API plugin if available, or another supported automated DNS
challenge method; do not rely on HTTP-01 for a wildcard certificate. If tenant
subdomains are not enabled yet, defer the wildcard certificate.

## 7. Update Keycloak clients

Changing the `.env` values does not edit clients already stored in Keycloak.
In the Workspace realm, update each relevant client's root URL, valid redirect
URIs, web origins, and post-logout redirect URIs to `teamspace.kz`. At minimum
check the Workspace web client, ODO client, Growy client, and Whiteboard client.
Keep their existing paths (`/odo/`, `/growy/`, `/whiteboard/`) and callback
paths, replacing only the host. Then sign out and sign in again in a fresh
browser session to receive tokens with the new issuer.

## 8. Restart and verify every service

Recreate only the affected services, using the repositories' deployment
scripts/workflows and the existing Compose files. Confirm the public endpoints:

```bash
curl -fsS https://teamspace.kz/health
curl -fsS https://teamspace.kz/auth/realms/Workspace/.well-known/openid-configuration
curl -fsS https://teamspace.kz/office/hosting/discovery -o /tmp/teamspace-collabora.xml
curl -fsS https://teamspace.kz/odo/ >/dev/null
curl -fsS https://teamspace.kz/growy/ >/dev/null
curl -fsS https://teamspace.kz/whiteboard/ >/dev/null
curl -fsSI https://mail.teamspace.kz/
```

Also test a tenant hostname, Service Desk tenant routing, Jitsi meetings (if
enabled), a document edit/save in Alem Drive, an ODO
OnlyOffice document, a Growy login, Whiteboard login, LiveKit signaling and
media, and inbound/outbound mail. Search browser network requests and service
logs for the old hostname before retiring its DNS/certificate.

## 9. Retire the old domain only after verification

Keep `axisworkspace.kz` DNS and its certificate for a short rollback window.
After every service and email flow is confirmed on the new domain, remove the
old virtual hosts, old Keycloak redirect URIs, and old DNS records. Existing
mailbox addresses are data, not URL settings; migrate or alias them separately
before disabling the old mail domain.
