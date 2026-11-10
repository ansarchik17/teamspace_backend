package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/repository"
	"regexp"
	"strings"
	"unicode/utf8"
)

var aiSectionColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validateAIChatID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return apperrors.Validation("invalid id")
	}
	return nil
}
func validateAIChatText(value *string, max int, required bool) error {
	if value == nil {
		if required {
			return apperrors.Validation("name is required")
		}
		return nil
	}
	*value = strings.TrimSpace(*value)
	if !utf8.ValidString(*value) || strings.ContainsRune(*value, 0) || utf8.RuneCountInString(*value) > max || (required && *value == "") {
		return apperrors.Validation("invalid name or description")
	}
	return nil
}
func (s *AlemService) GetOrganizedChats(ctx context.Context, user int64, archived bool) ([]repository.AIOrganizedChat, error) {
	return s.repo.ListOrganizedChats(ctx, user, archived)
}
func (s *AlemService) PatchChat(ctx context.Context, user int64, id string, p repository.AIChatPatch) (repository.AIOrganizedChat, error) {
	if err := validateAIChatID(id); err != nil {
		return repository.AIOrganizedChat{}, err
	}
	if p.Title != nil {
		if err := validateAIChatText(p.Title, 200, true); err != nil {
			return repository.AIOrganizedChat{}, err
		}
	}
	if p.SectionID != nil {
		if err := validateAIChatID(*p.SectionID); err != nil {
			return repository.AIOrganizedChat{}, err
		}
	}
	if p.Title == nil && p.Archived == nil && p.Pinned == nil && !p.SectionSet {
		return repository.AIOrganizedChat{}, apperrors.Validation("empty chat update")
	}
	return s.repo.PatchOrganizedChat(ctx, user, id, p)
}
func (s *AlemService) ChatSections(ctx context.Context, user int64) ([]repository.AIChatSection, error) {
	return s.repo.ListChatSections(ctx, user)
}
func (s *AlemService) SaveSection(ctx context.Context, user int64, id string, p repository.AIChatSectionInput) (repository.AIChatSection, error) {
	if id != "" {
		if err := validateAIChatID(id); err != nil {
			return repository.AIChatSection{}, err
		}
	}
	if err := validateAIChatText(p.Name, 120, id == "" || p.Name != nil); err != nil {
		return repository.AIChatSection{}, err
	}
	if err := validateAIChatText(p.Description, 2000, false); err != nil {
		return repository.AIChatSection{}, err
	}
	if p.Color != nil && !aiSectionColor.MatchString(*p.Color) {
		return repository.AIChatSection{}, apperrors.Validation("invalid section color")
	}
	if id != "" && p.Name == nil && p.Description == nil && p.Color == nil {
		return repository.AIChatSection{}, apperrors.Validation("empty section update")
	}
	return s.repo.SaveChatSection(ctx, user, id, p)
}
func (s *AlemService) DeleteSection(ctx context.Context, user int64, id string) error {
	if err := validateAIChatID(id); err != nil {
		return err
	}
	return s.repo.DeleteChatSection(ctx, user, id)
}
func (s *AlemService) CreateChatInSection(ctx context.Context, user int64, model, title string, section *string) (string, error) {
	if section == nil {
		return s.CreateChat(ctx, user, model, title)
	}
	if err := validateAIChatID(*section); err != nil {
		return "", err
	}
	if err := validateAIChatText(&title, 200, true); err != nil {
		return "", err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = s.defaultModel
	}
	if !isChatModel(model) {
		return "", apperrors.Validation("invalid chat model")
	}
	return s.repo.CreateConversationInSection(ctx, user, model, title, section)
}
