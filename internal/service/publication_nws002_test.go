package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNWS002_PublicationVisibilityWithArchive(t *testing.T) {
	now := time.Now()
	author := &domain.User{ID: 10, Role: domain.RoleUser}
	other := &domain.User{ID: 20, Role: domain.RoleUser}
	admin := &domain.User{ID: 99, Role: domain.RoleSuperAdmin}

	t.Run("draft publication visibility", func(t *testing.T) {
		pub := &domain.Publication{
			ID:             uuid.New(),
			AuthorID:       10,
			Status:         domain.PublicationStatusDraft,
			TargetAudience: "all",
			PublishedAt:    now,
		}
		require.True(t, publicationVisible(pub, author, now))
		require.False(t, publicationVisible(pub, other, now))
		require.True(t, publicationVisible(pub, admin, now))
	})

	t.Run("scheduled publication visibility before and after scheduled time", func(t *testing.T) {
		future := now.Add(2 * time.Hour)
		pub := &domain.Publication{
			ID:             uuid.New(),
			AuthorID:       10,
			Status:         domain.PublicationStatusScheduled,
			ScheduledAt:    &future,
			PublishedAt:    future,
			TargetAudience: "all",
		}
		// Before scheduled time: visible only to author and admin
		require.True(t, publicationVisible(pub, author, now))
		require.False(t, publicationVisible(pub, other, now))
		require.True(t, publicationVisible(pub, admin, now))

		// At/after scheduled time: visible to everyone
		require.True(t, publicationVisible(pub, other, future.Add(time.Minute)))
	})

	t.Run("archive_at expiration hides published post from public feed", func(t *testing.T) {
		expireAt := now.Add(1 * time.Hour)
		pub := &domain.Publication{
			ID:             uuid.New(),
			AuthorID:       10,
			Status:         domain.PublicationStatusPublished,
			TargetAudience: "all",
			PublishedAt:    now.Add(-1 * time.Hour),
			ArchiveAt:      &expireAt,
		}

		// Before archive_at: visible to all
		require.True(t, publicationVisible(pub, other, now))

		// After archive_at: hidden from public, visible to author & superadmin
		afterExpire := expireAt.Add(time.Minute)
		require.False(t, publicationVisible(pub, other, afterExpire))
		require.True(t, publicationVisible(pub, author, afterExpire))
		require.True(t, publicationVisible(pub, admin, afterExpire))
	})

	t.Run("explicitly archived post visibility", func(t *testing.T) {
		archivedTime := now.Add(-10 * time.Minute)
		pub := &domain.Publication{
			ID:             uuid.New(),
			AuthorID:       10,
			Status:         domain.PublicationStatusArchived,
			ArchivedAt:     &archivedTime,
			TargetAudience: "all",
			PublishedAt:    now.Add(-2 * time.Hour),
		}

		require.False(t, publicationVisible(pub, other, now))
		require.True(t, publicationVisible(pub, author, now))
		require.True(t, publicationVisible(pub, admin, now))
	})
}

func TestNWS002_DraftsAndArchiveJSONSerialization(t *testing.T) {
	futurePublish := time.Now().Add(48 * time.Hour)
	futureArchive := time.Now().Add(96 * time.Hour)

	t.Run("PublishPublicationRequest with scheduled_at and archive_at", func(t *testing.T) {
		reqJSON := `{
			"scheduled_at": "` + futurePublish.Format(time.RFC3339) + `",
			"archive_at": "` + futureArchive.Format(time.RFC3339) + `"
		}`
		var req domain.PublishPublicationRequest
		err := json.Unmarshal([]byte(reqJSON), &req)
		require.NoError(t, err)
		require.NotNil(t, req.ScheduledAt)
		require.NotNil(t, req.ArchiveAt)
		require.True(t, req.ArchiveAt.After(*req.ScheduledAt))
	})

	t.Run("UnarchivePublicationRequest default and custom target status", func(t *testing.T) {
		reqJSON := `{"target_status": "draft"}`
		var req domain.UnarchivePublicationRequest
		err := json.Unmarshal([]byte(reqJSON), &req)
		require.NoError(t, err)
		require.Equal(t, "draft", req.TargetStatus)
	})

	t.Run("ArchiveObsoleteRequest and Response", func(t *testing.T) {
		reqJSON := `{
			"older_than_days": 60,
			"types": ["news", "announcement"]
		}`
		var req domain.ArchiveObsoleteRequest
		err := json.Unmarshal([]byte(reqJSON), &req)
		require.NoError(t, err)
		require.NotNil(t, req.OlderThanDays)
		require.Equal(t, 60, *req.OlderThanDays)
		require.Equal(t, []string{"news", "announcement"}, req.Types)

		cutoff := time.Now().AddDate(0, 0, -60)
		resp := domain.ArchiveObsoleteResponse{
			ArchivedCount: 15,
			CutoffDate:    &cutoff,
		}
		data, err := json.Marshal(resp)
		require.NoError(t, err)
		require.Contains(t, string(data), `"archived_count":15`)
		require.Contains(t, string(data), `"cutoff_date"`)
	})

	t.Run("PublicationResponse includes archive_at and archived_at", func(t *testing.T) {
		pubID := uuid.New()
		archivedAt := time.Now()
		resp := domain.PublicationResponse{
			ID:         pubID,
			Title:      "Архивная новость",
			Status:     domain.PublicationStatusArchived,
			ArchivedAt: &archivedAt,
		}

		data, err := json.Marshal(resp)
		require.NoError(t, err)
		require.Contains(t, string(data), `"status":"archived"`)
		require.Contains(t, string(data), `"archived_at"`)
	})
}
