`cmd/user-merge` merges duplicate users into one primary account.

Rules:
- If `--primary-user-id` is omitted, the oldest `users.created_at` becomes primary.
- Duplicate users are reassigned in DB first.
- Duplicates are then soft-deleted in `users` with `merged_into_id`.
- Duplicate Keycloak users are deleted after successful DB commit.
- `--prefer-duplicate-hr-data` forces canonical HR fields from the duplicate into the primary account.
- `--prefer-duplicate-login` also replaces `email`, `username`, and `keycloak_id` on the primary account.

Run a dry-run first:

```bash
go run ./cmd/user-merge --user-ids 314,365 --dry-run
```

Merge and keep the oldest account automatically:

```bash
go run ./cmd/user-merge --user-ids 314,365
```

Merge and force a specific primary:

```bash
go run ./cmd/user-merge --user-ids 314,365 --primary-user-id 314
```

Merge a "manual old account + HR duplicate" case and keep the old `user_id`, but take real HR/IIN/profile data from the duplicate:

```bash
go run ./cmd/user-merge --user-ids 56,353 --primary-user-id 56 --prefer-duplicate-hr-data
```

Only use `--prefer-duplicate-login` if the duplicate's login identity should replace the primary account's current login:

```bash
go run ./cmd/user-merge --user-ids 56,353 --primary-user-id 56 --prefer-duplicate-hr-data --prefer-duplicate-login
```

The tool explicitly merges or de-duplicates:
- `user_hr_profiles`
- `chat_themes`
- `user_gamification_scores`
- `unified_ai_chats` and messages
- conversation memberships and message user-links
- notifications, devices, quick PIN, notes, public app access, calendar attendees
- hierarchy leader links
- the rest of single-column FK references to `users(id)` through dynamic FK discovery

Recommended operational flow:
1. Run `--dry-run`.
2. Back up the database.
3. Run the real merge.
4. Verify `users.deleted_at`, `users.merged_into_id`, `user_hr_profiles.user_id`, and Keycloak.
