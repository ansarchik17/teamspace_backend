-- +goose Up
CREATE TABLE organization_employment_settings (
 organization_id uuid PRIMARY KEY REFERENCES organizations(id),
 timezone text NOT NULL DEFAULT 'Asia/Almaty',
 revision bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE employee_admissions (
 organization_id uuid NOT NULL REFERENCES organizations(id),
 request_id uuid NOT NULL,
 actor_id bigint NOT NULL REFERENCES users(id),
 employee_id bigint UNIQUE REFERENCES users(id),
 start_date date,
 cancelled_at timestamptz,
 revision bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (organization_id,request_id),
 CHECK (employee_id IS NULL OR start_date IS NOT NULL)
);
-- Keep cancellation even if an in-flight employee creation commits afterwards.
-- Existing onb.* accounts with unknown dates require HR verification, not an
-- assumed date that could grant access prematurely.
-- +goose StatementBegin
CREATE FUNCTION workspace_user_can_login_at(employee bigint, instant timestamptz) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT u.is_active AND u.deleted_at IS NULL AND
  CASE WHEN a.employee_id IS NOT NULL THEN
   a.organization_id=u.organization_id AND a.cancelled_at IS NULL AND
   a.start_date <= (instant AT TIME ZONE COALESCE(s.timezone,'Asia/Almaty'))::date
  ELSE COALESCE(u.username,'') NOT LIKE 'onb.%' END
 FROM users u LEFT JOIN employee_admissions a ON a.employee_id=u.id
 LEFT JOIN organization_employment_settings s ON s.organization_id=a.organization_id
 WHERE u.id=employee),false)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION workspace_user_can_login(employee bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workspace_user_can_login_at(employee,CURRENT_TIMESTAMP)
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION workspace_user_can_login(bigint);
DROP FUNCTION workspace_user_can_login_at(bigint,timestamptz);
DROP TABLE employee_admissions;
DROP TABLE organization_employment_settings;
