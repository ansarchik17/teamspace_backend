package keycloak

import "fmt"

func BuildJWKSURL(keycloakURL, realm string) string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", keycloakURL, realm)
}

func BuildIssuer(keycloakURL, realm string) string {
	return fmt.Sprintf("%s/realms/%s", keycloakURL, realm)
}
