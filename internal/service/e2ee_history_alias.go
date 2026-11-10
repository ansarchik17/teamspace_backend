package service

import "github.com/west6ide/workspace-backend/internal/domain"

func aliasEncryptedKeysForDevice(messages []*domain.MessageView, currentDeviceID string, equivalentDeviceIDs []string) {
	if currentDeviceID == "" || len(equivalentDeviceIDs) == 0 || len(messages) == 0 {
		return
	}

	for _, message := range messages {
		aliasMessageEncryptedKeys(message, currentDeviceID, equivalentDeviceIDs)
	}
}

func aliasMessageEncryptedKeys(message *domain.MessageView, currentDeviceID string, equivalentDeviceIDs []string) {
	if message == nil {
		return
	}

	aliasEncryptedKeyMap(message.EncryptedKeys, currentDeviceID, equivalentDeviceIDs)
	if message.ReplyTo != nil {
		aliasMessageEncryptedKeys(message.ReplyTo, currentDeviceID, equivalentDeviceIDs)
	}
}

func aliasEncryptedKeyMap(keys map[string]string, currentDeviceID string, equivalentDeviceIDs []string) {
	if len(keys) == 0 {
		return
	}
	if _, ok := keys[currentDeviceID]; ok {
		return
	}

	for _, equivalentDeviceID := range equivalentDeviceIDs {
		if ciphertext, ok := keys[equivalentDeviceID]; ok {
			keys[currentDeviceID] = ciphertext
			return
		}
	}
}
