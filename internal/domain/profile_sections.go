package domain

import "time"

type UserEducation struct {
	ID              int64      `json:"id"`
	UserID          int64      `json:"user_id"`
	SchoolName      string     `json:"school_name"`
	SchoolAvatarURL *string    `json:"school_avatar_url,omitempty"`
	Degree          *string    `json:"degree,omitempty"`
	FieldOfStudy    string     `json:"field_of_study"`
	StartDate       time.Time  `json:"-"`
	EndDate         *time.Time `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type UserEducationResponse struct {
	ID              int64   `json:"id"`
	UserID          int64   `json:"user_id"`
	SchoolName      string  `json:"school_name"`
	SchoolAvatarURL *string `json:"school_avatar_url,omitempty"`
	Degree          *string `json:"degree,omitempty"`
	FieldOfStudy    string  `json:"field_of_study"`
	StartDate       string  `json:"start_date"`
	EndDate         *string `json:"end_date,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type EducationListResponse struct {
	UserID int64                   `json:"user_id"`
	Count  int                     `json:"count"`
	Items  []UserEducationResponse `json:"items"`
}

type CreateEducationRequest struct {
	SchoolName      string  `json:"school_name" binding:"required"`
	SchoolAvatarURL *string `json:"school_avatar_url,omitempty"`
	Degree          *string `json:"degree,omitempty"`
	FieldOfStudy    string  `json:"field_of_study" binding:"required"`
	StartDate       string  `json:"start_date" binding:"required"`
	EndDate         *string `json:"end_date,omitempty"`
}

type UpdateEducationRequest struct {
	SchoolName      *string `json:"school_name,omitempty"`
	SchoolAvatarURL *string `json:"school_avatar_url,omitempty"`
	Degree          *string `json:"degree,omitempty"`
	FieldOfStudy    *string `json:"field_of_study,omitempty"`
	StartDate       *string `json:"start_date,omitempty"`
	EndDate         *string `json:"end_date,omitempty"`
}

type UserLicenseCertification struct {
	ID                    int64      `json:"id"`
	UserID                int64      `json:"user_id"`
	Name                  string     `json:"name"`
	IssuingOrganization   string     `json:"issuing_organization"`
	SkillName             string     `json:"skill_name"`
	AvatarURL             *string    `json:"avatar_url,omitempty"`
	IssueDate             time.Time  `json:"-"`
	ExpirationDate        *time.Time `json:"-"`
	AttachmentObjectName  *string    `json:"attachment_object_name,omitempty"`
	AttachmentFileName    *string    `json:"attachment_file_name,omitempty"`
	AttachmentContentType *string    `json:"attachment_content_type,omitempty"`
	AttachmentSizeBytes   *int64     `json:"attachment_size_bytes,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type UserLicenseCertificationResponse struct {
	ID                    int64   `json:"id"`
	UserID                int64   `json:"user_id"`
	Name                  string  `json:"name"`
	IssuingOrganization   string  `json:"issuing_organization"`
	SkillName             string  `json:"skill_name"`
	AvatarURL             *string `json:"avatar_url,omitempty"`
	IssueDate             string  `json:"issue_date"`
	ExpirationDate        *string `json:"expiration_date,omitempty"`
	AttachmentObjectName  *string `json:"attachment_object_name,omitempty"`
	AttachmentFileName    *string `json:"attachment_file_name,omitempty"`
	AttachmentContentType *string `json:"attachment_content_type,omitempty"`
	AttachmentSizeBytes   *int64  `json:"attachment_size_bytes,omitempty"`
	AttachmentDownloadURL *string `json:"attachment_download_url,omitempty"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
}

type LicenseCertificationListResponse struct {
	UserID int64                              `json:"user_id"`
	Count  int                                `json:"count"`
	Items  []UserLicenseCertificationResponse `json:"items"`
}

type CreateLicenseCertificationRequest struct {
	Name                  string  `json:"name" binding:"required"`
	IssuingOrganization   string  `json:"issuing_organization" binding:"required"`
	SkillName             string  `json:"skill_name" binding:"required"`
	AvatarURL             *string `json:"avatar_url,omitempty"`
	IssueDate             string  `json:"issue_date" binding:"required"`
	ExpirationDate        *string `json:"expiration_date,omitempty"`
	AttachmentObjectName  *string `json:"attachment_object_name,omitempty"`
	AttachmentFileName    *string `json:"attachment_file_name,omitempty"`
	AttachmentContentType *string `json:"attachment_content_type,omitempty"`
	AttachmentSizeBytes   *int64  `json:"attachment_size_bytes,omitempty"`
}

type UpdateLicenseCertificationRequest struct {
	Name                  *string `json:"name,omitempty"`
	IssuingOrganization   *string `json:"issuing_organization,omitempty"`
	SkillName             *string `json:"skill_name,omitempty"`
	AvatarURL             *string `json:"avatar_url,omitempty"`
	IssueDate             *string `json:"issue_date,omitempty"`
	ExpirationDate        *string `json:"expiration_date,omitempty"`
	AttachmentObjectName  *string `json:"attachment_object_name,omitempty"`
	AttachmentFileName    *string `json:"attachment_file_name,omitempty"`
	AttachmentContentType *string `json:"attachment_content_type,omitempty"`
	AttachmentSizeBytes   *int64  `json:"attachment_size_bytes,omitempty"`
}
