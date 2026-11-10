package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"strings"
	"time"
)

func (s *KanbanService) UpsertODOIntegrationAssignment(ctx context.Context, actor int64, org uuid.UUID, req DocumentologAssignmentUpsertRequest) (*domain.Task, *domain.Board, error) {
	if strings.TrimSpace(req.AssignmentID) == "" || strings.TrimSpace(req.DocumentID) == "" || strings.HasPrefix(req.AuthorSub, "workspace:") || strings.HasPrefix(req.AssigneeSub, "workspace:") {
		return nil, nil, apperrors.Validation("canonical assignment and Keycloak identities required")
	}
	if req.SchemaVersion != 0 {
		return nil, nil, apperrors.Validation("legacy contract expected")
	}
	priority := strings.ToLower(strings.TrimSpace(req.Priority))
	if priority == "" || priority == "normal" {
		priority = "medium"
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	switch status {
	case "", "open":
		status = "assigned"
	case "done":
		status = "completed"
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, nil, apperrors.Validation("assignment title required")
	}
	e := domain.ODOAssignmentEvent{SchemaVersion: 0, OrganizationID: org, SourceSystem: "documentolog", IdentityIssuer: req.IdentityIssuer, AssignmentID: req.AssignmentID, DocumentID: req.DocumentID, AuthorSub: req.AuthorSub, AssigneeSub: req.AssigneeSub, Title: req.Title, Description: req.Description, Priority: priority, Status: status, Timezone: "UTC"}
	switch priority {
	case "low", "medium", "high", "critical":
	default:
		return nil, nil, apperrors.Validation("invalid assignment priority")
	}
	switch status {
	case "assigned", "in_progress", "completed", "closed", "cancelled":
	default:
		return nil, nil, apperrors.Validation("invalid assignment status")
	}
	if req.DepartmentID != "" {
		e.DepartmentID = &req.DepartmentID
	}
	if req.SourceURL != "" {
		e.SourceURL = &req.SourceURL
	}
	if req.DueDate != "" {
		if _, err := time.Parse("2006-01-02", req.DueDate); err != nil {
			return nil, nil, apperrors.Validation("invalid assignment date")
		}
		e.DueDate = &req.DueDate
	}
	receipt, err := s.taskRepo.ApplyODOAssignment(ctx, actor, e, req.BoardID)
	if err != nil {
		return nil, nil, err
	}
	return receipt.Task, &domain.Board{ID: receipt.BoardID, SystemKey: "documentolog-assignments:" + org.String()}, nil
}

func (s *KanbanService) ApplyODOAssignmentEvent(ctx context.Context, actor int64, e domain.ODOAssignmentEvent, sourceOrigin string) (*domain.ODOAssignmentReceipt, error) {
	if err := e.Validate(sourceOrigin); err != nil {
		return nil, apperrors.New(apperrors.ErrCodeValidation, "invalid assignment snapshot", 422)
	}
	return s.taskRepo.ApplyODOAssignment(ctx, actor, e, "")
}

func (s *KanbanService) ApplyODOAssignmentBackfill(ctx context.Context, actor int64, request domain.ODOAssignmentBackfill, origin string) (*domain.ODOAssignmentReceipt, error) {
	if err := request.Event.Validate(origin); err != nil {
		return nil, apperrors.New(apperrors.ErrCodeValidation, "invalid backfill snapshot", 422)
	}
	return s.taskRepo.ApplyODOBackfill(ctx, actor, request)
}
