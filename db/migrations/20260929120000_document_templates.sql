-- +goose Up
CREATE TABLE document_templates (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 name VARCHAR(200) NOT NULL CHECK (length(btrim(name)) > 0),
 format VARCHAR(4) NOT NULL CHECK (format IN ('docx','xlsx','pptx')),
 created_by BIGINT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE (organization_id,file_id)
);
CREATE INDEX document_templates_organization_created_idx ON document_templates(organization_id,created_at DESC,id);

-- +goose Down
DROP TABLE document_templates;
