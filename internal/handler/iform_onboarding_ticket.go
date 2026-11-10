package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

// Employee metadata belongs to the Workspace bridge. SD receives its existing
// ticket contract: start_date and a readable description, never an SD worker ID
// substituted with the new Workspace employee's numeric ID.
func iformSDTicketBody(inputs map[string]json.RawMessage, validateEmployee func(int64) error) (json.RawMessage, int64, error) {
	invalid := func() (json.RawMessage, int64, error) {
		return nil, 0, apperrors.New("IFORM_ONBOARDING_TICKET_INVALID", "invalid onboarding ticket data", 400)
	}
	if _, err := iformInputObject(inputs, []string{"company_id", "project_id", "object_id", "worker_id", "title", "category_id", "priority_id", "description", "type_id", "actual_started_at", "employee_id", "start_date", "workplace_requirements"}); err != nil {
		return nil, 0, err
	}
	// SD distinguishes the tenant from its clients. Keep the configured client
	// rather than silently using the actor's tenant; SD checks live membership.
	if raw, exists := inputs["company_id"]; exists {
		var company string
		if json.Unmarshal(raw, &company) != nil {
			return invalid()
		}
		id, err := uuid.Parse(company)
		if err != nil || id == uuid.Nil {
			return invalid()
		}
	}
	body := make(map[string]json.RawMessage, len(inputs))
	for key, value := range inputs {
		body[key] = value
	}
	_, employeeSet := inputs["employee_id"]
	_, requirementsSet := inputs["workplace_requirements"]
	if !employeeSet && !requirementsSet {
		encoded, err := json.Marshal(body) // Existing generic SD processes remain compatible.
		return encoded, 0, err
	}
	var employee int64
	var start, requirements, description string
	if json.Unmarshal(inputs["employee_id"], &employee) != nil || employee <= 0 || json.Unmarshal(inputs["start_date"], &start) != nil || json.Unmarshal(inputs["workplace_requirements"], &requirements) != nil {
		return invalid()
	}
	date, err := time.Parse("2006-01-02", start)
	if err != nil || date.Format("2006-01-02") != start || strings.TrimSpace(requirements) == "" || utf8.RuneCountInString(requirements) > 16000 {
		return invalid()
	}
	if raw, ok := inputs["description"]; ok && json.Unmarshal(raw, &description) != nil {
		return invalid()
	}
	if validateEmployee == nil {
		return nil, 0, apperrors.Internal("Employee verification unavailable")
	}
	if err := validateEmployee(employee); err != nil {
		return nil, 0, err
	}
	description = strings.TrimSpace(description)
	if description != "" {
		description += "\n\n"
	}
	description += fmt.Sprintf("Сотрудник Workspace: %d\nДата выхода: %s\nТребования к рабочему месту: %s", employee, start, strings.TrimSpace(requirements))
	if utf8.RuneCountInString(description) > 32000 {
		return invalid()
	}
	body["description"], _ = json.Marshal(description)
	delete(body, "employee_id")
	delete(body, "workplace_requirements")
	encoded, err := json.Marshal(body)
	return encoded, employee, err
}
