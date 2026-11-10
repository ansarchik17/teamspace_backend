package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type QuickPinRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewQuickPinRepository(db *pgxpool.Pool, logger *zap.Logger) *QuickPinRepository {
	return &QuickPinRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "quick_pin")),
	}
}

// Upsert сохраняет/обновляет PIN креды для (user_id, device_id)
func (r *QuickPinRepository) Upsert(ctx context.Context, c *domain.QuickPinCredential) error {
	if c == nil {
		return fmt.Errorf("upsert quick pin: credential is nil")
	}

	err := r.db.QueryRow(ctx, `
		INSERT INTO quick_pin_credential (
			user_id,
			device_id,
			pin_hash,
			encrypted_refresh_token,
			client_id,
			active
		) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (user_id, device_id)
		DO UPDATE SET
			pin_hash = EXCLUDED.pin_hash,
			encrypted_refresh_token = EXCLUDED.encrypted_refresh_token,
			client_id = COALESCE(EXCLUDED.client_id, quick_pin_credential.client_id),
			active = EXCLUDED.active,
			updated_at = now()
		RETURNING id, created_at, updated_at
	`,
		c.UserID,
		c.DeviceID,
		c.PinHash,
		c.EncryptedRefreshToken,
		c.ClientID,
		c.Active,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)

	if err != nil {
		return fmt.Errorf("upsert quick pin: %w", err)
	}
	return nil
}

// GetActiveByUserDevice возвращает активную запись для user+device
func (r *QuickPinRepository) GetActiveByUserDevice(ctx context.Context, userID int64, deviceID string) (*domain.QuickPinCredential, error) {
	var c domain.QuickPinCredential

	err := r.db.QueryRow(ctx, `
		SELECT
			id,
			user_id,
			device_id,
			pin_hash,
			encrypted_refresh_token,
			client_id,
			active,
			created_at,
			updated_at
		FROM quick_pin_credential
		WHERE user_id = $1
		  AND device_id = $2
		  AND active = true
	`, userID, deviceID).Scan(
		&c.ID,
		&c.UserID,
		&c.DeviceID,
		&c.PinHash,
		&c.EncryptedRefreshToken,
		&c.ClientID,
		&c.Active,
		&c.CreatedAt,
		&c.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("quick pin not found")
		}
		return nil, fmt.Errorf("get active quick pin: %w", err)
	}
	return &c, nil
}

func (r *QuickPinRepository) Deactivate(ctx context.Context, id int64) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE quick_pin_credential
		SET active = false, updated_at = now()
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("deactivate quick pin: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("deactivate quick pin: not found")
	}
	return nil
}

// HasActivePin: есть ли активный PIN у пользователя (без expires_at)
func (r *QuickPinRepository) HasActivePin(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM quick_pin_credential
			WHERE user_id = $1
			  AND active = true
			  AND encrypted_refresh_token IS NOT NULL
			  AND encrypted_refresh_token <> ''
		)
	`, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has active pin: %w", err)
	}
	return exists, nil
}
