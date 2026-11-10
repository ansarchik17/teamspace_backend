package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestMailcowProvisionerCreatesPrimaryMailbox(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/add/mailbox", r.URL.Path)
		require.Equal(t, "test-key", r.Header.Get("X-API-Key"))

		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "sultan.sultan", payload["local_part"])
		require.Equal(t, "cdn-kindy.kz", payload["domain"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"success","msg":["mailbox_added","sultan.sultan@cdn-kindy.kz"]}]`))
	}))
	defer server.Close()

	provisioner := NewMailcowMailboxProvisioner(MailcowProvisionerConfig{
		BaseURL:            server.URL,
		APIKey:             "test-key",
		DefaultDomain:      "cdn-kindy.kz",
		InsecureSkipVerify: true,
	}, zap.NewNop())

	mailbox, err := provisioner.ProvisionPrimaryMailbox(context.Background(), &domain.UserResponse{
		ID:        9,
		Email:     "sultan.sultan@gmail.com",
		FullName:  "Sultan Sultan",
		FirstName: "Sultan",
		LastName:  "Sultan",
	})
	require.NoError(t, err)
	require.Equal(t, "sultan.sultan@cdn-kindy.kz", mailbox)
}

func TestMailcowProvisionerRetriesConflictWithSuffix(t *testing.T) {
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			require.Equal(t, "sultan.sultan", payload["local_part"])
			_, _ = w.Write([]byte(`[{"type":"error","msg":["mailbox_exists","sultan.sultan@cdn-kindy.kz"]}]`))
			return
		}

		require.Equal(t, "sultan.sultan_1", payload["local_part"])
		_, _ = w.Write([]byte(`[{"type":"success","msg":["mailbox_added","sultan.sultan_1@cdn-kindy.kz"]}]`))
	}))
	defer server.Close()

	provisioner := NewMailcowMailboxProvisioner(MailcowProvisionerConfig{
		BaseURL:            server.URL,
		APIKey:             "test-key",
		DefaultDomain:      "cdn-kindy.kz",
		InsecureSkipVerify: true,
	}, zap.NewNop())

	mailbox, err := provisioner.ProvisionPrimaryMailbox(context.Background(), &domain.UserResponse{
		ID:    9,
		Email: "sultan.sultan@gmail.com",
	})
	require.NoError(t, err)
	require.Equal(t, "sultan.sultan_1@cdn-kindy.kz", mailbox)
}

func TestStalwartProvisionerCreatesPrimaryMailbox(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/jmap/", r.URL.Path)
		username, password, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "admin", username)
		require.Equal(t, "secret", password)

		var payload struct {
			MethodCalls [][]json.RawMessage `json:"methodCalls"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Len(t, payload.MethodCalls, 1)

		var method string
		require.NoError(t, json.Unmarshal(payload.MethodCalls[0][0], &method))
		w.Header().Set("Content-Type", "application/json")
		if method == "x:Domain/query" {
			_, _ = w.Write([]byte(`{"methodResponses":[["x:Domain/query",{"ids":["domain-1"]},"domain-query"]]}`))
			return
		}

		var args map[string]any
		require.NoError(t, json.Unmarshal(payload.MethodCalls[0][1], &args))
		create := args["create"].(map[string]any)["new-mailbox"].(map[string]any)
		require.Equal(t, "domain-1", create["domainId"])
		require.Equal(t, "sultan.sultan", create["name"])
		require.Equal(t, "User", create["@type"])
		require.NotEmpty(t, create["credentials"])
		_, _ = w.Write([]byte(`{"methodResponses":[["x:Account/set",{"created":{"new-mailbox":{"id":"account-1"}}},"account-set"]]}`))
	}))
	defer server.Close()

	provisioner := NewStalwartMailboxProvisioner(StalwartProvisionerConfig{
		BaseURL:        server.URL,
		AdminUser:      "admin",
		AdminPassword:  "secret",
		DefaultDomain:  "teamspace.kz",
		DefaultQuotaMB: 10,
	}, zap.NewNop())

	mailbox, err := provisioner.ProvisionPrimaryMailbox(context.Background(), &domain.UserResponse{
		ID:    9,
		Email: "sultan.sultan@gmail.com",
	})
	require.NoError(t, err)
	require.Equal(t, "sultan.sultan@teamspace.kz", mailbox)
	require.Equal(t, 2, requests)
}
