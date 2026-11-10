-- +goose Up
-- +goose StatementBegin

-- Extend message types to include voice and video_message
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_type_check CHECK (
    type IN ('text', 'image', 'video', 'file', 'audio', 'sticker',
             'share_task', 'share_event', 'voice', 'video_message')
);

-- Store original filename for file/image/audio messages so the frontend can display it
ALTER TABLE messages ADD COLUMN IF NOT EXISTS attachment_name TEXT;
ALTER TABLE channel_posts ADD COLUMN IF NOT EXISTS attachment_name TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE messages DROP COLUMN IF EXISTS attachment_name;
ALTER TABLE channel_posts DROP COLUMN IF EXISTS attachment_name;

ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_type_check CHECK (
    type IN ('text', 'image', 'video', 'file', 'audio', 'sticker', 'share_task', 'share_event')
);

-- +goose StatementEnd
