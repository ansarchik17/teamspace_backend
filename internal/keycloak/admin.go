package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type AdminClient struct {
	base         string
	tokenRealm   string
	clientID     string
	clientSecret string
	adminUser    string
	adminPass    string
	http         *http.Client
}

func NewAdmin(base, adminRealm, adminUser, adminPass string) *AdminClient {
	return &AdminClient{
		base: base, tokenRealm: adminRealm,
		adminUser: adminUser, adminPass: adminPass,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

func NewAdminWithClientCredentials(base, tokenRealm, clientID, clientSecret string) *AdminClient {
	return &AdminClient{
		base:         base,
		tokenRealm:   tokenRealm,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 10 * time.Second},
	}
}

func NewAdminWithFallback(base, tokenRealm, clientID, clientSecret, adminUser, adminPass string) *AdminClient {
	return &AdminClient{
		base:         base,
		tokenRealm:   tokenRealm,
		clientID:     clientID,
		clientSecret: clientSecret,
		adminUser:    adminUser,
		adminPass:    adminPass,
		http:         &http.Client{Timeout: 10 * time.Second},
	}
}

type adminTokenResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

func (a *AdminClient) tokenURL() string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", a.base, a.tokenRealm)
}

func (a *AdminClient) AdminToken(ctx context.Context) (string, error) {
	var errs []string

	if strings.TrimSpace(a.clientID) != "" {
		token, err := a.requestAdminToken(ctx, clientCredentialsForm(a.clientID, a.clientSecret))
		if err == nil {
			return token, nil
		}
		errs = append(errs, fmt.Sprintf("client_credentials: %v", err))
	}

	if strings.TrimSpace(a.adminUser) != "" {
		token, err := a.requestAdminToken(ctx, passwordGrantForm(a.adminUser, a.adminPass))
		if err == nil {
			return token, nil
		}
		errs = append(errs, fmt.Sprintf("password_grant: %v", err))
	}

	if len(errs) == 0 {
		return "", fmt.Errorf("admin token failed: no admin authentication method configured")
	}
	return "", fmt.Errorf("admin token failed: %s", strings.Join(errs, "; "))
}

func clientCredentialsForm(clientID, clientSecret string) url.Values {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	return form
}

func passwordGrantForm(adminUser, adminPass string) url.Values {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", "admin-cli")
	form.Set("username", adminUser)
	form.Set("password", adminPass)
	return form
}

func (a *AdminClient) requestAdminToken(ctx context.Context, form url.Values) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", a.tokenURL(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("admin token failed: status=%d", resp.StatusCode)
	}

	var out adminTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.AccessToken, nil
}

type CreateUserRequest struct {
	Username      string              `json:"username"`
	Email         string              `json:"email"`
	Enabled       bool                `json:"enabled"`
	EmailVerified bool                `json:"emailVerified,omitempty"`
	FirstName     string              `json:"firstName,omitempty"`
	LastName      string              `json:"lastName,omitempty"`
	Attributes    map[string][]string `json:"attributes,omitempty"`
}

func (a *AdminClient) CreateUser(ctx context.Context, realm string, req CreateUserRequest) (string, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return "", err
	}

	body, _ := json.Marshal(req)
	u := fmt.Sprintf("%s/admin/realms/%s/users", a.base, realm)

	rq, _ := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(rq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("create user failed: status=%d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("create user: missing Location header")
	}
	parts := strings.Split(strings.TrimRight(loc, "/"), "/")
	return parts[len(parts)-1], nil
}

type UserUpdateRequest struct {
	Username      *string             `json:"username,omitempty"`
	FirstName     *string             `json:"firstName,omitempty"`
	LastName      *string             `json:"lastName,omitempty"`
	Email         *string             `json:"email,omitempty"`
	EmailVerified *bool               `json:"emailVerified,omitempty"`
	Enabled       *bool               `json:"enabled,omitempty"`
	Attributes    map[string][]string `json:"attributes,omitempty"`
}

type IdentityProviderRepresentation struct {
	Alias       string            `json:"alias"`
	DisplayName string            `json:"displayName,omitempty"`
	ProviderID  string            `json:"providerId"`
	Enabled     bool              `json:"enabled"`
	Config      map[string]string `json:"config,omitempty"`
}

