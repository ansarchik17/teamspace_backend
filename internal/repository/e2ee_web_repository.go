package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"go.uber.org/zap"
)

func (r *E2EERepository) CreateWebPairingSession(ctx context.Context, session *domain.WebPairingSession) error {
	const query = `
		INSERT INTO web_pairing_sessions (
			id, qr_token_hash, session_token_hash, user_id,
			web_device_id, web_public_key, web_ephemeral_pub,
			status, request_ip, request_country, request_city, request_user_agent,
			expires_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())
	`

	_, err := r.db.Exec(ctx, query,
		session.ID,
		session.QRTokenHash,
		session.SessionTokenHash,
		session.UserID,
		session.WebDeviceID,
		session.WebPublicKey,
		session.WebEphemeralPub,
		session.Status,
		session.RequestIP,
		session.RequestCountry,
		session.RequestCity,
		session.RequestUserAgent,
		session.ExpiresAt,
	)
	if err != nil {
		r.logger.Error("failed to create web pairing session", zap.Error(err), zap.String("pairing_id", session.ID.String()))
		return fmt.Errorf("create web pairing session: %w", err)
	}

	return nil
}

func (r *E2EERepository) GetWebPairingSessionBySessionToken(ctx context.Context, pairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error) {
	const query = `
		SELECT id, qr_token_hash, session_token_hash, login_code_hash, user_id,
		       web_device_id, web_public_key, web_ephemeral_pub,
		       device_name, platform, status, approved_at, synced_at, redeemed_at,
		       request_ip, request_country, request_city, request_user_agent,
		       expires_at, created_at, updated_at
		FROM web_pairing_sessions
		WHERE id = $1 AND session_token_hash = $2
	`

	session := &domain.WebPairingSession{}
	if err := r.db.QueryRow(ctx, query, pairingID, sessionTokenHash).Scan(
		&session.ID,
		&session.QRTokenHash,
		&session.SessionTokenHash,
		&session.LoginCodeHash,
		&session.UserID,
		&session.WebDeviceID,
		&session.WebPublicKey,
		&session.WebEphemeralPub,
		&session.DeviceName,
		&session.Platform,
		&session.Status,
		&session.ApprovedAt,
		&session.SyncedAt,
		&session.RedeemedAt,
		&session.RequestIP,
		&session.RequestCountry,
		&session.RequestCity,
		&session.RequestUserAgent,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get web pairing session by session token: %w", err)
	}

	return session, nil
}

func (r *E2EERepository) GetWebPairingSessionByQRToken(ctx context.Context, pairingID uuid.UUID, qrTokenHash string) (*domain.WebPairingSession, error) {
	const query = `
		SELECT id, qr_token_hash, session_token_hash, login_code_hash, user_id,
		       web_device_id, web_public_key, web_ephemeral_pub,
		       device_name, platform, status, approved_at, synced_at, redeemed_at,
		       request_ip, request_country, request_city, request_user_agent,
		       expires_at, created_at, updated_at
		FROM web_pairing_sessions
		WHERE id = $1 AND qr_token_hash = $2
	`

	session := &domain.WebPairingSession{}
	if err := r.db.QueryRow(ctx, query, pairingID, qrTokenHash).Scan(
		&session.ID,
		&session.QRTokenHash,
		&session.SessionTokenHash,
		&session.LoginCodeHash,
		&session.UserID,
		&session.WebDeviceID,
		&session.WebPublicKey,
		&session.WebEphemeralPub,
		&session.DeviceName,
		&session.Platform,
		&session.Status,
		&session.ApprovedAt,
		&session.SyncedAt,
		&session.RedeemedAt,
		&session.RequestIP,
		&session.RequestCountry,
		&session.RequestCity,
		&session.RequestUserAgent,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get web pairing session by qr token: %w", err)
	}

	return session, nil
}

