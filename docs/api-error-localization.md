# BAS-010 / BE-03: Public API errors and messages

## Audit and implementation

The audit found 1,053 JSON response sites in 79 handler files. Some used the shared
`AppError` envelope; others returned a raw `error`, `message`, provider response or
validation diagnostic. All handler JSON responses now use the same public response
boundary. Successful data, user text and machine statuses remain unchanged.

There are 77 existing named application error codes in the bilingual catalog.
Server message literals are translated separately from user content. They use
deterministic catalog keys, so adding a new message does not renumber existing keys.
Public editor/calendar HTML responses and eGov error redirects also use safe
localized messages. Failed Drive batch items contain localized `error` and stable
`code`, including when the overall HTTP status is 207.

## Error contract

The existing structured format is retained:

```json
{"error":{"code":"INTERNAL_ERROR","message":"Что-то пошло не так. Попробуйте позже.","user_message":"Что-то пошло не так. Попробуйте позже."}}
```

With `Accept-Language: kk`, only the display text changes:

```json
{"error":{"code":"INTERNAL_ERROR","message":"Бірдеңе дұрыс болмады. Кейінірек қайталап көріңіз.","user_message":"Бірдеңе дұрыс болмады. Кейінірек қайталап көріңіз."}}
```

Legacy string-error/flat-message responses retain those shapes and gain `code` and
`user_message`. Existing `AppError` objects retain their specific codes. Legacy
uncoded errors receive a code based on HTTP status (`BAD_REQUEST`, `UNAUTHORIZED`,
`FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `VALIDATION_ERROR`, `FILE_TOO_BIG`,
`TOO_MANY_REQUESTS` or `INTERNAL_ERROR`). Unknown valid codes retain their identity
and receive a safe translated fallback based on HTTP status. Codes do not depend
on language or diagnostic text. Frontend code should branch on `code`, not `message`.

Locale selection is unchanged from BE-02: request header, profile, then `ru`.

## Diagnostics and business metadata

`AppError.Message`, wrapped errors and diagnostic details are kept for server
logging, but are not serialized as display messages. Arbitrary `UserMessage`
overrides do not bypass the catalog. SQL, validation engine internals, provider
responses, stack traces and credentials are not exposed as public error text.

`details` is allowlisted: storage limits, meeting conflict data, note versions and
localized field-validation hints remain available. Numeric limits must actually
be numbers. QR-login guidance is generated from bilingual templates. Diagnostic
keys are removed from nested conflict metadata.

Known successful DTOs containing recording/provider/broadcast/notification failure
diagnostics expose a friendly localized failure message instead of raw diagnostics.
Copies are used, so stored diagnostics are not modified. Proxy error responses for
Growy and iForm pass through the same boundary; successful streams and downloads
are forwarded unchanged.

## Adding responses

- Use `respondJSON`, `respondAbortJSON` or `respondAppError` in handlers.
- Use `apiMessage(c, "server literal")` for a displayed server message and add both
  translations to `internal/i18n/api_messages.json`.
- Add new application codes to `internal/i18n/errors.json` in both languages.
- Never return `err.Error()` or provider bodies as user content. Pass error objects
  to the public response boundary.

Tests cover code stability, both languages, diagnostic redaction, safe metadata,
successful user-content preservation, abort semantics, redirects and proxy errors.
Source audit tests detect handlers bypassing the JSON boundary and missing error
catalog entries. No database migration is required.
