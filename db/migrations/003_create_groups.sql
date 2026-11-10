-- +goose Up
-- +goose StatementBegin

-- Groups table
CREATE TABLE groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar_url TEXT,
    type VARCHAR(20) NOT NULL DEFAULT 'private' CHECK (type IN ('public', 'private')),
    max_members INTEGER NOT NULL DEFAULT 200,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

-- Group members with roles
CREATE TABLE group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(20) NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at TIMESTAMPTZ,
    is_muted BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

-- Indexes for groups
CREATE INDEX idx_groups_name ON groups USING gin(to_tsvector('english', name));
CREATE INDEX idx_groups_type ON groups(type) WHERE is_active = true AND deleted_at IS NULL;
CREATE INDEX idx_groups_created_by ON groups(created_by);
CREATE INDEX idx_groups_active ON groups(is_active) WHERE is_active = true AND deleted_at IS NULL;

-- Indexes for group members
CREATE INDEX idx_group_members_group ON group_members(group_id) WHERE left_at IS NULL;
CREATE INDEX idx_group_members_user ON group_members(user_id) WHERE left_at IS NULL;
CREATE INDEX idx_group_members_role ON group_members(group_id, role) WHERE left_at IS NULL;
CREATE INDEX idx_group_members_owners ON group_members(group_id) WHERE role = 'owner' AND left_at IS NULL;
CREATE INDEX idx_group_members_admins ON group_members(group_id) WHERE role IN ('owner', 'admin') AND left_at IS NULL;

-- Comments
COMMENT ON TABLE groups IS 'Group chats';
COMMENT ON COLUMN groups.type IS 'Group type: public or private';
COMMENT ON COLUMN groups.max_members IS 'Maximum number of members allowed';
COMMENT ON TABLE group_members IS 'Members of groups with their roles';
COMMENT ON COLUMN group_members.role IS 'User role in group: owner, admin, or member';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_group_members_admins;
DROP INDEX IF EXISTS idx_group_members_owners;
DROP INDEX IF EXISTS idx_group_members_role;
DROP INDEX IF EXISTS idx_group_members_user;
DROP INDEX IF EXISTS idx_group_members_group;
DROP INDEX IF EXISTS idx_groups_active;
DROP INDEX IF EXISTS idx_groups_created_by;
DROP INDEX IF EXISTS idx_groups_type;
DROP INDEX IF EXISTS idx_groups_name;

DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;

-- +goose StatementEnd
