package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

const ooCollaborationPurpose = "onlyoffice_collaboration"

func ValidateOnlyOfficeCollaborationToken(token, secret string) (*OnlyOfficeTokenClaims, error) {
	claims, err := validateOnlyOfficeTokenPurpose(token, secret, ooCollaborationPurpose)
	if err != nil {
		return nil, err
	}
	if claims.UserID <= 0 || claims.DocumentKey == "" {
		return nil, apperrors.Unauthorized("invalid collaboration session")
	}
	if _, err = uuid.Parse(claims.SessionID); err != nil {
		return nil, apperrors.Unauthorized("invalid collaboration session")
	}
	return claims, nil
}

// Avatar URLs load in the DocumentServer frame without Bearer credentials.
func officeAvatar(user domain.OfficeUser, publicURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(user.Image))
	if err == nil && parsed.User == nil {
		if (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" {
			return parsed.String()
		}
		if parsed.Scheme == "" && parsed.Host == "" && strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(user.Image, "//") {
			return strings.TrimRight(publicURL, "/") + parsed.String()
		}
	}
	initials := ""
	for _, word := range strings.Fields(user.Name) {
		initials += string([]rune(word)[0])
		if len([]rune(initials)) == 2 {
			break
		}
	}
	if initials == "" {
		initials = "?"
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64"><rect width="64" height="64" rx="32" fill="#475569"/><text x="32" y="41" text-anchor="middle" font-family="sans-serif" font-size="24" fill="white">%s</text></svg>`, html.EscapeString(strings.ToUpper(initials)))
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

func parseOfficeUserIDs(ids []string) ([]int64, error) {
	if len(ids) > 100 {
		return nil, apperrors.BadRequest("at most 100 user IDs")
	}
	result := make([]int64, 0, len(ids))
	seen := map[int64]bool{}
	for _, raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, apperrors.BadRequest("invalid user ID")
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, nil
}

func (s *DriveService) OfficeUsers(ctx context.Context, viewer int64, fileID uuid.UUID, key string, ids []string) ([]domain.OfficeUser, error) {
	if viewer <= 0 {
		return nil, apperrors.Unauthorized("unauthorized")
	}
	file, err := s.canAccessFile(ctx, viewer, fileID, domain.RoleViewer)
	if err != nil {
		return nil, err
	}
	if key != "" && key != activeOnlyOfficeKey(file) {
		return nil, apperrors.Conflict("editor session is stale")
	}
	parsed, err := parseOfficeUserIDs(ids)
	if err != nil {
		return nil, err
	}
	// Knowing a user ID never grants access to their profile. Return only users
	// who can access this file, matching the existing Drive sharing boundary.
	allowed := make([]int64, 0, len(parsed))
	for _, id := range parsed {
		role, err := s.getEffectiveFileRole(ctx, id, file)
		if err != nil {
			return nil, err
		}
		if role != nil && roleRank(*role) >= roleRank(domain.RoleViewer) {
			allowed = append(allowed, id)
		}
	}
	users, err := s.repo.OfficeUserProfiles(ctx, allowed)
	if err != nil {
		return nil, err
	}
	for i := range users {
		users[i].Image = officeAvatar(users[i], s.publicURL)
	}
	return users, nil
}

func (s *DriveService) UpdateOfficePresence(ctx context.Context, claims *OnlyOfficeTokenClaims, action string) error {
	if claims == nil || claims.UserID <= 0 {
		return apperrors.Unauthorized("invalid collaboration session")
	}
	fileID, err := uuid.Parse(claims.FileID)
	if err != nil {
		return apperrors.BadRequest("invalid file")
	}
	session, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return apperrors.Unauthorized("invalid session")
	}
	if action == "leave" {
		return s.repo.LeaveOfficePresence(ctx, session, fileID, claims.UserID)
	}
	if action != "heartbeat" {
		return apperrors.BadRequest("invalid action")
	}
	if !OnlyOfficeModeCanSave(claims.Mode) {
		return apperrors.Forbidden("viewers cannot register as editors")
	}
	file, err := s.canAccessFile(ctx, claims.UserID, fileID, domain.RoleEditor)
	if err != nil {
		return err
	}
	if claims.DocumentKey != activeOnlyOfficeKey(file) {
		return apperrors.Conflict("editor session is stale")
	}
	if err = s.repo.TouchOfficePresence(ctx, session, fileID, claims.UserID, claims.DocumentKey); err != nil {
		if errors.Is(err, repository.ErrDriveVersionConflict) {
			return apperrors.Conflict("editor session is stale")
		}
		return err
	}
	return nil
}

func (s *DriveService) OfficeParticipants(ctx context.Context, userID int64, fileID uuid.UUID) ([]domain.OfficeParticipant, error) {
	if userID <= 0 {
		return nil, apperrors.Unauthorized("unauthorized")
	}
	file, err := s.canAccessFile(ctx, userID, fileID, domain.RoleViewer)
	if err != nil {
		return nil, err
	}
	seen, err := s.repo.OfficePresence(ctx, fileID, activeOnlyOfficeKey(file))
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		role, err := s.getEffectiveFileRole(ctx, id, file)
		if err != nil {
			return nil, err
		}
		if role != nil && roleRank(*role) >= roleRank(domain.RoleEditor) {
			ids = append(ids, id)
		}
	}
	users, err := s.repo.OfficeUserProfiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]domain.OfficeParticipant, 0, len(users))
	for _, user := range users {
		id, _ := strconv.ParseInt(user.ID, 10, 64)
		user.Image = officeAvatar(user, s.publicURL)
		result = append(result, domain.OfficeParticipant{OfficeUser: user, LastSeenAt: seen[id]})
	}
	return result, nil
}
