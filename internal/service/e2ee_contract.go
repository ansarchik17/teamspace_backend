package service

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
)

const (
	canonicalDeviceKeyType       = "x25519"
	canonicalPublicKeyFormat     = "base64"
	canonicalBackupKeyFormat     = "pkcs8-der-base64"
	legacyRawBackupKeyFormat     = "raw-32-bytes"
	canonicalBackupKeyScope      = "device_identity"
	canonicalBackupFormatVersion = 1
	canonicalBackupCipher        = "aes-256-gcm"
	canonicalBackupAADMode       = "none"
	canonicalPasswordProcessing  = "utf8-raw"
	aesGCMTagLengthInBytes       = 16
	x25519PrivateKeyLengthBytes  = 32
	x25519PublicKeyLengthInBytes = 32
)

func normalizeKeyBackupMetadata(backup *domain.UserKeyBackup) {
	if backup == nil {
		return
	}

	backup.BackupKeyType = normalizeContractValue(backup.BackupKeyType, canonicalDeviceKeyType)
	backup.BackupKeyFormat = normalizeBackupKeyFormat(backup)
	backup.BackupKeyScope = normalizeContractValue(backup.BackupKeyScope, canonicalBackupKeyScope)
	if backup.BackupFormatVersion == 0 {
		backup.BackupFormatVersion = canonicalBackupFormatVersion
	}
	backup.CipherAlgorithm = normalizeContractValue(backup.CipherAlgorithm, canonicalBackupCipher)
	backup.AADMode = normalizeContractValue(backup.AADMode, canonicalBackupAADMode)
	backup.PasswordProcessing = normalizeContractValue(backup.PasswordProcessing, canonicalPasswordProcessing)
	if !backup.CipherTagEmbedded {
		backup.CipherTagEmbedded = true
	}
}

func normalizeContractValue(value, fallback string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if normalized == "" {
		return fallback
	}
	return normalized
}

func normalizeBackupKeyFormat(backup *domain.UserKeyBackup) string {
	storedFormat := normalizeContractValue(backup.BackupKeyFormat, "")
	inferredFormat := inferLegacyBackupKeyFormat(backup)
	if inferredFormat != "" && (storedFormat == "" || storedFormat == canonicalBackupKeyFormat) {
		return inferredFormat
	}
	if storedFormat != "" {
		return storedFormat
	}
	return canonicalBackupKeyFormat
}

func inferLegacyBackupKeyFormat(backup *domain.UserKeyBackup) string {
	if backup == nil {
		return ""
	}

	switch normalizeBackupKDFAlgorithm(backup.KDFAlgorithm) {
	case "pbkdf2", "pbkdf2-sha256":
	default:
		return ""
	}

	version := backup.Version
	if version == 0 {
		version = 1
	}
	if version != legacyPBKDF2SHA256Version {
		return ""
	}

	decodedCiphertext, err := decodeStandardBase64(backup.EncryptedPrivateKey)
	if err != nil {
		return ""
	}

	if len(decodedCiphertext) == x25519PrivateKeyLengthBytes+aesGCMTagLengthInBytes {
		return legacyRawBackupKeyFormat
	}

	return ""
}

func isLegacyCompatibilityBackup(backup *domain.UserKeyBackup) bool {
	if backup == nil {
		return false
	}

	switch normalizeBackupKDFAlgorithm(backup.KDFAlgorithm) {
	case "pbkdf2", "pbkdf2-sha256":
	default:
		return false
	}

	version := backup.Version
	if version == 0 {
		version = 1
	}

	return version == legacyPBKDF2SHA256Version
}

func shouldAdvertiseBackupInPasswordLogin(backup *domain.UserKeyBackup) bool {
	if backup == nil {
		return false
	}

	// Legacy PBKDF2 rows are still served by GET /e2ee/backup as compatibility data,
	// but ordinary web bootstrap should not advertise them as deterministic password-restore
	// candidates because they can strand long-time users in a broken restore loop.
	return !isLegacyCompatibilityBackup(backup)
}

func validateKeyBackupContract(backup *domain.UserKeyBackup) error {
	if backup == nil {
		return fmt.Errorf("e2ee_backup is required")
	}
	if _, err := decodeStandardBase64(backup.EncryptedPrivateKey); err != nil {
		return fmt.Errorf("encrypted_private_key must be standard base64 encoded")
	}
	salt, err := decodeStandardBase64(backup.Salt)
	if err != nil {
		return fmt.Errorf("salt must be standard base64 encoded")
	}
	if len(salt) < 16 {
		return fmt.Errorf("salt must decode to at least 16 bytes")
	}
	iv, err := decodeStandardBase64(backup.IV)
	if err != nil {
		return fmt.Errorf("iv must be standard base64 encoded")
	}
	if len(iv) != 12 {
		return fmt.Errorf("iv must decode to 12 bytes for aes-256-gcm")
	}

	switch normalizeBackupKDFAlgorithm(backup.KDFAlgorithm) {
	case "argon2id":
		if err := validateArgon2IDParams(backup.KDFParams); err != nil {
			return err
		}
	case "pbkdf2", "pbkdf2-sha256":
		if err := validatePBKDF2Params(backup.KDFParams); err != nil {
			return err
		}
	default:
		return fmt.Errorf("kdf_algorithm must be argon2id or pbkdf2-sha256")
	}

	if backup.Version <= 0 {
		return fmt.Errorf("version must be positive")
	}
	if backup.BackupKeyType != canonicalDeviceKeyType {
		return fmt.Errorf("backup_key_type must be %s", canonicalDeviceKeyType)
	}
	switch backup.BackupKeyFormat {
	case canonicalBackupKeyFormat, legacyRawBackupKeyFormat:
	default:
		return fmt.Errorf("backup_key_format must be %s", canonicalBackupKeyFormat)
	}
	if backup.BackupKeyScope != canonicalBackupKeyScope {
		return fmt.Errorf("backup_key_scope must be %s", canonicalBackupKeyScope)
	}
	if backup.BackupFormatVersion != canonicalBackupFormatVersion {
		return fmt.Errorf("backup_format_version must be %d", canonicalBackupFormatVersion)
	}
	if backup.CipherAlgorithm != canonicalBackupCipher {
		return fmt.Errorf("cipher_algorithm must be %s", canonicalBackupCipher)
	}
	if !backup.CipherTagEmbedded {
		return fmt.Errorf("cipher_tag_embedded must be true")
	}
	if backup.AADMode != canonicalBackupAADMode {
		return fmt.Errorf("aad_mode must be %s", canonicalBackupAADMode)
	}
	if backup.PasswordProcessing != canonicalPasswordProcessing {
		return fmt.Errorf("password_processing must be %s", canonicalPasswordProcessing)
	}

	return nil
}

