-- +goose Up
CREATE TABLE organization_invitations (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL CHECK (email = lower(btrim(email))),
    team_id UUID REFERENCES teams(id),
    created_by BIGINT NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ,
    accepted_by BIGINT REFERENCES users(id),
    registered_account BOOLEAN NOT NULL DEFAULT false,
    activation_pending BOOLEAN NOT NULL DEFAULT false,
    code_hash TEXT,
    code_expires_at TIMESTAMPTZ,
    code_sent_at TIMESTAMPTZ,
    code_attempts INTEGER NOT NULL DEFAULT 0 CHECK (code_attempts BETWEEN 0 AND 5)
);
CREATE UNIQUE INDEX organization_invitations_pending_email ON organization_invitations(organization_id,email) WHERE status='pending';
CREATE INDEX organization_invitations_company_created ON organization_invitations(organization_id,created_at DESC,id);

-- Confirmed registration contacts survive the existing corporate email sync.
-- Task 2 can use this evidence, but must obtain consent before making it a recovery address.
CREATE TABLE user_verified_contacts (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL UNIQUE CHECK (email=lower(btrim(email))),
    verified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    source TEXT NOT NULL CHECK (source='organization_invitation'),
    PRIMARY KEY(user_id,email)
);

-- +goose Down
DROP TABLE user_verified_contacts;
DROP TABLE organization_invitations;
