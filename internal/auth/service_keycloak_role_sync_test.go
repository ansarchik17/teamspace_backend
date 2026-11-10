package auth

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestMapKeycloakClaimsToDomainRole(t *testing.T) {
	tests := []struct {
		name   string
		claims jwt.MapClaims
		want   domain.Role
	}{
		{
			name: "admin realm role",
			claims: jwt.MapClaims{
				"realm_access": map[string]interface{}{
					"roles": []interface{}{"default-roles-workspace", "admin"},
				},
			},
			want: domain.RoleSuperAdmin,
		},
		{
			name: "manager realm role",
			claims: jwt.MapClaims{
				"realm_access": map[string]interface{}{
					"roles": []interface{}{"manager"},
				},
			},
			want: domain.RoleManager,
		},
		{
			name: "client role cannot elevate user",
			claims: jwt.MapClaims{
				"resource_access": map[string]interface{}{
					"workspace-client": map[string]interface{}{
						"roles": []interface{}{"admin"},
					},
				},
			},
			want: domain.RoleUser,
		},
		{
			name: "missing roles defaults to user",
			claims: jwt.MapClaims{
				"realm_access": map[string]interface{}{
					"roles": []interface{}{"default-roles-workspace"},
				},
			},
			want: domain.RoleUser,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapKeycloakClaimsToDomainRole(tt.claims); got != tt.want {
				t.Fatalf("mapKeycloakClaimsToDomainRole() = %q, want %q", got, tt.want)
			}
		})
	}
}
