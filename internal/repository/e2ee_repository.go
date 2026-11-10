package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type E2EERepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewE2EERepository(db *pgxpool.Pool, logger *zap.Logger) *E2EERepository {
	return &E2EERepository{
		db:     db,
		logger: logger.With(zap.String("repository", "e2ee")),
	}
}

// UpsertPublicKey saves or updates a public key for a user's device
func (r *E2EERepository) UpsertPublicKey(ctx context.Context, key *domain.UserPublicKey) error {
	const query = `
		INSERT INTO user_public_keys (
			user_id, device_id, public_key, status, trusted_at, last_seen_at, created_at
		)
		VALUES ($1, $2, $3, 'trusted', NOW(), NOW(), NOW())
		ON CONFLICT (user_id, device_id) 
		DO UPDATE SET 
			public_key   = EXCLUDED.public_key,
			status       = 'trusted',
			trusted_at   = COALESCE(user_public_keys.trusted_at, NOW()),
			revoked_at   = NULL,
			last_seen_at = NOW()
	`

	_, err := r.db.Exec(ctx, query, key.UserID, key.DeviceID, key.PublicKey)
	if err != nil {
		r.logger.Error("failed to upsert public key", zap.Error(err), zap.Int64("user_id", key.UserID), zap.String("device_id", key.DeviceID))
		return fmt.Errorf("upsert public key: %w", err)
	}

	return nil
}

