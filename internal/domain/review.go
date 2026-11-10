package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserReview represents a specific evaluation record
type UserReview struct {
	ID           uuid.UUID `json:"id" db:"id"`
	ReviewerID   int64     `json:"-" db:"reviewer_id"` // Скрываем reviewer_id в JSON для анонимности
	TargetUserID int64     `json:"target_user_id" db:"target_user_id"`

	DataFluency         int `json:"data_fluency" db:"data_fluency"`
	ProjectManagement   int `json:"project_management" db:"project_management"`
	DigitalProficiency  int `json:"digital_proficiency" db:"digital_proficiency"`
	CrisisCommunication int `json:"crisis_communication" db:"crisis_communication"`
	ServiceDesign       int `json:"service_design_thinking" db:"service_design_thinking"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// UserReviewStats represents aggregated stats for a user
type UserReviewStats struct {
	TargetUserID int64  `json:"target_user_id"`
	Period       string `json:"period"` // "current_month" or "all_time"
	ReviewCount  int    `json:"review_count"`

	AvgDataFluency         float64 `json:"avg_data_fluency"`
	AvgProjectManagement   float64 `json:"avg_project_management"`
	AvgDigitalProficiency  float64 `json:"avg_digital_proficiency"`
	AvgCrisisCommunication float64 `json:"avg_crisis_communication"`
	AvgServiceDesign       float64 `json:"avg_service_design_thinking"`
}

type CreateReviewRequest struct {
	TargetUserID        int64 `json:"target_user_id" binding:"required"`
	DataFluency         int   `json:"data_fluency" binding:"min=0,max=5"`
	ProjectManagement   int   `json:"project_management" binding:"min=0,max=5"`
	DigitalProficiency  int   `json:"digital_proficiency" binding:"min=0,max=5"`
	CrisisCommunication int   `json:"crisis_communication" binding:"min=0,max=5"`
	ServiceDesign       int   `json:"service_design_thinking" binding:"min=0,max=5"`
}
