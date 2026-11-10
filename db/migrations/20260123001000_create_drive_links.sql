-- +goose Up
CREATE TABLE drive_shared_links (
                                    token UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                    resource_type VARCHAR(20) NOT NULL, -- 'file' или 'folder'
                                    resource_id UUID NOT NULL,
                                    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                                    role VARCHAR(20) NOT NULL, -- 'viewer', 'commentator', 'editor'
                                    expires_at TIMESTAMP,      -- NULL = бессрочная ссылка
                                    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_drive_links_resource ON drive_shared_links(resource_type, resource_id);

-- +goose Down
DROP TABLE drive_shared_links;