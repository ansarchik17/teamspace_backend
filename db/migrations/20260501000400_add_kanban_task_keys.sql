-- +goose Up
ALTER TABLE boards
    ADD COLUMN IF NOT EXISTS task_key_prefix VARCHAR(16),
    ADD COLUMN IF NOT EXISTS task_sequence INT NOT NULL DEFAULT 0;

WITH name_parts AS (
    SELECT
        id,
        REGEXP_SPLIT_TO_ARRAY(
            TRIM(REGEXP_REPLACE(
                REGEXP_REPLACE(name, '([a-z0-9])([A-Z])', '\1 \2', 'g'),
                '[^A-Za-z0-9]+',
                ' ',
                'g'
            )),
            '\s+'
        ) AS parts
    FROM boards
)
UPDATE boards b
SET task_key_prefix = COALESCE(
    NULLIF((
        CASE
            WHEN ARRAY_LENGTH(np.parts, 1) >= 2 THEN
                UPPER(SUBSTRING(np.parts[1] FROM 1 FOR 1) || SUBSTRING(np.parts[ARRAY_LENGTH(np.parts, 1)] FROM 1 FOR 1))
            WHEN ARRAY_LENGTH(np.parts, 1) = 1 THEN
                UPPER(SUBSTRING(np.parts[1] FROM 1 FOR 2))
            ELSE
                ''
        END
    ), ''),
    'AB'
)
FROM name_parts np
WHERE b.id = np.id
  AND (b.task_key_prefix IS NULL OR b.task_key_prefix = '');

ALTER TABLE boards
    ALTER COLUMN task_key_prefix SET NOT NULL;

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS task_number INT,
    ADD COLUMN IF NOT EXISTS task_key VARCHAR(32);

WITH numbered AS (
    SELECT
        id,
        board_id,
        ROW_NUMBER() OVER (PARTITION BY board_id ORDER BY created_at, id)::INT AS rn
    FROM tasks
    WHERE board_id IS NOT NULL
)
UPDATE tasks t
SET
    task_number = numbered.rn,
    task_key = boards.task_key_prefix || '-' || numbered.rn
FROM numbered
JOIN boards ON boards.id = numbered.board_id
WHERE t.id = numbered.id
  AND (t.task_number IS NULL OR t.task_key IS NULL OR t.task_key = '');

UPDATE boards b
SET task_sequence = COALESCE((
    SELECT MAX(t.task_number)
    FROM tasks t
    WHERE t.board_id = b.id
), 0);

CREATE UNIQUE INDEX IF NOT EXISTS ux_tasks_board_task_number
    ON tasks(board_id, task_number)
    WHERE board_id IS NOT NULL AND task_number IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS ux_tasks_board_task_key
    ON tasks(board_id, task_key)
    WHERE board_id IS NOT NULL AND task_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_task_key
    ON tasks(task_key)
    WHERE task_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_task_key;
DROP INDEX IF EXISTS ux_tasks_board_task_key;
DROP INDEX IF EXISTS ux_tasks_board_task_number;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS task_key,
    DROP COLUMN IF EXISTS task_number;

ALTER TABLE boards
    DROP COLUMN IF EXISTS task_sequence,
    DROP COLUMN IF EXISTS task_key_prefix;