type IdentityProviderMapperRepresentation struct {
	Name                   string            `json:"name"`
	IdentityProviderAlias  string            `json:"identityProviderAlias"`
	IdentityProviderMapper string            `json:"identityProviderMapper"`
	Config                 map[string]string `json:"config"`
}

// CreateIdentityProvider creates a broker provider in a realm. Secrets are
// sent only to Keycloak and are intentionally not returned or persisted by
// Workspace.
func (a *AdminClient) CreateIdentityProvider(ctx context.Context, realm string, provider IdentityProviderRepresentation) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(provider)
	if err != nil {
		return fmt.Errorf("marshal identity provider: %w", err)
	}
	u := fmt.Sprintf("%s/admin/realms/%s/identity-provider/instances", a.base, url.PathEscape(realm))
	rq, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(rq)
	if err != nil {
		return fmt.Errorf("create identity provider request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("create identity provider failed: status=%d", resp.StatusCode)
	}
	return nil
}

// UpdateIdentityProvider updates an existing broker provider in a realm.
func (a *AdminClient) UpdateIdentityProvider(ctx context.Context, realm, alias string, provider IdentityProviderRepresentation) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(provider)
	if err != nil {
		return fmt.Errorf("marshal identity provider: %w", err)
	}
	u := fmt.Sprintf("%s/admin/realms/%s/identity-provider/instances/%s", a.base, url.PathEscape(realm), url.PathEscape(alias))
	rq, _ := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(rq)
	if err != nil {
		return fmt.Errorf("update identity provider request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("update identity provider failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (a *AdminClient) GetIdentityProvider(ctx context.Context, realm, alias string) (*IdentityProviderRepresentation, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/admin/realms/%s/identity-provider/instances/%s", a.base, url.PathEscape(realm), url.PathEscape(alias))
	rq, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.http.Do(rq)
	if err != nil {
		return nil, fmt.Errorf("get identity provider request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get identity provider failed: status=%d", resp.StatusCode)
	}
	var provider IdentityProviderRepresentation
	if err := json.NewDecoder(resp.Body).Decode(&provider); err != nil {
		return nil, fmt.Errorf("decode identity provider: %w", err)
	}
	return &provider, nil
}

// DeleteIdentityProvider removes a broker provider from a realm.
func (a *AdminClient) DeleteIdentityProvider(ctx context.Context, realm, alias string) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/admin/realms/%s/identity-provider/instances/%s", a.base, url.PathEscape(realm), url.PathEscape(alias))
	rq, _ := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.http.Do(rq)
	if err != nil {
		return fmt.Errorf("delete identity provider request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete identity provider failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (a *AdminClient) GetIdentityProviderMappers(ctx context.Context, realm, alias string) ([]IdentityProviderMapperRepresentation, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/identity-provider/instances/%s/mappers", a.base, url.PathEscape(realm), url.PathEscape(alias))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get identity provider mappers request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get identity provider mappers failed: status=%d", resp.StatusCode)
	}
	var mappers []IdentityProviderMapperRepresentation
	if err := json.NewDecoder(resp.Body).Decode(&mappers); err != nil {
		return nil, fmt.Errorf("decode identity provider mappers: %w", err)
	}
	return mappers, nil
}

// CreateIdentityProviderMapper adds a deterministic organization attribute to
// users imported through a provider. The attribute is later exposed as a
// trusted token claim by EnsureUserAttributeMappers.
func (a *AdminClient) CreateIdentityProviderMapper(ctx context.Context, realm, alias string, mapper IdentityProviderMapperRepresentation) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(mapper)
	if err != nil {
		return fmt.Errorf("marshal identity provider mapper: %w", err)
	}
	u := fmt.Sprintf("%s/admin/realms/%s/identity-provider/instances/%s/mappers", a.base, url.PathEscape(realm), url.PathEscape(alias))
	rq, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(rq)
	if err != nil {
		return fmt.Errorf("create identity provider mapper request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("create identity provider mapper failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (a *AdminClient) UpdateUser(ctx context.Context, realm, userID string, req UserUpdateRequest) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(req)
	u := fmt.Sprintf("%s/admin/realms/%s/users/%s", a.base, realm, userID)

	rq, _ := http.NewRequestWithContext(ctx, "PUT", u, bytes.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(rq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("update user failed: status=%d", resp.StatusCode)
	}
	return nil
}

type protocolMapperRep struct {
	ID              string            `json:"id,omitempty"`
	Name            string            `json:"name"`
	Protocol        string            `json:"protocol"`
	ProtocolMapper  string            `json:"protocolMapper"`
	ConsentRequired bool              `json:"consentRequired"`
	Config          map[string]string `json:"config"`
}

// EnsureUserAttributeMappers ensures that position, department_id, bin, and iin
// user attributes are mapped into JWT access and ID tokens for the given client.
// It is idempotent – existing mappers are not duplicated.
func (a *AdminClient) EnsureUserAttributeMappers(ctx context.Context, realm, clientID string) error {
	clientUUID, err := a.GetClientUUIDByClientID(ctx, realm, clientID)
	if err != nil {
		return fmt.Errorf("EnsureUserAttributeMappers: get client UUID: %w", err)
	}

	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}

	// Fetch existing mappers
	listURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s/protocol-mappers/models", a.base, realm, clientUUID)
	rq, _ := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	rq.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.http.Do(rq)
	if err != nil {
		return fmt.Errorf("EnsureUserAttributeMappers: list mappers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("EnsureUserAttributeMappers: list mappers failed: status=%d", resp.StatusCode)
	}
	var existing []protocolMapperRep
	if err := json.NewDecoder(resp.Body).Decode(&existing); err != nil {
		return fmt.Errorf("EnsureUserAttributeMappers: decode mappers: %w", err)
	}
	existingByName := make(map[string]struct{}, len(existing))
	for _, m := range existing {
		existingByName[m.Name] = struct{}{}
	}

	// Attributes we want mapped: claim name → user attribute name.
	// organization_id is written by the per-tenant identity-provider mapper.
	needed := []struct{ claim, attr string }{
		{"position", "position"},
		{"department_id", "department_id"},
		{"bin", "bin"},
		{"iin", "iin"},
		{"organization_id", "workspace_organization_id"},
	}

	token, err = a.AdminToken(ctx)
	if err != nil {
		return err
	}

	for _, pair := range needed {
		if _, ok := existingByName[pair.claim]; ok {
			continue // already exists
		}
		mapper := protocolMapperRep{
			Name:            pair.claim,
			Protocol:        "openid-connect",
			ProtocolMapper:  "oidc-usermodel-attribute-mapper",
			ConsentRequired: false,
			Config: map[string]string{
				"user.attribute":       pair.attr,
				"claim.name":           pair.claim,
				"jsonType.label":       "String",
				"id.token.claim":       "true",
				"access.token.claim":   "true",
				"userinfo.token.claim": "true",
				"multivalued":          "false",
				"aggregate.attrs":      "false",
			},
		}
		body, _ := json.Marshal(mapper)
		createURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s/protocol-mappers/models", a.base, realm, clientUUID)
		rq2, _ := http.NewRequestWithContext(ctx, "POST", createURL, bytes.NewReader(body))
		rq2.Header.Set("Authorization", "Bearer "+token)
		rq2.Header.Set("Content-Type", "application/json")
		resp2, err := a.http.Do(rq2)
		if err != nil {
			return fmt.Errorf("EnsureUserAttributeMappers: create mapper %q: %w", pair.claim, err)
		}
		resp2.Body.Close()
		if resp2.StatusCode >= 300 && resp2.StatusCode != 409 {
			return fmt.Errorf("EnsureUserAttributeMappers: create mapper %q failed: status=%d", pair.claim, resp2.StatusCode)
		}
	}
	return nil
}

type UserRepresentation struct {
	ID            string              `json:"id"`
	Enabled       bool                `json:"enabled"`
	EmailVerified bool                `json:"emailVerified"`
	Username      string              `json:"username"`
	Email         string              `json:"email"`
	FirstName     string              `json:"firstName"`
	LastName      string              `json:"lastName"`
	Attributes    map[string][]string `json:"attributes,omitempty"`
}

func (a *AdminClient) GetUserByID(ctx context.Context, realm, userID string) (*UserRepresentation, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/users/%s", a.base, realm, url.PathEscape(userID))
	rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get user by id failed: status=%d", resp.StatusCode)
	}

	var out UserRepresentation
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a *AdminClient) GetUsersByEmail(ctx context.Context, realm, email string) ([]UserRepresentation, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/users?email=%s&exact=true", a.base, realm, url.QueryEscape(email))
	rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get users by email failed: status=%d", resp.StatusCode)
	}

	var out []UserRepresentation
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *AdminClient) GetUsersByUsername(ctx context.Context, realm, username string) ([]UserRepresentation, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/users?username=%s&exact=true", a.base, realm, url.QueryEscape(username))
	rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get users by username failed: status=%d", resp.StatusCode)
	}

	var out []UserRepresentation
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

type setPasswordReq struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Temporary bool   `json:"temporary"`
}

