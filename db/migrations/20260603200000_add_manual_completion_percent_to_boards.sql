-- +goose Up

ALTER TABLE boards
    ADD COLUMN IF NOT EXISTS manual_completion_percent INTEGER;

ALTER TABLE boards
    DROP CONSTRAINT IF EXISTS chk_boards_manual_completion_percent;

ALTER TABLE boards
    ADD CONSTRAINT chk_boards_manual_completion_percent
        CHECK (manual_completion_percent IS NULL OR (manual_completion_percent >= 0 AND manual_completion_percent <= 100));

UPDATE boards
SET
    manual_completion_percent = CASE
        WHEN task_key_prefix = 'MP1' THEN 5
        WHEN task_key_prefix = 'MP2' THEN 20
        WHEN task_key_prefix = 'MP3' THEN 5
        WHEN task_key_prefix = 'MP4' THEN 5
        WHEN task_key_prefix = 'MP5' THEN 0
        ELSE manual_completion_percent
    END,
    updated_at = NOW()
WHERE owner_id = 35
  AND task_key_prefix IN ('MP1', 'MP2', 'MP3', 'MP4', 'MP5')
  AND name IN (
      'Проведение дноуглубительных работ в акватории порта Актау (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Строительство 2-х контейнеровозов (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Строительство 4-х сухогрузов (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Строительство 2-х сухогрузов (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Создание Нового Каспийского паромного моста'
  );

-- +goose Down

UPDATE boards
SET
    manual_completion_percent = NULL,
    updated_at = NOW()
WHERE owner_id = 35
  AND task_key_prefix IN ('MP1', 'MP2', 'MP3', 'MP4', 'MP5')
  AND name IN (
      'Проведение дноуглубительных работ в акватории порта Актау (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Строительство 2-х контейнеровозов (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Строительство 4-х сухогрузов (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Строительство 2-х сухогрузов (Поручение ГГ по итогам поездки в г. Актау от 18.04.2025 г.)',
      'Создание Нового Каспийского паромного моста'
  );

ALTER TABLE boards
    DROP CONSTRAINT IF EXISTS chk_boards_manual_completion_percent;

ALTER TABLE boards
    DROP COLUMN IF EXISTS manual_completion_percent;
