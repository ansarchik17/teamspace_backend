package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func validPBKDF2BackupRequestForTest() *SaveKeyBackupRequest {
	cipherTagEmbedded := true
	return &SaveKeyBackupRequest{
		EncryptedPrivateKey: "Y2lwaGVydGV4dA==",
		Salt:                "MDEyMzQ1Njc4OWFiY2RlZg==",
		IV:                  "MDEyMzQ1Njc4OWFi",
		KDFAlgorithm:        "pbkdf2-sha256",
		KDFParams: &domain.KeyDerivationParams{
			Iterations: intPtrForTest(legacyPBKDF2SHA256Iterations),
			Hash:       stringPtrForTest("sha256"),
			KeyLength:  intPtrForTest(legacyPBKDF2SHA256DerivedKeyLength),
		},
		Version:             1,
		BackupKeyType:       canonicalDeviceKeyType,
		BackupKeyFormat:     canonicalBackupKeyFormat,
		BackupKeyScope:      canonicalBackupKeyScope,
		BackupFormatVersion: canonicalBackupFormatVersion,
		CipherAlgorithm:     canonicalBackupCipher,
		CipherTagEmbedded:   &cipherTagEmbedded,
		AADMode:             canonicalBackupAADMode,
		PasswordProcessing:  canonicalPasswordProcessing,
	}
}

func TestBuildStrictValidatedKeyBackupRejectsImplicitContractDefaults(t *testing.T) {
	req := validPBKDF2BackupRequestForTest()
	req.CipherAlgorithm = ""

	_, err := BuildStrictValidatedKeyBackup(77, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cipher_algorithm is required")
}

func TestBuildValidatedKeyBackupKeepsCompatibilityDefaults(t *testing.T) {
	req := validPBKDF2BackupRequestForTest()
	req.BackupKeyType = ""
	req.BackupKeyFormat = ""
	req.BackupKeyScope = ""
	req.BackupFormatVersion = 0
	req.CipherAlgorithm = ""
	req.CipherTagEmbedded = nil
	req.AADMode = ""
	req.PasswordProcessing = ""

	backup, err := BuildValidatedKeyBackup(77, req)
	require.NoError(t, err)
	require.Equal(t, canonicalDeviceKeyType, backup.BackupKeyType)
	require.Equal(t, canonicalBackupKeyFormat, backup.BackupKeyFormat)
	require.Equal(t, canonicalBackupKeyScope, backup.BackupKeyScope)
	require.Equal(t, canonicalBackupFormatVersion, backup.BackupFormatVersion)
	require.Equal(t, canonicalBackupCipher, backup.CipherAlgorithm)
	require.True(t, backup.CipherTagEmbedded)
	require.Equal(t, canonicalBackupAADMode, backup.AADMode)
	require.Equal(t, canonicalPasswordProcessing, backup.PasswordProcessing)
}

func intPtrForTest(value int) *int {
	return &value
}

func stringPtrForTest(value string) *string {
	return &value
}
