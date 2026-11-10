-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS user_educations (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    school_name VARCHAR(255) NOT NULL,
    school_avatar_url TEXT,
    degree VARCHAR(255),
    field_of_study VARCHAR(255) NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_educations_user_id
    ON user_educations(user_id);

CREATE INDEX IF NOT EXISTS idx_user_educations_user_start_date
    ON user_educations(user_id, start_date DESC, id DESC);

CREATE TABLE IF NOT EXISTS user_license_certifications (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    issuing_organization VARCHAR(255) NOT NULL,
    skill_name VARCHAR(255) NOT NULL,
    avatar_url TEXT,
    issue_date DATE NOT NULL,
    expiration_date DATE,
    attachment_object_name TEXT,
    attachment_file_name VARCHAR(255),
    attachment_content_type VARCHAR(255),
    attachment_size_bytes BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_license_certifications_user_id
    ON user_license_certifications(user_id);

CREATE INDEX IF NOT EXISTS idx_user_license_certifications_user_issue_date
    ON user_license_certifications(user_id, issue_date DESC, id DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_user_license_certifications_user_issue_date;
DROP INDEX IF EXISTS idx_user_license_certifications_user_id;
DROP TABLE IF EXISTS user_license_certifications;

DROP INDEX IF EXISTS idx_user_educations_user_start_date;
DROP INDEX IF EXISTS idx_user_educations_user_id;
DROP TABLE IF EXISTS user_educations;

-- +goose StatementEnd
