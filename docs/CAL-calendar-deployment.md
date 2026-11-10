# Calendar release deployment

The CAL-001 through CAL-007 release migrations use versions
`20260929000100` through `20260929000500`. They must follow the deployed
main baseline (`20260928000100_create_organization_growy_position_roles.sql`).
The migration runner keeps Goose's default ordering checks enabled.

Before deployment, take a database backup and check `goose_db_version`.
These versions target a database upgraded from main without the previous
calendar migrations. If an environment already applied calendar migrations
under their old versions, do not simply run the renamed files: reconcile its
schema and migration ledger first. In particular, the old room migration
shared version `20260928000100` with Growy, so that number alone does not
identify which schema was applied. Do not reset the ledger or drop tables.

Regression checks:

```sh
go test ./cmd/migrate
TEST_MESSENGER_DATABASE_URL='<disposable PostgreSQL DSN>' go test -p 1 ./cmd/migrate ./internal/service ./internal/repository -run 'Calendar|Room|Recurring|Timezones|QMeet' -count=1
```

`TestCalendarReleaseUpgradeFromMainIntegration` models the main calendar
schema and applied migration ledger in an isolated schema, runs the real Goose
upgrade twice, and verifies existing RSVP data, calendar visibility backfill,
timezone defaults and the room double-booking exclusion constraint.
