package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

var identityProviderAliasPartPattern = regexp.MustCompile(`[^a-z0-9-]+`)

type OrganizationIdentityProviderService struct {
	repo    *repository.OrganizationIdentityProviderRepository
	kcAdmin identityProviderAdmin
	realm   string
	logger  *zap.Logger
}

func NewOrganizationIdentityProviderService(repo *repository.OrganizationIdentityProviderRepository, kcAdmin *keycloak.AdminClient, realm string, logger *zap.Logger) *OrganizationIdentityProviderService {
	svc := &OrganizationIdentityProviderService{repo: repo, realm: realm, logger: logger.With(zap.String("service", "organization_identity_provider"))}
	if kcAdmin != nil {
		svc.kcAdmin = kcAdmin
	}
	return svc
}

func (s *OrganizationIdentityProviderService) List(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.OrganizationIdentityProvider, error) {
	return s.repo.List(ctx, userID, organizationID)
}

func (s *OrganizationIdentityProviderService) Get(ctx context.Context, userID int64, organizationID, providerID uuid.UUID) (*domain.OrganizationIdentityProvider, error) {
	return s.repo.Get(ctx, userID, organizationID, providerID)
}

func (s *OrganizationIdentityProviderService) create(ctx context.Context, userID int64, organizationID uuid.UUID, req domain.CreateOrganizationIdentityProviderRequest, undo *providerCompensation) (*domain.OrganizationIdentityProvider, error) {
	normalized, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	normalized.Settings, err = domain.NormalizeIdentityProviderSettings(normalized.ProviderType, normalized.Settings)
	if err != nil {
		return nil, err
	}
	normalized.ClientSecret = strings.TrimSpace(normalized.ClientSecret)
	if len(normalized.ClientSecret) > 8192 {
		return nil, domain.ErrIdentityProviderSettings
	}
	if err := validateIdentityProviderSettings(normalized.ProviderType, normalized.Settings, normalized.ClientSecret); err != nil {
		return nil, err
	}
	if s.kcAdmin == nil {
		return nil, apperrors.Conflict("corporate login integration is not configured")
	}
	slug, err := s.repo.OrganizationSlug(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}
	alias := buildIdentityProviderAlias(slug, organizationID)
	provider := buildKeycloakIdentityProvider(alias, normalized.ProviderType, normalized.DisplayName, normalized.Settings, normalized.ClientSecret, normalized.Enabled == nil || *normalized.Enabled)
	if err := s.kcAdmin.CreateIdentityProvider(ctx, s.realm, provider); err != nil {
		return nil, fmt.Errorf("create Keycloak identity provider: %w", err)
	}
	*undo = func(cleanup context.Context) error { return s.kcAdmin.DeleteIdentityProvider(cleanup, s.realm, alias) }
	if err := s.ensureOrganizationMapper(ctx, alias, organizationID); err != nil {
		return nil, fmt.Errorf("configure Keycloak organization mapper: %w", err)
	}
	enabled := normalized.Enabled == nil || *normalized.Enabled
	item, err := s.repo.Create(ctx, userID, organizationID, normalized.ProviderType, alias, normalized.DisplayName, sanitizeIdentityProviderSettings(normalized.Settings), normalized.ClientSecret != "", enabled)
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (s *OrganizationIdentityProviderService) update(ctx context.Context, userID int64, organizationID, providerID uuid.UUID, req domain.UpdateOrganizationIdentityProviderRequest, undo *providerCompensation) (*domain.OrganizationIdentityProvider, error) {
	item, err := s.repo.Get(ctx, userID, organizationID, providerID)
	if err != nil {
		return nil, err
	}
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" || len([]rune(name)) > 255 {
			return nil, domain.ErrIdentityProviderNameRequired
		}
		req.DisplayName = &name
	}
	settings := item.Public().Settings
	if req.Settings != nil {
		settings, err = domain.NormalizeIdentityProviderSettings(item.ProviderType, req.Settings)
		if err != nil {
			return nil, err
		}
	}
	secret := ""
	if req.ClientSecret != nil {
		secret = strings.TrimSpace(*req.ClientSecret)
		if len(secret) > 8192 {
			return nil, domain.ErrIdentityProviderSettings
		}
		if item.ProviderType == domain.IdentityProviderOIDC && secret == "" {
			return nil, domain.ErrIdentityProviderSecretMissing
		}
	}
	if req.Settings != nil {
		if err := validateIdentityProviderSettings(item.ProviderType, settings, secretOrExisting(item, secret)); err != nil {
			return nil, err
		}
	}
	if s.kcAdmin == nil {
		return nil, apperrors.Conflict("corporate login integration is not configured")
	}
	provider, err := s.kcAdmin.GetIdentityProvider(ctx, s.realm, item.Alias)
	if err != nil {
		return nil, fmt.Errorf("read Keycloak identity provider: %w", err)
	}
	previousProvider := *provider
	provider.DisplayName = item.DisplayName
	if req.DisplayName != nil {
		provider.DisplayName = *req.DisplayName
	}
	if req.Enabled != nil {
		provider.Enabled = *req.Enabled
	}
	if req.Settings != nil {
		// Replacement removes omitted public options while preserving server-held secrets.
		config := map[string]string{}
		managed := map[string]bool{}
		for _, key := range domain.IdentityProviderSettingKeys(item.ProviderType) {
			managed[key] = true
		}
		for key, value := range provider.Config {
			if !managed[key] {
				config[key] = value
			}
		}
		provider.Config = config
	}
	provider.Config = mergeKeycloakProviderConfig(provider.Config, settings, secret)
	*undo = func(cleanup context.Context) error {
		return s.kcAdmin.UpdateIdentityProvider(cleanup, s.realm, item.Alias, previousProvider)
	}
	if err := s.kcAdmin.UpdateIdentityProvider(ctx, s.realm, item.Alias, *provider); err != nil {
		return nil, fmt.Errorf("update Keycloak identity provider: %w", err)
	}
	var secretSet *bool
	if req.ClientSecret != nil {
		value := secret != ""
		secretSet = &value
	}
	status := stringPtr("active")
	if !provider.Enabled {
		status = stringPtr("disabled")
	}
	updated, repoErr := s.repo.Update(ctx, userID, organizationID, providerID, req.DisplayName, settingsIfChanged(item.Settings, settings, req.Settings != nil), secretSet, req.Enabled, status, stringPtr(""), identityTimePtr(time.Now()))
	if repoErr != nil {
		return nil, repoErr
	}
	return updated, nil
}

