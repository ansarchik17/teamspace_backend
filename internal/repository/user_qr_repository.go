package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type UserQRToken struct {
	ID        uuid.UUID
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

type UserQRRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewUserQRRepository(db *pgxpool.Pool, logger *zap.Logger) *UserQRRepository {
	return &UserQRRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "user_qr")),
	}
}

func (r *UserQRRepository) RevokeActiveTokensForUser(ctx context.Context, userID int64, now time.Time) error {
	const query = `
        UPDATE user_qr_tokens
        SET revoked_at = $3
        WHERE user_id = $1
          AND revoked_at IS NULL
          AND expires_at > $2
    `

	if _, err := r.db.Exec(ctx, query, userID, now, now); err != nil {
		r.logger.Error("failed to revoke user qr tokens", zap.Error(err), zap.Int64("user_id", userID))
		return fmt.Errorf("revoke user qr tokens: %w", err)
	}

	return nil
}

func (r *UserQRRepository) Create(ctx context.Context, token *UserQRToken) error {
	const query = `
        INSERT INTO user_qr_tokens (id, user_id, token_hash, expires_at, revoked_at, created_at)
        VALUES ($1, $2, $3, $4, $5, $6)
    `

	if _, err := r.db.Exec(ctx, query, token.ID, token.UserID, token.TokenHash, token.ExpiresAt, token.RevokedAt, token.CreatedAt); err != nil {
		r.logger.Error("failed to create user qr token", zap.Error(err), zap.Int64("user_id", token.UserID))
		return fmt.Errorf("create user qr token: %w", err)
	}

	return nil
}

func (r *UserQRRepository) GetActiveByTokenHash(ctx context.Context, tokenHash string, now time.Time) (*UserQRToken, error) {
	const query = `
        SELECT id, user_id, token_hash, expires_at, revoked_at, created_at
        FROM user_qr_tokens
        WHERE token_hash = $1
          AND revoked_at IS NULL
          AND expires_at > $2
        LIMIT 1
    `

	token := &UserQRToken{}
	err := r.db.QueryRow(ctx, query, tokenHash, now).Scan(
		&token.ID,
		&token.UserID,
		&token.TokenHash,
		&token.ExpiresAt,
		&token.RevokedAt,
		&token.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		r.logger.Error("failed to get user qr token", zap.Error(err))
		return nil, fmt.Errorf("get user qr token: %w", err)
	}

	return token, nil
}
