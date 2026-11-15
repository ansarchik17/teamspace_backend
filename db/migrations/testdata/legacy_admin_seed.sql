-- +goose Up
-- Seed admin user (compatible with current schema)
INSERT INTO users (
    email,
    username,
    password_hash,
    first_name,
    last_name,
    is_active,
    role,
    created_at,
    updated_at
) VALUES (
    'admin@admin.kz',
    'admin',
    crypt('Qwerty12!', gen_salt('bf')),
    'Admin',
    'User',
    true,
    'admin',
    now(),
    now()
);

-- +goose Down
DELETE FROM users WHERE email = 'admin@admin.kz';
