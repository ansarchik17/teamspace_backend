# BAS-010 / BE-02: Shared server localization

`internal/i18n` owns the embedded server translation catalogs (`*.json`).
Templates use Go `text/template` named parameters, for example `{{.ID}}`.
The catalog is compiled once and safe to use concurrently.

## HTTP locale

Send `Accept-Language: kk` or `Accept-Language: ru`. Regional tags such as
`kk-KZ` and weighted lists are supported. The highest-priority supported language
with a positive quality value wins. Unsupported, wildcard-only or malformed
headers fall back to the authenticated user's saved `language`, then `ru`.
A request override does not change the stored preference.

Global middleware resolves public requests. Protected routes resolve the profile
after authentication. No database lookup is made when the header selects a supported
language. Lookup errors default to Russian without blocking the request.

Read the locale with `i18n.FromContext(ctx)` or render using
`i18n.TextContext(ctx, key, map[string]any{"ID": "42"})`.

## Recipient locale and background work

For background work, load the recipient's profile and use
`i18n.ResolveRecipient(user.Language)`. Do not reuse the sender's HTTP locale.
Render for each recipient separately using `i18n.Render(locale, key, params)`.

External notification server templates use the recipient's saved language.
The sender's payload language does not override it. HTTP and saved profile
languages are `ru` and `kk`.

## Fallback and errors

When the selected locale lacks a key or its translation is empty, the Russian
translation is used. An unknown key or missing template parameter returns an error
from `Render`; `Text`/`TextContext` return the key on such programmer errors.
Templates are rendered as plain text; HTML consumers must escape the result.

BE-03 through BE-06 cover API, notification, email and remaining server UI texts.
Responses include `Content-Language` and `Vary: Accept-Language`; localized AI
response caches and invalidation include both locales.
See [the frontend API contract](localization-api-contract.md) for examples,
codes and completeness checks.
No new database migration is required beyond BE-01's user-language migration.
