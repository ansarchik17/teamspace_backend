-- +goose Up
CREATE TABLE user_public_keys (
                                  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                                  device_id VARCHAR(255) NOT NULL, -- У пользователя может быть несколько устройств
                                  public_key TEXT NOT NULL,        -- Base64 encoded X25519 public key
                                  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                  PRIMARY KEY (user_id, device_id)
);

-- +goose Down
DROP TABLE user_public_keys;