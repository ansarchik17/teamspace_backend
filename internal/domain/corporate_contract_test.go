package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserResponseDoesNotExposeLegacyGovernmentScope(t *testing.T) {
	response := (&User{
		ID:           1,
		Role:         RoleUser,
		MinistryID:   int64Ptr(10),
		StateBodyID:  int64Ptr(20),
		DepartmentID: int64Ptr(30),
		DivisionID:   int64Ptr(40),
	}).ToResponse()

	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal user response: %v", err)
	}

	encoded := string(payload)
	for _, legacyField := range []string{"ministry_id", "state_body_id", "department_id", "division_id", "prime_minister_id", "deputy_prime_minister_id"} {
		if strings.Contains(encoded, legacyField) {
			t.Fatalf("legacy field %q leaked into user response: %s", legacyField, encoded)
		}
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
