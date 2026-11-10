-- +goose Up
CREATE TABLE organization_user_blocks (
 user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 organization_id UUID NOT NULL REFERENCES organizations(id),
 actor_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementBegin
CREATE FUNCTION preserve_administrative_user_block() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM organization_user_blocks WHERE user_id=NEW.id) THEN
  NEW.is_active := false;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_administrative_user_block BEFORE INSERT OR UPDATE ON users
 FOR EACH ROW EXECUTE FUNCTION preserve_administrative_user_block();
-- +goose Down
DROP TRIGGER preserve_administrative_user_block ON users;
DROP FUNCTION preserve_administrative_user_block();
DROP TABLE organization_user_blocks;