func (r *E2EERepository) ApproveWebPairingSession(
	ctx context.Context,
	pairingID uuid.UUID,
	userID int64,
	loginCodeHash *string,
	deviceName, platform *string,
) (*domain.WebPairingSession, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin approve web pairing session tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	session, err := scanWebPairingSession(tx.QueryRow(ctx, `
		SELECT id, qr_token_hash, session_token_hash, login_code_hash, user_id,
		       web_device_id, web_public_key, web_ephemeral_pub,
		       device_name, platform, status, approved_at, synced_at, redeemed_at,
		       request_ip, request_country, request_city, request_user_agent,
		       expires_at, created_at, updated_at
		FROM web_pairing_sessions
		WHERE id = $1
		FOR UPDATE
	`, pairingID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lock web pairing session: %w", err)
	}

	now := time.Now()
	if now.After(session.ExpiresAt) {
		if _, updateErr := tx.Exec(ctx, `
			UPDATE web_pairing_sessions
			SET status = $2, updated_at = NOW()
			WHERE id = $1 AND status = ANY($3)
		`, pairingID, domain.WebPairingStatusExpired, []string{domain.WebPairingStatusPending, domain.WebPairingStatusApproved}); updateErr != nil {
			r.logger.Warn("failed to expire stale web pairing session", zap.Error(updateErr), zap.String("pairing_id", pairingID.String()))
		}
		return nil, fmt.Errorf("web pairing session expired")
	}

	if session.Status == domain.WebPairingStatusRevoked || session.Status == domain.WebPairingStatusExpired {
		return nil, fmt.Errorf("web pairing session is not active")
	}

	if session.UserID != nil && *session.UserID != userID {
		return nil, fmt.Errorf("web pairing session already bound to another user")
	}

	if err = r.revokeStaleWebDeviceOwnershipTx(ctx, tx, session.WebDeviceID, userID, &pairingID); err != nil {
		return nil, err
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO user_public_keys (
			user_id, device_id, public_key, device_name, platform,
			status, trusted_at, revoked_at, last_seen_at,
			linked_ip, linked_country, linked_city,
			last_ip, last_country, last_city, last_user_agent, last_login_at,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'trusted', NOW(), NULL, NOW(), $6, $7, $8, $6, $7, $8, $9, NOW(), NOW())
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			public_key = EXCLUDED.public_key,
			device_name = COALESCE(EXCLUDED.device_name, user_public_keys.device_name),
			platform = COALESCE(EXCLUDED.platform, user_public_keys.platform),
			status = 'trusted',
			trusted_at = COALESCE(user_public_keys.trusted_at, NOW()),
			revoked_at = NULL,
			last_seen_at = NOW(),
			linked_ip = COALESCE(user_public_keys.linked_ip, EXCLUDED.linked_ip),
			linked_country = COALESCE(user_public_keys.linked_country, EXCLUDED.linked_country),
			linked_city = COALESCE(user_public_keys.linked_city, EXCLUDED.linked_city),
			last_ip = EXCLUDED.last_ip,
			last_country = EXCLUDED.last_country,
			last_city = EXCLUDED.last_city,
			last_user_agent = EXCLUDED.last_user_agent,
			last_login_at = NOW()
	`, userID, session.WebDeviceID, session.WebPublicKey, deviceName, platform, session.RequestIP, session.RequestCountry, session.RequestCity, session.RequestUserAgent); err != nil {
		return nil, fmt.Errorf("upsert trusted web device public key: %w", err)
	}

	session, err = scanWebPairingSession(tx.QueryRow(ctx, `
		UPDATE web_pairing_sessions
		SET user_id = $2,
			login_code_hash = $3,
		    device_name = COALESCE($4, device_name),
		    platform = COALESCE($5, platform),
		    status = $6,
		    approved_at = COALESCE(approved_at, NOW()),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, qr_token_hash, session_token_hash, login_code_hash, user_id,
		          web_device_id, web_public_key, web_ephemeral_pub,
		          device_name, platform, status, approved_at, synced_at, redeemed_at,
		          request_ip, request_country, request_city, request_user_agent,
		          expires_at, created_at, updated_at
	`, pairingID, userID, loginCodeHash, deviceName, platform, domain.WebPairingStatusApproved))
	if err != nil {
		return nil, fmt.Errorf("approve web pairing session: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit approve web pairing session: %w", err)
	}

	return session, nil
}

func (r *E2EERepository) RejectWebPairingSession(
	ctx context.Context,
	pairingID uuid.UUID,
	userID int64,
) (*domain.WebPairingSession, error) {
	session, err := scanWebPairingSession(r.db.QueryRow(ctx, `
		UPDATE web_pairing_sessions
		SET user_id = COALESCE(user_id, $2),
		    status = $3,
		    updated_at = NOW()
		WHERE id = $1
		  AND status NOT IN ($3, $4, $5)
		RETURNING id, qr_token_hash, session_token_hash, login_code_hash, user_id,
		          web_device_id, web_public_key, web_ephemeral_pub,
		          device_name, platform, status, approved_at, synced_at, redeemed_at,
		          request_ip, request_country, request_city, request_user_agent,
		          expires_at, created_at, updated_at
	`, pairingID, userID, domain.WebPairingStatusRevoked, domain.WebPairingStatusExpired, domain.WebPairingStatusRedeemed))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("reject web pairing session: %w", err)
	}

	return session, nil
}

func (r *E2EERepository) MarkWebPairingHistorySynced(ctx context.Context, pairingID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE web_pairing_sessions
		SET status = CASE
			WHEN status = $2 THEN $2
			ELSE $3
		END,
		    synced_at = COALESCE(synced_at, NOW()),
		    updated_at = NOW()
		WHERE id = $1
		  AND status <> $4
		  AND status <> $5
	`, pairingID, domain.WebPairingStatusRedeemed, domain.WebPairingStatusSynced, domain.WebPairingStatusRevoked, domain.WebPairingStatusExpired)
	if err != nil {
		return fmt.Errorf("mark web pairing history synced: %w", err)
	}

	return nil
}

func (r *E2EERepository) RedeemWebPairingSession(
	ctx context.Context,
	pairingID uuid.UUID,
	sessionTokenHash string,
) (*domain.WebPairingSession, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin redeem web pairing session tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	session, err := scanWebPairingSession(tx.QueryRow(ctx, `
		SELECT id, qr_token_hash, session_token_hash, login_code_hash, user_id,
		       web_device_id, web_public_key, web_ephemeral_pub,
		       device_name, platform, status, approved_at, synced_at, redeemed_at,
		       request_ip, request_country, request_city, request_user_agent,
		       expires_at, created_at, updated_at
		FROM web_pairing_sessions
		WHERE id = $1 AND session_token_hash = $2
		FOR UPDATE
	`, pairingID, sessionTokenHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lock web pairing session by session token: %w", err)
	}

	now := time.Now()
	if now.After(session.ExpiresAt) {
		if _, updateErr := tx.Exec(ctx, `
			UPDATE web_pairing_sessions
			SET status = $2, updated_at = NOW()
			WHERE id = $1
		`, pairingID, domain.WebPairingStatusExpired); updateErr != nil {
			r.logger.Warn("failed to expire redeemed web pairing session", zap.Error(updateErr), zap.String("pairing_id", pairingID.String()))
		}
		return nil, fmt.Errorf("web pairing session expired")
	}

	if session.Status == domain.WebPairingStatusPending {
		return nil, fmt.Errorf("web pairing session is not approved yet")
	}
	if session.Status == domain.WebPairingStatusRevoked || session.Status == domain.WebPairingStatusExpired {
		return nil, fmt.Errorf("web pairing session is not active")
	}
	if session.LoginCodeHash != nil && *session.LoginCodeHash != "" {
		return nil, fmt.Errorf("web pairing session requires one-time login code")
	}
	if session.Status == domain.WebPairingStatusRedeemed {
		return nil, fmt.Errorf("web pairing session already redeemed")
	}

	session, err = scanWebPairingSession(tx.QueryRow(ctx, `
		UPDATE web_pairing_sessions
		SET status = $2,
		    redeemed_at = COALESCE(redeemed_at, NOW()),
		    login_code_hash = NULL,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, qr_token_hash, session_token_hash, login_code_hash, user_id,
		          web_device_id, web_public_key, web_ephemeral_pub,
		          device_name, platform, status, approved_at, synced_at, redeemed_at,
		          request_ip, request_country, request_city, request_user_agent,
		          expires_at, created_at, updated_at
	`, pairingID, domain.WebPairingStatusRedeemed))
	if err != nil {
		return nil, fmt.Errorf("mark web pairing session redeemed: %w", err)
	}

	if session.UserID != nil {
		if _, err = tx.Exec(ctx, `
			UPDATE user_public_keys
			SET last_seen_at = NOW()
			WHERE user_id = $1 AND device_id = $2
		`, *session.UserID, session.WebDeviceID); err != nil {
			return nil, fmt.Errorf("touch linked web device last_seen_at: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit redeem web pairing session: %w", err)
	}

	return session, nil
}

func (r *E2EERepository) AppendHistoryKeysForDevice(
	ctx context.Context,
	userID int64,
	pairingID uuid.UUID,
	webDeviceID string,
	items []domain.WebHistorySyncItem,
) (int, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE web_pairing_sessions
		SET updated_at = NOW()
		WHERE id = $1
		  AND user_id = $2
		  AND web_device_id = $3
		  AND status IN ($4, $5, $6)
	`, pairingID, userID, webDeviceID, domain.WebPairingStatusApproved, domain.WebPairingStatusSynced, domain.WebPairingStatusRedeemed)
	if err != nil {
		return 0, fmt.Errorf("validate web pairing session before history sync: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, fmt.Errorf("web pairing session not found or not approved")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin append history keys tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	updatedCount := 0
	for _, item := range items {
		tag, execErr := tx.Exec(ctx, `
			UPDATE messages AS m
			SET encrypted_keys = jsonb_set(
					COALESCE(m.encrypted_keys, '{}'::jsonb),
					ARRAY[$2],
					to_jsonb($3::text),
					true
				),
				updated_at = NOW()
			WHERE m.id = $1
			  AND m.is_deleted = false
			  AND (
					EXISTS (
						SELECT 1
						FROM conversation_participants cp
						WHERE cp.conversation_id = m.conversation_id
						  AND cp.user_id = $4
						  AND cp.left_at IS NULL
					)
					OR EXISTS (
						SELECT 1
						FROM group_members gm
						WHERE gm.group_id = m.conversation_id
						  AND gm.user_id = $4
						  AND gm.left_at IS NULL
					)
			  )
		`, item.MessageID, webDeviceID, item.EncryptedKeyForWeb, userID)
		if execErr != nil {
			return 0, fmt.Errorf("append history key for message %s: %w", item.MessageID, execErr)
		}
		updatedCount += int(tag.RowsAffected())
	}

	if _, err = tx.Exec(ctx, `
		UPDATE web_pairing_sessions
		SET status = CASE
			WHEN status = $2 THEN $2
			ELSE $3
		END,
		    synced_at = COALESCE(synced_at, NOW()),
		    updated_at = NOW()
		WHERE id = $1
	`, pairingID, domain.WebPairingStatusRedeemed, domain.WebPairingStatusSynced); err != nil {
		return 0, fmt.Errorf("mark web pairing session synced after history append: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit append history keys tx: %w", err)
	}

	return updatedCount, nil
}

func (r *E2EERepository) UpdateLinkedDeviceAccessMetadata(ctx context.Context, userID int64, deviceID string, meta *domain.ClientMetadata) error {
	if meta == nil {
		_, err := r.db.Exec(ctx, `
			UPDATE user_public_keys
			SET last_seen_at = NOW(),
			    last_login_at = NOW()
			WHERE user_id = $1 AND device_id = $2
		`, userID, deviceID)
		if err != nil {
			return fmt.Errorf("touch linked device access metadata: %w", err)
		}
		return nil
	}

	_, err := r.db.Exec(ctx, `
		UPDATE user_public_keys
		SET last_seen_at = NOW(),
		    last_login_at = NOW(),
		    last_ip = COALESCE($3, last_ip),
		    last_country = COALESCE($4, last_country),
		    last_city = COALESCE($5, last_city),
		    last_user_agent = COALESCE($6, last_user_agent)
		WHERE user_id = $1 AND device_id = $2
	`, userID, deviceID, meta.IP, meta.Country, meta.City, meta.UserAgent)
	if err != nil {
		return fmt.Errorf("update linked device access metadata: %w", err)
	}

	return nil
}

func (r *E2EERepository) ListLinkedDevices(ctx context.Context, userID int64) ([]*domain.LinkedDevice, error) {
	const query = `
		SELECT user_id, device_id, public_key, device_name, platform,
		       status, trusted_at, revoked_at, last_seen_at, last_login_at,
		       linked_ip, linked_country, linked_city,
		       last_ip, last_country, last_city, last_user_agent,
		       created_at
		FROM user_public_keys
		WHERE user_id = $1
		ORDER BY COALESCE(last_login_at, trusted_at, created_at) DESC, created_at DESC
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list linked devices: %w", err)
	}
	defer rows.Close()

	devices := make([]*domain.LinkedDevice, 0)
	for rows.Next() {
		device := &domain.LinkedDevice{}
		if err = rows.Scan(
			&device.UserID,
			&device.DeviceID,
			&device.PublicKey,
			&device.DeviceName,
			&device.Platform,
			&device.Status,
			&device.TrustedAt,
			&device.RevokedAt,
			&device.LastSeenAt,
			&device.LastLoginAt,
			&device.LinkedIP,
			&device.LinkedCountry,
			&device.LinkedCity,
			&device.LastIP,
			&device.LastCountry,
			&device.LastCity,
			&device.UserAgent,
			&device.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan linked device: %w", err)
		}
		devices = append(devices, device)
	}

	return devices, nil
}

func (r *E2EERepository) RevokeLinkedDevice(ctx context.Context, userID int64, deviceID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin revoke linked device tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	tag, err := tx.Exec(ctx, `
		UPDATE user_public_keys
		SET status = 'revoked',
		    revoked_at = NOW(),
		    last_seen_at = NOW()
		WHERE user_id = $1 AND device_id = $2
	`, userID, deviceID)
	if err != nil {
		return fmt.Errorf("revoke linked device key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("linked device not found")
	}

	if _, err = tx.Exec(ctx, `
		UPDATE web_pairing_sessions
		SET status = $3,
		    updated_at = NOW()
		WHERE user_id = $1
		  AND web_device_id = $2
		  AND status <> $4
	`, userID, deviceID, domain.WebPairingStatusRevoked, domain.WebPairingStatusExpired); err != nil {
		return fmt.Errorf("revoke linked device pairing session: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit revoke linked device tx: %w", err)
	}

	return nil
}

func (r *E2EERepository) GetUserBasicProfile(ctx context.Context, userID int64) (*domain.User, error) {
	const query = `
		SELECT id, email, username, first_name, last_name, is_first_login
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`

	user := &domain.User{}
	if err := r.db.QueryRow(ctx, query, userID).Scan(
		&user.ID,
		&user.Email,
		&user.Username,
		&user.FirstName,
		&user.LastName,
		&user.IsFirstLogin,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get user basic profile: %w", err)
	}

	return user, nil
}

func scanWebPairingSession(row pgx.Row) (*domain.WebPairingSession, error) {
	session := &domain.WebPairingSession{}
	err := row.Scan(
		&session.ID,
		&session.QRTokenHash,
		&session.SessionTokenHash,
		&session.LoginCodeHash,
		&session.UserID,
		&session.WebDeviceID,
		&session.WebPublicKey,
		&session.WebEphemeralPub,
		&session.DeviceName,
		&session.Platform,
		&session.Status,
		&session.ApprovedAt,
		&session.SyncedAt,
		&session.RedeemedAt,
		&session.RequestIP,
		&session.RequestCountry,
		&session.RequestCity,
		&session.RequestUserAgent,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	return session, err
}

// revokeStaleWebDeviceOwnershipTx makes a browser slot single-owner from the backend perspective.
// The same web_device_id must not continue carrying trusted rows or active QR state for another account
// once this browser slot is approved or registered for the current user.
func (r *E2EERepository) revokeStaleWebDeviceOwnershipTx(
	ctx context.Context,
	tx pgx.Tx,
	deviceID string,
	keepUserID int64,
	keepPairingID *uuid.UUID,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE user_public_keys
		SET status = 'revoked',
		    revoked_at = COALESCE(revoked_at, NOW()),
		    last_seen_at = NOW()
		WHERE device_id = $1
		  AND user_id <> $2
		  AND revoked_at IS NULL
		  AND status <> 'revoked'
	`, deviceID, keepUserID); err != nil {
		return fmt.Errorf("revoke stale web device ownership: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE web_pairing_sessions
		SET status = $2,
		    updated_at = NOW()
		WHERE web_device_id = $1
		  AND status <> $2
		  AND status <> $3
		  AND ($4::uuid IS NULL OR id <> $4)
	`, deviceID, domain.WebPairingStatusRevoked, domain.WebPairingStatusExpired, keepPairingID); err != nil {
		return fmt.Errorf("revoke stale web pairing sessions: %w", err)
	}

	return nil
}

// RegisterWebDeviceFromPasswordLogin registers a web device directly via password-based login.
// This mirrors the user_public_keys INSERT in ApproveWebPairingSession but does NOT require
// a QR pairing session — the device is trusted on the basis of an authenticated JWT alone.
//
// Security: the caller MUST have verified the user's JWT before invoking this method.
func (r *E2EERepository) RegisterWebDeviceFromPasswordLogin(
	ctx context.Context,
	userID int64,
	deviceID string,
	publicKey string,
	deviceName *string,
	platform *string,
	meta *domain.ClientMetadata,
) error {
	var ip, country, city, userAgent *string
	if meta != nil {
		ip = meta.IP
		country = meta.Country
		city = meta.City
		userAgent = meta.UserAgent
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin register web device from password login tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err = r.revokeStaleWebDeviceOwnershipTx(ctx, tx, deviceID, userID, nil); err != nil {
		return err
	}

	const query = `
		INSERT INTO user_public_keys (
			user_id, device_id, public_key, device_name, platform,
			status, trusted_at, revoked_at, last_seen_at,
			linked_ip, linked_country, linked_city,
			last_ip, last_country, last_city, last_user_agent, last_login_at,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'trusted', NOW(), NULL, NOW(),
		        $6, $7, $8, $6, $7, $8, $9, NOW(), NOW())
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			public_key      = EXCLUDED.public_key,
			device_name     = COALESCE(EXCLUDED.device_name, user_public_keys.device_name),
			platform        = COALESCE(EXCLUDED.platform, user_public_keys.platform),
			status          = 'trusted',
			trusted_at      = COALESCE(user_public_keys.trusted_at, NOW()),
			revoked_at      = NULL,
			last_seen_at    = NOW(),
			linked_ip       = COALESCE(user_public_keys.linked_ip,      EXCLUDED.linked_ip),
			linked_country  = COALESCE(user_public_keys.linked_country,  EXCLUDED.linked_country),
			linked_city     = COALESCE(user_public_keys.linked_city,     EXCLUDED.linked_city),
			last_ip         = EXCLUDED.last_ip,
			last_country    = EXCLUDED.last_country,
			last_city       = EXCLUDED.last_city,
			last_user_agent = EXCLUDED.last_user_agent,
			last_login_at   = NOW()
	`

	_, err = tx.Exec(ctx, query,
		userID, deviceID, publicKey,
		deviceName, platform,
		ip, country, city, userAgent,
	)
	if err != nil {
		r.logger.Error("failed to register web device from password login",
			zap.Error(err),
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
		)
		return fmt.Errorf("register web device from password login: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit register web device from password login tx: %w", err)
	}

	r.logger.Info("web device registered via password login",
		zap.Int64("user_id", userID),
		zap.String("device_id", deviceID),
	)
	return nil
}

func (r *E2EERepository) RevokeOtherWebDevices(ctx context.Context, userID int64, keepDeviceID string) ([]string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin revoke other web devices tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	rows, err := tx.Query(ctx, `
		UPDATE user_public_keys
		SET status = 'revoked',
		    revoked_at = NOW(),
		    last_seen_at = NOW()
		WHERE user_id = $1
		  AND device_id <> $2
		  AND revoked_at IS NULL
		  AND status = 'trusted'
		  AND LOWER(COALESCE(platform, '')) = 'web'
		RETURNING device_id
	`, userID, keepDeviceID)
	if err != nil {
		return nil, fmt.Errorf("revoke other web devices: %w", err)
	}
	defer rows.Close()

	revokedDeviceIDs := make([]string, 0)
	for rows.Next() {
		var deviceID string
		if err := rows.Scan(&deviceID); err != nil {
			return nil, fmt.Errorf("scan revoked web device id: %w", err)
		}
		revokedDeviceIDs = append(revokedDeviceIDs, deviceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate revoked web device ids: %w", err)
	}
	rows.Close()

	if len(revokedDeviceIDs) > 0 {
		if _, err = tx.Exec(ctx, `
			UPDATE web_pairing_sessions
			SET status = $3,
			    updated_at = NOW()
			WHERE user_id = $1
			  AND web_device_id = ANY($2)
			  AND status <> $3
			  AND status <> $4
		`, userID, revokedDeviceIDs, domain.WebPairingStatusRevoked, domain.WebPairingStatusExpired); err != nil {
			return nil, fmt.Errorf("revoke pairing sessions for other web devices: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit revoke other web devices tx: %w", err)
	}

	return revokedDeviceIDs, nil
}

func (r *E2EERepository) CreateWebLoginNotification(
	ctx context.Context,
	userID int64,
	deviceID string,
	deviceName, platform *string,
	meta *domain.ClientMetadata,
	revokedDeviceIDs []string,
) error {
	var profileLanguage string
	if err := r.db.QueryRow(ctx, "SELECT language FROM users WHERE id = $1", userID).Scan(&profileLanguage); err != nil {
		return fmt.Errorf("resolve web login notification language: %w", err)
	}
	locale := i18n.ResolveRecipient(profileLanguage)
	title := i18n.SystemNotification(locale, "Новый вход в веб-клиент")

	details := make([]string, 0, 4)
	if value := strings.TrimSpace(stringValue(deviceName)); value != "" {
		details = append(details, value)
	} else {
		details = append(details, i18n.SystemNotification(locale, "Неизвестное устройство"))
	}

	location := formatLocation(meta)
	if location != "" {
		details = append(details, location)
	}
	if value := strings.TrimSpace(stringValue(metaValue(meta, "ip"))); value != "" {
		details = append(details, "IP: "+value)
	}
	if value := strings.TrimSpace(stringValue(metaValue(meta, "user_agent"))); value != "" {
		details = append(details, value)
	}

	content := strings.Join(details, " | ")
	if len(revokedDeviceIDs) > 0 {
		content += i18n.SystemNotification(locale, " | Завершено web-сеансов: %d", len(revokedDeviceIDs))
	}

	metadata := map[string]string{
		"device_id": deviceID, "language": string(locale),
	}
	if value := strings.TrimSpace(stringValue(platform)); value != "" {
		metadata["platform"] = value
	}
	if value := strings.TrimSpace(stringValue(deviceName)); value != "" {
		metadata["device_name"] = value
	}
	if value := strings.TrimSpace(stringValue(metaValue(meta, "ip"))); value != "" {
		metadata["ip"] = value
	}
	if value := strings.TrimSpace(stringValue(metaValue(meta, "country"))); value != "" {
		metadata["country"] = value
	}
	if value := strings.TrimSpace(stringValue(metaValue(meta, "city"))); value != "" {
		metadata["city"] = value
	}
	if value := strings.TrimSpace(stringValue(metaValue(meta, "user_agent"))); value != "" {
		metadata["user_agent"] = value
	}
	if len(revokedDeviceIDs) > 0 {
		metadata["revoked_device_ids"] = strings.Join(revokedDeviceIDs, ",")
	}

	_, err := r.db.Exec(ctx, `
		INSERT INTO notifications (id, user_id, organizer_id, type, title, content, is_read, metadata, created_at)
		VALUES ($1, $2, NULL, $3, $4, $5, FALSE, $6::jsonb, NOW())
	`, uuid.New(), userID, "security_web_login", title, content, mapToJSON(metadata))
	if err != nil {
		return fmt.Errorf("create web login notification: %w", err)
	}

	return nil
}

func mapToJSON(values map[string]string) string {
	if len(values) == 0 {
		return "{}"
	}

	payload, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func metaValue(meta *domain.ClientMetadata, field string) *string {
	if meta == nil {
		return nil
	}

	switch field {
	case "ip":
		return meta.IP
	case "country":
		return meta.Country
	case "city":
		return meta.City
	case "user_agent":
		return meta.UserAgent
	default:
		return nil
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatLocation(meta *domain.ClientMetadata) string {
	if meta == nil {
		return ""
	}

	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(stringValue(meta.City)); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(stringValue(meta.Country)); value != "" {
		parts = append(parts, value)
	}
	return strings.Join(parts, ", ")
}
