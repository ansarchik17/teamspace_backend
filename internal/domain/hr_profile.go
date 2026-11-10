package domain

import (
	"encoding/json"
	"time"
)

// HRPerson is the normalized employee payload used by the HR integration flow.
// Dates are represented as strings because upstream payloads commonly arrive
// in XML/JSON using YYYY-MM-DD format instead of RFC3339.
type HRPerson struct {
	PersonCode         string `json:"person_code"`
	BadgeCode          string `json:"badge_code"`
	IIN                string `json:"iin"`
	LastName           string `json:"last_name"`
	FirstName          string `json:"first_name"`
	MiddleName         string `json:"middle_name"`
	Photo              string `json:"photo"`
	DocType            string `json:"doc_type"`
	PersonStatus       string `json:"person_status"`
	LastOrderNumber    string `json:"last_order_number"`
	LastOrderDate      string `json:"last_order_date"`
	LastBeginDate      string `json:"last_begin_date"`
	FirstBeginDate     string `json:"first_begin_date"`
	AbsenceStatus      string `json:"absence_status"`
	AbsenceOrderNumber string `json:"absence_order_number"`
	AbsenceOrderDate   string `json:"absence_order_date"`
	BeginAbsenceDate   string `json:"begin_absence_date"`
	EndAbsenceDate     string `json:"end_absence_date"`
	CardType           string `json:"card_type"`
	BIN                string `json:"bin"`
	NameGO             string `json:"name_go"`
	JobName            string `json:"job_name"`
	JobUID             string `json:"job_uid"`
	SubName            string `json:"sub_name"`
	SubUID             string `json:"sub_uid"`
}

type UserHRProfile struct {
	ID                 int64           `json:"id" db:"id"`
	UserID             int64           `json:"user_id" db:"user_id"`
	PersonCode         string          `json:"person_code" db:"person_code"`
	BadgeCode          string          `json:"badge_code" db:"badge_code"`
	IIN                string          `json:"iin" db:"iin"`
	LastName           string          `json:"last_name" db:"last_name"`
	FirstName          string          `json:"first_name" db:"first_name"`
	MiddleName         string          `json:"middle_name" db:"middle_name"`
	Photo              string          `json:"photo" db:"photo"`
	DocType            string          `json:"doc_type" db:"doc_type"`
	PersonStatus       string          `json:"person_status" db:"person_status"`
	LastOrderNumber    string          `json:"last_order_number" db:"last_order_number"`
	LastOrderDate      *time.Time      `json:"last_order_date,omitempty" db:"last_order_date"`
	LastBeginDate      *time.Time      `json:"last_begin_date,omitempty" db:"last_begin_date"`
	FirstBeginDate     *time.Time      `json:"first_begin_date,omitempty" db:"first_begin_date"`
	AbsenceStatus      string          `json:"absence_status" db:"absence_status"`
	AbsenceOrderNumber string          `json:"absence_order_number" db:"absence_order_number"`
	AbsenceOrderDate   *time.Time      `json:"absence_order_date,omitempty" db:"absence_order_date"`
	BeginAbsenceDate   *time.Time      `json:"begin_absence_date,omitempty" db:"begin_absence_date"`
	EndAbsenceDate     *time.Time      `json:"end_absence_date,omitempty" db:"end_absence_date"`
	CardType           string          `json:"card_type" db:"card_type"`
	BIN                string          `json:"bin" db:"bin"`
	NameGO             string          `json:"name_go" db:"name_go"`
	JobName            string          `json:"job_name" db:"job_name"`
	JobUID             string          `json:"job_uid" db:"job_uid"`
	SubName            string          `json:"sub_name" db:"sub_name"`
	SubUID             string          `json:"sub_uid" db:"sub_uid"`
	RawPayload         json.RawMessage `json:"-" db:"raw_payload" swaggertype:"object"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
}

type UserHRProfileSummary struct {
	ID                 int64           `json:"id" db:"id"`
	UserID             int64           `json:"user_id" db:"user_id"`
	PersonCode         string          `json:"person_code" db:"person_code"`
	BadgeCode          string          `json:"badge_code" db:"badge_code"`
	IIN                string          `json:"iin" db:"iin"`
	LastName           string          `json:"last_name" db:"last_name"`
	FirstName          string          `json:"first_name" db:"first_name"`
	MiddleName         string          `json:"middle_name" db:"middle_name"`
	DocType            string          `json:"doc_type" db:"doc_type"`
	PersonStatus       string          `json:"person_status" db:"person_status"`
	LastOrderNumber    string          `json:"last_order_number" db:"last_order_number"`
	LastOrderDate      *time.Time      `json:"last_order_date,omitempty" db:"last_order_date"`
	LastBeginDate      *time.Time      `json:"last_begin_date,omitempty" db:"last_begin_date"`
	FirstBeginDate     *time.Time      `json:"first_begin_date,omitempty" db:"first_begin_date"`
	AbsenceStatus      string          `json:"absence_status" db:"absence_status"`
	AbsenceOrderNumber string          `json:"absence_order_number" db:"absence_order_number"`
	AbsenceOrderDate   *time.Time      `json:"absence_order_date,omitempty" db:"absence_order_date"`
	BeginAbsenceDate   *time.Time      `json:"begin_absence_date,omitempty" db:"begin_absence_date"`
	EndAbsenceDate     *time.Time      `json:"end_absence_date,omitempty" db:"end_absence_date"`
	CardType           string          `json:"card_type" db:"card_type"`
	BIN                string          `json:"bin" db:"bin"`
	NameGO             string          `json:"name_go" db:"name_go"`
	JobName            string          `json:"job_name" db:"job_name"`
	JobUID             string          `json:"job_uid" db:"job_uid"`
	SubName            string          `json:"sub_name" db:"sub_name"`
	SubUID             string          `json:"sub_uid" db:"sub_uid"`
	RawPayload         json.RawMessage `json:"-" db:"raw_payload" swaggertype:"object"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
}

