package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNWS002ExpiryAndBroadcastEligibility(t *testing.T) {
	now := time.Now()
	expired, future := now.Add(-time.Hour), now.Add(time.Hour)
	pub := &domain.Publication{Status: domain.PublicationStatusPublished, PublishedAt: expired, ArchiveAt: &expired}
	require.False(t, publicationBroadcastable(pub, now))
	pub.ArchiveAt = &now
	require.False(t, publicationBroadcastable(pub, now))
	pub.ArchiveAt = &future
	require.True(t, publicationBroadcastable(pub, now))
	pub.ScheduledAt = &future
	require.False(t, publicationBroadcastable(pub, now))
	require.Error(t, validatePublicationExpiry(&expired, &future, domain.PublicationStatusScheduled, now))
	require.Error(t, validatePublicationExpiry(&expired, nil, domain.PublicationStatusPublished, now))
	require.NoError(t, validatePublicationExpiry(&future, nil, domain.PublicationStatusPublished, now))
}

func TestNWS002ArchiveTransitionsIntegration(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	for _, name := range []string{
		"20260630000100_create_publications.sql", "20260701000200_create_publication_views.sql",
		"20260929001000_publications_news_cover_status_rich.sql", "20260929001100_publications_media_gallery_pdf_video.sql",
		"20260929001200_publication_object_cleanup.sql", "20260929001300_publications_reactions_threads_moderation.sql",
		"20260930001400_publications_pinned_banners_broadcasts.sql", "20260930001500_publication_audience_scope.sql",
		"20261001000100_publications_drafts_scheduled_archive.sql",
		"20261001000200_publications_target_audience_departments.sql",
	} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	svc := NewPublicationService(repo, repository.NewUserRepository(pool, zap.NewNop()), zap.NewNop())
	id := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO publications(id,author_id,title,status,archive_at,scheduled_at,published_at) VALUES($1,1,'Expired','archived',NOW()-INTERVAL '1 hour',NOW()+INTERVAL '1 hour',NOW()+INTERVAL '1 hour')`, id)
	require.NoError(t, err)
	_, err = svc.UnarchivePublication(ctx, id, 2, "published")
	require.ErrorIs(t, err, ErrForbiddenAction)
	_, err = svc.UnarchivePublication(ctx, id, 1, "published")
	require.NoError(t, err)
	pub, err := repo.GetByID(ctx, id, 2)
	require.NoError(t, err)
	require.Nil(t, pub.ArchiveAt)
	require.Nil(t, pub.ScheduledAt)
	require.Nil(t, pub.ArchivedAt)
	require.True(t, publicationVisible(pub, &domain.User{ID: 2}, time.Now()))
	archived, err := repo.ArchiveDueWithIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, archived)
	_, err = svc.UnarchivePublication(ctx, id, 1, "draft")
	require.Error(t, err, "restore must reject non-archived publications")

	expiry := time.Now().Add(time.Hour)
	_, err = pool.Exec(ctx, `UPDATE publications SET status='draft',archive_at=$2 WHERE id=$1`, id, expiry)
	require.NoError(t, err)
	schedule := expiry.Add(time.Hour)
	_, err = svc.PublishPublication(ctx, id, 1, &schedule, nil)
	require.Error(t, err, "preserved expiry must be validated against new schedule")
	_, err = svc.UpdatePublication(ctx, id, 1, domain.UpdatePublicationRequest{
		SourceType: "user", Type: "news", Title: "Expired", TargetAudience: "all", Importance: "normal",
		Status: domain.PublicationStatusScheduled, ScheduledAt: &schedule,
	})
	require.Error(t, err)

	_, err = pool.Exec(ctx, `UPDATE publications SET status='published',published_at=NOW()-INTERVAL '1 hour',scheduled_at=NULL,archive_at=NOW()-INTERVAL '1 minute' WHERE id=$1;`, id)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO publication_broadcast_queue(publication_id,triggered_by) VALUES($1,1)`, id)
	require.NoError(t, err)
	jobs, err := repo.PendingBroadcasts(ctx)
	require.NoError(t, err)
	require.Empty(t, jobs)
	_, err = svc.BroadcastPublication(ctx, id, 1, domain.BroadcastPublicationRequest{})
	require.ErrorContains(t, err, "expired or future")

	_, err = pool.Exec(ctx, `UPDATE publications SET status='draft',target_audience='ministry',target_ministry_id=999 WHERE id=$1`, id)
	require.NoError(t, err)
	list, err := repo.List(ctx, 2, true, nil, nil, "", "", nil, nil, nil, "", "draft", 1, 20)
	require.NoError(t, err)
	require.Len(t, list, 1, "superadmin must see drafts across audiences")
	list, err = repo.List(ctx, 2, false, nil, nil, "", "", nil, nil, nil, "", "draft", 1, 20)
	require.NoError(t, err)
	require.Empty(t, list, "other readers must not see drafts")
}
