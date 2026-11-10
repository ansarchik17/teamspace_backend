package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// SaveDeviceMetadata inserts or updates extended metadata in user_device_telemetry.
func (r *DeviceRepository) SaveDeviceMetadata(ctx context.Context, device *domain.UserDevice) error {
	if device.DeviceID == "" {
		return fmt.Errorf("device_id is required to save metadata")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save device metadata tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// 1. Get or create active device row in user_devices
	var userDeviceID uuid.UUID
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, created_at
		FROM user_devices
		WHERE user_id = $1
		  AND device_id = $2
		  AND active = TRUE
		LIMIT 1
	`, device.UserID, device.DeviceID).Scan(&userDeviceID, &createdAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Create a new active device record
			if device.ID == uuid.Nil {
				device.ID = uuid.New()
			}
			now := time.Now().UTC()
			device.LastUsedAt = now
			device.CreatedAt = now
			device.UpdatedAt = now
			device.Active = true

			err = tx.QueryRow(ctx, `
				INSERT INTO user_devices (
					id, user_id, fcm_token, device_type, platform, device_id, voip_token,
					last_used_at, active, logged_out_at, created_at, updated_at
				)
				VALUES ($1, $2, NULL, $3, $4, $5, NULL, $6, TRUE, NULL, $7, $8)
				RETURNING id, created_at
			`, device.ID, device.UserID, device.DeviceType, device.Platform, device.DeviceID, device.LastUsedAt, device.CreatedAt, device.UpdatedAt).Scan(&userDeviceID, &createdAt)
			if err != nil {
				return fmt.Errorf("create user_device row: %w", err)
			}
		} else {
			return fmt.Errorf("lookup active user_device row: %w", err)
		}
	}

	device.ID = userDeviceID
	device.CreatedAt = createdAt

	now := time.Now().UTC()
	device.LastUsedAt = now
	device.UpdatedAt = now
	if device.LastSeenAt == nil {
		device.LastSeenAt = &now
	}

	// 2. Touch user_devices.last_used_at
	_, err = tx.Exec(ctx, `
		UPDATE user_devices
		SET last_used_at = $2,
		    updated_at = $2
		WHERE id = $1
	`, userDeviceID, now)
	if err != nil {
		return fmt.Errorf("touch user_device row: %w", err)
	}

	// 3. Upsert telemetry in user_device_telemetry
	query := `
		INSERT INTO user_device_telemetry (
			user_device_id, os_version, app_version, build_number, last_seen_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		ON CONFLICT (user_device_id)
		DO UPDATE SET
			os_version = EXCLUDED.os_version,
			app_version = EXCLUDED.app_version,
			build_number = EXCLUDED.build_number,
			last_seen_at = EXCLUDED.last_seen_at,
			updated_at = EXCLUDED.updated_at
	`

	_, err = tx.Exec(ctx, query,
		userDeviceID,
		device.OSVersion,
		device.AppVersion,
		device.BuildNumber,
		device.LastSeenAt,
		now,
		now,
	)
	if err != nil {
		r.logger.Error("Failed to save device telemetry metadata", zap.Error(err), zap.Int64("user_id", device.UserID), zap.String("device_id", device.DeviceID))
		return fmt.Errorf("save device telemetry metadata: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit save device metadata tx: %w", err)
	}

	return nil
}

// UpdateHeartbeat updates the last_seen_at and operational fields of an active device.
func (r *DeviceRepository) UpdateHeartbeat(ctx context.Context, userID int64, deviceID string, lastSeen time.Time) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return fmt.Errorf("device_id is required for heartbeat")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin heartbeat tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Look up active user_device ID
	var userDeviceID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM user_devices
		WHERE user_id = $1
		  AND device_id = $2
		  AND active = TRUE
		LIMIT 1
	`, userID, deviceID).Scan(&userDeviceID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			// Skeleton insert
			skeleton := &domain.UserDevice{
				UserID:     userID,
				DeviceID:   deviceID,
				DeviceType: "unknown",
				Platform:   "unknown",
				LastSeenAt: &lastSeen,
			}
			return r.SaveDeviceMetadata(ctx, skeleton)
		}
		return fmt.Errorf("heartbeat lookup: %w", err)
	}

	// Update last_used_at on user_devices
	_, err = tx.Exec(ctx, `
		UPDATE user_devices
		SET last_used_at = $2,
		    updated_at = NOW()
		WHERE id = $1
	`, userDeviceID, lastSeen)
	if err != nil {
		return fmt.Errorf("update heartbeat user_devices: %w", err)
	}

	// Upsert heartbeat info in user_device_telemetry
	_, err = tx.Exec(ctx, `
		INSERT INTO user_device_telemetry (
			user_device_id, last_seen_at, created_at, updated_at
		)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (user_device_id)
		DO UPDATE SET
			last_seen_at = EXCLUDED.last_seen_at,
			updated_at = NOW()
	`, userDeviceID, lastSeen)
	if err != nil {
		return fmt.Errorf("update heartbeat user_device_telemetry: %w", err)
	}

	return tx.Commit(ctx)
}

