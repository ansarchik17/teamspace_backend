package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"strings"
	"testing"
	"time"
)

func TestNWS001PublicationVisibility(t *testing.T) {
	now := time.Now()
	pub := &domain.Publication{AuthorID: 1, Status: domain.PublicationStatusDraft, TargetAudience: "all", PublishedAt: now}
	owner := &domain.User{ID: 1}
	other := &domain.User{ID: 2}
	require.True(t, publicationVisible(pub, owner, now))
	require.False(t, publicationVisible(pub, other, now))
	require.True(t, publicationVisible(pub, &domain.User{ID: 3, Role: domain.RoleSuperAdmin}, now))
	pub.Status = domain.PublicationStatusScheduled
	future := now.Add(time.Hour)
	pub.ScheduledAt = &future
	pub.PublishedAt = future
	require.False(t, publicationVisible(pub, other, now))
	require.True(t, publicationVisible(pub, other, future.Add(time.Second)))
	pub.Status = domain.PublicationStatusArchived
	require.False(t, publicationVisible(pub, other, future.Add(time.Second)))
	pub.Status = domain.PublicationStatusPublished
	pub.ScheduledAt = nil
	pub.PublishedAt = now
	m1, m2 := int64(1), int64(2)
	pub.TargetAudience = "ministry"
	pub.TargetMinistryID = &m1
	other.MinistryID = &m2
	require.False(t, publicationVisible(pub, other, now))
	other.MinistryID = &m1
	require.True(t, publicationVisible(pub, other, now))
}

func TestNWS001SanitizesStoredHTML(t *testing.T) {
	raw := `<p onclick="evil()">Hello <strong>world</strong><script>alert(1)</script><img src=x onerror=evil()><a href="javascript:alert(1)">bad</a><a href="https://example.com">good</a><svg onload="evil()"></svg></p>`
	clean := sanitizePublicationHTML(raw)
	for _, forbidden := range []string{"onclick", "script", "onerror", "javascript:", "svg", "evil()", "<img"} {
		require.NotContains(t, clean, forbidden)
	}
	require.Contains(t, clean, "<strong>world</strong>")
	require.Contains(t, clean, `href="https://example.com"`)
	require.Equal(t, clean, sanitizePublicationHTML(clean))
	require.NotContains(t, sanitizePublicationHTML(`<a href="&#106;avascript:alert(1)">x</a>`), "href")
}

func TestNWS001CoverSigningRejectsForeignObjects(t *testing.T) {
	svc := NewPublicationService(nil, nil, zap.NewNop())
	svc.SetFileService(&FileService{})
	for _, key := range []string{"image/2/private.jpg", "document/1/private.pdf", "image/1/../2/private.jpg", "image/1/x\\other"} {
		require.Error(t, svc.validatePublicationCover(context.Background(), 1, "https://api.test/api/v1/files/download?object_name="+key))
	}
	require.NoError(t, svc.validatePublicationCover(context.Background(), 1, "https://api.test/api/v1/files/download?object_name=image/1/cover.jpg"))
	require.Error(t, svc.validatePublicationCover(context.Background(), 1, "javascript:alert(1)"))
	require.NoError(t, svc.validatePublicationCover(context.Background(), 1, "https://example.com/cover.jpg"))
	require.Error(t, svc.validatePublicationCover(context.Background(), 1, strings.Repeat("x", 8193)))
}
