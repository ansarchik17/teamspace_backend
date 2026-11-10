# Growy API proxy latency

The authenticated `/api/v1/growy/api/*` proxy previously provisioned the organization and exchanged a Workspace handoff for every CRM, WhatsApp and Telegram request. An initial CRM board with ten columns repeats this sequence for each column, context and summary request.

The proxy now coalesces concurrent credential exchanges and reuses the Growy access token in process memory for at most 60 seconds. Reuse is bounded by the upstream JWT expiry and `expires_in`, with a ten-second safety margin measured from the start of the exchange. Missing or malformed lifetimes disable reuse. Failures and canceled exchanges are not retained. Retained entries are bounded, and upstream 401/403 responses invalidate the matching credential without replaying mutations.

The key includes the current server-resolved organization, role, position, profile, Workspace user/session identity and upstream/signing configuration. Workspace membership is queried on **every** request before consulting the cache. Growy's existing live Workspace access check remains enabled on **every** API request. Neither business responses nor authorization decisions are cached. A user/role/organization change requires a new exchange.

No additional environment variables are needed. This change deploys in **Workspace backend**, not Growy or Workspace frontend. Each backend instance keeps its own cache; restarting clears it.

## Diagnose after deployment

Inspect the response `Server-Timing` header in DevTools Network:

- `workspace_access`: local authorization and membership lookup, milliseconds.
- `growy_session`: exchange/provision or wait for an in-flight exchange, milliseconds; `reused` / `exchanged` description.
- `growy_api`: time until upstream response headers, milliseconds, including Growy's live access check.

Requests exceeding two seconds are logged as `Slow Growy API request` with these phases and total stream duration. Route IDs are masked. Logs exclude authorization headers, tokens, query strings and message bodies.

Tests cover concurrent exchange coalescing, user/tenant/role/session isolation, expiry, failed and canceled exchanges, invalidation, and credential rotation. An unchanged warm identity needs only its API upstream request; provisioning and exchange occur once per reuse window. Actual production latency must be compared after deployment; this does not establish a measured speedup for database queries or Telegram network calls.
