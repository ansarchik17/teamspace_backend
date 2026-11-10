-- +goose Up
CREATE TABLE iform_operation_receipts (
 organization_id uuid NOT NULL REFERENCES organizations(id),
 request_id uuid NOT NULL,
 step_id varchar(64) NOT NULL,
 occurrence integer NOT NULL CHECK(occurrence>0),
 actor_id bigint NOT NULL REFERENCES users(id),
 operation text NOT NULL,
 request_hash varchar(64) NOT NULL,
 response jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,request_id,step_id,occurrence)
);

-- +goose Down
DROP TABLE iform_operation_receipts;
