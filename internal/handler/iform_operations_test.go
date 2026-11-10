package handler

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIFormOperationRequiresCurrentHRMembershipInExactCompany(t *testing.T) {
	company := uuid.New()
	op := domain.IFormOperation{OrganizationID: company, Subject: "workspace:10"}
	snapshot := &domain.IFormContext{OrganizationID: company, Actor: op.Subject, Members: []domain.IFormMember{{ID: op.Subject, Active: true, Roles: []string{"EMPLOYEE", "APPROVER"}}}}
	if iformHRAllowed(snapshot, op) {
		t.Fatal("approver received HR operations")
	}
	snapshot.Members[0].Roles = []string{"HR"}
	if !iformHRAllowed(snapshot, op) {
		t.Fatal("HR rejected")
	}
	snapshot.Members[0].Active = false
	if iformHRAllowed(snapshot, op) {
		t.Fatal("inactive HR accepted")
	}
	snapshot.Members[0].Active = true
	snapshot.OrganizationID = uuid.New()
	if iformHRAllowed(snapshot, op) {
		t.Fatal("foreign company accepted")
	}
	snapshot.OrganizationID = company
	snapshot.Actor = "workspace:99"
	if iformHRAllowed(snapshot, op) {
		t.Fatal("different actor accepted")
	}
}

func TestIFormEmployeeDocumentFlowsKeepOnboardingRestricted(t *testing.T) {
	company := uuid.New()
	op := domain.IFormOperation{OrganizationID: company, Subject: "workspace:10", EmployeeDocumentFlow: true}
	snapshot := &domain.IFormContext{OrganizationID: company, Actor: op.Subject, Members: []domain.IFormMember{{ID: op.Subject, Active: true, Roles: []string{"EMPLOYEE"}}}}
	for _, operation := range []string{"workspace.odo.create", "workspace.odo.wait"} {
		op.Operation = operation
		if !iformOperationAllowed(snapshot, op) {
			t.Fatalf("employee document flow denied: %s", operation)
		}
	}
	for _, operation := range []string{"workspace.employee.create", "workspace.sd.create", "workspace.sd.wait", "workspace.capabilities", "unrecognized"} {
		op.Operation = operation
		if iformOperationAllowed(snapshot, op) {
			t.Fatalf("employee received privileged operation: %s", operation)
		}
	}
	op.Operation = "workspace.odo.create"
	op.EmployeeDocumentFlow = false
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("HR document flow was downgraded to employee authorization")
	}
	op.EmployeeDocumentFlow = true
	snapshot.Members[0].Active = false
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("inactive employee allowed")
	}
	snapshot.Members[0].Active = true
	snapshot.Members[0].Roles = []string{"APPROVER"}
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("approval role alone allowed initiation")
	}
	snapshot.Members[0].Roles = []string{"EMPLOYEE"}
	snapshot.OrganizationID = uuid.New()
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("foreign company accepted")
	}
	snapshot.OrganizationID = company
	snapshot.Actor = "workspace:11"
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("foreign actor accepted")
	}
}

func TestIFormUpstreamUsesOnlyConfiguredOriginsAndNeverFollowsRedirects(t *testing.T) {
	ctx := context.Background()
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer delegated" || r.Header.Get("Idempotency-Key") != "stable-key" || r.Header.Get("Cookie") != "" {
			t.Error("wrong delegation boundary")
		}
		http.Redirect(w, r, target.URL, 302)
	}))
	defer source.Close()
	if _, err := iformUpstream(ctx, source.URL, "/api/v1/documents", "POST", "Bearer delegated", "", "stable-key", json.RawMessage(`{}`)); err == nil {
		t.Fatal("redirect accepted")
	}
	if called {
		t.Fatal("delegated credential followed a redirect")
	}
	for _, base := range []string{"", "file:///etc/passwd", "https://user:secret@example.test", "https://example.test/path", "https://example.test?url=other"} {
		if _, err := iformUpstream(ctx, base, "/tickets", "GET", "Bearer delegated", "", "", nil); err == nil {
			t.Fatalf("invalid service origin accepted: %s", base)
		}
	}
}

func TestIFormInputsRejectPrivilegeAndCredentialOverrides(t *testing.T) {
	for _, key := range []string{"role", "password", "company_id", "endpoint", "authorization"} {
		if _, err := iformInputObject(map[string]json.RawMessage{key: json.RawMessage(`"injected"`)}, []string{"title", "description"}); err == nil {
			t.Fatalf("unlisted input accepted: %s", key)
		}
	}
	for _, id := range []string{"../users", "id?company=other", "a/b", "%2e%2e", "https://other.test", ""} {
		if iformExternalID(id) {
			t.Fatalf("unsafe resource identity: %s", id)
		}
	}
}

func TestIFormODOStageNameHandlesOmittedZeroAndBounds(t *testing.T) {
	for _, tc := range []struct {
		document string
		want     string
	}{
		{`{"workflow_plan":{"phase":"planned","stages":[{"name":"Подпись"}]}}`, "Регистрация документа в ОДО"},
		{`{"workflow_plan":{"phase":"signing","stages":[{"name":"Подписать приказ"}]}}`, "Подписать приказ"},
		{`{"workflow_plan":{"phase":"execution","current_stage_index":1,"stages":[{"name":"A"},{"name":"B"}]}}`, "B"},
		{`{"workflow_plan":{"phase":"execution","current_stage_index":9,"stages":[{"name":"A"}]}}`, ""},
		{`{}`, ""},
	} {
		var document map[string]interface{}
		if err := json.Unmarshal([]byte(tc.document), &document); err != nil {
			t.Fatal(err)
		}
		if got := iformODOStageName(document); got != tc.want {
			t.Fatalf("stage=%q want %q", got, tc.want)
		}
	}
}

func TestIFormRequiresODOToConfirmTheConfiguredRoute(t *testing.T) {
	inputs := map[string]json.RawMessage{"workflow_plan": json.RawMessage(`{"stages":[{"id":"sign","kind":"signing"}]}`)}
	for _, tc := range []struct {
		response string
		want     bool
	}{
		{`{"id":"doc"}`, false},
		{`{"workflow_plan":{"stages":[]}}`, false},
		{`{"workflow_plan":{"stages":[{"id":"other","kind":"signing"}]}}`, false},
		{`{"workflow_plan":{"stages":[{"id":"sign","kind":"signing"}]}}`, true},
	} {
		var response map[string]interface{}
		_ = json.Unmarshal([]byte(tc.response), &response)
		if iformODORouteConfirmed(inputs, response) != tc.want {
			t.Fatal(tc.response)
		}
	}
	if !iformODORouteConfirmed(map[string]json.RawMessage{}, map[string]interface{}{"id": "legacy"}) {
		t.Fatal("legacy document creation broken")
	}
}