func (s *OrganizationIdentityProviderService) delete(ctx context.Context, userID int64, organizationID, providerID uuid.UUID, undo *providerCompensation) error {
	item, err := s.repo.Get(ctx, userID, organizationID, providerID)
	if err != nil {
		return err
	}
	if s.kcAdmin == nil {
		return apperrors.Conflict("corporate login integration is not configured")
	}
	previous, err := s.kcAdmin.GetIdentityProvider(ctx, s.realm, item.Alias)
	if err != nil {
		return fmt.Errorf("read Keycloak identity provider: %w", err)
	}
	mappers, err := s.kcAdmin.GetIdentityProviderMappers(ctx, s.realm, item.Alias)
	if err != nil {
		return fmt.Errorf("read Keycloak identity provider mappers: %w", err)
	}
	if err := s.kcAdmin.DeleteIdentityProvider(ctx, s.realm, item.Alias); err != nil {
		return fmt.Errorf("delete Keycloak identity provider: %w", err)
	}
	*undo = func(cleanup context.Context) error {
		if err := s.kcAdmin.CreateIdentityProvider(cleanup, s.realm, *previous); err != nil {
			return err
		}
		for _, mapper := range mappers {
			if err := s.kcAdmin.CreateIdentityProviderMapper(cleanup, s.realm, item.Alias, mapper); err != nil {
				return err
			}
		}
		return nil
	}
	return s.repo.Delete(ctx, userID, organizationID, providerID)
}

func (s *OrganizationIdentityProviderService) ensureOrganizationMapper(ctx context.Context, alias string, organizationID uuid.UUID) error {
	return s.kcAdmin.CreateIdentityProviderMapper(ctx, s.realm, alias, keycloak.IdentityProviderMapperRepresentation{
		Name:                   "workspace-organization-id",
		IdentityProviderAlias:  alias,
		IdentityProviderMapper: "hardcoded-attribute-idp-mapper",
		Config: map[string]string{
			"attribute.name":  "workspace_organization_id",
			"attribute.value": organizationID.String(),
		},
	})
}

