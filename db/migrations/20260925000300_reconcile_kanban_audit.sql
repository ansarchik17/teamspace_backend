-- +goose Up
-- Legacy member is read-only; owner remains the protected board creator.
-- Safe both when the colliding version applied audit and when it applied MSG-008.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='board_members'::regclass AND conname='board_members_role_brd007') THEN
  ALTER TABLE board_members ADD CONSTRAINT board_members_role_brd007 CHECK (role IN ('owner','admin','editor','viewer','member')) NOT VALID;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='task_history'::regclass AND attname='board_id' AND NOT attisdropped) THEN
  ALTER TABLE task_history ADD COLUMN board_id UUID;
  UPDATE task_history h SET board_id=t.board_id FROM tasks t WHERE t.id=h.task_id;
 END IF;
END $$;
-- +goose StatementEnd
-- History must survive physical deletion of a task or actor. No cascading edits.
-- +goose StatementBegin
DO $$ DECLARE c RECORD; BEGIN
 FOR c IN SELECT conname FROM pg_constraint WHERE conrelid='task_history'::regclass AND contype='f' LOOP
  EXECUTE format('ALTER TABLE task_history DROP CONSTRAINT %I',c.conname);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kanban_prepare_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT board_id INTO NEW.board_id FROM tasks WHERE id=NEW.task_id;
 IF NEW.board_id IS NULL THEN
  RAISE EXCEPTION 'task must exist when appending history' USING ERRCODE='23503';
 END IF;
 NEW.created_at:=clock_timestamp();
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS task_history_prepare ON task_history;
CREATE TRIGGER task_history_prepare BEFORE INSERT ON task_history FOR EACH ROW EXECUTE FUNCTION kanban_prepare_history();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kanban_reject_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'task history is append-only' USING ERRCODE='42501';
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS task_history_immutable ON task_history;
CREATE TRIGGER task_history_immutable BEFORE UPDATE OR DELETE ON task_history FOR EACH STATEMENT EXECUTE FUNCTION kanban_reject_history_mutation();
DROP TRIGGER IF EXISTS task_history_no_truncate ON task_history;
CREATE TRIGGER task_history_no_truncate BEFORE TRUNCATE ON task_history FOR EACH STATEMENT EXECUTE FUNCTION kanban_reject_history_mutation();
CREATE INDEX IF NOT EXISTS task_history_board_task ON task_history(board_id,task_id,created_at DESC,id DESC);

-- +goose Down
-- Forward-only reconciliation: do not remove an audit installation that may
-- already have been applied under the old colliding version.
SELECT 1;