// GetAdminUserList returns a list of users matching filters, along with device metrics.
func (r *DeviceRepository) GetAdminUserList(
	ctx context.Context,
	searchQuery string,
	platform string,
	deviceType string,
	isOnline *bool,
	appVersion string,
	onlineUserIDs []int64,
	limit, offset int,
) ([]*domain.AdminUserListItem, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{`u.deleted_at IS NULL`, `EXISTS (
		SELECT 1
		FROM user_devices ud
		JOIN user_device_telemetry t ON ud.id = t.user_device_id
		WHERE ud.user_id = u.id
	)`}
	args := []any{}
	argCounter := 1

	// Filter by search query
	searchQuery = strings.TrimSpace(searchQuery)
	if searchQuery != "" {
		whereClauses = append(whereClauses, fmt.Sprintf(`(
			u.username ILIKE $%d OR
			u.email ILIKE $%d OR
			u.first_name ILIKE $%d OR
			u.last_name ILIKE $%d OR
			u.full_name_local ILIKE $%d
		)`, argCounter, argCounter, argCounter, argCounter, argCounter))
		args = append(args, "%"+searchQuery+"%")
		argCounter++
	}

	// Filter by platform
	platform = strings.TrimSpace(platform)
	if platform != "" {
		whereClauses = append(whereClauses, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM user_devices ud 
			JOIN user_device_telemetry t ON ud.id = t.user_device_id
			WHERE ud.user_id = u.id AND ud.platform ILIKE $%d
		)`, argCounter))
		args = append(args, platform)
		argCounter++
	}

	// Filter by device type
	deviceType = strings.TrimSpace(deviceType)
	if deviceType != "" {
		whereClauses = append(whereClauses, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM user_devices ud 
			JOIN user_device_telemetry t ON ud.id = t.user_device_id
			WHERE ud.user_id = u.id AND ud.device_type ILIKE $%d
		)`, argCounter))
		args = append(args, deviceType)
		argCounter++
	}

	// Filter by app version
	appVersion = strings.TrimSpace(appVersion)
	if appVersion != "" {
		whereClauses = append(whereClauses, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM user_devices ud 
			JOIN user_device_telemetry t ON ud.id = t.user_device_id
			WHERE ud.user_id = u.id AND t.app_version ILIKE $%d
		)`, argCounter))
		args = append(args, appVersion)
		argCounter++
	}

	// Filter by online status
	if isOnline != nil {
		recentThreshold := time.Now().Add(-5 * time.Minute)
		if *isOnline {
			whereClauses = append(whereClauses, fmt.Sprintf(`(u.id = ANY($%d) OR u.last_seen_at >= $%d)`, argCounter, argCounter+1))
			args = append(args, onlineUserIDs, recentThreshold)
			argCounter += 2
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf(`NOT (u.id = ANY($%d) OR u.last_seen_at >= $%d)`, argCounter, argCounter+1))
			args = append(args, onlineUserIDs, recentThreshold)
			argCounter += 2
		}
	}

	whereQuery := "WHERE " + strings.Join(whereClauses, " AND ")

	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM users u
		%s
	`, whereQuery)

	var total int
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count admin users: %w", err)
	}

	if total == 0 {
		return []*domain.AdminUserListItem{}, 0, nil
	}

	selectQuery := fmt.Sprintf(`
		SELECT
			u.id AS user_id,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
			u.email,
			u.username,
			u.last_login_at,
			u.last_seen_at,
			'' AS organization_name,
			COALESCE(u.position, '') AS position_name,
			COALESCE(latest_device.platform, '') AS platform,
			COALESCE(latest_device.device_type, '') AS device_type,
			latest_device.os_version,
			latest_device.app_version,
			latest_device.build_number
		FROM users u
		LEFT JOIN LATERAL (
			SELECT ud.id, ud.platform, ud.device_type, t.os_version, t.app_version, t.build_number
			FROM user_devices ud
			JOIN user_device_telemetry t ON ud.id = t.user_device_id
			WHERE ud.user_id = u.id
			ORDER BY t.last_seen_at DESC NULLS LAST, ud.last_used_at DESC NULLS LAST, ud.created_at DESC
			LIMIT 1
		) latest_device ON TRUE
		%s
		ORDER BY u.last_seen_at DESC NULLS LAST, u.created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereQuery, argCounter, argCounter+1)

	selectArgs := append(args, limit, offset)
	rows, err := r.db.Query(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query admin users: %w", err)
	}
	defer rows.Close()

	items := make([]*domain.AdminUserListItem, 0, limit)
	for rows.Next() {
		var item domain.AdminUserListItem
		var lastLogin, lastSeen sql.NullTime
		var osVersion, appVersion, buildNumber sql.NullString
		err := rows.Scan(
			&item.UserID, &item.FullName, &item.Email, &item.Username,
			&lastLogin, &lastSeen, &item.OrganizationName, &item.PositionName,
			&item.Platform, &item.DeviceType, &osVersion, &appVersion, &buildNumber,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan admin user list item: %w", err)
		}
		if lastLogin.Valid {
			item.LastLoginAt = &lastLogin.Time
		}
		if lastSeen.Valid {
			item.LastSeenAt = &lastSeen.Time
		}
		if osVersion.Valid {
			item.OSVersion = &osVersion.String
		}
		if appVersion.Valid {
			item.AppVersion = &appVersion.String
		}
		if buildNumber.Valid {
			item.BuildNumber = &buildNumber.String
		}
		items = append(items, &item)
	}

	return items, total, nil
}