func buildIdentityProviderAlias(slug string, organizationID uuid.UUID) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	slug = identityProviderAliasPartPattern.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "organization"
	}
	return fmt.Sprintf("workspace-%s-%s", slug, strings.ReplaceAll(organizationID.String()[:8], "-", ""))
}

func buildKeycloakIdentityProvider(alias, providerType, displayName string, settings map[string]string, clientSecret string, enabled bool) keycloak.IdentityProviderRepresentation {
	config := make(map[string]string, len(settings)+3)
	for key, value := range settings {
		config[keycloakConfigKey(key)] = strings.TrimSpace(value)
	}
	if clientSecret != "" {
		config["clientSecret"] = clientSecret
	}
	providerID := "oidc"
	if providerType == domain.IdentityProviderSAML {
		providerID = "saml"
	}
	return keycloak.IdentityProviderRepresentation{Alias: alias, DisplayName: displayName, ProviderID: providerID, Enabled: enabled, Config: config}
}

func mergeKeycloakProviderConfig(existing map[string]string, settings map[string]string, clientSecret string) map[string]string {
	merged := make(map[string]string, len(existing)+len(settings)+1)
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range settings {
		merged[keycloakConfigKey(key)] = strings.TrimSpace(value)
	}
	if clientSecret != "" {
		merged["clientSecret"] = clientSecret
	}
	return merged
}

func validateIdentityProviderSettings(providerType string, settings map[string]string, clientSecret string) error {
	if providerType == domain.IdentityProviderOIDC {
		for _, key := range []string{"authorizationUrl", "tokenUrl", "clientId"} {
			if strings.TrimSpace(settings[key]) == "" && strings.TrimSpace(settings[snakeCaseKey(key)]) == "" {
				return fmt.Errorf("%w: %s is required", domain.ErrIdentityProviderSettings, key)
			}
		}
		if strings.TrimSpace(clientSecret) == "" {
			return domain.ErrIdentityProviderSecretMissing
		}
	}
	if providerType == domain.IdentityProviderSAML {
		if value(settings, "singleSignOnServiceUrl") == "" || value(settings, "entityId") == "" {
			return fmt.Errorf("%w: singleSignOnServiceUrl and entityId are required", domain.ErrIdentityProviderSettings)
		}
	}
	return nil
}

func sanitizeIdentityProviderSettings(settings map[string]string) map[string]string {
	result := make(map[string]string, len(settings))
	for key, value := range settings {
		key = strings.TrimSpace(key)
		lower := strings.ToLower(key)
		if lower == "clientsecret" || lower == "client_secret" || lower == "bindcredential" || lower == "bind_credential" {
			continue
		}
		result[key] = strings.TrimSpace(value)
	}
	return result
}

func keycloakConfigKey(key string) string {
	keys := map[string]string{
		"authorization_url": "authorizationUrl", "token_url": "tokenUrl", "user_info_url": "userInfoUrl",
		"logout_url": "logoutUrl", "client_id": "clientId", "issuer": "issuer", "jwks_url": "jwksUrl",
		"default_scope": "defaultScope", "client_auth_method": "clientAuthMethod", "validate_signature": "validateSignature",
		"use_jwks_url": "useJwksUrl", "single_sign_on_service_url": "singleSignOnServiceUrl", "entity_id": "entityId",
		"single_logout_service_url": "singleLogoutServiceUrl", "signing_certificate": "signingCertificate", "name_id_policy_format": "nameIDPolicyFormat",
	}
	if mapped, ok := keys[strings.ToLower(strings.TrimSpace(key))]; ok {
		return mapped
	}
	return key
}

func snakeCaseKey(key string) string {
	var out []rune
	for _, ch := range key {
		if ch >= 'A' && ch <= 'Z' {
			out = append(out, '_', ch+('a'-'A'))
		} else {
			out = append(out, ch)
		}
	}
	return string(out)
}

func value(settings map[string]string, key string) string {
	if strings.TrimSpace(settings[key]) != "" {
		return strings.TrimSpace(settings[key])
	}
	return strings.TrimSpace(settings[snakeCaseKey(key)])
}

func secretOrExisting(item *domain.OrganizationIdentityProvider, secret string) string {
	if secret != "" || item.SecretSet {
		return "configured"
	}
	return ""
}

func settingsIfChanged(current, next map[string]string, changed bool) map[string]string {
	if !changed {
		return nil
	}
	return next
}

func stringPtr(value string) *string             { return &value }
func identityTimePtr(value time.Time) *time.Time { return &value }
