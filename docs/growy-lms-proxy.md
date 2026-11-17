# Workspace training reads

The common Workspace proxy supports two independent Growy deployments. A CRM
deployment may expose the same training paths while denying them because its LMS
module is disabled. A successful token exchange therefore does not prove that it
is the correct training backend.

The existing route allowlist selects the connection:

| Workspace route | Backend path | Connection |
| --- | --- | --- |
| GET users/enrollments/ | /api/my/users/enrollments/ | LMS |
| GET users/enrollments/:id/progress-detail/ | /api/my/users/enrollments/:id/progress-detail/ | LMS |
| GET training-programs/my-unfinished/ | /api/training-programs/my-unfinished/ | LMS |
| CRM, WhatsApp, Telegram, context | /api/platform/v1/... | Existing CRM |

Company module gates are independent: training reads and the browser LMS handoff
require `growy`; all platform paths require `crm`. The remote CRM's internal
access-check callback also checks `crm`, including after a token was issued.
Migration `20261103000100_crm_company_module.sql` preserves existing access by
copying the old learning gate to CRM once; future changes are independent.
The browser handoff provisions the LMS, never the remote CRM.

LMS uses `GROWY_LMS_PUBLIC_URL`, defaulting to `APP_PUBLIC_URL/growy`, and optional
`GROWY_LMS_INTERNAL_URL`. CRM continues to use `GROWY_PUBLIC_URL`,
`GROWY_API_INTERNAL_URL` and `GROWY_TENANT_PROVISION_URL`. Never point CRM's private
origin at the LMS container. The two Compose variants no longer force that default.

For each LMS credential exchange, the same immutable Workspace organization is
idempotently provisioned on the LMS backend, then the current user's signed ticket
is exchanged there. API reads use that same connection. Credential cache keys
separate the deployments even if their public URLs coincide and change when the
private origin or tenant-domain configuration changes.

LMS requires company context on the shared public host. Workspace builds `Origin`
from the organization slug returned by the live membership check and the configured
`TENANT_BASE_DOMAIN` (default `teamspace.kz`). It never forwards the incoming Origin,
Referer, Cookie, Host or Authorization. The destination stays fixed in server
configuration, and upstream tokens retain the user's existing role and company.
Only allowlisted training GETs are enabled; this change grants no management roles.

The authenticated proxy separates Workspace authentication from Growy authentication.
A 401 from the Growy ticket exchange or business API is returned as HTTP 502, with
the existing public error code and sanitized message. This applies to both LMS and
CRM connections: a rejected server-managed Growy credential must not trigger
Workspace token refresh or logout. Missing Workspace identity still returns 401;
local company/module denial and upstream 403/409 retain their existing statuses.
Upstream 401/403 still invalidate the cached Growy credential. Requests, including
CRM mutations, are not replayed automatically by the backend. No new settings or
database migrations are required.

Deploy Workspace backend with the existing CRM settings intact. Then compare both
personal training reads through Workspace against ordinary direct LMS login for
each role. Also check CRM access, unsupported training writes, unauthenticated
requests and another company's host. Keep credentials and business records out of
verification reports.
