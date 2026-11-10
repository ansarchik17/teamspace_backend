-- +goose Up
CREATE TABLE folders (
                         id uuid PRIMARY KEY,
                         owner_id bigint NOT NULL REFERENCES users(id),
                         parent_id uuid NULL REFERENCES folders(id),
                         name text NOT NULL,
                         created_at timestamptz NOT NULL,
                         updated_at timestamptz NOT NULL,
                         deleted_at timestamptz NULL
);

CREATE TABLE files (
                       id uuid PRIMARY KEY,
                       owner_id bigint NOT NULL REFERENCES users(id),
                       folder_id uuid NULL REFERENCES folders(id),
                       real_name text NOT NULL,
                       object_name text NOT NULL,
                       size bigint NOT NULL,
                       mime_type text NOT NULL,
                       version int NOT NULL DEFAULT 1,
                       created_at timestamptz NOT NULL,
                       updated_at timestamptz NOT NULL,
                       deleted_at timestamptz NULL
);

CREATE TABLE file_versions (
                               id uuid PRIMARY KEY,
                               file_id uuid NOT NULL REFERENCES files(id),
                               version int NOT NULL,
                               object_name text NOT NULL,
                               size bigint NOT NULL,
                               created_by bigint NOT NULL REFERENCES users(id),
                               created_at timestamptz NOT NULL
);

CREATE TABLE permissions (
                             id bigserial PRIMARY KEY,
                             resource_type text NOT NULL CHECK (resource_type IN ('file','folder')),
                             resource_id uuid NOT NULL,
                             user_id bigint NOT NULL REFERENCES users(id),
                             role text NOT NULL CHECK (role IN ('viewer','editor','owner')),
                             created_at timestamptz NOT NULL,
                             UNIQUE(resource_type, resource_id, user_id)
);

CREATE INDEX idx_folders_owner_parent ON folders(owner_id, parent_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_files_folder ON files(folder_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_perm_res ON permissions(resource_type, resource_id);

-- +goose Down
DROP TABLE permissions;
DROP TABLE file_versions;
DROP TABLE files;
DROP TABLE folders;