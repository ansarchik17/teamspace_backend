package handler

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestIFormSDHandoffUsesVerifiedEmployeeAndExistingSDContract(t *testing.T) {
	inputs := map[string]json.RawMessage{"company_id": json.RawMessage(`"22222222-2222-4222-8222-222222222222"`), "employee_id": json.RawMessage(`24`), "start_date": json.RawMessage(`"2026-12-01"`), "workplace_requirements": json.RawMessage(`"Laptop and access"`), "description": json.RawMessage(`"New hire"`), "worker_id": json.RawMessage(`"11111111-1111-4111-8111-111111111111"`)}
	checked := 0
	validate := func(id int64) error {
		checked++
		if id != 24 {
			t.Fatal("wrong employee checked")
		}
		return nil
	}
	body, employee, err := iformSDTicketBody(inputs, validate)
	if err != nil || employee != 24 || checked != 1 {
		t.Fatalf("handoff: %s %v", body, err)
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["employee_id"] != nil || payload["workplace_requirements"] != nil || string(payload["start_date"]) != `"2026-12-01"` || string(payload["worker_id"]) != string(inputs["worker_id"]) {
		t.Fatal("invented SD fields or worker identity")
	}
	if string(payload["company_id"]) != string(inputs["company_id"]) {
		t.Fatal("the SD client must not be replaced with the Workspace tenant")
	}
	var description string
	_ = json.Unmarshal(payload["description"], &description)
	for _, text := range []string{"New hire", "Сотрудник Workspace: 24", "2026-12-01", "Laptop and access"} {
		if !strings.Contains(description, text) {
			t.Fatal("missing IT context: " + text)
		}
	}
	again, _, _ := iformSDTicketBody(inputs, validate)
	if string(again) != string(body) || string(inputs["description"]) != `"New hire"` {
		t.Fatal("retry changed request payload")
	}
	denied := errors.New("receipt belongs to another request")
	if _, _, err = iformSDTicketBody(inputs, func(int64) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("employee check was bypassed")
	}
}

func TestIFormSDClientMustBeAUUIDBeforeAnyEmployeeLookup(t *testing.T) {
	for _, company := range []string{`null`, `24`, `""`, `"workspace:40"`, `"00000000-0000-0000-0000-000000000000"`} {
		_, _, err := iformSDTicketBody(map[string]json.RawMessage{"company_id": json.RawMessage(company)}, func(int64) error {
			t.Fatal("invalid SD client reached employee lookup")
			return nil
		})
		if err == nil {
			t.Fatalf("accepted invalid SD client %s", company)
		}
	}
}

func TestIFormSDHandoffRejectsMissingInvalidAndForeignContextBeforeSend(t *testing.T) {
	base := map[string]json.RawMessage{"employee_id": json.RawMessage(`24`), "start_date": json.RawMessage(`"2026-12-01"`), "workplace_requirements": json.RawMessage(`"Laptop"`)}
	for key, values := range map[string][]string{"employee_id": {"", "null", "0", "-1", "24.5", `"24"`}, "start_date": {"", "null", `"2026-02-30"`, `"2026-2-03"`, `"2026-12-01T00:00:00Z"`}, "workplace_requirements": {"", "null", `" "`, `{}`}} {
		for _, value := range values {
			inputs := make(map[string]json.RawMessage)
			for k, v := range base {
				inputs[k] = v
			}
			if value == "" {
				delete(inputs, key)
			} else {
				inputs[key] = json.RawMessage(value)
			}
			if _, _, err := iformSDTicketBody(inputs, func(int64) error { t.Fatal("invalid context checked as ready"); return nil }); err == nil {
				t.Fatalf("accepted %s=%s", key, value)
			}
		}
	}
	if _, _, err := iformSDTicketBody(map[string]json.RawMessage{"title": json.RawMessage(`"Generic task"`)}, nil); err != nil {
		t.Fatal("generic process broken: ", err)
	}
}
