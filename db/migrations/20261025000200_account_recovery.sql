-- +goose Up
ALTER TABLE user_verified_contacts DROP CONSTRAINT user_verified_contacts_source_check;
ALTER TABLE user_verified_contacts ADD CONSTRAINT user_verified_contacts_source_check CHECK(source IN ('organization_invitation','recovery_email'));

CREATE TABLE user_recovery_emails (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL UNIQUE CHECK(email=lower(btrim(email))),
    version UUID NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE recovery_email_challenges (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    purpose TEXT NOT NULL CHECK(purpose IN ('bind','remove')),
    code_hash TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '10 minutes',
    consumed_at TIMESTAMPTZ
);
CREATE INDEX recovery_email_challenges_user_created ON recovery_email_challenges(user_id,created_at DESC);
CREATE TABLE password_recovery_requests (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    email_version UUID NOT NULL,
    keycloak_id TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    locale TEXT NOT NULL DEFAULT 'ru' CHECK(locale IN ('ru','kk')),
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','processing','password_set','completed','revoked')),
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '30 minutes'
);
CREATE INDEX password_recovery_user_created ON password_recovery_requests(user_id,created_at DESC);
CREATE INDEX password_recovery_completion_pending ON password_recovery_requests(user_id) WHERE state='password_set';
CREATE TABLE user_credential_state (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    invalid_before TIMESTAMPTZ,
    reset_request_id UUID REFERENCES password_recovery_requests(id),
    reset_pending BOOLEAN NOT NULL DEFAULT false
);
CREATE TABLE password_recovery_mail_jobs (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    locale TEXT NOT NULL CHECK(locale IN ('ru','kk')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_recovery_mail_ready ON password_recovery_mail_jobs(available_at) WHERE attempts<3;
CREATE TABLE account_security_notifications (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    event TEXT NOT NULL CHECK(event IN ('recovery_email_changed','recovery_email_removed','password_recovered')),
    locale TEXT NOT NULL CHECK(locale IN ('ru','kk')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX account_security_notifications_ready ON account_security_notifications(available_at,id) WHERE attempts<5;
CREATE TABLE account_recovery_rate_limits (
    key_hash TEXT PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    requests INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE account_security_audit (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE account_security_audit,account_recovery_rate_limits,account_security_notifications,password_recovery_mail_jobs,user_credential_state,password_recovery_requests,recovery_email_challenges,user_recovery_emails;
-- Invitation evidence is retained; recovery-only evidence belongs to this feature.
DELETE FROM user_verified_contacts WHERE source='recovery_email';
ALTER TABLE user_verified_contacts DROP CONSTRAINT user_verified_contacts_source_check;
ALTER TABLE user_verified_contacts ADD CONSTRAINT user_verified_contacts_source_check CHECK(source='organization_invitation');
