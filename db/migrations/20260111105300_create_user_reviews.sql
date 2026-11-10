-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS user_reviews (
                                            id UUID PRIMARY KEY,
                                            reviewer_id BIGINT NOT NULL REFERENCES users(id),
                                            target_user_id BIGINT NOT NULL REFERENCES users(id),
                                            data_fluency SMALLINT NOT NULL CHECK (data_fluency BETWEEN 0 AND 5),
                                            project_management SMALLINT NOT NULL CHECK (project_management BETWEEN 0 AND 5),
                                            digital_proficiency SMALLINT NOT NULL CHECK (digital_proficiency BETWEEN 0 AND 5),
                                            crisis_communication SMALLINT NOT NULL CHECK (crisis_communication BETWEEN 0 AND 5),
                                            service_design_thinking SMALLINT NOT NULL CHECK (service_design_thinking BETWEEN 0 AND 5),

                                            created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_reviews_target ON user_reviews(target_user_id);

CREATE UNIQUE INDEX idx_one_review_per_month
    ON user_reviews (reviewer_id, target_user_id, (date_trunc('month', created_at)));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_one_review_per_month;
DROP INDEX IF EXISTS idx_user_reviews_target;
DROP TABLE IF EXISTS user_reviews;
-- +goose StatementEnd