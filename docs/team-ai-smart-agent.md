# Main Team AI chat

The `/web/team-rag` page keeps its authenticated persistent API:
`POST /api/v1/ai/chats/{id}/messages`. The main dialog now has the shared
Smart Agent tools, rather than requiring a separate Smart Agent panel.

## Workspace integration

- `read_user_data` reads the existing Workspace catalogue, including tasks,
  calendar, notes, messenger, CRM, EDO and other user-authorized connectors.
- `drive_search` / `search_file_content` use the shared Go file retrieval
  service with the current user's file permissions. The document worker and
  attached-document readers are unchanged. Partial indexing is reported as such.
- `web_search` retrieves public snippets through the existing Python service:
  Go -> `http://alemcloud-api:8000/tools/web-search` -> SearXNG.
  The incoming user's bearer token (or session cookie) authenticates the call.
  Redirects are not followed. No arbitrary model-provided endpoint is accepted.
- Calendar creation/cancellation/reminders, Kanban task creation/closure,
  broadcasts and online meeting creation reuse `SmartAgentService` and the
  underlying Workspace services. Existing records must be resolved to IDs before
  proposing a change; name-only cancellation/closure is not executed.

The model selects the relevant sources. Corporate questions should search
authorized internal documents first; an explicit web request goes directly to
public search. External results remain distinct from internal evidence. Empty
or unavailable internal sources are never treated as proof that a file does not
exist. Tool results, document text and web snippets are untrusted data.

## Employee recipients

`colleague_search` calls the authenticated `/api/v1/search/users` directory with
`q` (name, username or email) and `limit`. It retains the directory's existing
visibility checks. `colleagues` is the current company roster; its API does not
support search or pagination, so the catalogue no longer advertises those filters.
Recipient lookup starts with employees, not CRM contacts or group chats. A name
such as “Test Company” does not imply a group. Prior user turns retain the message
body and recipient corrections; ambiguous search matches require clarification.
Directory `count` is the returned page size, not a corpus total.

## Task deadline input

Main-chat tasks accept a date (`YYYY-MM-DD` or `DD.MM.YYYY`) without an exact
clock time. The displayed default is 23:59:59 on that day in Asia/Almaty.
“Конец недели” means the end of the current Sunday, which is shown explicitly
before confirmation. Exact user-supplied times are retained; a conflicting model
clock is rejected. The most recent matching deadline input is used for time
provenance, rather than unrelated times earlier in the chat. The model must not
ask the user to enter ISO 8601. Internal stored/executed deadlines remain RFC3339.
Task title, priority and assignee remain required and use the existing user-input
and directory binding checks across follow-up messages.

## Directory-backed participant binding

Before preparing an action, the main chat can recover a single participant's
`participant_query` from successful `colleague_search`/`colleagues` evidence read
in the current turn. A directory username, email or full name must literally
match user-authored input with identifier boundaries. A single matching identity
can supply omitted recipient IDs; an explicitly supplied different ID is never
replaced. Multiple matches, partial directory results and CRM contacts are not
used for this binding. Explicit personal events with empty attendees stay empty.
Message content is preserved and all existing validation and confirmation checks
still run on the canonical action arguments.

A participant-only validation failure gets at most two internal directory
resolution attempts. Missing message content still asks the user immediately.
The source username from earlier user turns remains available when the latest
turn provides only the message body. Internal resolution feedback is not shown
as a checked data source.

## Current-turn action scope

Explicit list/count/information requests without an accompanying action instruction
exclude action tools from both native tool schemas and the JSON prompt catalogue.
The tool dispatcher also blocks stale action calls for those turns, returning
internal feedback so the model can answer the current question. This conservative
classifier is not a complete intent parser; combined lookup-and-action requests
keep the normal action validation and confirmation flow.

For cancel/reminder/close tools, `event_query`/`task_query` carries the user's
exact selection phrase even when an ID has already been found. A target-only
validation failure during an action request gets at most two internal resolution
attempts before returning clarification. Missing message content, reminder timing
and other user choices still require clarification immediately. Internal repair
feedback is not published as a checked data source and never executes a mutation.
Textual “yes” does not consume a confirmation token; the confirmation button does.

## Calendar action display

`event_query` is the user's selection phrase (for example “tomorrow”), not the
event title. Cancellation and reminder previews read the event by ID with the
caller's calendar ACL and show its actual title, start and end separately from
that selection phrase. Cancellation re-reads the event before deleting and uses
the live title in the success result; a read or delete failure never reports
successful cancellation. Reminders likewise use the live calendar title.

## Calendar day ranges

For AI `events` and `event_search` collection reads, `YYYY-MM-DD` boundaries
include the full final day in the requested IANA `timezone` (default
`Asia/Almaty`, matching the main chat clock). The adapter converts the local
start date to midnight and the final date to the following local midnight before
calling the existing API. Equal dates mean one whole day. Calendar arithmetic
uses `AddDate` for DST boundaries. Explicit RFC3339 timestamps retain their
exact instants and exclusive upper bound. Missing, invalid or non-positive ranges
return `invalid_request` before reading. Detail reads and direct calendar API
semantics are unchanged.

## Personal CRM reads

For `crm_leads`, `crm_deals` and `crm_tasks`, `params.mine="true"` selects
records assigned to the current CRM user. The reader resolves the authenticated
Growy `/context/` user first: leads and deals use its `user_id` as `manager_id`,
and tasks use `assigned_to=me`, matching the CRM UI. Workspace and Growy user IDs
are not interchangeable. Model-supplied manager/assignee IDs are rejected.
Missing or denied CRM identity fails the read without falling back to all records.
Omitting `mine`, or setting it to `"false"`, preserves the normal permission-scoped
collection. Personal filtering applies to collections; detail reads retain the
upstream object's ACL. For counts, use the filtered response's `count`, not the
number of records on one page. Access/integration errors do not mean zero records.

