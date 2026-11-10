# Notes deployment recovery

The failed notes release added versions `20260928000300` and `20260928000400`
below the already applied calendar version `20260929000500`. Goose rejected
the upgrade before executing these notes migrations.

For that production state, the two migrations now use `20260929000600` and
`20260929000700`. Existing notes are preserved. Before rollout, back up the
database and inspect the applied versions. If another environment already
applied the old notes versions, reconcile its actual schema and migration
ledger separately; do not rerun renamed migrations blindly or reset the ledger.

Deployment now runs the candidate migration command before replacing the API
and document worker. A rejected migration therefore leaves running services
intact. Schema changes are not automatically reverted on application rollback;
release migrations must remain compatible with the previous application.

Rollback checks that the exact previous image is cached locally and uses
`--pull never`. This avoids depending on GHCR access during recovery. Keep the
previous image on the production host; do not prune it before a successful
release. If it is missing, the script fails explicitly and registry access must
be restored by the operator. This does not repair GHCR token permissions.

Checks include a real Goose upgrade from a calendar-version ledger, preservation
of existing notes, populated full-text search, a second no-op upgrade, and Bash
regression tests for migration failure and cached-image rollback.