func validateExplicitKeyBackupContract(req *SaveKeyBackupRequest) error {
	if req == nil {
		return fmt.Errorf("e2ee_backup is required")
	}
	if strings.TrimSpace(req.EncryptedPrivateKey) == "" {
		return fmt.Errorf("encrypted_private_key is required")
	}
	if strings.TrimSpace(req.Salt) == "" {
		return fmt.Errorf("salt is required")
	}
	if strings.TrimSpace(req.IV) == "" {
		return fmt.Errorf("iv is required")
	}
	if strings.TrimSpace(req.KDFAlgorithm) == "" {
		return fmt.Errorf("kdf_algorithm is required")
	}
	if req.KDFParams == nil {
		return fmt.Errorf("kdf_params is required")
	}
	if req.Version == 0 {
		return fmt.Errorf("version is required")
	}
	if strings.TrimSpace(req.BackupKeyType) == "" {
		return fmt.Errorf("backup_key_type is required")
	}
	if strings.TrimSpace(req.BackupKeyFormat) == "" {
		return fmt.Errorf("backup_key_format is required")
	}
	if strings.TrimSpace(req.BackupKeyScope) == "" {
		return fmt.Errorf("backup_key_scope is required")
	}
	if req.BackupFormatVersion == 0 {
		return fmt.Errorf("backup_format_version is required")
	}
	if strings.TrimSpace(req.CipherAlgorithm) == "" {
		return fmt.Errorf("cipher_algorithm is required")
	}
	if req.CipherTagEmbedded == nil {
		return fmt.Errorf("cipher_tag_embedded is required")
	}
	if strings.TrimSpace(req.AADMode) == "" {
		return fmt.Errorf("aad_mode is required")
	}
	if strings.TrimSpace(req.PasswordProcessing) == "" {
		return fmt.Errorf("password_processing is required")
	}
	return nil
}

func validatePBKDF2Params(params *domain.KeyDerivationParams) error {
	if params == nil {
		return fmt.Errorf("kdf_params is required for pbkdf2-sha256")
	}
	if params.Iterations == nil || *params.Iterations < legacyPBKDF2SHA256Iterations {
		return fmt.Errorf("kdf_params.iterations must be at least %d", legacyPBKDF2SHA256Iterations)
	}
	if params.Hash == nil || strings.TrimSpace(strings.ToLower(*params.Hash)) != "sha256" {
		return fmt.Errorf("kdf_params.hash must be sha256")
	}
	if params.KeyLength == nil || *params.KeyLength != legacyPBKDF2SHA256DerivedKeyLength {
		return fmt.Errorf("kdf_params.key_length must be %d", legacyPBKDF2SHA256DerivedKeyLength)
	}
	return nil
}

func validateArgon2IDParams(params *domain.KeyDerivationParams) error {
	if params == nil {
		return nil
	}
	if params.MemoryKiB != nil && *params.MemoryKiB <= 0 {
		return fmt.Errorf("kdf_params.memory_kib must be positive")
	}
	if params.Iterations != nil && *params.Iterations <= 0 {
		return fmt.Errorf("kdf_params.iterations must be positive")
	}
	if params.Parallelism != nil && *params.Parallelism <= 0 {
		return fmt.Errorf("kdf_params.parallelism must be positive")
	}
	if params.KeyLength != nil && *params.KeyLength != legacyPBKDF2SHA256DerivedKeyLength {
		return fmt.Errorf("kdf_params.key_length must be %d", legacyPBKDF2SHA256DerivedKeyLength)
	}
	return nil
}

func validateCanonicalPublicKey(value, fieldName string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}

	decoded, err := decodeStandardBase64(normalized)
	if err != nil {
		return "", fmt.Errorf("%s must be standard base64 encoded %s public key (%s)", fieldName, canonicalDeviceKeyType, canonicalPublicKeyFormat)
	}
	if len(decoded) != x25519PublicKeyLengthInBytes {
		return "", fmt.Errorf("%s must decode to %d-byte %s public key", fieldName, x25519PublicKeyLengthInBytes, canonicalDeviceKeyType)
	}

	return normalized, nil
}

func decodeStandardBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty base64 value")
	}

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}

	return base64.RawStdEncoding.DecodeString(value)
}
