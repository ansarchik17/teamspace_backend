package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGrowyAPIUpstreamPathUsesLMSRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path, want string
	}{
		{http.MethodGet, "users/enrollments/", "/api/my/users/enrollments/"},
		{http.MethodGet, "users/enrollments/42/progress-detail/", "/api/my/users/enrollments/42/progress-detail/"},
		{http.MethodGet, "training-programs/my-unfinished/", "/api/training-programs/my-unfinished/"},
		{http.MethodGet, "crm/leads/", "/api/platform/v1/crm/leads/"},
		{http.MethodPost, "crm/leads/", "/api/platform/v1/crm/leads/"},
		{http.MethodGet, "whatsapp/conversations/42/messages/", "/api/platform/v1/whatsapp/conversations/42/messages/"},
		{http.MethodGet, "context/", "/api/platform/v1/context/"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			got, allowed := growyAPIUpstreamPath(tc.method, tc.path)
			if !allowed || got != tc.want {
				t.Fatalf("upstream = %q, allowed = %t; want %q", got, allowed, tc.want)
			}
		})
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodHead} {
		for _, path := range []string{"users/enrollments/", "users/enrollments/42/progress-detail/", "training-programs/my-unfinished/"} {
			if got, allowed := growyAPIUpstreamPath(method, path); allowed || got != "" {
				t.Errorf("allowed training write: %s %s", method, path)
			}
		}
	}
	for _, path := range []string{"users/enrollments/../", "users/enrollments/42/leave/", "users/enrollments//", "training-programs/42/", "crm/leads//"} {
		if _, allowed := growyAPIUpstreamPath(http.MethodGet, path); allowed {
			t.Errorf("allowed unsupported path: %s", path)
		}
	}
}

func TestGrowyTrainingConnectionIsIndependentOfCRM(t *testing.T) {
	for _, internal := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "internal"}[internal], func(t *testing.T) {
			crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/api/platform/v1/") {
					t.Errorf("LMS request reached CRM: %s", r.URL.Path)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer crm.Close()
			lms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Origin") != "https://sultan-corp.teamspace.kz" && internal {
					t.Errorf("missing server-resolved company context: %s", r.Header.Get("Origin"))
				}
				if r.URL.Path != "/api/my/users/enrollments/" || r.URL.Query().Get("as_employee") != "true" || r.Header.Get("Cookie") != "" {
					t.Errorf("incorrect personal LMS read: %s", r.URL.String())
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer lms.Close()
			t.Setenv("GROWY_PUBLIC_URL", crm.URL)
			t.Setenv("GROWY_API_INTERNAL_URL", crm.URL)
			t.Setenv("APP_PUBLIC_URL", lms.URL)
			t.Setenv("GROWY_LMS_PUBLIC_URL", lms.URL)
			t.Setenv("GROWY_LMS_INTERNAL_URL", "")
			t.Setenv("TENANT_BASE_DOMAIN", "teamspace.kz")
			if internal {
				t.Setenv("GROWY_LMS_PUBLIC_URL", "https://teamspace.kz/growy")
				t.Setenv("GROWY_LMS_INTERNAL_URL", lms.URL)
			}
			for _, path := range []string{"users/enrollments/", "crm/leads/"} {
				upstream, _ := growyAPIUpstreamPath(http.MethodGet, path)
				connection, err := growyConnectionForPath(upstream)
				if err != nil {
					t.Fatal(err)
				}
				if connection.lms {
					upstream += "?as_employee=true"
				}
				request, err := connection.request(t.Context(), http.MethodGet, upstream, "sultan-corp", nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := growyHTTPClient().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Fatalf("read failed: %d", response.StatusCode)
				}
			}
		})
	}
}

func TestGrowyLMSBaseDefaultsToWorkspaceRatherThanCRM(t *testing.T) {
	t.Setenv("GROWY_PUBLIC_URL", "https://growy.kz")
	t.Setenv("GROWY_LMS_PUBLIC_URL", "")
	t.Setenv("APP_PUBLIC_URL", "https://teamspace.kz")
	connection, err := growyConnectionForPath("/api/training-programs/my-unfinished/")
	if err != nil || !connection.lms || connection.base != "https://teamspace.kz/growy" {
		t.Fatalf("LMS target = %+v, error = %v", connection, err)
	}
}

func TestGrowyAPIPathRejectsPrivilegedAndTraversalRoutes(t *testing.T) {
	for _, path := range []string{"admin/tenants/provision/", "../token/", "crm/leads/../../admin/", "https://evil.test/crm/", "crm/%2e%2e/", "token/workspace/", "telegram/accounts/", "telegram/accounts/12/status/", "telegram/accounts/12/auth/qr/", "telegram/accounts/12/dialogs/45/messages/", "telegram/inbox/", "telegram/accounts/1/../../admin/", "telegram/accounts/%2e%2e/", "telegram/accounts/1/messages.html", "telegram/accounts/1//auth/qr/"} {
		if growyAPIPath.MatchString(path) && !strings.Contains(path, "//") {
			t.Errorf("accepted unsafe route %q", path)
		}
	}
	for _, path := range []string{"crm/leads/", "crm/deals/123/", "whatsapp/conversations/1/messages/", "context/"} {
		if !growyAPIPath.MatchString(path) {
			t.Errorf("rejected allowed route %q", path)
		}
	}
}

func TestGrowyPublicBaseRequiresHTTPSOutsideLoopback(t *testing.T) {
	for _, endpoint := range []string{"http://example.test", "https://user:password@example.test", "https://example.test?target=evil"} {
		t.Setenv("GROWY_PUBLIC_URL", endpoint)
		if _, err := growyPublicBase(); err == nil {
			t.Errorf("accepted invalid base URL")
		}
	}
	t.Setenv("GROWY_PUBLIC_URL", "http://127.0.0.1:8080/growy")
	if _, err := growyPublicBase(); err != nil {
		t.Fatal(err)
	}
}

func TestGrowyTrainingReadPathsAreNarrow(t *testing.T) {
	for _, path := range []string{"users/enrollments/", "users/enrollments/42/progress-detail/", "training-programs/my-unfinished/"} {
		if !growyTrainingReadPath.MatchString(path) {
			t.Errorf("read path not allowed: %s", path)
		}
		if growyAPIPath.MatchString(path) {
			t.Errorf("training unexpectedly allowed for write methods: %s", path)
		}
	}
	for _, path := range []string{"users/enrollments/42/leave/", "users/7/", "admin/tenants/", "users/enrollments/../../admin/", "training-programs/7/"} {
		if growyTrainingReadPath.MatchString(path) {
			t.Errorf("unsafe training route allowed: %s", path)
		}
	}
}
