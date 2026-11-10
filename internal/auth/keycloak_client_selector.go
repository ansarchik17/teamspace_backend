package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/west6ide/workspace-backend/internal/keycloak"
)

func resolveKeycloakClientForToken(token string, defaultClient *keycloak.Client, clients map[string]*keycloak.Client) *keycloak.Client {
	if defaultClient == nil {
		return nil
	}

	clientID := extractAZPFromJWT(token)
	if clientID == "" {
		return defaultClient
	}

	if client := clients[strings.TrimSpace(clientID)]; client != nil {
		return client
	}

	return defaultClient
}

func extractAZPFromJWT(tokenStr string) string {
	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return ""
	}

	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	azp, _ := claims["azp"].(string)
	return strings.TrimSpace(azp)
}
