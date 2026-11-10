package handler

import (
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
)

func TestIFormPublishedInitiationRechecksLivePositionAndTenant(t *testing.T) {
	company, position := uuid.New(), uuid.NewString()
	p := &domain.IFormInitiation{Mode: "POSITIONS", PositionIDs: []string{position}}
	op := domain.IFormOperation{OrganizationID: company, Subject: "workspace:10", Operation: "workspace.sd.create", ProcessInitiation: p}
	member := domain.IFormMember{ID: op.Subject, Active: true, Roles: []string{"EMPLOYEE"}, PositionID: position}
	snapshot := &domain.IFormContext{OrganizationID: company, Actor: op.Subject, Members: []domain.IFormMember{member}}
	if !iformOperationAllowed(snapshot, op) {
		t.Fatal("selected employee denied")
	}
	for _, operation := range []string{"workspace.odo.create", "workspace.odo.wait", "workspace.sd.create", "workspace.sd.wait"} {
		op.Operation = operation
		if !iformOperationAllowed(snapshot, op) {
			t.Fatal(operation)
		}
	}
	op.Operation = "workspace.sd.create"
	snapshot.Members[0].PositionID = uuid.NewString()
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("position reassignment did not revoke access")
	}
	snapshot.Members[0] = member
	snapshot.Members[0].Active = false
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("inactive member allowed")
	}
	snapshot.Members[0] = member
	snapshot.Members[0].Roles = []string{"APPROVER"}
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("approval elevated to initiation")
	}
	snapshot.Members[0] = member
	snapshot.OrganizationID = uuid.New()
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("foreign tenant allowed")
	}
	snapshot.OrganizationID = company
	snapshot.Actor = "workspace:11"
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("different actor allowed")
	}
	snapshot.Actor = op.Subject
	op.Operation = "workspace.employee.create"
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("employee provisioning elevated")
	}
	op.Operation = "workspace.sd.create"
	op.ProcessInitiation = nil
	if iformOperationAllowed(snapshot, op) {
		t.Fatal("legacy restriction removed")
	}
}

func TestIFormInitiationModesAndEnvelopeValidation(t *testing.T) {
	position := uuid.NewString()
	for _, p := range []domain.IFormInitiation{{Mode: "UNKNOWN"}, {Mode: "POSITIONS"}, {Mode: "POSITIONS", PositionIDs: []string{"Analyst"}}, {Mode: "POSITIONS", PositionIDs: []string{position, position}}, {Mode: "HR", PositionIDs: []string{position}}} {
		if p.Valid() {
			t.Fatalf("malformed policy accepted: %+v", p)
		}
	}
	employee := domain.IFormMember{Active: true, Roles: []string{"EMPLOYEE"}}
	all := domain.IFormInitiation{Mode: "ALL_EMPLOYEES"}
	hr := domain.IFormInitiation{Mode: "HR"}
	if !all.Allows(employee) || hr.Allows(employee) {
		t.Fatal("employee / HR policy boundary lost")
	}
	employee.Roles = []string{"HR"}
	if all.Allows(employee) || !hr.Allows(employee) {
		t.Fatal("HR policy boundary lost")
	}
	op := domain.IFormOperation{OrganizationID: uuid.New(), RequestID: uuid.New(), Subject: "workspace:10", StepID: "create", Occurrence: 1, Operation: "workspace.sd.create", ProcessInitiation: &all}
	if !op.Valid() {
		t.Fatal("scoped SD envelope rejected")
	}
	op.EmployeeDocumentFlow = true
	if op.Valid() {
		t.Fatal("mixed legacy and new authorization accepted")
	}
	op.EmployeeDocumentFlow = false
	op.Operation = "workspace.employee.create"
	if op.Valid() {
		t.Fatal("non-HR provisioning envelope accepted")
	}
}
