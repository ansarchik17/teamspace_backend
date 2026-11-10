package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func normalizeDriveTags(tags []string) ([]string, error) {
	if len(tags) > 20 {
		return nil, apperrors.BadRequest("at most 20 tags are allowed")
	}
	result := make([]string, 0, len(tags))
	seen := make(map[string]bool)
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > 64 || strings.ContainsRune(tag, 0) {
			return nil, apperrors.BadRequest("tags must contain 1 to 64 characters")
		}
		key := strings.ToLower(tag)
		if !seen[key] {
			result = append(result, tag)
			seen[key] = true
		}
	}
	return result, nil
}

func (s *DriveService) GetFileTags(ctx context.Context, userID int64, id uuid.UUID) ([]string, error) {
	if _, err := s.canAccessFile(ctx, userID, id, domain.RoleViewer); err != nil {
		return nil, err
	}
	return s.repo.GetDriveFileTags(ctx, id)
}

func (s *DriveService) SetFileTags(ctx context.Context, userID int64, id uuid.UUID, tags []string) ([]string, error) {
	if _, err := s.canAccessFile(ctx, userID, id, domain.RoleEditor); err != nil {
		return nil, err
	}
	normalized, err := normalizeDriveTags(tags)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetDriveFileTags(ctx, id, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}
