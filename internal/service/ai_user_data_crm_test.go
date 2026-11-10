package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestAIReaderCRMCurrentUserFilter(t *testing.T) {
	for dataset, filter := range map[string]string{"crm_leads": "manager_id", "crm_deals": "manager_id", "crm_tasks": "assigned_to"} {
		t.Run(dataset, func(t *testing.T) {
			for _, mine := range []string{"true", "false", ""} {
				contextReads, collectionReads := 0, 0
				reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer current" || r.Header.Get("Cookie") != "session=current" || r.Host != "tenant.test" {
						t.Fatal("lost authenticated tenant identity")
					}
					if r.URL.Path == "/api/v1/growy/api/context/" {
						contextReads++
						io.WriteString(w, `{"user_id":901,"tenant":{"id":7}}`)
						return
					}
					collectionReads++
					q := r.URL.Query()
					want := ""
					if mine == "true" {
						want = "901"
						if filter == "assigned_to" {
							want = "me"
						}
					}
					if q.Get(filter) != want || q.Has("mine") || q.Has("assignee_ids") || q.Get("page_size") != "25" || q.Get("status") != "open" {
						t.Fatalf("incorrect CRM filters: %v", q)
					}
					io.WriteString(w, `{"count":83,"results":[{"id":11}],"next":"https://crm.test/page2"}`)
				}), nil)
				params := map[string]string{"status": "open"}
				if mine != "" {
					params["mine"] = mine
				}
				result := reader.Read(context.Background(), AIDataIdentity{UserID: 2, Authorization: "Bearer current", Cookie: "session=current", Host: "tenant.test"}, AIDataQuery{Dataset: dataset, Params: params})
				if result.Status != "ok" || collectionReads != 1 || (mine == "true" && contextReads != 1) || (mine != "true" && contextReads != 0) {
					t.Fatalf("unexpected reads/result: %d %d %+v", contextReads, collectionReads, result)
				}
				data := result.Data.(map[string]any)
				if data["count"] != float64(83) || data["has_next"] != true {
					t.Fatalf("lost total or pagination: %+v", data)
				}
			}
		})
	}
}

func TestAIReaderCRMIdentityFailuresDoNotReadCollection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		code       int
		body, want string
	}{
		{"forbidden", 403, `{}`, "forbidden"},
		{"upstream", 502, `{}`, "unavailable"},
		{"missing", 200, `{"tenant":{"id":1}}`, "unavailable"},
		{"zero", 200, `{"user_id":0}`, "unavailable"},
		{"fraction", 200, `{"user_id":2.5}`, "unavailable"},
		{"string", 200, `{"user_id":"901"}`, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/growy/api/context/" {
					t.Fatal("CRM identity failure fell back to unscoped collection")
				}
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			}), nil)
			got := reader.Read(context.Background(), AIDataIdentity{UserID: 2, Authorization: "Bearer current"}, AIDataQuery{Dataset: "crm_leads", Params: map[string]string{"mine": "true"}})
			if got.Status != tc.want {
				t.Fatalf("unexpected status: %+v", got)
			}
		})
	}
}

func TestAIReaderCRMRejectsModelSelectedIdentity(t *testing.T) {
	reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid arguments reached CRM") }), nil)
	for _, query := range []AIDataQuery{
		{Dataset: "crm_leads", Params: map[string]string{"manager_id": "2"}},
		{Dataset: "crm_tasks", Params: map[string]string{"assigned_to": "2"}},
		{Dataset: "crm_deals", Params: map[string]string{"mine": "yes"}},
		{Dataset: "crm_leads", ID: "11", Params: map[string]string{"mine": "true"}},
	} {
		got := reader.Read(context.Background(), AIDataIdentity{UserID: 2, Authorization: "Bearer current"}, query)
		if got.Status != "invalid_request" {
			b, _ := json.Marshal(got)
			t.Fatalf("accepted unsafe query: %s", b)
		}
	}
}
