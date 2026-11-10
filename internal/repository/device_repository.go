package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type DeviceRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewDeviceRepository(db *pgxpool.Pool, logger *zap.Logger) *DeviceRepository {
	return &DeviceRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "device")),
	}
}

// SaveDevice saves or updates a device token
func (r *DeviceRepository) SaveDevice(ctx context.Context, device *domain.UserDevice) error {
	if device.ID == uuid.Nil {
		device.ID = uuid.New()
	}
	now := time.Now().UTC()
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	device.LastUsedAt = now
	device.UpdatedAt = now
	device.Active = true
	device.FCMToken = strings.TrimSpace(device.FCMToken)
	device.DeviceType = strings.TrimSpace(device.DeviceType)
	device.Platform = strings.TrimSpace(device.Platform)
	device.DeviceID = strings.TrimSpace(device.DeviceID)
	device.VoIPToken = trimOptionalString(device.VoIPToken)
	if device.Platform == "" {
		device.Platform = device.DeviceType
	}
	device.LoggedOutAt = nil

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save device tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if device.DeviceID != "" && device.FCMToken != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE user_devices
			SET active = FALSE,
			    logged_out_at = $3,
			    updated_at = $3
			WHERE user_id = $1
			  AND device_id = $2
			  AND active = TRUE
			  AND fcm_token IS DISTINCT FROM $4
		`, device.UserID, device.DeviceID, now, device.FCMToken); err != nil {
			return fmt.Errorf("deactivate prior device tokens: %w", err)
		}
	}

	if device.FCMToken != "" {
		query := `
			INSERT INTO user_devices (
				id, user_id, fcm_token, device_type, platform, device_id, voip_token,
				last_used_at, active, logged_out_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE, NULL, $9, $10)
			ON CONFLICT (fcm_token)
			WHERE fcm_token IS NOT NULL
			  AND TRIM(fcm_token) <> ''
			DO UPDATE SET
				user_id = EXCLUDED.user_id,
				device_type = COALESCE(NULLIF(EXCLUDED.device_type, ''), user_devices.device_type),
				platform = COALESCE(NULLIF(EXCLUDED.platform, ''), user_devices.platform),
				device_id = COALESCE(EXCLUDED.device_id, user_devices.device_id),
				voip_token = COALESCE(EXCLUDED.voip_token, user_devices.voip_token),
				last_used_at = EXCLUDED.last_used_at,
				active = TRUE,
				logged_out_at = NULL,
				updated_at = EXCLUDED.updated_at
			RETURNING id, created_at
		`

		err = tx.QueryRow(ctx, query,
			device.ID,
			device.UserID,
			device.FCMToken,
			device.DeviceType,
			device.Platform,
			nullIfEmpty(device.DeviceID),
			device.VoIPToken,
			device.LastUsedAt,
			device.CreatedAt,
			device.UpdatedAt,
		).Scan(&device.ID, &device.CreatedAt)
	} else {
		query := `
			INSERT INTO user_devices (
				id, user_id, fcm_token, device_type, platform, device_id, voip_token,
				last_used_at, active, logged_out_at, created_at, updated_at
			)
			VALUES ($1, $2, NULL, $3, $4, $5, $6, $7, TRUE, NULL, $8, $9)
			ON CONFLICT (user_id, device_id)
			WHERE device_id IS NOT NULL
			  AND TRIM(device_id) <> ''
			  AND active = TRUE
			DO UPDATE SET
				device_type = COALESCE(NULLIF(EXCLUDED.device_type, ''), user_devices.device_type),
				platform = COALESCE(NULLIF(EXCLUDED.platform, ''), user_devices.platform),
				fcm_token = COALESCE(user_devices.fcm_token, EXCLUDED.fcm_token),
				voip_token = COALESCE(EXCLUDED.voip_token, user_devices.voip_token),
				last_used_at = EXCLUDED.last_used_at,
				active = TRUE,
				logged_out_at = NULL,
				updated_at = EXCLUDED.updated_at
			RETURNING id, created_at
		`

		err = tx.QueryRow(ctx, query,
			device.ID,
			device.UserID,
			device.DeviceType,
			device.Platform,
			nullIfEmpty(device.DeviceID),
			device.VoIPToken,
			device.LastUsedAt,
			device.CreatedAt,
			device.UpdatedAt,
		).Scan(&device.ID, &device.CreatedAt)
	}

	if err != nil {
		r.logger.Error("Failed to save user device", zap.Error(err))
		return fmt.Errorf("save device: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit save device tx: %w", err)
	}

	r.logger.Info("Device saved successfully",
		zap.Int64("user_id", device.UserID),
		zap.String("token_prefix", tokenPrefix(device.FCMToken)),
		zap.String("device_type", device.DeviceType),
		zap.String("device_id", device.DeviceID),
		zap.Bool("has_voip_token", trimOptionalString(device.VoIPToken) != nil))

	return nil
}

func (r *DeviceRepository) GetUserDevices(ctx context.Context, userID int64) ([]domain.UserDevice, error) {
	query := `
		SELECT id, user_id, COALESCE(fcm_token, ''), device_type, platform, device_id, voip_token,
		       active, last_used_at, logged_out_at, created_at, updated_at
		FROM user_devices
		WHERE user_id = $1
		  AND active = TRUE
		  AND logged_out_at IS NULL
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get user devices: %w", err)
	}
	defer rows.Close()

	devices := make([]domain.UserDevice, 0, 8)
	for rows.Next() {
		var device domain.UserDevice
		var voipToken sql.NullString
		var deviceID sql.NullString
		var loggedOutAt sql.NullTime
		if err := rows.Scan(
			&device.ID,
			&device.UserID,
			&device.FCMToken,
			&device.DeviceType,
			&device.Platform,
			&deviceID,
			&voipToken,
			&device.Active,
			&device.LastUsedAt,
			&loggedOutAt,
			&device.CreatedAt,
			&device.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		if deviceID.Valid {
			device.DeviceID = strings.TrimSpace(deviceID.String)
		}
		if voipToken.Valid {
			device.VoIPToken = trimOptionalString(&voipToken.String)
		}
		if loggedOutAt.Valid {
			value := loggedOutAt.Time
			device.LoggedOutAt = &value
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return devices, nil
}

// GetUserTokens gets all FCM tokens for a user
func (r *DeviceRepository) GetUserTokens(ctx context.Context, userID int64) ([]string, error) {
	query := `
		SELECT fcm_token
		FROM user_devices
		WHERE user_id = $1
		  AND active = TRUE
		  AND logged_out_at IS NULL
		  AND NULLIF(TRIM(fcm_token), '') IS NOT NULL
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get user tokens: %w", err)
	}
	defer rows.Close()

	tokens := make([]string, 0, 8)
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return tokens, nil
}

func (r *DeviceRepository) DeactivateToken(ctx context.Context, userID int64, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	now := time.Now().UTC()
	ct, err := r.db.Exec(ctx,
		`UPDATE user_devices
		 SET active = FALSE,
		     logged_out_at = COALESCE(logged_out_at, $3),
		     updated_at = $3
		 WHERE user_id = $1
		   AND fcm_token = $2
		   AND active = TRUE`,
		userID, token, now,
	)
	if err != nil {
		r.logger.Warn("Failed to deactivate token",
			zap.Int64("user_id", userID),
			zap.String("token_opt", tokenPrefix(token)),
			zap.Error(err),
		)
		return fmt.Errorf("deactivate token: %w", err)
	}

	if ct.RowsAffected() > 0 {
		r.logger.Info("Token deactivated",
			zap.Int64("user_id", userID),
			zap.String("token_prefix", tokenPrefix(token)),
		)
	}

	return nil
}

func (r *DeviceRepository) DeactivateTokenByValue(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	now := time.Now().UTC()
	_, err := r.db.Exec(ctx, `
		UPDATE user_devices
		SET active = FALSE,
		    logged_out_at = COALESCE(logged_out_at, $2),
		    updated_at = $2
		WHERE fcm_token = $1
		  AND active = TRUE
	`, token, now)
	if err != nil {
		return fmt.Errorf("deactivate token by value: %w", err)
	}

	return nil
}

func (r *DeviceRepository) DeactivateDevice(ctx context.Context, userID int64, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}

	now := time.Now().UTC()
	_, err := r.db.Exec(ctx, `
		UPDATE user_devices
		SET active = FALSE,
		    logged_out_at = COALESCE(logged_out_at, $3),
		    updated_at = $3
		WHERE user_id = $1
		  AND device_id = $2
		  AND active = TRUE
	`, userID, deviceID, now)
	if err != nil {
		return fmt.Errorf("deactivate device: %w", err)
	}

	return nil
}

func (r *DeviceRepository) DeactivateAllUserDevices(ctx context.Context, userID int64) error {
	now := time.Now().UTC()
	_, err := r.db.Exec(ctx, `
		UPDATE user_devices
		SET active = FALSE,
		    logged_out_at = COALESCE(logged_out_at, $2),
		    updated_at = $2
		WHERE user_id = $1
		  AND active = TRUE
	`, userID, now)
	if err != nil {
		return fmt.Errorf("deactivate all user devices: %w", err)
	}

	return nil
}

func (r *DeviceRepository) ClearVoIPToken(ctx context.Context, userID int64, deviceID string, voipToken string) error {
	deviceID = strings.TrimSpace(deviceID)
	voipToken = strings.TrimSpace(voipToken)
	if userID == 0 || (deviceID == "" && voipToken == "") {
		return nil
	}

	now := time.Now().UTC()
	query := `
		UPDATE user_devices
		SET voip_token = NULL,
		    updated_at = $4
		WHERE user_id = $1
		  AND active = TRUE
		  AND (
		    ($2 <> '' AND device_id = $2)
		    OR
		    ($3 <> '' AND voip_token = $3)
		  )
	`
	ct, err := r.db.Exec(ctx, query, userID, deviceID, voipToken, now)
	if err != nil {
		return fmt.Errorf("clear voip token: %w", err)
	}

	if ct.RowsAffected() > 0 {
		r.logger.Info("VoIP token cleared",
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
			zap.String("token_prefix", tokenPrefix(voipToken)),
		)
	}

	return nil
}

func (r *DeviceRepository) ClearFCMToken(ctx context.Context, userID int64, deviceID string, fcmToken string) error {
	deviceID = strings.TrimSpace(deviceID)
	fcmToken = strings.TrimSpace(fcmToken)
	if userID == 0 || (deviceID == "" && fcmToken == "") {
		return nil
	}

	now := time.Now().UTC()
	query := `
		UPDATE user_devices
		SET fcm_token = NULL,
		    updated_at = $4
		WHERE user_id = $1
		  AND active = TRUE
		  AND (
		    ($2 <> '' AND device_id = $2)
		    OR
		    ($3 <> '' AND fcm_token = $3)
		  )
	`
	ct, err := r.db.Exec(ctx, query, userID, deviceID, fcmToken, now)
	if err != nil {
		return fmt.Errorf("clear fcm token: %w", err)
	}

	if ct.RowsAffected() > 0 {
		r.logger.Info("FCM token cleared",
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
			zap.String("token_prefix", tokenPrefix(fcmToken)),
		)
	}

	return nil
}

func nullIfEmpty(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

func tokenPrefix(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 10 {
		return token
	}
	return token[:10] + "..."
}