type UserHRProfileSummaryResponse struct {
	User      *UserResponse         `json:"user,omitempty"`
	HRProfile *UserHRProfileSummary `json:"hr_profile"`
}

type UserHRProfileResponse struct {
	User      *UserResponse  `json:"user,omitempty"`
	HRProfile *UserHRProfile `json:"hr_profile"`
}

type HROrgMapping struct {
	ID             int64     `json:"id" db:"id"`
	ExternalSystem string    `json:"external_system" db:"external_system"`
	NameGO         string    `json:"name_go" db:"name_go"`
	SubName        string    `json:"sub_name" db:"sub_name"`
	JobUID         string    `json:"job_uid" db:"job_uid"`
	SubUID         string    `json:"sub_uid" db:"sub_uid"`
	MinistryID     *int64    `json:"ministry_id,omitempty" db:"ministry_id"`
	StateBodyID    *int64    `json:"state_body_id,omitempty" db:"state_body_id"`
	DepartmentID   *int64    `json:"department_id,omitempty" db:"department_id"`
	DivisionID     *int64    `json:"division_id,omitempty" db:"division_id"`
	IsActive       bool      `json:"is_active" db:"is_active"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

type CreateHROrgMappingRequest struct {
	ExternalSystem string `json:"external_system"`
	NameGO         string `json:"name_go"`
	SubName        string `json:"sub_name"`
	JobUID         string `json:"job_uid"`
	SubUID         string `json:"sub_uid"`
	MinistryID     *int64 `json:"ministry_id"`
	StateBodyID    *int64 `json:"state_body_id"`
	DepartmentID   *int64 `json:"department_id"`
	DivisionID     *int64 `json:"division_id"`
	IsActive       *bool  `json:"is_active"`
}

type UpdateHROrgMappingRequest struct {
	ExternalSystem string `json:"external_system"`
	NameGO         string `json:"name_go"`
	SubName        string `json:"sub_name"`
	JobUID         string `json:"job_uid"`
	SubUID         string `json:"sub_uid"`
	MinistryID     *int64 `json:"ministry_id"`
	StateBodyID    *int64 `json:"state_body_id"`
	DepartmentID   *int64 `json:"department_id"`
	DivisionID     *int64 `json:"division_id"`
	IsActive       *bool  `json:"is_active"`
}

type HierarchyAssignment struct {
	MinistryID   *int64
	StateBodyID  *int64
	DepartmentID *int64
	DivisionID   *int64
}

type ManagementVacationResponse struct {
	UserID    int64      `json:"user_id"`
	FirstName string     `json:"first_name"`
	LastName  string     `json:"last_name"`
	AvatarURL *string    `json:"avatar_url"`
	Position  string     `json:"position"`
	BeginDate *time.Time `json:"begin_date,omitempty"`
	EndDate   *time.Time `json:"end_date,omitempty"`
}
