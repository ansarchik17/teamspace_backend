# Agent Assistant Architecture

## Overview

`POST /api/v1/agent/chat` remains backward compatible and continues to use the legacy smart-agent flow through `AgentService.ProcessLegacy`.

New assistant capabilities are exposed through:

- `POST /api/v1/agent/chat/v2`
- `POST /api/v1/agent/sessions`
- `GET /api/v1/agent/sessions/{sessionId}`
- `GET /api/v1/agent/sessions/{sessionId}/messages`
- `DELETE /api/v1/agent/sessions/{sessionId}`
- `POST /api/v1/agent/intent/resolve`
- `POST /api/v1/agent/actions/execute`
- `POST /api/v1/agent/feedback`
- `GET /api/v1/agent/capabilities`

## Pipeline

The assistant flow is implemented as a graph-style pipeline under `internal/agent`:

1. `normalize_input`
2. `load_context`
3. `resolve_intent`
4. `extract_entities`
5. `check_permissions`
6. `route_action`
7. `build_response`
8. `save_memory`
9. `audit_log`

`internal/agent/graph` isolates flow execution so business logic does not depend directly on a third-party graph runtime. `AGENT_GRAPHIFY_ENABLED` is reserved for switching runtime strategy without changing handlers/services.

## Sessions And Memory

Persistent state is stored in PostgreSQL:

- `agent_sessions`
- `agent_messages`
- `agent_action_confirmations`
- `agent_feedback`
- `agent_audit_logs`

`session_id` ownership is always enforced by `user_id` from auth context. Old clients are unaffected because legacy `/agent/chat` still returns the legacy contract.

## Confirmation Flow

Mutating or risky actions return:

```json
{
  "requires_confirmation": true,
  "confirmation_token": "token",
  "proposed_action": {
    "type": "create_reminder",
    "payload": {
      "title": "Call team",
      "remind_at": "2026-05-23T09:00:00+05:00"
    }
  }
}
```

`POST /api/v1/agent/actions/execute` validates:

- token hash
- same user
- same session
- pending status
- not expired

## Config

Supported env vars:

- `AGENT_ENABLED`
- `AGENT_V2_ENABLED`
- `AGENT_INTENT_CONFIDENCE_THRESHOLD`
- `AGENT_SESSION_TTL`
- `AGENT_CONFIRMATION_TTL`
- `AGENT_RATE_LIMIT_PER_MINUTE`
- `AGENT_GRAPHIFY_ENABLED`
- `AGENT_EXTERNAL_TIMEOUT`

## Adding A New Tool

1. Implement `internal/agent.Tool`.
2. Register it in `BuildDefaultTools(...)`.
3. Map the intent in `NewKeywordIntentResolver(...)`.
4. Add tests for direct execution and confirmation behavior if applicable.

## Security Notes

- `user_id` is read only from auth context.
- confirmation tokens are stored hashed.
- audit logs do not store JWTs or secrets.
- new agent endpoints are rate-limited separately from global API limits.
- request handlers return structured `AppError` responses without leaking internals.

## Migration Notes

The migration `20260522010000_create_agent_assistant_tables.sql` is additive and does not modify existing tables or existing agent contracts.
