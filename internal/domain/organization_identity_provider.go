package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	IdentityProviderOIDC = "oidc"
	IdentityProviderSAML = "saml"
)

type OrganizationIdentityProvider struct {
	ID             uuid.UUID         `json:"id"`
	OrganizationID uuid.UUID         `json:"organization_id"`
	ProviderType   string            `json:"provider_type"`
	Alias          string            `json:"alias"`
	DisplayName    string            `json:"display_name"`
	Enabled        bool              `json:"enabled"`
	Settings       map[string]string `json:"settings,omitempty"`
	SecretSet      bool              `json:"secret_set"`
	Status         string            `json:"status"`
	LastError      string            `json:"-"`
	LastTestedAt   *time.Time        `json:"last_tested_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type CreateOrganizationIdentityProviderRequest struct {
	ProviderType string            `json:"provider_type" binding:"required"`
	DisplayName  string            `json:"display_name" binding:"required"`
	Settings     map[string]string `json:"settings"`
	ClientSecret string            `json:"client_secret"`
	Enabled      *bool             `json:"enabled"`
}

type UpdateOrganizationIdentityProviderRequest struct {
	DisplayName  *string           `json:"display_name"`
	Settings     map[string]string `json:"settings"`
	ClientSecret *string           `json:"client_secret"`
	Enabled      *bool             `json:"enabled"`
}

var (
	ErrIdentityProviderTypeInvalid   = errors.New("identity provider type is invalid")
	ErrIdentityProviderNameRequired  = errors.New("identity provider display name is required")
	ErrIdentityProviderSettings      = errors.New("identity provider settings are invalid")
	ErrIdentityProviderSecretMissing = errors.New("identity provider client secret is required")
)

func (r CreateOrganizationIdentityProviderRequest) Normalize() (CreateOrganizationIdentityProviderRequest, error) {
	r.ProviderType = strings.ToLower(strings.TrimSpace(r.ProviderType))
	r.DisplayName = strings.TrimSpace(r.DisplayName)
	if r.ProviderType != IdentityProviderOIDC && r.ProviderType != IdentityProviderSAML {
		return r, ErrIdentityProviderTypeInvalid
	}
	if r.DisplayName == "" {
		return r, ErrIdentityProviderNameRequired
	}
	if len([]rune(r.DisplayName)) > 255 {
		return r, ErrIdentityProviderNameRequired
	}
	if r.Settings == nil {
		r.Settings = map[string]string{}
	}
	for key, value := range r.Settings {
		if strings.TrimSpace(key) == "" || len([]rune(key)) > 120 || len([]rune(value)) > 4096 {
			return r, ErrIdentityProviderSettings
		}
	}
	return r, nil
}
