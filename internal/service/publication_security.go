package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	htmlparser "golang.org/x/net/html"
	"html"
	"net/url"
	"strings"
	"time"
)

func publicationVisible(pub *domain.Publication, user *domain.User, now time.Time) bool {
	if pub == nil || user == nil || pub.DeletedAt != nil {
		return false
	}
	if pub.AuthorID == user.ID || user.Role == domain.RoleSuperAdmin {
		return true
	}
	if pub.Status != "" && pub.Status != domain.PublicationStatusPublished && pub.Status != domain.PublicationStatusScheduled {
		return false
	}
	if pub.PublishedAt.After(now) || (pub.ScheduledAt != nil && pub.ScheduledAt.After(now)) {
		return false
	}
	if pub.ArchiveAt != nil && (pub.ArchiveAt.Before(now) || pub.ArchiveAt.Equal(now)) {
		return false
	}
	switch pub.TargetAudience {
	case domain.PublicationAudienceAll, "":
		return true
	case domain.PublicationAudienceMinistry, domain.PublicationAudienceBranch:
		if user.MinistryID == nil {
			return false
		}
		if pub.TargetMinistryID != nil && *user.MinistryID == *pub.TargetMinistryID {
			return true
		}
		for _, mid := range pub.TargetMinistryIDs {
			if mid == *user.MinistryID {
				return true
			}
		}
		return false
	case domain.PublicationAudienceDepartment:
		if user.DepartmentID == nil {
			return false
		}
		if pub.TargetDepartmentID != nil && *user.DepartmentID == *pub.TargetDepartmentID {
			return true
		}
		for _, did := range pub.TargetDepartmentIDs {
			if did == *user.DepartmentID {
				return true
			}
		}
		return false
	}
	return false
}

func publicationBroadcastable(pub *domain.Publication, now time.Time) bool {
	return pub != nil && pub.DeletedAt == nil && pub.Status == domain.PublicationStatusPublished &&
		!pub.PublishedAt.After(now) && (pub.ScheduledAt == nil || !pub.ScheduledAt.After(now)) &&
		(pub.ArchiveAt == nil || pub.ArchiveAt.After(now))
}

func validatePublicationExpiry(archiveAt, scheduledAt *time.Time, status string, now time.Time) error {
	if archiveAt == nil || status == domain.PublicationStatusArchived {
		return nil
	}
	if scheduledAt != nil && !archiveAt.After(*scheduledAt) {
		return apperrors.Validation("archive_at must be after scheduled_at")
	}
	if (status == domain.PublicationStatusPublished || status == domain.PublicationStatusScheduled) && !archiveAt.After(now) {
		return apperrors.Validation("archive_at must be in the future")
	}
	return nil
}

func (s *PublicationService) readablePublication(ctx context.Context, id uuid.UUID, userID int64) (*domain.Publication, error) {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !publicationVisible(pub, user, time.Now()) {
		return nil, ErrForbiddenView
	}
	return pub, nil
}

func (s *PublicationService) CheckPublicationEditAccess(ctx context.Context, id uuid.UUID, userID int64) (*domain.Publication, error) {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}
	return pub, nil
}

// Validate before signing: caller-supplied object keys are not proof of ownership.
func (s *PublicationService) validatePublicationCover(ctx context.Context, userID int64, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.User != nil {
		return apperrors.Validation("invalid cover URL")
	}
	if strings.HasSuffix(u.Path, "/api/v1/files/download") {
		key := u.Query().Get("object_name")
		if key == "" || s.fileService == nil {
			return apperrors.Validation("invalid storage cover URL")
		}
		// Only publisher-owned cover uploads can become publication download capabilities.
		// The generic legacy file ACL helper is intentionally not used: it fails open for unknown keys.
		if !strings.HasPrefix(key, fmt.Sprintf("image/%d/", userID)) || strings.Contains(key, "..") || strings.ContainsAny(key, "\\\r\n") {
			return apperrors.Forbidden("cover object must belong to the publication author")
		}
		return nil
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return apperrors.Validation("cover URL must use HTTP or HTTPS")
	}
	return nil
}

// Render a deliberately small editor vocabulary, never client-provided attributes.
func sanitizePublicationHTML(raw string) string {
	root, err := htmlparser.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	allowed := map[string]bool{"p": true, "br": true, "b": true, "strong": true, "i": true, "em": true, "u": true, "s": true, "ul": true, "ol": true, "li": true, "blockquote": true, "h1": true, "h2": true, "h3": true, "pre": true, "code": true, "a": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true}
	blocked := map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "embed": true, "svg": true, "math": true, "template": true}
	var out strings.Builder
	var visit func(*htmlparser.Node, int)
	visit = func(n *htmlparser.Node, depth int) {
		if depth > 100 {
			return
		}
		if n.Type == htmlparser.TextNode {
			out.WriteString(html.EscapeString(n.Data))
			return
		}
		if n.Type == htmlparser.ElementNode && blocked[n.Data] {
			return
		}
		tag := n.Type == htmlparser.ElementNode && allowed[n.Data]
		if tag {
			out.WriteString("<" + n.Data)
			if n.Data == "a" {
				for _, attr := range n.Attr {
					if attr.Key != "href" {
						continue
					}
					u, err := url.Parse(strings.TrimSpace(attr.Val))
					if err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto" || (u.Scheme == "" && u.Host == "" && strings.HasPrefix(u.Path, "/"))) {
						out.WriteString(` href="` + html.EscapeString(u.String()) + `" rel="nofollow noopener noreferrer"`)
					}
				}
			}
			out.WriteString(">")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, depth+1)
		}
		if tag && n.Data != "br" {
			out.WriteString("</" + n.Data + ">")
		}
	}
	visit(root, 0)
	return out.String()
}
