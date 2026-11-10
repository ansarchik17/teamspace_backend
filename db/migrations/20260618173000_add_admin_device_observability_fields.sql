-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS user_device_telemetry (
    user_device_id UUID PRIMARY KEY REFERENCES user_devices(id) ON DELETE CASCADE,
    os_version TEXT,
    app_version TEXT,
    build_number TEXT,
    last_seen_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_device_telemetry_last_seen ON user_device_telemetry(last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_device_telemetry_app_version ON user_device_telemetry(app_version);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_device_telemetry_app_version;
DROP INDEX IF EXISTS idx_device_telemetry_last_seen;
DROP TABLE IF EXISTS user_device_telemetry;
-- +goose StatementEnd
