package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserPublicKey represents a user's public key for a specific device
type UserPublicKey struct {
	UserID    int64     `json:"user_id" db:"user_id"`
	DeviceID  string    `json:"device_id" db:"device_id"`
	PublicKey string    `json:"public_key" db:"public_key"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

const (
	LinkedDeviceStatusPending = "pending"
	LinkedDeviceStatusTrusted = "trusted"
	LinkedDeviceStatusRevoked = "revoked"

	WebPairingStatusPending  = "pending"
	WebPairingStatusApproved = "approved"
	WebPairingStatusSynced   = "synced"
	WebPairingStatusRedeemed = "redeemed"
	WebPairingStatusExpired  = "expired"
	WebPairingStatusRevoked  = "revoked"
)

// LinkedDevice stores trusted chat device metadata.
type LinkedDevice struct {
	UserID        int64      `json:"user_id" db:"user_id"`
	DeviceID      string     `json:"device_id" db:"device_id"`
	PublicKey     string     `json:"public_key" db:"public_key"`
	DeviceName    *string    `json:"device_name,omitempty" db:"device_name"`
	Platform      *string    `json:"platform,omitempty" db:"platform"`
	Status        string     `json:"status" db:"status"`
	TrustedAt     *time.Time `json:"trusted_at,omitempty" db:"trusted_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty" db:"last_seen_at"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	LinkedIP      *string    `json:"linked_ip,omitempty" db:"linked_ip"`
	LinkedCountry *string    `json:"linked_country,omitempty" db:"linked_country"`
	LinkedCity    *string    `json:"linked_city,omitempty" db:"linked_city"`
	LastIP        *string    `json:"last_ip,omitempty" db:"last_ip"`
	LastCountry   *string    `json:"last_country,omitempty" db:"last_country"`
	LastCity      *string    `json:"last_city,omitempty" db:"last_city"`
	UserAgent     *string    `json:"user_agent,omitempty" db:"last_user_agent"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
}

// WebPairingSession stores temporary QR-linking state for web devices.
type WebPairingSession struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	UserID           *int64     `json:"user_id,omitempty" db:"user_id"`
	WebDeviceID      string     `json:"web_device_id" db:"web_device_id"`
	WebPublicKey     string     `json:"web_public_key" db:"web_public_key"`
	WebEphemeralPub  *string    `json:"web_ephemeral_pub,omitempty" db:"web_ephemeral_pub"`
	DeviceName       *string    `json:"device_name,omitempty" db:"device_name"`
	Platform         *string    `json:"platform,omitempty" db:"platform"`
	Status           string     `json:"status" db:"status"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	SyncedAt         *time.Time `json:"synced_at,omitempty" db:"synced_at"`
	RedeemedAt       *time.Time `json:"redeemed_at,omitempty" db:"redeemed_at"`
	RequestIP        *string    `json:"request_ip,omitempty" db:"request_ip"`
	RequestCountry   *string    `json:"request_country,omitempty" db:"request_country"`
	RequestCity      *string    `json:"request_city,omitempty" db:"request_city"`
	RequestUserAgent *string    `json:"request_user_agent,omitempty" db:"request_user_agent"`
	ExpiresAt        time.Time  `json:"expires_at" db:"expires_at"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`

	// Internal secrets are stored only as hashes in DB.
	QRTokenHash      string  `json:"-" db:"qr_token_hash"`
	SessionTokenHash string  `json:"-" db:"session_token_hash"`
	LoginCodeHash    *string `json:"-" db:"login_code_hash"`
}

type WebHistorySyncItem struct {
	MessageID          uuid.UUID `json:"message_id"`
	EncryptedKeyForWeb string    `json:"encrypted_key_for_web"`
}

type ClientMetadata struct {
	IP        *string `json:"ip,omitempty"`
	Country   *string `json:"country,omitempty"`
	City      *string `json:"city,omitempty"`
	UserAgent *string `json:"user_agent,omitempty"`
}

// KeyDerivationParams хранит algorithm-specific KDF параметры, которые нужны клиенту,
// чтобы детерминированно вывести тот же symmetric key при расшифровке backup.
type KeyDerivationParams struct {
	Iterations  *int    `json:"iterations,omitempty"`
	MemoryKiB   *int    `json:"memory_kib,omitempty"`
	Parallelism *int    `json:"parallelism,omitempty"`
	KeyLength   *int    `json:"key_length,omitempty"`
	Hash        *string `json:"hash,omitempty"`
}

// UserKeyBackup хранит зашифрованный приватный ключ пользователя в облаке.
// Приватный ключ шифруется на СТОРОНЕ КЛИЕНТА мастер-паролем пользователя.
// Сервер хранит только зашифрованный blob — расшифровать без пароля невозможно.
type UserKeyBackup struct {
	UserID              int64                `json:"user_id" db:"user_id"`
	EncryptedPrivateKey string               `json:"encrypted_private_key" db:"encrypted_private_key"` // Base64(AES-GCM(privateKey))
	Salt                string               `json:"salt" db:"salt"`                                   // Base64 random salt для KDF
	IV                  string               `json:"iv" db:"iv"`                                       // Base64 nonce для AES-GCM
	KDFAlgorithm        string               `json:"kdf_algorithm" db:"kdf_algorithm"`                 // Например "argon2id" или "pbkdf2-sha256"
	KDFParams           *KeyDerivationParams `json:"kdf_params,omitempty"`
	Version             int                  `json:"version" db:"version"` // Legacy schema version
	BackupKeyType       string               `json:"backup_key_type" db:"backup_key_type"`
	BackupKeyFormat     string               `json:"backup_key_format" db:"backup_key_format"`
	BackupKeyScope      string               `json:"backup_key_scope" db:"backup_key_scope"`
	BackupFormatVersion int                  `json:"backup_format_version" db:"backup_format_version"`
	CipherAlgorithm     string               `json:"cipher_algorithm" db:"cipher_algorithm"`
	CipherTagEmbedded   bool                 `json:"cipher_tag_embedded" db:"cipher_tag_embedded"`
	AADMode             string               `json:"aad_mode" db:"aad_mode"`
	PasswordProcessing  string               `json:"password_processing" db:"password_processing"`
	CreatedAt           time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at" db:"updated_at"`
}
