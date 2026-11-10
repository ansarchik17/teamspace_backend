package service

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestAIReaderColleagueSearchUsesDirectoryAndIdentity(t *testing.T) {
	for _, query := range []string{"t.company", "Test Company", "t.company@a.teamspace.kz"} {
		t.Run(query, func(t *testing.T) {
			reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/search/users" || r.URL.Query().Get("q") != query || r.URL.Query().Get("limit") != "25" {
					t.Fatalf("wrong directory request: %s", r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer sultan" || r.Header.Get("Cookie") != "session=current" || r.Host != "tenant.test" {
					t.Fatal("directory identity lost")
				}
				io.WriteString(w, `{"query":"Test Company","users":[{"id":38,"username":"t.company","first_name":"Test","last_name":"Company","email":"t.company@a.teamspace.kz"}],"count":1}`)
			}), nil)
			result := reader.Read(context.Background(), AIDataIdentity{UserID: 2, Authorization: "Bearer sultan", Cookie: "session=current", Host: "tenant.test"}, AIDataQuery{Dataset: "colleague_search", Params: map[string]string{"q": query}})
			if result.Status != "ok" {
				t.Fatal(result)
			}
			users := result.Data.(map[string]any)["users"].([]any)
			if len(users) != 1 || users[0].(map[string]any)["id"] != float64(38) {
				t.Fatalf("lost recipient: %+v", result)
			}
		})
	}
}

func TestAIReaderColleagueSearchDoesNotBypassDirectoryAccess(t *testing.T) {
	reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }), nil)
	got := reader.Read(context.Background(), AIDataIdentity{UserID: 2, Authorization: "Bearer sultan"}, AIDataQuery{Dataset: "colleague_search", Params: map[string]string{"q": "Test Company"}})
	if got.Status != "forbidden" {
		t.Fatal(got)
	}
}
