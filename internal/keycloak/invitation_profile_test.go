package keycloak

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvitationProfilePreservesConfiguration(t *testing.T) {
	profile := map[string]any{"unmanagedAttributePolicy": "DISABLED", "groups": []any{map[string]any{"name": "existing-group"}}, "attributes": []any{map[string]any{"name": "username", "validations": map[string]any{"length": map[string]any{"min": 3}}}, map[string]any{"name": "iin", "permissions": map[string]any{"view": []any{"admin", "user"}, "edit": []any{"admin"}}}}}
	updates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/realms/master/protocol/openid-connect/token" {
			_, _ = w.Write([]byte(`{"access_token":"synthetic-admin","expires_in":300}`))
			return
		}
		require.Equal(t, "/admin/realms/test/users/profile", r.URL.Path)
		if r.Method == http.MethodPut {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&profile))
			updates++
			w.WriteHeader(204)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(profile))
	}))
	defer server.Close()
	admin := NewAdmin(server.URL, "master", "test", "test")
	require.NoError(t, admin.EnsureInvitationProfileAttributes(context.Background(), "test"))
	require.NoError(t, admin.EnsureInvitationProfileAttributes(context.Background(), "test"))
	require.Equal(t, 1, updates)
	require.Equal(t, "DISABLED", profile["unmanagedAttributePolicy"])
	require.Len(t, profile["groups"], 1)
	attributes := profile["attributes"].([]any)
	require.Len(t, attributes, 3)
	require.Equal(t, []any{"admin", "user"}, attributes[1].(map[string]any)["permissions"].(map[string]any)["view"])
	marker := attributes[2].(map[string]any)
	require.Equal(t, "workspace_invitation_id", marker["name"])
	require.Equal(t, []any{"admin"}, marker["permissions"].(map[string]any)["edit"])
}
