-- +goose Up
CREATE TABLE odo_assignment_backfill_audit (
 operation_id UUID NOT NULL,
 event_id UUID NOT NULL UNIQUE REFERENCES odo_assignment_events(event_id),
 organization_id UUID NOT NULL REFERENCES organizations(id),
 assignment_id TEXT NOT NULL,
 task_id UUID NOT NULL REFERENCES tasks(id),
 previous_board_id UUID,
 previous_task_hash TEXT NOT NULL,
 actor_id BIGINT NOT NULL REFERENCES users(id),
 reason TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(operation_id,organization_id,assignment_id)
);
-- +goose Down
DROP TABLE odo_assignment_backfill_audit;
