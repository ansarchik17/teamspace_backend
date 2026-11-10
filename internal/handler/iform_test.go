package handler

import "testing"

func TestIFormProxyPaths(t *testing.T) {
	for _, path := range []string{"/me", "/directory", "/approval-delegations", "/approval-delegations/delegation-1/revoke", "/forms/p_123/lookups/department", "/documents/abc/download", "/employee-chats/abc/messages"} {
		if _, ok := iformAPIPath(path); !ok {
			t.Fatalf("valid path rejected: %s", path)
		}
	}
	for _, path := range []string{"//me", "/me/../tenants", "/me/%2e%2e/tenants", "/api/internal/iform/context", "/actuator/health", "/callbacks/signing/x", "/me?tenant=other", "/me\\tenants", "https://other.test/me"} {
		if _, ok := iformAPIPath(path); ok {
			t.Fatalf("unsafe path allowed: %s", path)
		}
	}
	t.Setenv("IFORM_API_URL", "http://127.0.0.1:8081")
	if _, err := iformAPIBase(); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"", "file:///etc/passwd", "https://user:secret@example.test", "https://example.test?target=x", "https://example.test/api"} {
		t.Setenv("IFORM_API_URL", base)
		if _, err := iformAPIBase(); err == nil {
			t.Fatalf("unsafe base accepted: %s", base)
		}
	}
}
