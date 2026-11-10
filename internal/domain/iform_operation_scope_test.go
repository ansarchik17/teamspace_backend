package domain

import (
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestIFormEmployeeDocumentScopeIsBoundedAndLegacyEnvelopeIsUnchanged(t *testing.T) {
	op := IFormOperation{OrganizationID: uuid.New(), RequestID: uuid.New(), Subject: "workspace:10", StepID: "createDocument", Occurrence: 1, Operation: "workspace.odo.create"}
	legacy, _ := json.Marshal(op)
	if strings.Contains(string(legacy), "employee_document_flow") || !op.Valid() {
		t.Fatal("legacy operation envelope changed")
	}
	op.EmployeeDocumentFlow = true
	for _, operation := range []string{"workspace.odo.create", "workspace.odo.wait"} {
		op.Operation = operation
		if !op.Valid() {
			t.Fatalf("document scope rejected: %s", operation)
		}
	}
	for _, operation := range []string{"workspace.employee.create", "workspace.sd.create", "workspace.capabilities", "unknown"} {
		op.Operation = operation
		if op.Valid() {
			t.Fatalf("document scope accepted privileged operation: %s", operation)
		}
	}
}
