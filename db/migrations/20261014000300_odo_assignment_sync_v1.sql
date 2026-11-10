-- +goose Up
ALTER TABLE tasks
 ADD COLUMN source_organization_id UUID REFERENCES organizations(id),
 ADD COLUMN source_revision BIGINT NOT NULL DEFAULT 0 CHECK(source_revision BETWEEN 0 AND 9007199254740991),
 ADD COLUMN source_status TEXT,
 ADD COLUMN source_due_date TEXT,
 ADD COLUMN source_timezone TEXT,
 ADD COLUMN source_completed_at TIMESTAMPTZ,
 ADD COLUMN source_closed_at TIMESTAMPTZ,
 ADD COLUMN source_identity_issuer TEXT,
 ADD COLUMN odo_snapshot JSONB,
 ADD COLUMN odo_snapshot_hash TEXT;

-- Only explicit company boards are attributable. Global legacy boards are
-- deliberately left unresolved for the separately audited backfill.
UPDATE tasks t SET source_organization_id=o.id
FROM boards b JOIN users owner_user ON owner_user.id=b.owner_id
JOIN organizations o ON o.id=owner_user.organization_id
WHERE t.board_id=b.id AND t.source_system='documentolog'
 AND b.system_key='documentolog-assignments:'||o.id::text
 AND NOT EXISTS(SELECT 1 FROM task_assignees ta JOIN users u ON u.id=ta.user_id
 WHERE ta.task_id=t.id AND u.organization_id IS DISTINCT FROM o.id);

DROP INDEX IF EXISTS ux_tasks_documentolog_assignment;
CREATE UNIQUE INDEX ux_tasks_odo_company_assignment ON tasks(source_organization_id,source_system,source_assignment_id)
 WHERE source_system='documentolog' AND source_organization_id IS NOT NULL AND source_assignment_id IS NOT NULL AND source_assignment_id<>'' AND deleted_at IS NULL;
CREATE UNIQUE INDEX ux_tasks_odo_unresolved_assignment ON tasks(source_assignment_id)
 WHERE source_system='documentolog' AND source_organization_id IS NULL AND source_assignment_id IS NOT NULL AND source_assignment_id<>'' AND deleted_at IS NULL;

CREATE TABLE odo_assignment_events (
 event_id UUID PRIMARY KEY,
 organization_id UUID NOT NULL REFERENCES organizations(id),
 assignment_id TEXT NOT NULL,
 task_id UUID NOT NULL REFERENCES tasks(id),
 received_revision BIGINT NOT NULL CHECK(received_revision BETWEEN 1 AND 9007199254740991),
 request_hash TEXT NOT NULL,
 received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ix_odo_assignment_events_task ON odo_assignment_events(task_id);

-- +goose Down
-- A downgrade to global uniqueness is unsafe after different companies have
-- synchronized identical source IDs. Keep this data migration forward-only.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'ODO v1 migration requires an explicit audited rollback'; END $$;
-- +goose StatementEnd