func (a *AdminClient) SetPassword(ctx context.Context, realm, userID, password string, temporary bool) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/admin/realms/%s/users/%s/reset-password", a.base, realm, userID)
	payload, _ := json.Marshal(setPasswordReq{Type: "password", Value: password, Temporary: temporary})

	rq, _ := http.NewRequestWithContext(ctx, "PUT", u, bytes.NewReader(payload))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(rq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &RequestError{Operation: "set password", StatusCode: resp.StatusCode}
	}
	return nil
}

type RoleRepresentation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (a *AdminClient) GetRealmRole(ctx context.Context, realm, roleName string) (*RoleRepresentation, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/roles/%s", a.base, realm, url.PathEscape(roleName))
	rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get realm role failed: status=%d", resp.StatusCode)
	}
	var out RoleRepresentation
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a *AdminClient) SetUserRealmRoles(ctx context.Context, realm, userID string, roles []string) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}

	var reps []RoleRepresentation
	for _, rn := range roles {
		rp, err := a.GetRealmRole(ctx, realm, rn)
		if err != nil {
			return err
		}
		reps = append(reps, *rp)
	}

	u := fmt.Sprintf("%s/admin/realms/%s/users/%s/role-mappings/realm", a.base, realm, userID)
	body, _ := json.Marshal(reps)

	rq, _ := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(rq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("set user roles failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (a *AdminClient) DeleteUserRealmRoles(ctx context.Context, realm, userID string, roles []string) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}

	var reps []RoleRepresentation
	for _, rn := range roles {
		rp, err := a.GetRealmRole(ctx, realm, rn)
		if err != nil {
			return err
		}
		reps = append(reps, *rp)
	}

	u := fmt.Sprintf("%s/admin/realms/%s/users/%s/role-mappings/realm", a.base, realm, userID)
	body, _ := json.Marshal(reps)

	rq, _ := http.NewRequestWithContext(ctx, "DELETE", u, bytes.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+token)
	rq.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(rq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("delete user roles failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (a *AdminClient) LogoutUser(ctx context.Context, realm, userID string) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/users/%s/logout", a.base, realm, userID)
	rq, _ := http.NewRequestWithContext(ctx, "POST", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("logout user failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (a *AdminClient) DeleteUser(ctx context.Context, realm, userID string) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/users/%s", a.base, realm, userID)
	rq, _ := http.NewRequestWithContext(ctx, "DELETE", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete user failed: status=%d", resp.StatusCode)
	}
	return nil
}

type clientRep struct {
	ID       string `json:"id"`
	ClientID string `json:"clientId"`
}

func (a *AdminClient) GetClientUUIDByClientID(ctx context.Context, realm, clientID string) (string, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return "", err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/clients?clientId=%s", a.base, realm, url.QueryEscape(clientID))
	rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("get client failed: status=%d", resp.StatusCode)
	}

	var out []clientRep
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", fmt.Errorf("client not found by clientId=%s", clientID)
	}
	return out[0].ID, nil
}

type userSessionRep struct {
	ID     string `json:"id"`
	UserID string `json:"userId"`
}

func (a *AdminClient) ListClientUserSessions(ctx context.Context, realm, clientUUID string) ([]userSessionRep, error) {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/admin/realms/%s/clients/%s/user-sessions", a.base, realm, url.PathEscape(clientUUID))
	rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	rq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(rq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list user-sessions failed: status=%d", resp.StatusCode)
	}

	var out []userSessionRep
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
