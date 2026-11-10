package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAIODOAssignmentsResolveRealCurrentUser(t *testing.T) {
	profileStatus := http.StatusOK
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer workspace-user" || r.Header.Get("Cookie") != "" || r.Host != "acme.teamspace.test" {
			t.Fatal("lost actor/tenant or forwarded session cookie")
		}
		if r.URL.Path == "/api/v1/auth/me" {
			w.WriteHeader(profileStatus)
			w.Write([]byte(`{"id":"odo-user-uuid","roles":["admin"]}`))
			return
		}
		reads++
		if r.URL.Path != "/api/v1/assignments" || r.URL.Query().Get("executor_id") != "odo-user-uuid" || r.URL.Query().Get("limit") != "25" {
			t.Fatalf("wrong user-scoped request: %+v", r.URL)
		}
		w.Write([]byte(`{"items":[{"id":"own-assignment","document_id":"doc-1"}],"pagination":{"has_next":false}}`))
	}))
	defer server.Close()
	reader := NewHTTPAIUserDataReader(nil, map[string]AIConnector{"odo": {BaseURL: server.URL, ForwardHost: true}})
	actor := AIDataIdentity{UserID: 42, Authorization: "Bearer workspace-user", Cookie: "session=private", Host: "acme.teamspace.test"}
	if result := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "odo_assignments"}); result.Status != "ok" {
		t.Fatal(result)
	}
	if result := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "odo_assignments", Params: map[string]string{"executor_id": "another-user"}}); result.Status != "invalid_request" {
		t.Fatal("allowed model-selected actor")
	}
	profileStatus = http.StatusForbidden
	if result := reader.Read(context.Background(), actor, AIDataQuery{Dataset: "odo_assignments"}); result.Status != "forbidden" || reads != 1 {
		t.Fatal("revoked identity reached assignments")
	}
}

func TestAIIFormPaginationAndIdentity(t *testing.T) {
	reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/iform/api/requests" || r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer user" || r.Host != "acme.teamspace.test" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("pageSize") != "25" {
			t.Fatalf("wrong Iform bridge request: %+v", r)
		}
		items := make([]map[string]any, 25)
		for i := range items {
			items[i] = map[string]any{"id": "request", "status": "WAITING_FOR_APPROVAL"}
		}
		json.NewEncoder(w).Encode(items)
	}), nil)
	result := reader.Read(context.Background(), AIDataIdentity{UserID: 42, Authorization: "Bearer user", Host: "acme.teamspace.test"}, AIDataQuery{Dataset: "iform_requests", Params: map[string]string{"page": "2", "pageSize": "999"}})
	if result.Status != "ok" || !result.Truncated {
		t.Fatalf("lost pagination: %+v", result)
	}
	data := result.Data.(map[string]any)
	if data["has_next"] != true || len(data["items"].([]any)) != 25 {
		t.Fatal(data)
	}
	for _, entry := range reader.Catalog() {
		if entry.Key == "iform_requests" {
			path, err := aiDatasetPath(entry, AIDataIdentity{UserID: 42}, AIDataQuery{Dataset: "iform_requests"})
			if err != nil || !strings.Contains(path, "page=0") || !strings.Contains(path, "pageSize=25") {
				t.Fatalf("lost first page: %s %v", path, err)
			}
		}
	}
}
