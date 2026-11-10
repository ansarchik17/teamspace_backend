package middleware

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestClassifyTokenValidationError(t *testing.T) {
	t.Parallel()

	expired := classifyTokenValidationError(errors.New("token has invalid claims: token is expired"))
	assert.Equal(t, apperrors.ErrCodeTokenExpired, expired.Code)
	assert.Equal(t, "Token has expired", expired.Message)

	invalid := classifyTokenValidationError(errors.New("signature is invalid"))
	assert.Equal(t, apperrors.ErrCodeTokenInvalid, invalid.Code)
	assert.Equal(t, "Invalid token", invalid.Message)
}

func TestMapKeycloakRolesToDomain(t *testing.T) {
	t.Parallel()

	assert.Equal(t, domain.RoleSuperAdmin, mapKeycloakRolesToDomain([]string{"admin"}))
	assert.Equal(t, domain.RoleSuperAdmin, mapKeycloakRolesToDomain([]string{"external_user_admin"}))
	assert.Equal(t, domain.RoleSuperAdmin, mapKeycloakRolesToDomain([]string{"user", "external_user_admin"}))
	assert.Equal(t, domain.RoleUser, mapKeycloakRolesToDomain([]string{"user"}))
}