## Confirmation and persistence

The shared action contract registry drives the advertised required fields and
server checks for every main-chat action. Missing values return a clarification
without creating a Redis action or confirmation card, in native and JSON modes.
Already-provided values are retained through the persisted user turns; only
missing details are requested. Model suggestions and document/tool instructions
are not user-supplied parameters.

| Action | Required details before preview |
| --- | --- |
| Task | Title, assignee, deadline, explicit priority |
| Calendar meeting | Title, date, start, end/duration, participants or explicitly personal event |
| Video meeting | Title, participants, date, start, end/duration |
| Message | Recipients and message text |
| Reminder | Specific calendar event and lead time |
| Cancel meeting / close task | An unambiguous target resolved to its ID |
| Email draft | Recipient, subject and body or a substantive user-provided content brief |

No generated "Task for ..." titles, implicit medium priority or one-hour meeting
duration are accepted. Absolute dates and common relative dates are supported;
ambiguous/unsupported wording is clarified. Optional descriptions not supplied
by the user are omitted from both preview and frozen execution arguments.
Description and estimate remain optional; the normal Kanban/calendar APIs are
unchanged. The legacy Go chat route also preflights the entire action batch
before executing anything, so a later incomplete action cannot follow an earlier
write in the same batch.

`prepare_email_draft` only prepares a persisted chat card; it does not create
mailbox state or send email and has no confirmation/send token. "Write an email
to Arsen" with no content triggers a question about subject and content. Once
the user supplies a brief, the model may formulate the draft using that exact
brief as provenance. The bare request to write to a recipient is not a brief.
This is separate from `send_broadcast`, which sends an internal message only
after confirmation. There is no email transport tool in this chat, and the
assistant must not claim that an email was sent.

Write tools first create a server-owned confirmation card containing exact
parameters. Confirm/cancel sends `confirmation_token` and `action_decision` in
the existing multipart messages API. Plain text saying "confirm" is not an
authorization token and cannot execute the prepared action.

Redis stores the prepared arguments for `AGENT_CONFIRMATION_TTL` (default five
minutes). Keys are scoped to organization, user and persistent chat. Atomic
`GETDEL` consumes a token once, including cancellation. Replays, expired tokens
and tokens submitted from another chat cannot execute a write. Redis failure
prevents action preparation. Confirmation controls and web source cards are
stored in the existing `data_cards` JSON; no new database migration is required.

The agent runs inside the main chat completion loop. Native function calls and
the existing strict JSON fallback support the same allowlist and argument
validation. AI provider calls still go through the existing paid caller. The
Python retrieval endpoint calls no model and returns no generated answer; it
does not bypass accounting restrictions on the old adaptive generation endpoint.

## Deployment

Deploy the updated `workspace-ai` image first (adds the authenticated GET
`/tools/web-search?query=...&limit=6` endpoint), then the Go backend and frontend.
Use the existing Redis, document worker, database and MinIO. No new container or
network is required: `workspace-api` and `alemcloud-api` already share the
Workspace network, and only Python needs access to its private SearXNG network.

`AI_WEB_SEARCH_URL` defaults to
`http://alemcloud-api:8000/tools/web-search`. Override it only for different
service routing. Python continues using its existing `SEARXNG_BASE_URL` and
authentication settings. `AGENT_ENABLED=false` disables main-chat agent tools.

Health checks do not validate permissions, indexing coverage or the model's
ability to call tools. After deployment, verify with an authenticated user:

1. Ask a general question and a follow-up in the same chat.
2. Find a known document by content; open its evidence and ask about an upload.
3. Ask for current public information and open the returned source links.
4. Prepare a calendar event: verify no event exists before confirmation, cancel
   the proposal, then create a new proposal and confirm it once.
5. Re-submit the same confirmation and attempt it from another chat; verify no
   duplicate or cross-chat action occurs.
6. Reopen the persistent conversation and check its messages and source cards.

## Scoped financial exemptions for testing

Keep `AI_ACCOUNTING_ENABLED=true` and `AI_ENFORCEMENT_MODE=enforced` globally.
An operator can set `AI_QUOTA_TEST_SUBJECTS` to comma-separated
`user_id@organization_uuid` pairs in `/opt/workspace/.env`. No setting means
no exemptions. Invalid entries or wildcards fail startup; usernames/emails
are not trusted identifiers for this policy.

Each entry lifts the named user's five-hour and weekly financial gates **only
while the live user belongs to that exact organization**. It also lifts that
organization's budget gate for organization-funded background calls. Personal
quotas of other members and all gates in other organizations remain enforced.
Accounting, reservations, settlement, model pricing, account/company suspension,
authorization, concurrency controls and integer bounds continue to apply.
The own-limits API reports effective `observe` mode only for that selected user;
global administration still reports the globally configured policy.

For the server identity verified during testing:

```dotenv
AI_QUOTA_TEST_SUBJECTS=2@28b76f77-27e7-4b10-ba3c-dc478dc95d35
```

This is an explicit deployment setting, not a hardcoded account exception.
Use the updated backend image and recreate API/worker after changing the setting;
`docker restart` alone does not reload container environment. Apply consistently
to every API replica. Remove the entry and recreate to restore the original
quotas; no stored budgets, history or balances are changed by the exemption.

Verify as the test user with `GET /api/v1/ai/limits`: effective mode should be
`observe` and financial blockers must not prevent `can_start_request`. Verify
another user remains `enforced`. Then repeat the document question and confirm
usage still increases. SQL admission/isolation coverage is provided by
`TestAIQuotaTestSubjectIntegration` with disposable `TEST_MESSENGER_DATABASE_URL`.
