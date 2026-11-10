package domain

import (
	"github.com/google/uuid"
	"time"
)

// CreateDocumentRequest selects exactly one of format or template_id.
type CreateDocumentRequest struct {
	Name       string     `json:"name" binding:"required"`
	FolderID   *uuid.UUID `json:"folder_id"`
	Format     string     `json:"format"`
	TemplateID *uuid.UUID `json:"template_id"`
}

type PublishDocumentTemplateRequest struct {
	Name   string    `json:"name" binding:"required"`
	FileID uuid.UUID `json:"file_id" binding:"required"`
}

type DocumentTemplate struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	FileID         uuid.UUID `json:"file_id"`
	Name           string    `json:"name"`
	Format         string    `json:"format"`
	CreatedBy      int64     `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}
