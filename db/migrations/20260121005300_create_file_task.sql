-- +goose Up
-- 1. Удаляем колонку file_id (IF EXISTS спасет от ошибки, если её уже нет)
ALTER TABLE task_attachments DROP COLUMN IF EXISTS file_id;

-- 2. Добавляем новые колонки с проверкой IF NOT EXISTS
ALTER TABLE task_attachments ADD COLUMN IF NOT EXISTS file_name VARCHAR(255) NOT NULL DEFAULT 'unknown';
ALTER TABLE task_attachments ADD COLUMN IF NOT EXISTS object_name VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE task_attachments ADD COLUMN IF NOT EXISTS file_size BIGINT NOT NULL DEFAULT 0;
ALTER TABLE task_attachments ADD COLUMN IF NOT EXISTS mime_type VARCHAR(100) NOT NULL DEFAULT 'application/octet-stream';

-- +goose Down
-- Удаляем добавленные колонки
ALTER TABLE task_attachments DROP COLUMN IF EXISTS mime_type;
ALTER TABLE task_attachments DROP COLUMN IF EXISTS file_size;
ALTER TABLE task_attachments DROP COLUMN IF EXISTS object_name;
ALTER TABLE task_attachments DROP COLUMN IF EXISTS file_name;

-- Возвращаем file_id
ALTER TABLE task_attachments ADD COLUMN IF NOT EXISTS file_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';