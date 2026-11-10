package domain

import (
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
)

type EmploymentAdmission struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	RequestID      uuid.UUID `json:"request_id"`
	ActorID        int64     `json:"-"`
	EmployeeID     *int64    `json:"employee_id"`
	StartDate      *string   `json:"start_date"`
	Timezone       string    `json:"timezone"`
	Cancelled      bool      `json:"cancelled"`
	Revision       int64     `json:"revision"`
	Allowed        bool      `json:"allowed"`
}

func ValidEmploymentDate(value string) bool {
	date, err := time.Parse("2006-01-02", value)
	return err == nil && date.Format("2006-01-02") == value
}
func ValidCompanyTimezone(value string) bool {
	if value == "" || value == "Local" {
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}
