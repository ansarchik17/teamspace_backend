# BAS-010 / BE-01: User language

Apply `db/migrations/20261009000100_user_language.sql` before starting the updated backend.
Existing and new users receive `language: "ru"` by default. The database only allows `ru` and `kk`.

Both endpoints require the user's usual authentication:

- `GET /api/v1/auth/me`: the profile response includes `language`.
- `PUT /api/v1/auth/me/language`: JSON body `{"language":"kk"}` or `{"language":"ru"}`;
  returns HTTP 200 with the updated profile.

Missing, null, empty and unsupported language values return HTTP 400.
The endpoint derives the user ID from authentication, so a client cannot change another user's preference.
The preference is stored in `users`, independent of sessions or devices. Normal profile and Keycloak identity updates do not overwrite it.

This change stores the preference; translation catalogs and using the preference for notifications belong to subsequent localization tasks.
