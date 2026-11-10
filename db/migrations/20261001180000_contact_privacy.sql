-- +goose Up
ALTER TABLE employee_contacts
 ADD COLUMN personal_email TEXT CHECK (personal_email IS NULL OR length(personal_email) <= 254),
 ADD COLUMN personal_phone TEXT CHECK (personal_phone IS NULL OR personal_phone ~ '^\+[1-9][0-9]{6,14}$'),
 ADD COLUMN personal_email_visibility TEXT NOT NULL DEFAULT 'admins' CHECK (personal_email_visibility IN ('private','admins','managers','company')),
 ADD COLUMN personal_phone_visibility TEXT NOT NULL DEFAULT 'admins' CHECK (personal_phone_visibility IN ('private','admins','managers','company'));

-- +goose Down
ALTER TABLE employee_contacts DROP COLUMN personal_email, DROP COLUMN personal_phone,
 DROP COLUMN personal_email_visibility, DROP COLUMN personal_phone_visibility;
