package domain

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/google/uuid"
)

type IFormOperation struct {
	OrganizationID       uuid.UUID                  `json:"organization_id"`
	Subject              string                     `json:"subject"`
	RequestID            uuid.UUID                  `json:"request_id"`
	StepID               string                     `json:"step_id"`
	Occurrence           int                        `json:"occurrence"`
	Operation            string                     `json:"operation"`
	Inputs               map[string]json.RawMessage `json:"inputs"`
	EmployeeDocumentFlow bool                       `json:"employee_document_flow,omitempty"`
	ProcessInitiation    *IFormInitiation           `json:"process_initiation,omitempty"`
}

var iformStepID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func (r IFormOperation) Key() string {
	return fmt.Sprintf("%s:%s:%s:%d", r.OrganizationID, r.RequestID, r.StepID, r.Occurrence)
}
func (r IFormOperation) Valid() bool {
	if r.ProcessInitiation != nil && (!r.ProcessInitiation.Valid() || r.EmployeeDocumentFlow || r.Operation == "workspace.capabilities" || (r.Operation == "workspace.employee.create" && r.ProcessInitiation.Mode != "HR")) {
		return false
	}
	if r.EmployeeDocumentFlow && r.Operation != "workspace.odo.create" && r.Operation != "workspace.odo.wait" {
		return false
	}
	if r.OrganizationID == uuid.Nil || r.RequestID == uuid.Nil || r.Occurrence < 1 || r.Occurrence > 100 || !iformStepID.MatchString(r.StepID) {
		return false
	}
	switch r.Operation {
	case "workspace.capabilities", "workspace.odo.create", "workspace.odo.wait", "workspace.sd.create", "workspace.sd.wait", "workspace.employee.create":
		return true
	}
	return false
}
