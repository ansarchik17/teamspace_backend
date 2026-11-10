-- +goose Up
-- +goose StatementBegin

ALTER TABLE user_educations
    ADD COLUMN IF NOT EXISTS school_avatar_object_name TEXT;

ALTER TABLE user_license_certifications
    ADD COLUMN IF NOT EXISTS avatar_object_name TEXT;

UPDATE user_educations
SET school_avatar_object_name = regexp_replace(school_avatar_url, '^.*object_name=', '')
WHERE school_avatar_url IS NOT NULL
  AND school_avatar_url LIKE '%object_name=%'
  AND school_avatar_object_name IS NULL;

UPDATE user_license_certifications
SET avatar_object_name = regexp_replace(avatar_url, '^.*object_name=', '')
WHERE avatar_url IS NOT NULL
  AND avatar_url LIKE '%object_name=%'
  AND avatar_object_name IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE user_license_certifications
    DROP COLUMN IF EXISTS avatar_object_name;

ALTER TABLE user_educations
    DROP COLUMN IF EXISTS school_avatar_object_name;

-- +goose StatementEnd
