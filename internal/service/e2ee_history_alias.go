package service

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
