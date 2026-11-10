package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// EnsureInvitationProfileAttributes declares only the backend-owned attributes
// needed by registration. It preserves the realm's unmanaged-attribute policy
// and all existing user-profile settings. The ownership marker is never user-editable.
func (a *AdminClient) EnsureInvitationProfileAttributes(ctx context.Context, realm string) error {
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users/profile", a.base, url.PathEscape(realm))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get invitation profile configuration: status=%d", resp.StatusCode)
	}
	var profile map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return err
	}
	attributes, ok := profile["attributes"].([]any)
	if !ok {
		return fmt.Errorf("missing Keycloak profile attributes")
	}
	changed := false
	for _, name := range []string{"iin", "workspace_invitation_id"} {
		found := false
		for _, raw := range attributes {
			attribute, ok := raw.(map[string]any)
			if !ok || attribute["name"] != name {
				continue
			}
			found = true
			if name == "workspace_invitation_id" {
				permissions := map[string]any{"view": []string{"admin"}, "edit": []string{"admin"}}
				before, _ := json.Marshal(attribute["permissions"])
				after, _ := json.Marshal(permissions)
				if !bytes.Equal(before, after) {
					attribute["permissions"] = permissions
					changed = true
				}
			}
		}
		if !found {
			attributes = append(attributes, map[string]any{"name": name, "multivalued": false, "permissions": map[string]any{"view": []string{"admin"}, "edit": []string{"admin"}}})
			changed = true
		}
	}
	if !changed {
		return nil
	}
	profile["attributes"] = attributes
	data, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	update, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	update.Header.Set("Authorization", "Bearer "+token)
	update.Header.Set("Content-Type", "application/json")
	saved, err := a.http.Do(update)
	if err != nil {
		return err
	}
	defer saved.Body.Close()
	if saved.StatusCode >= 300 {
		return fmt.Errorf("configure invitation profile attributes: status=%d", saved.StatusCode)
	}
	return nil
}
