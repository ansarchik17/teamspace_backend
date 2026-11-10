package keycloak

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteUserTreatsMissingIdentityAsCompleted(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"access_token":"test","expires_in":300}`))
					return
				}
				if r.Method != http.MethodDelete || r.URL.Path != "/admin/realms/Test/users/retired" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			client := NewAdminWithClientCredentials(server.URL, "Test", "test-admin", "test-only")
			err := client.DeleteUser(context.Background(), "Test", "retired")
			if (err != nil) != (status == http.StatusServiceUnavailable) {
				t.Fatalf("status=%d error=%v", status, err)
			}
		})
	}
}
