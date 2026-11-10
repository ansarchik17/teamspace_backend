package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func validateDriveName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 255 || strings.ContainsAny(name, "/\\\x00\r\n") || name == "." || name == ".." {
		return apperrors.BadRequest("name must contain 1–255 characters and no path separators or control characters")
	}
	return nil
}

// Batch items commit independently. A failed item never hides completed results.
func (s *DriveService) BatchItems(ctx context.Context, userID int64, req domain.DriveBatchRequest) (*domain.DriveBatchResponse, error) {
	switch req.Action {
	case "copy", "move", "rename", "delete":
	default:
		return nil, apperrors.BadRequest("invalid batch action")
	}
	if len(req.Items) == 0 || len(req.Items) > 100 {
		return nil, apperrors.BadRequest("items must contain 1–100 entries")
	}
	if req.TargetParentID != nil && *req.TargetParentID == uuid.Nil {
		return nil, apperrors.BadRequest("invalid target_parent_id")
	}
	if (req.Action == "rename" || req.Action == "delete") && req.TargetParentID != nil {
		return nil, apperrors.BadRequest("target_parent_id is only supported for copy/move")
	}
	seen := map[string]bool{}
	for _, item := range req.Items {
		if item.ResourceID == uuid.Nil || (item.ResourceType != domain.ResourceFile && item.ResourceType != domain.ResourceFolder) {
			return nil, apperrors.BadRequest("invalid resource")
		}
		key := string(item.ResourceType) + item.ResourceID.String()
		if seen[key] {
			return nil, apperrors.BadRequest("duplicate resource in batch")
		}
		seen[key] = true
		if req.Action == "rename" {
			if item.Name == nil {
				return nil, apperrors.BadRequest("name is required")
			}
			if err := validateDriveName(strings.TrimSpace(*item.Name)); err != nil {
				return nil, err
			}
		}
	}
	out := &domain.DriveBatchResponse{Results: make([]domain.DriveBatchResult, 0, len(req.Items))}
	for _, item := range req.Items {
		result := domain.DriveBatchResult{ResourceID: item.ResourceID, ResourceType: item.ResourceType, Status: 200}
		var err error
		if ctx.Err() != nil {
			err = apperrors.Internal("request cancelled")
		} else {
			switch req.Action {
			case "copy":
				result.Result, err = s.CopyItem(ctx, userID, item.ResourceType, item.ResourceID, req.TargetParentID)
			case "move":
				result.Result, err = s.MoveItem(ctx, userID, item.ResourceType, item.ResourceID, req.TargetParentID)
			case "rename":
				name := strings.TrimSpace(*item.Name)
				if item.ResourceType == domain.ResourceFile {
					err = s.UpdateFile(ctx, userID, item.ResourceID, &name, nil)
				} else {
					err = s.UpdateFolder(ctx, userID, item.ResourceID, &name, nil)
				}
				if err == nil {
					result.Result = map[string]string{"name": name}
				}
			case "delete":
				if item.ResourceType == domain.ResourceFile {
					result.Result, err = s.DeleteFile(ctx, userID, item.ResourceID)
				} else {
					result.Result, err = s.DeleteFolder(ctx, userID, item.ResourceID, item.Recursive)
				}
			}
		}
		if err != nil {
			app := apperrors.ToAppError(err)
			result.Status = app.StatusCode
			result.Error = app.Message
			result.ErrorCode = string(app.Code)
			result.Result = nil
			out.Failed++
		} else {
			out.Succeeded++
		}
		out.Results = append(out.Results, result)
	}
	return out, nil
}

func (s *DriveService) validateFolderDestination(ctx context.Context, source uuid.UUID, target *uuid.UUID) error {
	seen := map[uuid.UUID]bool{}
	for target != nil {
		if *target == source || seen[*target] {
			return apperrors.BadRequest("folder destination creates a cycle")
		}
		seen[*target] = true
		folder, err := s.repo.GetFolderByID(ctx, *target)
		if err != nil || folder == nil || folder.DeletedAt != nil {
			return apperrors.NotFound("target folder not found")
		}
		target = folder.ParentID
	}
	return nil
}