// GetKeysByUser returns all public keys for a specific user (one per device)
func (r *E2EERepository) GetKeysByUser(ctx context.Context, userID int64) (map[string]string, error) {
	const query = `
		SELECT device_id, public_key 
		FROM user_public_keys 
		WHERE user_id = $1
		  AND revoked_at IS NULL
		  AND (status IS NULL OR status = 'trusted')
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		r.logger.Error("failed to get public keys", zap.Error(err), zap.Int64("user_id", userID))
		return nil, fmt.Errorf("get public keys: %w", err)
	}
	defer rows.Close()

	keys := make(map[string]string)
	for rows.Next() {
		var deviceID, publicKey string
		if err := rows.Scan(&deviceID, &publicKey); err != nil {
			return nil, err
		}
		keys[deviceID] = publicKey
	}

	return keys, nil
}

// DeleteKey removes a key for a specific device (e.g. on logout)
func (r *E2EERepository) DeleteKey(ctx context.Context, userID int64, deviceID string) error {
	const query = `
		UPDATE user_public_keys
		SET status = 'revoked',
		    revoked_at = NOW(),
		    last_seen_at = NOW()
		WHERE user_id = $1 AND device_id = $2
	`
	_, err := r.db.Exec(ctx, query, userID, deviceID)
	return err
}

// ============================================================
// Key Backup методы (для поддержки смены устройства)
// ============================================================

// UpsertKeyBackup сохраняет или обновляет зашифрованный бэкап приватного ключа пользователя.
// Сервер хранит ТОЛЬКО зашифрованный blob — расшифровать без мастер-пароля пользователя невозможно.
func (r *E2EERepository) UpsertKeyBackup(ctx context.Context, backup *domain.UserKeyBackup) error {
	var kdfParamsJSON []byte
	if backup.KDFParams != nil {
		encoded, err := json.Marshal(backup.KDFParams)
		if err != nil {
			return fmt.Errorf("marshal kdf params: %w", err)
		}
		kdfParamsJSON = encoded
	}

	const query = `
		INSERT INTO user_key_backups (
			user_id, encrypted_private_key, salt, iv, kdf_algorithm, kdf_params, version,
			backup_key_type, backup_key_format, backup_key_scope, backup_format_version,
			cipher_algorithm, cipher_tag_embedded, aad_mode, password_processing,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			encrypted_private_key = EXCLUDED.encrypted_private_key,
			salt                  = EXCLUDED.salt,
			iv                    = EXCLUDED.iv,
			kdf_algorithm         = EXCLUDED.kdf_algorithm,
			kdf_params            = EXCLUDED.kdf_params,
			version               = EXCLUDED.version,
			backup_key_type       = EXCLUDED.backup_key_type,
			backup_key_format     = EXCLUDED.backup_key_format,
			backup_key_scope      = EXCLUDED.backup_key_scope,
			backup_format_version = EXCLUDED.backup_format_version,
			cipher_algorithm      = EXCLUDED.cipher_algorithm,
			cipher_tag_embedded   = EXCLUDED.cipher_tag_embedded,
			aad_mode              = EXCLUDED.aad_mode,
			password_processing   = EXCLUDED.password_processing,
			updated_at            = NOW()
	`

	_, err := r.db.Exec(ctx, query,
		backup.UserID,
		backup.EncryptedPrivateKey,
		backup.Salt,
		backup.IV,
		backup.KDFAlgorithm,
		kdfParamsJSON,
		backup.Version,
		backup.BackupKeyType,
		backup.BackupKeyFormat,
		backup.BackupKeyScope,
		backup.BackupFormatVersion,
		backup.CipherAlgorithm,
		backup.CipherTagEmbedded,
		backup.AADMode,
		backup.PasswordProcessing,
	)
	if err != nil {
		r.logger.Error("failed to upsert key backup",
			zap.Error(err),
			zap.Int64("user_id", backup.UserID),
		)
		return fmt.Errorf("upsert key backup: %w", err)
	}

	r.logger.Info("key backup saved", zap.Int64("user_id", backup.UserID))
	return nil
}

// GetKeyBackup возвращает зашифрованный бэкап приватного ключа пользователя.
// Возвращает nil, nil если бэкап не найден (пользователь ещё не создавал бэкап).
func (r *E2EERepository) GetKeyBackup(ctx context.Context, userID int64) (*domain.UserKeyBackup, error) {
	const query = `
		SELECT user_id, encrypted_private_key, salt, iv, kdf_algorithm, kdf_params, version,
		       backup_key_type, backup_key_format, backup_key_scope, backup_format_version,
		       cipher_algorithm, cipher_tag_embedded, aad_mode, password_processing,
		       created_at, updated_at
		FROM user_key_backups
		WHERE user_id = $1
	`

	backup := &domain.UserKeyBackup{}
	var kdfParamsJSON []byte
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&backup.UserID,
		&backup.EncryptedPrivateKey,
		&backup.Salt,
		&backup.IV,
		&backup.KDFAlgorithm,
		&kdfParamsJSON,
		&backup.Version,
		&backup.BackupKeyType,
		&backup.BackupKeyFormat,
		&backup.BackupKeyScope,
		&backup.BackupFormatVersion,
		&backup.CipherAlgorithm,
		&backup.CipherTagEmbedded,
		&backup.AADMode,
		&backup.PasswordProcessing,
		&backup.CreatedAt,
		&backup.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Бэкап ещё не создан — это нормально
		}
		r.logger.Error("failed to get key backup", zap.Error(err), zap.Int64("user_id", userID))
		return nil, fmt.Errorf("get key backup: %w", err)
	}

	if len(kdfParamsJSON) > 0 {
		var params domain.KeyDerivationParams
		if err := json.Unmarshal(kdfParamsJSON, &params); err != nil {
			r.logger.Error("failed to decode key backup kdf params", zap.Error(err), zap.Int64("user_id", userID))
			return nil, fmt.Errorf("decode key backup kdf params: %w", err)
		}
		backup.KDFParams = &params
	}

	return backup, nil
}

// DeleteKeyBackup удаляет бэкап приватного ключа (например, при удалении аккаунта или сбросе E2EE).
func (r *E2EERepository) DeleteKeyBackup(ctx context.Context, userID int64) error {
	const query = `DELETE FROM user_key_backups WHERE user_id = $1`
	_, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		r.logger.Error("failed to delete key backup", zap.Error(err), zap.Int64("user_id", userID))
		return fmt.Errorf("delete key backup: %w", err)
	}
	return nil
}
