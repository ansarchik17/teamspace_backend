-- +goose Up
CREATE TABLE security_audit_events (
 id BIGSERIAL PRIMARY KEY,
 organization_id UUID,
 actor_id BIGINT,
 object_type TEXT NOT NULL DEFAULT '' CHECK(length(object_type)<=64),
 object_id TEXT NOT NULL DEFAULT '' CHECK(length(object_id)<=128),
 action TEXT NOT NULL CHECK(length(action) BETWEEN 1 AND 120),
 outcome TEXT NOT NULL CHECK(outcome IN ('success','failure')),
 ip INET CHECK(ip IS NULL OR masklen(ip)=CASE family(ip) WHEN 4 THEN 32 ELSE 128 END),
 request_id TEXT NOT NULL DEFAULT '' CHECK(length(request_id)<=128),
 method TEXT NOT NULL DEFAULT '',
 route TEXT NOT NULL DEFAULT '' CHECK(length(route)<=255),
 status INTEGER NOT NULL DEFAULT 0 CHECK(status BETWEEN 0 AND 599),
 administration_audit_id BIGINT UNIQUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
-- Deliberately no cascading references: deleting a user or company preserves history.
CREATE INDEX security_audit_company_id_idx ON security_audit_events(organization_id,id DESC);
CREATE INDEX security_audit_action_id_idx ON security_audit_events(action,id DESC);
CREATE INDEX security_audit_actor_id_idx ON security_audit_events(actor_id,id DESC);
CREATE INDEX security_audit_created_idx ON security_audit_events(created_at);
CREATE INDEX security_audit_ip_idx ON security_audit_events USING gist(ip inet_ops);

-- +goose StatementBegin
CREATE FUNCTION security_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Security audit is append-only' USING ERRCODE='42501';
END $$;
-- +goose StatementEnd
CREATE TRIGGER security_audit_no_change BEFORE UPDATE OR DELETE ON security_audit_events FOR EACH ROW EXECUTE FUNCTION security_audit_append_only();
CREATE TRIGGER security_audit_no_truncate BEFORE TRUNCATE ON security_audit_events FOR EACH STATEMENT EXECUTE FUNCTION security_audit_append_only();

-- Preserve BAS-017 history without inventing an IP for past events.
INSERT INTO security_audit_events(organization_id,actor_id,action,outcome,object_type,object_id,administration_audit_id,created_at)
SELECT organization_id,actor_id,'admin.'||action,'success',object_type,object_id,id,created_at FROM organization_admin_audit ORDER BY id;

-- +goose StatementBegin
CREATE FUNCTION mirror_administration_security_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO security_audit_events(organization_id,actor_id,action,outcome,ip,request_id,object_type,object_id,administration_audit_id)
 VALUES(NEW.organization_id,NEW.actor_id,'admin.'||NEW.action,'success',NULLIF(current_setting('workspace.audit_ip',true),'')::inet,COALESCE(current_setting('workspace.audit_request_id',true),''),NEW.object_type,NEW.object_id,NEW.id);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER administration_security_audit AFTER INSERT ON organization_admin_audit FOR EACH ROW EXECUTE FUNCTION mirror_administration_security_audit();

-- +goose Down
DROP TRIGGER administration_security_audit ON organization_admin_audit;
DROP FUNCTION mirror_administration_security_audit();
DROP TABLE security_audit_events;
DROP FUNCTION security_audit_append_only();
