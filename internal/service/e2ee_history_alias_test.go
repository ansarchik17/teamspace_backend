package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAliasEncryptedKeysForDevice_CopiesEquivalentEnvelopeToCurrentDevice(t *testing.T) {
	t.Parallel()

	messages := []*domain.MessageView{
		{
			Message: domain.Message{
				ID: uuid.New(),
				EncryptedKeys: map[string]string{
					"mobile-device-1": "ciphertext-for-restored-key",
				},
			},
		},
	}

	aliasEncryptedKeysForDevice(messages, "web-device-1", []string{"mobile-device-1"})

	assert.Equal(t, "ciphertext-for-restored-key", messages[0].EncryptedKeys["web-device-1"])
}

func TestAliasEncryptedKeysForDevice_LeavesCurrentEnvelopeUntouched(t *testing.T) {
	t.Parallel()

	messages := []*domain.MessageView{
		{
			Message: domain.Message{
				ID: uuid.New(),
				EncryptedKeys: map[string]string{
					"web-device-1":    "already-current",
					"mobile-device-1": "older-envelope",
				},
			},
		},
	}

	aliasEncryptedKeysForDevice(messages, "web-device-1", []string{"mobile-device-1"})

	assert.Equal(t, "already-current", messages[0].EncryptedKeys["web-device-1"])
}

func TestAliasEncryptedKeysForDevice_AppliesToReplyMessagesToo(t *testing.T) {
	t.Parallel()

	messages := []*domain.MessageView{
		{
			Message: domain.Message{
				ID: uuid.New(),
			},
			ReplyTo: &domain.MessageView{
				Message: domain.Message{
					ID: uuid.New(),
					EncryptedKeys: map[string]string{
						"mobile-device-1": "reply-ciphertext",
					},
				},
			},
		},
	}

	aliasEncryptedKeysForDevice(messages, "web-device-1", []string{"mobile-device-1"})

	assert.Equal(t, "reply-ciphertext", messages[0].ReplyTo.EncryptedKeys["web-device-1"])
}
